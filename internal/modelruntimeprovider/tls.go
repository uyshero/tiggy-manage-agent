package modelruntimeprovider

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	TLSModeDisabled    = "disabled"
	TLSModeMTLS        = "mtls"
	TLSModeServiceMesh = "service_mesh"
)

type ClientTLSConfig struct {
	CAFile          string
	CertificateFile string
	KeyFile         string
	ServerName      string
}

func NewHTTPClient(timeout time.Duration, config ClientTLSConfig) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: strings.TrimSpace(config.ServerName)}
	if strings.TrimSpace(config.CAFile) != "" {
		roots, err := loadCertificatePool(config.CAFile, true)
		if err != nil {
			return nil, fmt.Errorf("configure model runtime server CA: %w", err)
		}
		tlsConfig.RootCAs = roots
	}
	certFile, keyFile := strings.TrimSpace(config.CertificateFile), strings.TrimSpace(config.KeyFile)
	if (certFile == "") != (keyFile == "") {
		return nil, errors.New("model runtime TLS client certificate and key must be configured together")
	}
	if certFile != "" {
		certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("load model runtime TLS client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{certificate}
	}
	transport.TLSClientConfig = tlsConfig
	return &http.Client{Transport: transport, Timeout: timeout}, nil
}

func NewServerTLSConfig(clientCAFile string) (*tls.Config, error) {
	clientCAs, err := loadCertificatePool(clientCAFile, false)
	if err != nil {
		return nil, fmt.Errorf("configure model runtime client CA: %w", err)
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  clientCAs,
	}, nil
}

func loadCertificatePool(path string, includeSystem bool) (*x509.CertPool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("certificate authority file is required")
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pool *x509.CertPool
	if includeSystem {
		pool, err = x509.SystemCertPool()
	}
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("certificate authority file contains no valid PEM certificates")
	}
	return pool, nil
}
