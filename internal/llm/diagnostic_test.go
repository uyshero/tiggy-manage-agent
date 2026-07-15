package llm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiagnosticProviderConnectionAndSanitizedAuthenticationFailure(t *testing.T) {
	const apiKey = "diagnostic-secret-key"
	const upstreamSecret = "upstream-secret-body"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Method != http.MethodGet {
			t.Fatalf("unexpected provider diagnostic request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer "+apiKey {
			t.Fatalf("expected bearer credential")
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"` + upstreamSecret + `"}}`))
	}))
	defer server.Close()

	result := (DiagnosticService{}).TestProvider(t.Context(), DiagnosticConfig{
		ProviderType: ProviderOpenAICompatible, BaseURL: server.URL + "/v1?token=query-secret",
		APIKey: apiKey, APIKeyConfigured: true,
	})
	if result.Status != DiagnosticStatusFailed || result.ErrorType != DiagnosticErrorAuthentication || result.Retryable {
		t.Fatalf("unexpected authentication result: %+v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{apiKey, upstreamSecret, "query-secret", server.URL} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("diagnostic result leaked secret %q: %s", secret, encoded)
		}
	}
}

func TestDiagnosticEmbeddingProtocolsAndDimensionValidation(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		path     string
		response string
	}{
		{name: "openai", protocol: EmbeddingProtocolOpenAI, path: "/v1/embeddings", response: `{"data":[{"embedding":[0.1,0.2,0.3]}]}`},
		{name: "tei", protocol: EmbeddingProtocolTEI, path: "/v1/embed", response: `[[0.1,0.2,0.3]]`},
		{name: "ollama", protocol: EmbeddingProtocolOllama, path: "/v1/api/embed", response: `{"embeddings":[[0.1,0.2,0.3]]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != test.path || r.Method != http.MethodPost {
					t.Fatalf("unexpected embedding request: %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(test.response))
			}))
			defer server.Close()

			service := DiagnosticService{}
			config := DiagnosticConfig{
				ProviderType: ProviderOpenAICompatible, BaseURL: server.URL + "/v1",
				Model: "embedding-model", CapabilityType: "embedding", Protocol: test.protocol, ExpectedDimensions: 3,
			}
			result := service.TestModel(t.Context(), config)
			if result.Status != DiagnosticStatusSucceeded || result.Dimensions != 3 {
				t.Fatalf("unexpected embedding result: %+v", result)
			}
			config.ExpectedDimensions = 4
			mismatch := service.TestModel(t.Context(), config)
			if mismatch.Status != DiagnosticStatusFailed || mismatch.ErrorType != DiagnosticErrorDimensionMismatch || mismatch.Dimensions != 3 {
				t.Fatalf("unexpected dimension mismatch result: %+v", mismatch)
			}
		})
	}
}

func TestDiagnosticChatAndRerankerRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/chat/completions":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
		case "/v1/rerank":
			_, _ = w.Write([]byte(`{"results":[{"index":0,"relevance_score":0.9},{"index":1,"relevance_score":0.1}]}`))
		case "/v1/score":
			_, _ = w.Write([]byte(`{"data":[{"index":0,"score":0.9},{"index":1,"score":0.1}]}`))
		default:
			t.Fatalf("unexpected model diagnostic path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	service := DiagnosticService{}
	base := DiagnosticConfig{ProviderType: ProviderOpenAICompatible, BaseURL: server.URL + "/v1", Model: "test-model"}
	chat := base
	chat.CapabilityType = "text"
	if result := service.TestModel(t.Context(), chat); result.Status != DiagnosticStatusSucceeded {
		t.Fatalf("unexpected chat result: %+v", result)
	}
	for _, protocol := range []string{RerankProtocolJina, RerankProtocolCohere, RerankProtocolVLLM} {
		config := base
		config.CapabilityType = "reranker"
		config.Protocol = protocol
		result := service.TestModel(t.Context(), config)
		if result.Status != DiagnosticStatusSucceeded || result.CandidateCount != 2 {
			t.Fatalf("unexpected reranker result for %s: %+v", protocol, result)
		}
	}
}

func TestDiagnosticMissingConfiguredCredentialDoesNotCallProvider(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer server.Close()

	result := (DiagnosticService{}).TestProvider(t.Context(), DiagnosticConfig{
		ProviderType: ProviderOpenAICompatible, BaseURL: server.URL, APIKeyConfigured: true,
	})
	if result.Status != DiagnosticStatusFailed || result.ErrorType != DiagnosticErrorConfiguration || called {
		t.Fatalf("unexpected missing credential result: %+v called=%t", result, called)
	}
}
