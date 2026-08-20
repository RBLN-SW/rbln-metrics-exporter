package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/collector"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/gateway"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/scheduler"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/server"
	"github.com/spf13/cobra"
)

func NewApp() *cobra.Command {
	builder := newConfigBuilder(os.Getenv)

	cmd := &cobra.Command{
		Use:           "rbln-metrics-exporter",
		Short:         "Expose RBLN device metrics via Prometheus",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := builder.finalize(); err != nil {
				return err
			}
			return Start(cmd.Context(), builder.cfg)
		},
	}

	builder.bindFlags(cmd.Flags())

	return cmd
}

// Version is stamped at build time via
// -ldflags "-X github.com/rebellions-sw/rbln-metrics-exporter/internal/cmd.Version=...".
var Version = "dev"

// logStartup records the snapshot needed to read the rest of the logs cold:
// which build is running and every resolved knob that shapes its behavior.
func logStartup(cfg Config) {
	slog.Info("Starting rbln-metrics-exporter", "version", Version, "config", cfg)
}

func Start(ctx context.Context, config Config) error {
	logStartup(config)
	if os.Getenv("PROMETHEUS_METRIC_NAMES") != "true" {
		slog.Warn(
			"Legacy metric names are deprecated and will be removed in the next version; set PROMETHEUS_METRIC_NAMES=true to enable the new metric names",
			"env", "PROMETHEUS_METRIC_NAMES",
		)
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer stop()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if config.Mode == ModeGateway {
		return startGateway(ctx, config)
	}

	dClient, err := daemon.NewClient(ctx, config.RBLNDaemonURL)
	if err != nil {
		return err
	}

	metricRegistry := prometheus.NewRegistry()
	isKubernetes := resolveKubernetesMode(config.KubernetesMode)
	var podResourceMapper *collector.PodResourceMapper
	if isKubernetes {
		podResourceMapper, err = collector.NewPodResourceMapper(ctx)
		if err != nil {
			return err
		}
	} else {
		podResourceMapper = collector.NewNoopPodResourceMapper()
	}
	collectorFactory := collector.NewCollectorFactory(
		podResourceMapper,
		metricRegistry,
		dClient,
		config.NodeName,
		isKubernetes,
	)
	collectors := collectorFactory.NewCollectors()

	up := collector.NewUpGauge()
	metricRegistry.MustRegister(up)

	sched := scheduler.NewScheduler(podResourceMapper, collectors, config.Interval, up)
	go sched.Run(ctx)

	server := server.NewMetricServer(metricRegistry, config.Port)
	if err := server.Start(ctx); err != nil {
		return fmt.Errorf("http metrics server stopped: %w", err)
	}

	return nil
}

func startGateway(ctx context.Context, config Config) error {
	handler := gateway.NewHandler()
	defer handler.Close()

	mux := http.NewServeMux()
	mux.Handle("/metrics", handler)

	srv := server.NewServer(mux, config.Port)
	if err := srv.Start(ctx); err != nil {
		return fmt.Errorf("http metrics server stopped: %w", err)
	}

	return nil
}

func resolveKubernetesMode(mode string) bool {
	var kubernetes bool
	switch mode {
	case KubernetesModeOn:
		kubernetes = true
	case KubernetesModeOff:
		kubernetes = false
	default:
		kubernetes = collector.IsKubernetes()
	}
	// The resolved mode decides whether pod attribution exists at all, so it
	// belongs in the info-level startup story, not behind the debug gate.
	slog.Info("Resolved Kubernetes mode", "kubernetesMode", mode, "kubernetes", kubernetes)
	return kubernetes
}
