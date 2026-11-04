package daemon

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Stable across exporter restarts, shared by clients of one daemon. A different
// metric composition must use a new id instead of changing an existing group.
const DefaultMetricGroupID = "9491461b-5a6d-4df2-9e44-5a91e58eb8ef"

type Options struct {
	MetricGroupID     string
	CreateMetricGroup bool
	PCIDevicesPath    string
	TLSCA             string
	TLSCert           string
	TLSKey            string
}

func DefaultOptions() Options {
	return Options{MetricGroupID: DefaultMetricGroupID, CreateMetricGroup: true}
}

func transportCredentials(o Options) (credentials.TransportCredentials, error) {
	if (o.TLSCert == "") != (o.TLSKey == "") {
		return nil, fmt.Errorf("SMD TLS certificate and key must be supplied together")
	}
	if o.TLSCA == "" && o.TLSCert == "" {
		return insecure.NewCredentials(), nil
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if o.TLSCA != "" {
		pem, err := os.ReadFile(o.TLSCA)
		if err != nil {
			return nil, fmt.Errorf("read SMD TLS CA: %w", err)
		}
		cfg.RootCAs = x509.NewCertPool()
		if !cfg.RootCAs.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("SMD TLS CA contains no certificates")
		}
	}
	if o.TLSCert != "" {
		cert, err := tls.LoadX509KeyPair(o.TLSCert, o.TLSKey)
		if err != nil {
			return nil, fmt.Errorf("load SMD client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return credentials.NewTLS(cfg), nil
}
