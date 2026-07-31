package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInferenceServiceEmbeddingProtocols(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		path     string
		response string
	}{
		{name: "openai", protocol: EmbeddingProtocolOpenAI, path: "/v1/embeddings", response: `{"data":[{"index":1,"embedding":[0.3,0.4]},{"index":0,"embedding":[0.1,0.2]}],"usage":{"prompt_tokens":4,"total_tokens":4}}`},
		{name: "tei", protocol: EmbeddingProtocolTEI, path: "/v1/embed", response: `[[0.1,0.2],[0.3,0.4]]`},
		{name: "ollama", protocol: EmbeddingProtocolOllama, path: "/v1/api/embed", response: `{"embeddings":[[0.1,0.2],[0.3,0.4]],"prompt_eval_count":5}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != test.path || r.Header.Get("Authorization") != "Bearer secret" {
					t.Fatalf("unexpected request path=%s authorization=%q", r.URL.Path, r.Header.Get("Authorization"))
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(test.response))
			}))
			defer server.Close()

			response, err := (InferenceService{}).Embed(context.Background(), InferenceConfig{
				BaseURL: server.URL + "/v1", APIKey: "secret", Model: "embed", Protocol: test.protocol,
			}, []string{"first", "second"})
			if err != nil || len(response.Embeddings) != 2 || response.Embeddings[0][0] != 0.1 {
				t.Fatalf("unexpected response: %+v err=%v", response, err)
			}
		})
	}
}

func TestInferenceServiceRerankProtocols(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		path     string
		response string
	}{
		{name: "jina", protocol: RerankProtocolJina, path: "/v1/rerank", response: `{"results":[{"index":0,"relevance_score":0.2},{"index":2,"relevance_score":0.9}]}`},
		{name: "cohere", protocol: RerankProtocolCohere, path: "/v1/rerank", response: `{"results":[{"index":0,"relevance_score":0.2},{"index":2,"relevance_score":0.9}]}`},
		{name: "vllm", protocol: RerankProtocolVLLM, path: "/v1/score", response: `{"data":[{"index":0,"score":0.2},{"index":2,"score":0.9}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != test.path {
					t.Fatalf("unexpected path %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(test.response))
			}))
			defer server.Close()

			results, err := (InferenceService{}).Rerank(context.Background(), InferenceConfig{
				BaseURL: server.URL + "/v1", Model: "rerank", Protocol: test.protocol,
			}, "query", []string{"one", "two", "three"}, 1)
			if err != nil || len(results) != 1 || results[0].Index != 2 || results[0].Score != 0.9 {
				t.Fatalf("unexpected results: %+v err=%v", results, err)
			}
		})
	}
}

func TestInferenceServiceRejectsInvalidProviderResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[0.1]},{"index":1,"embedding":[0.2,0.3]}]}`))
	}))
	defer server.Close()

	_, err := (InferenceService{}).Embed(context.Background(), InferenceConfig{
		BaseURL: server.URL, Model: "embed", Protocol: EmbeddingProtocolOpenAI,
	}, []string{"first", "second"})
	if err == nil {
		t.Fatal("expected inconsistent dimensions to fail")
	}
}
