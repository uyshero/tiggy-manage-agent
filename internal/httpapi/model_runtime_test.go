package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	modelruntime "tiggy-manage-agent/internal/modelruntimeprovider"
	"tiggy-manage-agent/internal/runner"
)

type recordingModelRuntimeExecutor struct {
	generate func(context.Context, modelruntime.GenerateRequest) (llm.Response, error)
}

func (e recordingModelRuntimeExecutor) Generate(ctx context.Context, request modelruntime.GenerateRequest) (llm.Response, error) {
	return e.generate(ctx, request)
}

func (recordingModelRuntimeExecutor) Embed(context.Context, modelruntime.EmbeddingRequest) (llm.EmbeddingResponse, error) {
	return llm.EmbeddingResponse{}, nil
}

func (recordingModelRuntimeExecutor) Rerank(context.Context, modelruntime.RerankRequest) ([]llm.RerankResult, error) {
	return nil, nil
}

func TestModelRuntimeGenerateUsesConfiguredExecutor(t *testing.T) {
	store := newTestStore()
	executor := recordingModelRuntimeExecutor{generate: func(_ context.Context, request modelruntime.GenerateRequest) (llm.Response, error) {
		if request.Route.ProviderID != "fake" || request.Route.ProviderType != "fake" || request.Route.Model != "fake-demo" {
			t.Fatalf("unexpected approved route: %+v", request.Route)
		}
		return llm.Response{
			Message: llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: "remote answer"}}},
			Usage:   llm.Usage{TotalTokens: 4}, FinishReason: "stop",
		}, nil
	}}
	server := &Server{
		store: store, logger: slog.Default(), defaultLLMProvider: "fake", defaultLLMModel: "fake-demo",
		modelRuntimeAdmission: newModelRuntimeAdmission(DefaultModelRuntimePolicy()), modelRuntimeExecutor: executor,
	}
	handler := http.HandlerFunc(server.withV2Request(server.generateModelRuntimeText))
	response := postJSONWithStatus[modelRuntimeGenerateResponse](t, handler, http.MethodPost, "/v2/model-runtime/generate", `{
		"messages":[{"role":"user","content":"hello"}]
	}`, http.StatusOK)
	if response.Text != "remote answer" || response.Usage.TotalTokens != 4 {
		t.Fatalf("unexpected response: %+v", response)
	}
	if len(store.modelInvocations) != 1 || store.modelInvocations[0].Status != managedagents.ModelInvocationStatusCompleted {
		t.Fatalf("remote invocation was not audited: %+v", store.modelInvocations)
	}
}

func TestModelRuntimeGenerateUsesPlatformDefault(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreRunnerAndLLMDefaults(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil, "fake", "fake-demo")

	response := postJSONWithStatus[modelRuntimeGenerateResponse](t, server, http.MethodPost, "/v2/model-runtime/generate", `{
		"messages": [{"role":"system","content":"Answer briefly."},{"role":"user","content":"hello runtime"}],
		"max_output_tokens": 200
	}`, http.StatusOK)
	if response.ProviderID != "fake" || response.Model != "fake-demo" {
		t.Fatalf("unexpected model route: %+v", response)
	}
	if response.Text == "" {
		t.Fatal("expected generated text")
	}
	if len(store.modelInvocations) != 1 || store.modelInvocations[0].Capability != managedagents.ModelInvocationCapabilityGenerate ||
		store.modelInvocations[0].Status != managedagents.ModelInvocationStatusCompleted || store.modelInvocations[0].InputItems != 2 {
		t.Fatalf("unexpected generate invocation: %+v", store.modelInvocations)
	}
}

