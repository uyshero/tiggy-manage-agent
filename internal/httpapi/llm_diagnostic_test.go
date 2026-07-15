package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/runner"
)

func TestLLMDiagnosticEndpointsAndAuditSanitization(t *testing.T) {
	const envName = "TMA_TEST_LLM_DIAGNOSTIC_KEY"
	const apiKey = "do-not-return-diagnostic-key"
	t.Setenv(envName, apiKey)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+apiKey {
			t.Fatalf("diagnostic request did not resolve configured API key")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case "/v1/embeddings":
			_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2,0.3]}]}`))
		default:
			t.Fatalf("unexpected diagnostic path: %s", r.URL.Path)
		}
	}))
	defer upstream.Close()

	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)
	postJSON[managedagents.LLMProvider](t, server, "/v1/llm-providers", `{
		"id":"diagnostic-provider","provider_type":"openai-compatible",
		"base_url":"`+upstream.URL+`/v1?private=query-secret","api_key_env":"`+envName+`"
	}`)
	createLLMModel(t, server, `{
		"provider_id":"diagnostic-provider","model":"embedding-model","context_window_tokens":8192,
		"capability_type":"embedding","capabilities":{"dimensions":3,"distance_metric":"cosine","normalized":true,"max_batch_size":32,"protocol":"openai_embeddings"}
	}`)

	providerResult := postDiagnostic(t, server, "/v1/llm-providers/diagnostic-provider/test")
	if providerResult.Status != llm.DiagnosticStatusSucceeded || !providerResult.Authenticated {
		t.Fatalf("unexpected provider diagnostic: %+v", providerResult)
	}
	modelResult := postDiagnostic(t, server, "/v2/llm-models/diagnostic-provider/embedding-model/test")
	if modelResult.Status != llm.DiagnosticStatusSucceeded || modelResult.Dimensions != 3 || modelResult.Protocol != llm.EmbeddingProtocolOpenAI {
		t.Fatalf("unexpected model diagnostic: %+v", modelResult)
	}

	audits, err := store.ListOperatorAudit(managedagents.ListOperatorAuditInput{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	var diagnosticAudits int
	for _, audit := range audits {
		if audit.Action != "llm.provider.test" && audit.Action != "llm.model.test" {
			continue
		}
		diagnosticAudits++
		encoded, _ := json.Marshal(audit)
		for _, secret := range []string{apiKey, "query-secret", upstream.URL} {
			if strings.Contains(string(encoded), secret) {
				t.Fatalf("diagnostic audit leaked %q: %s", secret, encoded)
			}
		}
	}
	if diagnosticAudits != 2 {
		t.Fatalf("expected two diagnostic audits, got %d", diagnosticAudits)
	}
}

func postDiagnostic(t *testing.T, handler http.Handler, path string) llm.DiagnosticResult {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("POST %s expected 200, got %d: %s", path, response.Code, response.Body.String())
	}
	var result llm.DiagnosticResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode diagnostic result: %v", err)
	}
	return result
}
