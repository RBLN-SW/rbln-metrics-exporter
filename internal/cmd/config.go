package cmd

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/pflag"
)

const (
	MinIntervalSeconds = 1
	MaxIntervalSeconds = 60
)

type Config struct {
	RBLNDaemonURL string
	Port          int
	Interval      time.Duration
	Oneshot       bool
}

type configBuilder struct {
	cfg         Config
	intervalSec int
}

func newConfigBuilder(getenv func(string) string) *configBuilder {
	cfg := Config{
		RBLNDaemonURL: getenvDefault(getenv, "RBLN_METRICS_EXPORTER_RBLN_DAEMON_URL", "127.0.0.1:50051"),
		Port:          getenvIntDefault(getenv, "RBLN_METRICS_EXPORTER_PORT", 9090),
		Interval:      time.Duration(getenvIntDefault(getenv, "RBLN_METRICS_EXPORTER_INTERVAL", 5)) * time.Second,
		Oneshot:       getenvBoolDefault(getenv, "RBLN_METRICS_EXPORTER_ONESHOT", false),
	}

	return &configBuilder{
		cfg:         cfg,
		intervalSec: int(cfg.Interval / time.Second),
	}
}

func (b *configBuilder) bindFlags(fs *pflag.FlagSet) {
	fs.StringVar(&b.cfg.RBLNDaemonURL, "rbln-daemon-url", b.cfg.RBLNDaemonURL, "Endpoint to RBLN daemon grpc server")
	fs.IntVar(&b.cfg.Port, "port", b.cfg.Port, "Port to listen for requests")
	fs.IntVar(&b.intervalSec, "interval", b.intervalSec, fmt.Sprintf("Interval of collecting metrics (%d-%d seconds)", MinIntervalSeconds, MaxIntervalSeconds))
	fs.BoolVar(&b.cfg.Oneshot, "oneshot", b.cfg.Oneshot, "Collect once and exit")
}

func (b *configBuilder) finalize() error {
	if b.intervalSec < MinIntervalSeconds || b.intervalSec > MaxIntervalSeconds {
		return fmt.Errorf("interval must be %d-%d seconds", MinIntervalSeconds, MaxIntervalSeconds)
	}
	b.cfg.Interval = time.Duration(b.intervalSec) * time.Second
	// Deprecated compatibility shim: remove when inputs no longer include http(s) schemes.
	b.cfg.RBLNDaemonURL = stripSchemePrefix(b.cfg.RBLNDaemonURL)
	return nil
}

func getenvDefault(getenv func(string) string, key, def string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return def
}

func getenvIntDefault(getenv func(string) string, key string, def int) int {
	if v := getenv(key); v != "" {
		i, err := strconv.Atoi(v)
		if err == nil {
			return i
		}
	}
	return def
}

func getenvBoolDefault(getenv func(string) string, key string, def bool) bool {
	if v := getenv(key); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "y", "on":
			return true
		case "0", "false", "no", "n", "off":
			return false
		}
	}
	return def
}

// stripSchemePrefix keeps backward compatibility with URLs that include http(s)://
func stripSchemePrefix(addr string) string {
	if strings.HasPrefix(addr, "http://") {
		return strings.TrimPrefix(addr, "http://")
	}
	if strings.HasPrefix(addr, "https://") {
		return strings.TrimPrefix(addr, "https://")
	}
	return addr
}
