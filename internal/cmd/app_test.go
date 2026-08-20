package cmd

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/rebellions-sw/rbln-metrics-exporter/internal/logging"
)

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	logger, err := logging.New(&buf, "info", "json")
	if err != nil {
		t.Fatalf("logging.New: %v", err)
	}
	old := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

func lastRecord(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
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
		"mode":            "local",
		"daemon":          "127.0.0.1:50051",
		"port":            float64(9090),
		"interval":        "5s",
		"node":            "node-1",
		"kubernetes_mode": "auto",
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
		if m["mode"] != tc.mode || m["kubernetes"] != tc.want {
			t.Fatalf("mode/kubernetes = %v/%v, want %v/%v", m["mode"], m["kubernetes"], tc.mode, tc.want)
		}
	}
}
