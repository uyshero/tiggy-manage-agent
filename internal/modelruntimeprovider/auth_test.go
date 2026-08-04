package modelruntimeprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/llm"
)

const testRuntimeSigningSecret = "runtime-signing-secret-with-32-bytes-minimum"

func TestSignedRuntimeAuthenticationRoundTrip(t *testing.T) {
	auth := AuthConfig{
		Mode: AuthModeSigned, Secret: testRuntimeSigningSecret,
		Issuer: "platform-test", Audience: "runtime-test", TokenTTL: time.Minute,
	}
	handler, err := NewHandler(HandlerConfig{Auth: auth, Executor: stubExecutor{
		generate: func(context.Context, GenerateRequest) (llm.Response, error) {
			return llm.Response{FinishReason: "stop"}, nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	executor, err := NewHTTPExecutorWithAuth(server.URL, auth, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Generate(t.Context(), GenerateRequest{Route: Route{ProviderID: "p", Model: "m"}}); err != nil {
		t.Fatal(err)
	}
}

func TestSignedRuntimeTokenIsBoundToMethodAndPath(t *testing.T) {
	authenticator, err := newRequestAuthenticator(AuthConfig{
		Mode: AuthModeSigned, Secret: testRuntimeSigningSecret,
		Issuer: "platform-test", Audience: "runtime-test", TokenTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := authenticator.authorization(http.MethodPost, "/internal/v1/generate")
	if err != nil {
		t.Fatal(err)
	}
	valid := httptest.NewRequest(http.MethodPost, "/internal/v1/generate", nil)
	valid.Header.Set("Authorization", authorization)
	if !authenticator.verifyRequest(valid) {
		t.Fatal("signed token should authenticate its bound request")
	}
	wrongPath := httptest.NewRequest(http.MethodPost, "/internal/v1/embeddings", nil)
	wrongPath.Header.Set("Authorization", authorization)
	if authenticator.verifyRequest(wrongPath) {
		t.Fatal("signed token must not authenticate a different path")
	}
	wrongMethod := httptest.NewRequest(http.MethodGet, "/internal/v1/generate", nil)
	wrongMethod.Header.Set("Authorization", authorization)
	if authenticator.verifyRequest(wrongMethod) {
		t.Fatal("signed token must not authenticate a different method")
	}
}

func TestSignedRuntimeTokenExpiresAndRejectsTampering(t *testing.T) {
	now := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	authenticator, err := newRequestAuthenticator(AuthConfig{
		Mode: AuthModeSigned, Secret: testRuntimeSigningSecret,
		Issuer: "platform-test", Audience: "runtime-test", TokenTTL: 30 * time.Second,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := authenticator.authorization(http.MethodPost, "/internal/v1/generate")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/generate", nil)
	request.Header.Set("Authorization", authorization)
	now = now.Add(31 * time.Second)
	if authenticator.verifyRequest(request) {
		t.Fatal("expired signed token must be rejected")
	}
	now = now.Add(-31 * time.Second)
	request.Header.Set("Authorization", authorization+"x")
	if authenticator.verifyRequest(request) {
		t.Fatal("tampered signed token must be rejected")
	}
}

func TestSignedRuntimeAuthenticationValidatesConfiguration(t *testing.T) {
	tests := []AuthConfig{
		{Mode: AuthModeSigned, Secret: "short"},
		{Mode: AuthModeSigned, Secret: testRuntimeSigningSecret, TokenTTL: 301 * time.Second},
		{Mode: "unknown", Secret: strings.Repeat("s", 32)},
	}
	for _, config := range tests {
		if _, err := newRequestAuthenticator(config); err == nil {
			t.Fatalf("expected invalid auth configuration: %+v", config)
		}
	}
}
