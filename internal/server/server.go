package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type MetricServer struct {
	server *http.Server
}

func NewMetricServer(gatherer prometheus.Gatherer, port int) *MetricServer {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{}))
	return NewServer(mux, port)
}

func NewServer(handler http.Handler, port int) *MetricServer {
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		// Without ErrorLog, net/http's internal records (handler panics,
		// protocol errors) reach slog through the stdlib-log bridge at info,
		// invisible to level-based alerting.
		ErrorLog: slog.NewLogLogger(slog.Default().Handler(), slog.LevelError),
	}

	return &MetricServer{
		server: server,
	}
}

func (ms *MetricServer) Start(ctx context.Context) error {
	serverErr := make(chan error, 1)
	go func() {
		if err := ms.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = ms.server.Shutdown(shutdownCtx)
		return <-serverErr
	}
}
