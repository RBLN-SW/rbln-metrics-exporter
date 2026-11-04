package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/logging"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/smdtest"
)

// syncBuffer lets a test read logs while Start's goroutines write them.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func captureLogs(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	logger, err := logging.New(buf, "info", "json")
	if err != nil {
		t.Fatalf("logging.New: %v", err)
	}
	old := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(old) })
	return buf
}

func lastRecord(t *testing.T, buf *syncBuffer) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(buf.String()), &m); err != nil {
		t.Fatalf("not JSON: %v: %s", err, buf.String())
	}
	return m
}

func TestLogStartupEmitsVersionAndReadableConfig(t *testing.T) {
	buf := captureLogs(t)
	logStartup(Config{
		Mode:           ModeLocal,
		RBLNDaemonURL:  "127.0.0.1:50051",
		Port:           9090,
		Interval:       5 * time.Second,
		NodeName:       "node-1",
		KubernetesMode: KubernetesModeAuto,
	})

	m := lastRecord(t, buf)
	if m["msg"] != "Starting rbln-metrics-exporter" {
		t.Fatalf("msg = %v", m["msg"])
	}
	if m["version"] != Version {
		t.Fatalf("version = %v, want %q", m["version"], Version)
	}
	cfg, ok := m["config"].(map[string]any)
	if !ok {
		t.Fatalf("config not a group: %v", m["config"])
	}
	for key, want := range map[string]any{
		"mode":           "local",
		"daemon":         "127.0.0.1:50051",
		"port":           float64(9090),
		"interval":       "5s",
		"node":           "node-1",
		"kubernetesMode": "auto",
	} {
		if cfg[key] != want {
			t.Fatalf("config.%s = %v, want %v", key, cfg[key], want)
		}
	}
}

func TestResolveKubernetesModeLogsResolution(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want bool
	}{
		{KubernetesModeOn, true},
		{KubernetesModeOff, false},
	} {
		buf := captureLogs(t)
		if got := resolveKubernetesMode(tc.mode); got != tc.want {
			t.Fatalf("resolveKubernetesMode(%q) = %v, want %v", tc.mode, got, tc.want)
		}
		m := lastRecord(t, buf)
		if m["level"] != "info" || m["msg"] != "Resolved Kubernetes mode" {
			t.Fatalf("record = %v, want info resolution record", m)
		}
		if m["kubernetesMode"] != tc.mode || m["kubernetes"] != tc.want {
			t.Fatalf("kubernetesMode/kubernetes = %v/%v, want %v/%v", m["kubernetesMode"], m["kubernetes"], tc.mode, tc.want)
		}
	}
}

func freeTCPAddr(t *testing.T) *net.TCPAddr {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer func() { _ = lis.Close() }()
	return lis.Addr().(*net.TCPAddr)
}

func freeAddr(t *testing.T) string { return freeTCPAddr(t).String() }

func freePort(t *testing.T) int { return freeTCPAddr(t).Port }

// serveDaemon brings a fake rbln-smd up on addr and returns its stop func.
func serveDaemon(t *testing.T, addr string, transport *smdtest.TLS) func() {
	t.Helper()
	_, stop := smdtest.Serve(t, &smdtest.Server{}, addr, transport)
	t.Cleanup(stop)
	return stop
}

func startExporter(t *testing.T, cfg Config) <-chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Start(ctx, cfg) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Start returned %v after cancel, want nil", err)
			}
		case <-time.After(10 * time.Second):
			t.Errorf("Start did not return after cancel")
		}
	})
	return done
}

// eventually polls cond until it holds, failing if Start returns first: the
// exporter must ride out an absent daemon, not exit.
func eventually(t *testing.T, done <-chan error, what string, cond func() (ok bool, last string)) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("Start returned %v while waiting for %s", err, what)
		default:
		}
		var ok bool
		if ok, last = cond(); ok {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s not observed within deadline; last: %s", what, last)
}

var scrapeClient = &http.Client{Timeout: 5 * time.Second}

func waitForUp(t *testing.T, url string, want int, done <-chan error) {
	t.Helper()
	line := fmt.Sprintf("\nrbln_up %d\n", want)
	eventually(t, done, fmt.Sprintf("rbln_up %d", want), func() (bool, string) {
		resp, err := scrapeClient.Get(url)
		if err != nil {
			return false, err.Error()
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		ok := resp.StatusCode == http.StatusOK && strings.Contains(string(body), line)
		return ok, fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, body)
	})
}

func localConfig(daemonAddr string, port int, caFile string) Config {
	return Config{
		Mode:           ModeLocal,
		RBLNDaemonURL:  daemonAddr,
		Port:           port,
		Interval:       time.Second,
		NodeName:       "node-1",
		KubernetesMode: KubernetesModeOff,
		SMD:            daemon.Options{TLSCA: caFile},
	}
}

func TestStartWaitsForDaemonThatComesUpLater(t *testing.T) {
	logs := captureLogs(t)
	transport := smdtest.NewTLS(t)
	daemonAddr := freeAddr(t)
	port := freePort(t)
	url := fmt.Sprintf("http://127.0.0.1:%d/metrics", port)
	done := startExporter(t, localConfig(daemonAddr, port, transport.CAFile))

	// No daemon yet: /metrics answers, reporting the daemon as unreachable,
	// and collects keep failing without ending the process.
	waitForUp(t, url, 0, done)
	eventually(t, done, "a failed collect", func() (bool, string) {
		l := logs.String()
		return strings.Contains(l, `"msg":"Waiting for rbln-smd, retrying"`), l
	})

	serveDaemon(t, daemonAddr, transport)
	waitForUp(t, url, 1, done)

	if l := logs.String(); strings.Contains(l, "Metrics collection failed") {
		t.Fatalf("start-up wait reported as an outage: %s", l)
	}
}

func TestStartRecoversWhenDaemonRestarts(t *testing.T) {
	transport := smdtest.NewTLS(t)
	daemonAddr := freeAddr(t)
	port := freePort(t)
	url := fmt.Sprintf("http://127.0.0.1:%d/metrics", port)
	stop := serveDaemon(t, daemonAddr, transport)
	done := startExporter(t, localConfig(daemonAddr, port, transport.CAFile))
	waitForUp(t, url, 1, done)

	stop()
	waitForUp(t, url, 0, done)

	serveDaemon(t, daemonAddr, transport)
	waitForUp(t, url, 1, done)
}

func TestStartGatewayReportsAbsentTargetThenRecovers(t *testing.T) {
	transport := smdtest.NewTLS(t)
	daemonAddr := freeAddr(t)
	port := freePort(t)
	url := fmt.Sprintf("http://127.0.0.1:%d/metrics?target=%s", port, daemonAddr)
	done := startExporter(t, Config{Mode: ModeGateway, Port: port, SMD: daemon.Options{TLSCA: transport.CAFile}})

	waitForUp(t, url, 0, done)

	serveDaemon(t, daemonAddr, transport)
	waitForUp(t, url, 1, done)
}