func TestModelRuntimeGenerateValidatesProviderAndCapability(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreRunnerAndLLMDefaults(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil, "fake", "fake-demo")

	assertModelRuntimeError(t, server, `{"messages":[]}`, http.StatusBadRequest, "invalid_request")
	assertModelRuntimeError(t, server, `{"messages":[{"role":"tool","content":"x"}]}`, http.StatusBadRequest, "invalid_request")

	store.providers["disabled"] = managedagents.LLMProvider{ID: "disabled", ProviderType: "fake", Enabled: false}
	store.models[llmModelKey("disabled", "text")] = managedagents.LLMModel{ProviderID: "disabled", Model: "text", CapabilityType: managedagents.LLMModelCapabilityText}
	assertModelRuntimeError(t, server, `{"provider_id":"disabled","model":"text","messages":[{"role":"user","content":"x"}]}`, http.StatusConflict, "model_provider_disabled")

	store.models[llmModelKey("fake", "embed")] = managedagents.LLMModel{ProviderID: "fake", Model: "embed", CapabilityType: managedagents.LLMModelCapabilityEmbedding}
	assertModelRuntimeError(t, server, `{"provider_id":"fake","model":"embed","messages":[{"role":"user","content":"x"}]}`, http.StatusBadRequest, "unsupported_model_capability")
	assertModelRuntimeError(t, server, `{"provider_id":"fake","model":"missing","messages":[{"role":"user","content":"x"}]}`, http.StatusNotFound, "model_not_found")
}

