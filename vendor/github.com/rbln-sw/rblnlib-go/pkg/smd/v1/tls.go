package smd

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"

	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
)

// TLSFiles describes server trust and an optional client identity. An empty
// CAFile uses system trust; CertFile and KeyFile must be supplied together.
type TLSFiles struct{ CAFile, CertFile, KeyFile, ServerName string }

// LoadTLSConfig always returns TLS (or an error), never a plaintext fallback.
// Pass nil to Options.TLS only when plaintext is the deployment's explicit mode.
func LoadTLSConfig(files TLSFiles) (*tls.Config, error) {
	if (files.CertFile == "") != (files.KeyFile == "") {
		return nil, &system.Error{Kind: system.InvalidArgument, Message: "SMD TLS certificate and key must be supplied together"}
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: files.ServerName}
	failure := func(op string, err error) error {
		kind := system.InvalidData
		if errors.Is(err, os.ErrNotExist) {
			kind = system.NotFound
		} else if errors.Is(err, os.ErrPermission) {
			kind = system.PermissionDenied
		}
		return &system.Error{Kind: kind, Op: op, Cause: err}
	}
	if files.CAFile != "" {
		pem, err := os.ReadFile(files.CAFile)
		if err != nil {
			return nil, failure("SMD TLS CA", err)
		}
		cfg.RootCAs = x509.NewCertPool()
		if !cfg.RootCAs.AppendCertsFromPEM(pem) {
			return nil, &system.Error{Kind: system.InvalidData, Message: "SMD TLS CA has no certificates"}
		}
	}
	if files.CertFile != "" {
		cert, err := tls.LoadX509KeyPair(files.CertFile, files.KeyFile)
		if err != nil {
			return nil, failure("SMD TLS client identity", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}
