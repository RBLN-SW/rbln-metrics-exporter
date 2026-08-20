// Package logging configures the process-wide slog logger according to the
// RBLN logging contract (rbln-npu-operator/docs/logging.md).
//
// Canonical copy — 수정 시 모든 repo의 복사본을 함께 갱신할 것:
// rbln-metrics-exporter, rbln-npu-feature-discovery, rbln-k8s-driver-manager,
// sandbox-device-plugin, rbln-npu-operator.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

// LevelTrace extends slog's levels downward for the contract's "trace" level.
const LevelTrace = slog.Level(-8)

// New builds a contract-conformant slog logger writing to w.
// level: "error"|"warning"|"info"|"debug"|"trace" ("" = info).
// format: "json"|"text" ("" = json).
func New(w io.Writer, level, format string) (*slog.Logger, error) {
	lvl, err := parseLevel(level)
	if err != nil {
		return nil, err
	}
	opts := &slog.HandlerOptions{
		Level: lvl,
		// caller 비용/노이즈는 debug 이상에서만 감수한다.
		AddSource:   lvl <= slog.LevelDebug,
		ReplaceAttr: replaceAttr,
	}
	f, err := parseFormat(format)
	if err != nil {
		return nil, err
	}
	var h slog.Handler
	switch f {
	case "json":
		h = slog.NewJSONHandler(w, opts)
	case "text":
		h = slog.NewTextHandler(w, opts)
	}
	return slog.New(h), nil
}

// Setup installs the process-wide default logger (stdout).
func Setup(level, format string) error {
	logger, err := New(os.Stdout, level, format)
	if err != nil {
		return err
	}
	slog.SetDefault(logger)
	return nil
}

// SetupFromEnv reads LOG_LEVEL / LOG_FORMAT and installs the logger.
// 빈 값이면 info/json (프로덕션 기본). Invalid 값은 프로세스를 죽이지 않는다:
// 해당 변수만 계약 기본값으로 대체하고, 설치된 로거로 "fallback" 키를 담은
// Warn을 남긴다 (contract: substituting a default adds a fallback key).
func SetupFromEnv() {
	level, format := os.Getenv("LOG_LEVEL"), os.Getenv("LOG_FORMAT")
	var levelErr, formatErr error
	if _, err := parseLevel(level); err != nil {
		levelErr, level = err, "info"
	}
	if _, err := parseFormat(format); err != nil {
		formatErr, format = err, "json"
	}
	if err := Setup(level, format); err != nil {
		// 위에서 검증/대체했으므로 도달 불가.
		slog.Error("Failed to install logger", "err", err)
		return
	}
	// LOG_LEVEL=error + invalid LOG_FORMAT이면 format Warn이 게이트에 억제된다 —
	// 명시적으로 error 게이트를 고른 결과이므로 수용.
	if levelErr != nil {
		slog.Warn("Invalid LOG_LEVEL, using default", "err", levelErr, "fallback", "info")
	}
	if formatErr != nil {
		slog.Warn("Invalid LOG_FORMAT, using default", "err", formatErr, "fallback", "json")
	}
}

func parseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "error":
		return slog.LevelError, nil
	case "warning":
		return slog.LevelWarn, nil
	case "debug":
		return slog.LevelDebug, nil
	case "trace":
		return LevelTrace, nil
	}
	return 0, fmt.Errorf("unknown log level %q (error|warning|info|debug|trace)", s)
}

func parseFormat(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "json":
		return "json", nil
	case "text":
		return "text", nil
	}
	return "", fmt.Errorf("unknown log format %q (json|text)", s)
}

// replaceAttr normalizes slog output to the contract: key "ts" with
// RFC3339Nano, lowercase "level" ("trace" for LevelTrace), and a zap-style
// "caller" ("file:line") instead of the verbose source group.
func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return a
	}
	switch a.Key {
	case slog.TimeKey:
		// 사용자 attr가 "time" 키를 쓸 수 있다 — 레코드 타임스탬프만 변환한다.
		if a.Value.Kind() != slog.KindTime {
			return a
		}
		a.Key = "ts"
		a.Value = slog.StringValue(a.Value.Time().Format(time.RFC3339Nano))
	case slog.LevelKey:
		lvl, ok := a.Value.Any().(slog.Level)
		if !ok {
			return a
		}
		if lvl == LevelTrace {
			a.Value = slog.StringValue("trace")
		} else {
			a.Value = slog.StringValue(strings.ToLower(lvl.String()))
		}
	case slog.SourceKey:
		src, ok := a.Value.Any().(*slog.Source)
		if !ok {
			return a
		}
		a.Key = "caller"
		a.Value = slog.StringValue(fmt.Sprintf("%s:%d", trimPath(src.File), src.Line))
	}
	return a
}

// trimPath keeps at most the last two path segments for a zap-style short caller.
func trimPath(file string) string {
	idx := strings.LastIndexByte(file, '/')
	if idx == -1 {
		return file
	}
	if idx2 := strings.LastIndexByte(file[:idx], '/'); idx2 != -1 {
		return file[idx2+1:]
	}
	return file
}
