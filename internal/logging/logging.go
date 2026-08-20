// Package logging configures the process-wide slog logger: structured json
// or text records controlled by LOG_LEVEL and LOG_FORMAT, defaulting to
// info/json.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

// LevelTrace extends slog's levels downward with a "trace" level below debug.
const LevelTrace = slog.Level(-8)

// New builds a slog logger writing to w.
// level: "error"|"warning"|"info"|"debug"|"trace" ("" = info).
// format: "json"|"text" ("" = json).
func New(w io.Writer, level, format string) (*slog.Logger, error) {
	lvl, err := parseLevel(level)
	if err != nil {
		return nil, err
	}
	opts := &slog.HandlerOptions{
		Level: lvl,
		// Caller info is only worth its cost/noise at debug and trace verbosity.
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

// SetupFromEnv reads LOG_LEVEL / LOG_FORMAT and installs the logger.
// Empty values mean info/json (production defaults). Invalid values do not
// kill the process — a typo in a DaemonSet env would CrashLoopBackOff the
// exporter and drop all telemetry from the node. Instead, only the offending
// variable falls back to its default, recorded by a Warn with a "fallback" key.
func SetupFromEnv() {
	setupFromEnv(os.Stdout)
}

func setupFromEnv(w io.Writer) {
	level, format := os.Getenv("LOG_LEVEL"), os.Getenv("LOG_FORMAT")
	var levelErr, formatErr error
	if _, err := parseLevel(level); err != nil {
		levelErr, level = err, "info"
	}
	if _, err := parseFormat(format); err != nil {
		formatErr, format = err, "json"
	}
	logger, err := New(w, level, format)
	if err != nil {
		// Unreachable: both inputs were validated or substituted above.
		slog.Error("Failed to install logger", "err", err)
		return
	}
	slog.SetDefault(logger)
	redirectGrpclog()
	// With LOG_LEVEL=error and an invalid LOG_FORMAT, the format Warn is
	// suppressed by the level gate — accepted, since the error gate was
	// explicitly requested.
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

// replaceAttr normalizes slog output: key "ts" with RFC3339Nano, lowercase
// "level" ("trace" for LevelTrace), and a zap-style "caller" ("file:line")
// instead of the verbose source group.
func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return a
	}
	switch a.Key {
	case slog.TimeKey:
		// A user attr may also use the "time" key — convert only the record
		// timestamp. Residual limitation: a user "time" attr that is itself
		// KindTime is indistinguishable from the record timestamp, so it is
		// also renamed, duplicating the "ts" key in that record.
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
