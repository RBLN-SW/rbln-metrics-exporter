package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
	"github.com/spf13/pflag"
)

const (
	MinIntervalSeconds = 1
	MaxIntervalSeconds = 60
)

type Config struct {
	Mode           string
	RBLNDaemonURL  string
	Port           int
	Interval       time.Duration
	Oneshot        bool
	NodeName       string
	KubernetesMode string
	SMD            daemon.Options
}

// LogValue renders the resolved configuration for the startup record with
// stable camelCase keys and a human-readable interval.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("mode", c.Mode),
		slog.String("daemon", c.RBLNDaemonURL),
		slog.Int("port", c.Port),
		slog.String("interval", c.Interval.String()),
		slog.Bool("oneshot", c.Oneshot),
		slog.String("node", c.NodeName),
		slog.String("kubernetesMode", c.KubernetesMode),
		slog.String("metricGroup", c.SMD.MetricGroupID),
		slog.Bool("createMetricGroup", c.SMD.CreateMetricGroup),
	)
}

type configBuilder struct {
	cfg         Config
	intervalSec int
}

func newConfigBuilder(getenv func(string) string) *configBuilder {
	cfg := Config{
		Mode:           getenvDefault(getenv, "RBLN_METRICS_EXPORTER_MODE", ModeLocal),
		RBLNDaemonURL:  getenvDefault(getenv, "RBLN_METRICS_EXPORTER_RBLN_DAEMON_URL", "127.0.0.1:50051"),
		Port:           getenvIntDefault(getenv, "RBLN_METRICS_EXPORTER_PORT", 9090),
		Interval:       time.Duration(getenvIntDefault(getenv, "RBLN_METRICS_EXPORTER_INTERVAL", 5)) * time.Second,
		Oneshot:        getenvBoolDefault(getenv, "RBLN_METRICS_EXPORTER_ONESHOT", false),
		NodeName:       detectNodeName(getenv, "NODE_NAME", "unknown"),
		KubernetesMode: getenvDefault(getenv, "RBLN_METRICS_EXPORTER_KUBERNETES_MODE", KubernetesModeAuto),
	}

	cfg.SMD = daemon.Options{
		MetricGroupID:     getenvDefault(getenv, "RBLN_METRICS_EXPORTER_METRIC_GROUP_ID", daemon.DefaultMetricGroupID),
		CreateMetricGroup: getenvBoolDefault(getenv, "RBLN_METRICS_EXPORTER_CREATE_METRIC_GROUP", true),
		PCIDevicesPath:    getenvDefault(getenv, "RBLN_METRICS_EXPORTER_PCI_DEVICES_PATH", "/sys/bus/pci/devices"),
		TLSCA:             getenv("RBLN_METRICS_EXPORTER_SMD_TLS_CA"), TLSCert: getenv("RBLN_METRICS_EXPORTER_SMD_TLS_CERT"), TLSKey: getenv("RBLN_METRICS_EXPORTER_SMD_TLS_KEY"),
	}
	return &configBuilder{
		cfg:         cfg,
		intervalSec: int(cfg.Interval / time.Second),
	}
}

func (b *configBuilder) bindFlags(fs *pflag.FlagSet) {
	fs.StringVar(&b.cfg.SMD.MetricGroupID, "metric-group-id", b.cfg.SMD.MetricGroupID, "SMD clock-free on-demand metric group ID")
	fs.BoolVar(&b.cfg.SMD.CreateMetricGroup, "create-metric-group", b.cfg.SMD.CreateMetricGroup, "Create the clock-free metric group when absent (requires SMD permission)")
	fs.StringVar(&b.cfg.SMD.PCIDevicesPath, "pci-devices-path", b.cfg.SMD.PCIDevicesPath, "Local PCI sysfs root for VF profiles (ignored in gateway mode)")
	fs.StringVar(&b.cfg.SMD.TLSCA, "smd-tls-ca", b.cfg.SMD.TLSCA, "CA PEM for the SMD TLS endpoint")
	fs.StringVar(&b.cfg.SMD.TLSCert, "smd-tls-cert", b.cfg.SMD.TLSCert, "Client certificate PEM for SMD mTLS")
	fs.StringVar(&b.cfg.SMD.TLSKey, "smd-tls-key", b.cfg.SMD.TLSKey, "Client private key PEM for SMD mTLS")

	fs.StringVar(&b.cfg.Mode, "mode", b.cfg.Mode, "Exporter mode: local (collect from the local daemon on a schedule), gateway (collect from the rbln-smd given by /metrics?target=<host:port> on each scrape)")
	fs.StringVar(&b.cfg.RBLNDaemonURL, "rbln-daemon-url", b.cfg.RBLNDaemonURL, "Endpoint to RBLN daemon grpc server (local mode only)")
	fs.IntVar(&b.cfg.Port, "port", b.cfg.Port, "Port to listen for requests")
	fs.IntVar(&b.intervalSec, "interval", b.intervalSec, fmt.Sprintf("Interval of collecting metrics (%d-%d seconds)", MinIntervalSeconds, MaxIntervalSeconds))
	fs.BoolVar(&b.cfg.Oneshot, "oneshot", b.cfg.Oneshot, "Collect once and exit")
	fs.StringVar(&b.cfg.NodeName, "node-name", b.cfg.NodeName, "Name of the node")
	fs.StringVar(&b.cfg.KubernetesMode, "kubernetes-mode", b.cfg.KubernetesMode, "Kubernetes mode: auto, on, off")
}

func (b *configBuilder) finalize() error {
	if b.cfg.SMD.MetricGroupID == "default" || strings.TrimSpace(b.cfg.SMD.MetricGroupID) == "" {
		return fmt.Errorf("metric-group-id must name a dedicated clock-free group, not default")
	}
	if (b.cfg.SMD.TLSCert == "") != (b.cfg.SMD.TLSKey == "") {
		return fmt.Errorf("smd-tls-cert and smd-tls-key must be supplied together")
	}

	if b.intervalSec < MinIntervalSeconds || b.intervalSec > MaxIntervalSeconds {
		return fmt.Errorf("interval must be %d-%d seconds", MinIntervalSeconds, MaxIntervalSeconds)
	}
	b.cfg.Interval = time.Duration(b.intervalSec) * time.Second
	b.cfg.Mode = strings.ToLower(b.cfg.Mode)
	switch b.cfg.Mode {
	case ModeLocal, ModeGateway:
	default:
		return fmt.Errorf("mode must be one of %q, %q", ModeLocal, ModeGateway)
	}
	b.cfg.KubernetesMode = strings.ToLower(b.cfg.KubernetesMode)
	switch b.cfg.KubernetesMode {
	case KubernetesModeAuto, KubernetesModeOn, KubernetesModeOff:
	default:
		return fmt.Errorf("kubernetes-mode must be one of %q, %q, %q", KubernetesModeAuto, KubernetesModeOn, KubernetesModeOff)
	}
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

func detectNodeName(getenv func(string) string, key string, def string) string {
	if v := getenv(key); v != "" {
		return v
	}
	if host, err := os.Hostname(); err == nil && host != "" {
		return host
	}
	return def
}

const (
	KubernetesModeAuto = "auto"
	KubernetesModeOn   = "on"
	KubernetesModeOff  = "off"
)

const (
	ModeLocal   = "local"
	ModeGateway = "gateway"
)
