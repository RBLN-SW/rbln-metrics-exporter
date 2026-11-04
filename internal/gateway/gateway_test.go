package gateway

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

func TestBadRequestsReturnPlainTextWithoutReflectingInput(t *testing.T) {
	payload := `<script>alert("input")</script>`
	for _, tc := range []struct {
		name    string
		target  string
		options daemon.Options
		want    string
	}{
		{name: "invalid address", target: payload, options: daemon.DefaultOptions(), want: "invalid target: expected host:port\n"},
		{name: "client configuration failure", target: payload + ":50051", options: daemon.Options{TLSCA: filepath.Join(t.TempDir(), "private-config.pem")}, want: "unable to initialize SMD client\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHandler(tc.options)
			t.Cleanup(h.Close)
			request := httptest.NewRequest(http.MethodGet, "/metrics?target="+url.QueryEscape(tc.target), nil)
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || response.Body.String() != tc.want {
				t.Fatalf("response: %d %q", response.Code, response.Body.String())
			}
			if !strings.HasPrefix(response.Header().Get("Content-Type"), "text/plain") || response.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("unsafe error headers: %v", response.Header())
			}
			if strings.Contains(response.Body.String(), payload) || strings.Contains(response.Body.String(), tc.options.TLSCA) && tc.options.TLSCA != "" {
				t.Fatal("input or internal configuration reflected in error response")
			}
			if len(h.clients) != 0 {
				t.Fatal("rejected request cached a client")
			}
		})
	}
}
