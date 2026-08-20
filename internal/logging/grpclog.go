package logging

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"google.golang.org/grpc/grpclog"
)

// grpcLogger routes gRPC-internal records to the slog default logger.
// grpclog's own default writes plain text to stderr through private
// log.Loggers that bypass the stdlib-log bridge, contaminating the
// single-line JSON stream that log pipelines parse. Transport chatter
// (info/warning severity) maps to debug; only genuine errors surface at
// error level.
type grpcLogger struct{}

func redirectGrpclog() {
	grpclog.SetLoggerV2(grpcLogger{})
}

func (grpcLogger) Info(args ...any) { grpcLog(slog.LevelDebug, "info", fmt.Sprint(args...)) }

func (grpcLogger) Infoln(args ...any) { grpcLog(slog.LevelDebug, "info", sprintln(args)) }

func (grpcLogger) Infof(format string, args ...any) {
	grpcLog(slog.LevelDebug, "info", fmt.Sprintf(format, args...))
}

func (grpcLogger) Warning(args ...any) { grpcLog(slog.LevelDebug, "warning", fmt.Sprint(args...)) }

func (grpcLogger) Warningln(args ...any) { grpcLog(slog.LevelDebug, "warning", sprintln(args)) }

func (grpcLogger) Warningf(format string, args ...any) {
	grpcLog(slog.LevelDebug, "warning", fmt.Sprintf(format, args...))
}

func (grpcLogger) Error(args ...any) { grpcLog(slog.LevelError, "error", fmt.Sprint(args...)) }

func (grpcLogger) Errorln(args ...any) { grpcLog(slog.LevelError, "error", sprintln(args)) }

func (grpcLogger) Errorf(format string, args ...any) {
	grpcLog(slog.LevelError, "error", fmt.Sprintf(format, args...))
}

// Fatal must exit per the grpclog.LoggerV2 contract.
func (grpcLogger) Fatal(args ...any) {
	grpcLog(slog.LevelError, "fatal", fmt.Sprint(args...))
	os.Exit(1)
}

func (grpcLogger) Fatalln(args ...any) { grpcLog(slog.LevelError, "fatal", sprintln(args)); os.Exit(1) }

func (grpcLogger) Fatalf(format string, args ...any) {
	grpcLog(slog.LevelError, "fatal", fmt.Sprintf(format, args...))
	os.Exit(1)
}

// V mirrors grpclog's default verbosity of 0.
func (grpcLogger) V(l int) bool { return l <= 0 }

func grpcLog(level slog.Level, severity, detail string) {
	slog.Log(context.Background(), level, "gRPC log", "severity", severity, "detail", detail)
}

func sprintln(args []any) string {
	return strings.TrimSuffix(fmt.Sprintln(args...), "\n")
}
