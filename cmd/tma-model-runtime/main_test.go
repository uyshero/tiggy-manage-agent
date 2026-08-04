package main

import (
	"strings"
	"testing"

	modelruntime "tiggy-manage-agent/internal/modelruntimeprovider"
)

func TestRuntimeProductionRequiresSignedProtectedTransport(t *testing.T) {
	clearRuntimeEnvironment(t)
	t.Setenv("TMA_ENV", "production")
	t.Setenv("TMA_MODEL_RUNTIME_AUTH_TOKEN", strings.Repeat("s", 32))
	if _, err := runtimeConfigFromEnv(); err == nil || !strings.Contains(err.Error(), "AUTH_MODE") {
		t.Fatalf("expected signed authentication requirement, got %v", err)
	}
	t.Setenv("TMA_MODEL_RUNTIME_AUTH_MODE", modelruntime.AuthModeSigned)
	if _, err := runtimeConfigFromEnv(); err == nil || !strings.Contains(err.Error(), "TLS_MODE") {
		t.Fatalf("expected protected transport requirement, got %v", err)
	}
	t.Setenv("TMA_MODEL_RUNTIME_TLS_MODE", modelruntime.TLSModeServiceMesh)
	config, err := runtimeConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if config.auth.Mode != modelruntime.AuthModeSigned || config.tlsMode != modelruntime.TLSModeServiceMesh {
		t.Fatalf("unexpected production runtime config: %+v", config)
	}
}

func TestRuntimeMTLSRequiresCertificates(t *testing.T) {
	clearRuntimeEnvironment(t)
	t.Setenv("TMA_MODEL_RUNTIME_AUTH_TOKEN", "runtime-secret")
	t.Setenv("TMA_MODEL_RUNTIME_TLS_MODE", modelruntime.TLSModeMTLS)
	if _, err := runtimeConfigFromEnv(); err == nil || !strings.Contains(err.Error(), "server certificate") {
		t.Fatalf("expected mTLS certificate validation error, got %v", err)
	}
}

func TestRuntimeRejectsInvalidBackpressureThreshold(t *testing.T) {
	clearRuntimeEnvironment(t)
	t.Setenv("TMA_MODEL_RUNTIME_AUTH_TOKEN", "runtime-secret")
	t.Setenv("TMA_MODEL_RUNTIME_BACKPRESSURE_THRESHOLD_MS", "0")
	if _, err := runtimeConfigFromEnv(); err == nil || !strings.Contains(err.Error(), "BACKPRESSURE_THRESHOLD") {
		t.Fatalf("expected backpressure threshold validation error, got %v", err)
	}
}

func clearRuntimeEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"TMA_ENV", "TMA_MODEL_RUNTIME_HTTP_ADDR", "TMA_MODEL_RUNTIME_HEALTH_HTTP_ADDR",
		"TMA_MODEL_RUNTIME_AUTH_MODE", "TMA_MODEL_RUNTIME_AUTH_TOKEN",
		"TMA_MODEL_RUNTIME_TOKEN_ISSUER", "TMA_MODEL_RUNTIME_TOKEN_AUDIENCE",
		"TMA_MODEL_RUNTIME_TOKEN_TTL_SECONDS", "TMA_MODEL_RUNTIME_TLS_MODE",
		"TMA_MODEL_RUNTIME_TLS_SERVER_CERT_FILE", "TMA_MODEL_RUNTIME_TLS_SERVER_KEY_FILE",
		"TMA_MODEL_RUNTIME_TLS_CLIENT_CA_FILE", "TMA_MODEL_RUNTIME_BACKPRESSURE_THRESHOLD_MS",
	} {
		t.Setenv(key, "")
	}
}
