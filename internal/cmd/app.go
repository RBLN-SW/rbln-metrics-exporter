package cmd

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/collector"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
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

func Start(ctx context.Context, config Config) error {
	slog.Info("Starting rbln-metrics-exporter", "config", config)
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer stop()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	dClient, err := daemon.NewClient(ctx, config.RBLNDaemonURL)
	if err != nil {
		return err
	}

	metricRegistry := prometheus.NewRegistry()
	podResourceMapper, err := collector.NewPodResourceMapper(ctx)
	if err != nil {
		return err
	}
	collectorFactory := collector.NewCollectorFactory(podResourceMapper, metricRegistry, dClient, config.NodeName)
	collectors := collectorFactory.NewCollectors()

	sched := scheduler.NewScheduler(podResourceMapper, collectors, config.Interval)
	go sched.Run(ctx)

	server := server.NewMetricServer(metricRegistry, config.Port)
	if err := server.Start(ctx); err != nil {
		slog.Error("http metrics server stopped", "err", err)
		return err
	}

	return nil
}