func TestModelRuntimeEmbeddingAndRerankUseCapabilityDefaults(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/embeddings":
			_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[0.1,0.2]},{"index":1,"embedding":[0.3,0.4]}],"usage":{"prompt_tokens":5,"total_tokens":5}}`))
		case "/v1/rerank":
			_, _ = w.Write([]byte(`{"results":[{"index":0,"relevance_score":0.1},{"index":1,"relevance_score":0.9}]}`))
		default:
			t.Fatalf("unexpected upstream path %s", r.URL.Path)
		}
	}))
	defer upstream.Close()

	store := newTestStore()
	store.providers["inference"] = managedagents.LLMProvider{
		ID: "inference", ProviderType: "openai", BaseURL: upstream.URL + "/v1", Enabled: true,
	}
	store.models[llmModelKey("inference", "embed")] = managedagents.LLMModel{
		ProviderID: "inference", Model: "embed", CapabilityType: managedagents.LLMModelCapabilityEmbedding,
		Capabilities:       managedagents.LLMModelCapabilities{Dimensions: 2, MaxBatchSize: 2, Protocol: "openai_embeddings"},
		IsDefaultEmbedding: true,
	}
	store.models[llmModelKey("inference", "rerank")] = managedagents.LLMModel{
		ProviderID: "inference", Model: "rerank", CapabilityType: managedagents.LLMModelCapabilityReranker,
		Capabilities:      managedagents.LLMModelCapabilities{MaxCandidates: 3, Protocol: "jina_rerank"},
		IsDefaultReranker: true,
	}
	server := NewServerWithStoreRunnerAndLLMDefaults(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil, "fake", "fake-demo")

	embeddings := postJSONWithStatus[modelRuntimeEmbeddingResponse](t, server, http.MethodPost, "/v2/model-runtime/embeddings", `{
		"inputs":["first","second"]
	}`, http.StatusOK)
	if embeddings.ProviderID != "inference" || embeddings.Model != "embed" || embeddings.Dimensions != 2 ||
		len(embeddings.Embeddings) != 2 || embeddings.Embeddings[1].Embedding[0] != 0.3 || embeddings.Usage.TotalTokens != 5 {
		t.Fatalf("unexpected embedding response: %+v", embeddings)
	}

	reranked := postJSONWithStatus[modelRuntimeRerankResponse](t, server, http.MethodPost, "/v2/model-runtime/rerank", `{
		"query":"best","documents":["first","second"],"top_n":1
	}`, http.StatusOK)
	if reranked.ProviderID != "inference" || reranked.Model != "rerank" || len(reranked.Results) != 1 || reranked.Results[0].Index != 1 {
		t.Fatalf("unexpected rerank response: %+v", reranked)
	}
	if len(store.modelInvocations) != 2 || store.modelInvocations[0].Capability != managedagents.ModelInvocationCapabilityEmbedding ||
		store.modelInvocations[0].InputTokens != 5 || store.modelInvocations[0].InputItems != 2 || store.modelInvocations[0].OutputItems != 2 ||
		store.modelInvocations[1].Capability != managedagents.ModelInvocationCapabilityRerank || store.modelInvocations[1].OutputItems != 1 {
		t.Fatalf("unexpected inference invocations: %+v", store.modelInvocations)
	}

	report := getJSON[managedagents.ModelInvocationReport](t, server, "/v2/model-runtime/invocations?capability=rerank&limit=10")
	if report.Summary.RecordCount != 1 || report.Summary.CompletedCount != 1 || len(report.Records) != 1 || report.Records[0].Model != "rerank" {
		t.Fatalf("unexpected invocation report: %+v", report)
	}
}

func TestModelRuntimeEmbeddingAndRerankValidateCapabilityContracts(t *testing.T) {
	store := newTestStore()
	store.models[llmModelKey("fake", "embed")] = managedagents.LLMModel{
		ProviderID: "fake", Model: "embed", CapabilityType: managedagents.LLMModelCapabilityEmbedding,
		Capabilities:       managedagents.LLMModelCapabilities{Dimensions: 2, MaxBatchSize: 1, Protocol: "unsupported"},
		IsDefaultEmbedding: true,
	}
	store.models[llmModelKey("fake", "rerank")] = managedagents.LLMModel{
		ProviderID: "fake", Model: "rerank", CapabilityType: managedagents.LLMModelCapabilityReranker,
		Capabilities:      managedagents.LLMModelCapabilities{MaxCandidates: 1, Protocol: "jina_rerank"},
		IsDefaultReranker: true,
	}
	server := NewServerWithStoreRunnerAndLLMDefaults(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil, "fake", "fake-demo")

	assertModelRuntimePathError(t, server, "/v2/model-runtime/embeddings", `{"inputs":["one"]}`, http.StatusBadRequest, "unsupported_model_protocol")
	assertModelRuntimePathError(t, server, "/v2/model-runtime/embeddings", `{"provider_id":"fake","inputs":["one"]}`, http.StatusBadRequest, "invalid_request")
	assertModelRuntimePathError(t, server, "/v2/model-runtime/rerank", `{"query":"q","documents":["one","two"]}`, http.StatusBadRequest, "invalid_request")
	assertModelRuntimePathError(t, server, "/v2/model-runtime/rerank", `{"query":"q","documents":["one"],"top_n":2}`, http.StatusBadRequest, "invalid_request")
}

func TestModelRuntimeGenerateReturnsAuditedQuotaError(t *testing.T) {
	store := newTestStore()
	server := &Server{
		store: store, logger: slog.Default(), defaultLLMProvider: "fake", defaultLLMModel: "fake-demo",
		modelRuntimeAdmission: newModelRuntimeAdmission(ModelRuntimePolicy{
			ModelIdentityRequestsPerMinute: 1,
		}),
	}
	handler := http.HandlerFunc(server.withV2Request(server.generateModelRuntimeText))
	body := `{"messages":[{"role":"user","content":"hello"}]}`
	postJSONWithStatus[modelRuntimeGenerateResponse](t, handler, http.MethodPost, "/v2/model-runtime/generate", body, http.StatusOK)

	request := httptest.NewRequest(http.MethodPost, "/v2/model-runtime/generate", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") == "" {
		t.Fatalf("expected quota response, got %d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	var envelope v2ErrorEnvelope
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != "model_quota_exceeded" || envelope.Error.Details["scope"] != "identity" {
		t.Fatalf("unexpected quota error: %+v", envelope.Error)
	}
	if len(store.modelInvocations) != 2 || store.modelInvocations[1].Status != managedagents.ModelInvocationStatusFailed || store.modelInvocations[1].ErrorCode != "model_quota_exceeded" {
		t.Fatalf("quota rejection was not audited: %+v", store.modelInvocations)
	}
}

func assertModelRuntimeError(t *testing.T, server http.Handler, body string, status int, code string) {
	t.Helper()
	assertModelRuntimePathError(t, server, "/v2/model-runtime/generate", body, status, code)
}

func assertModelRuntimePathError(t *testing.T, server http.Handler, path string, body string, status int, code string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != status {
		t.Fatalf("expected status %d, got %d: %s", status, response.Code, response.Body.String())
	}
	var envelope v2ErrorEnvelope
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if envelope.Error.Code != code {
		t.Fatalf("expected error code %q, got %+v", code, envelope.Error)
	}
}
