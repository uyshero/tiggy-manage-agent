package modelruntimeprovider

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tiggy-manage-agent/internal/llm"
)

func TestHTTPExecutorUsesMutuallyAuthenticatedTLS(t *testing.T) {
	certificates := writeRuntimeTLSFixture(t)
	handler, err := NewHandler(HandlerConfig{AuthToken: "runtime-secret", Executor: stubExecutor{
		generate: func(context.Context, GenerateRequest) (llm.Response, error) {
			return llm.Response{FinishReason: "stop"}, nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	serverTLS, err := NewServerTLSConfig(certificates.caFile)
	if err != nil {
		t.Fatal(err)
	}
	serverCertificate, err := tls.LoadX509KeyPair(certificates.serverCertFile, certificates.serverKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	serverTLS.Certificates = []tls.Certificate{serverCertificate}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = serverTLS
	server.StartTLS()
	defer server.Close()

	client, err := NewHTTPClient(5*time.Second, ClientTLSConfig{
		CAFile: certificates.caFile, CertificateFile: certificates.clientCertFile,
		KeyFile: certificates.clientKeyFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	executor, err := NewHTTPExecutor(server.URL, "runtime-secret", client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Generate(t.Context(), GenerateRequest{Route: Route{ProviderID: "p", Model: "m"}}); err != nil {
		t.Fatal(err)
	}

	clientWithoutCertificate, err := NewHTTPClient(5*time.Second, ClientTLSConfig{CAFile: certificates.caFile})
	if err != nil {
		t.Fatal(err)
	}
	unauthenticated, err := NewHTTPExecutor(server.URL, "runtime-secret", clientWithoutCertificate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unauthenticated.Generate(t.Context(), GenerateRequest{Route: Route{ProviderID: "p", Model: "m"}}); err == nil {
		t.Fatal("runtime mTLS server accepted a client without a certificate")
	}
}

func TestHTTPClientRequiresCompleteClientKeyPair(t *testing.T) {
	if _, err := NewHTTPClient(time.Second, ClientTLSConfig{CertificateFile: "/tmp/client.crt"}); err == nil {
		t.Fatal("expected incomplete client certificate configuration to fail")
	}
}

type runtimeTLSFixture struct {
	caFile         string
	serverCertFile string
	serverKeyFile  string
	clientCertFile string
	clientKeyFile  string
}

func writeRuntimeTLSFixture(t *testing.T) runtimeTLSFixture {
	t.Helper()
	directory := t.TempDir()
	now := time.Now().UTC()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "TMA Runtime Test CA"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caFile := filepath.Join(directory, "ca.crt")
	writePEMFile(t, caFile, "CERTIFICATE", caDER)

	issue := func(name string, serial int64, usages []x509.ExtKeyUsage, dnsNames []string, addresses []net.IP) (string, string) {
		t.Helper()
		key, keyErr := rsa.GenerateKey(rand.Reader, 2048)
		if keyErr != nil {
			t.Fatal(keyErr)
		}
		template := &x509.Certificate{
			SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name},
			NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
			KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			ExtKeyUsage: usages, DNSNames: dnsNames, IPAddresses: addresses,
		}
		certificateDER, createErr := x509.CreateCertificate(rand.Reader, template, caTemplate, &key.PublicKey, caKey)
		if createErr != nil {
			t.Fatal(createErr)
		}
		certificateFile := filepath.Join(directory, name+".crt")
		keyFile := filepath.Join(directory, name+".key")
		writePEMFile(t, certificateFile, "CERTIFICATE", certificateDER)
		writePEMFile(t, keyFile, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key))
		return certificateFile, keyFile
	}
	serverCertFile, serverKeyFile := issue("server", 2, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
	clientCertFile, clientKeyFile := issue("client", 3, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, nil, nil)
	return runtimeTLSFixture{
		caFile: caFile, serverCertFile: serverCertFile, serverKeyFile: serverKeyFile,
		clientCertFile: clientCertFile, clientKeyFile: clientKeyFile,
	}
}

func writePEMFile(t *testing.T, path, blockType string, bytes []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: bytes}), 0o600); err != nil {
		t.Fatal(err)
	}
}
