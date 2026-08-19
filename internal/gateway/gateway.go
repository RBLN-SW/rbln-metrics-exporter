package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/collector"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

const (
	defaultScrapeTimeout = 10 * time.Second
	// Subtracted from the Prometheus-provided timeout so the gateway answers
	// before Prometheus aborts the scrape (cf. blackbox_exporter --timeout-offset).
	timeoutOffset    = 500 * time.Millisecond
	minScrapeTimeout = time.Second
)

type Handler struct {
	mu      sync.Mutex
	clients map[string]*daemon.Client
}

func NewHandler() *Handler {
	return &Handler{
		clients: make(map[string]*daemon.Client),
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target := stripSchemePrefix(r.URL.Query().Get("target"))
	if target == "" {
		http.Error(w, "missing required parameter: target=<host:port> of an rbln-smd grpc endpoint", http.StatusBadRequest)
		return
	}
	if _, _, err := net.SplitHostPort(target); err != nil {
		http.Error(w, fmt.Sprintf("invalid target %q: expected <host:port>: %v", target, err), http.StatusBadRequest)
		return
	}

	client, err := h.clientFor(target)
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid target %q: %v", target, err), http.StatusBadRequest)
		return
	}

	registry := prometheus.NewRegistry()
	up := collector.NewUpGauge()
	registry.MustRegister(up)

	collectorFactory := collector.NewCollectorFactory(
		collector.NewNoopPodResourceMapper(),
		registry,
		client,
		hostLabel(target),
		false,
	)

	ctx, cancel := context.WithTimeout(r.Context(), scrapeTimeout(r))
	defer cancel()

	up.Set(1)
	for _, c := range collectorFactory.NewCollectors() {
		if err := c.GetMetrics(ctx); err != nil {
			slog.Warn("Gateway collect failed", "target", target, "err", err,
				"effect", "scrape returns rbln_up 0")
			up.Set(0)
			break
		}
	}

	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(w, r)
}

func (h *Handler) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for target, client := range h.clients {
		if err := client.Close(); err != nil {
			slog.Warn("Failed to close daemon client", "target", target, "err", err,
				"effect", "connection may leak until process exit")
		}
	}
	clear(h.clients)
}

func (h *Handler) clientFor(target string) (*daemon.Client, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if client, ok := h.clients[target]; ok {
		return client, nil
	}
	client, err := daemon.NewLazyClient(target)
	if err != nil {
		return nil, err
	}
	h.clients[target] = client
	return client, nil
}

func hostLabel(target string) string {
	if host, _, err := net.SplitHostPort(target); err == nil {
		return host
	}
	return target
}

func scrapeTimeout(r *http.Request) time.Duration {
	if v := r.Header.Get("X-Prometheus-Scrape-Timeout-Seconds"); v != "" {
		if seconds, err := strconv.ParseFloat(v, 64); err == nil && seconds > 0 {
			// Undercut Prometheus's own deadline so a hung target still yields
			// a served response with rbln_up 0 (up==1), not a failed scrape (up==0).
			timeout := time.Duration(seconds*float64(time.Second)) - timeoutOffset
			return max(timeout, minScrapeTimeout)
		}
	}
	return defaultScrapeTimeout
}

func stripSchemePrefix(addr string) string {
	if after, ok := strings.CutPrefix(addr, "http://"); ok {
		return after
	}
	if after, ok := strings.CutPrefix(addr, "https://"); ok {
		return after
	}
	return addr
}
