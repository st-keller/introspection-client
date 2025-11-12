// Package transport provides HTTP/2 client with mTLS 1.3.
// Note: HTTP/3 deferred until libraries reach production maturity (see technical-debt.md).
package transport

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"

	"golang.org/x/net/http2"
)

// BuildHTTP2Client creates an HTTP/2 client with mTLS 1.3.
// Note: Previously BuildHTTP3Client - downgraded due to kernel UDP buffer limits.
func BuildHTTP2Client(certPath, keyPath, caPath string) (*http.Client, error) {
	if certPath == "" {
		return nil, fmt.Errorf("certPath required")
	}
	if keyPath == "" {
		return nil, fmt.Errorf("keyPath required")
	}
	if caPath == "" {
		return nil, fmt.Errorf("caPath required")
	}

	// Auto-detect CA chain (ADR-013: production uses ca-chain.cert.pem)
	actualCAPath := caPath
	caDir := "/certs"
	caChainPath := caDir + "/ca-chain.cert.pem"
	if _, err := os.Stat(caChainPath); err == nil {
		actualCAPath = caChainPath
	}

	// Load client certificate
	clientCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load client certificate: %w", err)
	}

	// Load CA certificate
	caCert, err := os.ReadFile(actualCAPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate: %w", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}

	// mTLS 1.3 configuration
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      caCertPool,
		MinVersion:   tls.VersionTLS13, // Enforce TLS 1.3
		MaxVersion:   tls.VersionTLS13,
	}

	// HTTP/2 transport with mTLS
	transport := &http2.Transport{
		TLSClientConfig: tlsConfig,
	}

	client := &http.Client{
		Transport: transport,
	}

	return client, nil
}
