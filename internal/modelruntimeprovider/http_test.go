package modelruntimeprovider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/llm"
)

type stubExecutor struct {
	generate func(context.Context, GenerateRequest) (llm.Response, error)
	stream   func(context.Context, GenerateRequest, func(llm.Delta) error) (llm.Response, error)
	embed    func(context.Context, EmbeddingRequest) (llm.EmbeddingResponse, error)
	rerank   func(context.Context, RerankRequest) ([]llm.RerankResult, error)
}

func (s stubExecutor) Generate(ctx context.Context, request GenerateRequest) (llm.Response, error) {
	if s.generate == nil {
		return llm.Response{}, errors.New("unexpected generate")
	}
	return s.generate(ctx, request)
}

func (s stubExecutor) GenerateStream(ctx context.Context, request GenerateRequest, sink func(llm.Delta) error) (llm.Response, error) {
	if s.stream == nil {
		return llm.Response{}, errors.New("unexpected generate stream")
	}
	return s.stream(ctx, request, sink)
}

func (s stubExecutor) Embed(ctx context.Context, request EmbeddingRequest) (llm.EmbeddingResponse, error) {
	if s.embed == nil {
		return llm.EmbeddingResponse{}, errors.New("unexpected embed")
	}
	return s.embed(ctx, request)
}

func (s stubExecutor) Rerank(ctx context.Context, request RerankRequest) ([]llm.RerankResult, error) {
	if s.rerank == nil {
		return nil, errors.New("unexpected rerank")
	}
	return s.rerank(ctx, request)
}

func TestHTTPExecutorRoundTripsGenerate(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{AuthToken: "runtime-secret", Executor: stubExecutor{
		generate: func(_ context.Context, request GenerateRequest) (llm.Response, error) {
			if request.Route.APIKey != "provider-secret" || request.Route.Model != "model-a" || len(request.Messages) != 1 {
				t.Fatalf("unexpected request: %+v", request)
			}
			return llm.Response{
				Message: llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: "answer"}}},
				Usage:   llm.Usage{InputTokens: 2, OutputTokens: 1, TotalTokens: 3}, FinishReason: "stop",
			}, nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	executor, err := NewHTTPExecutor(server.URL, "runtime-secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	response, err := executor.Generate(context.Background(), GenerateRequest{
		Route:    Route{ProviderID: "provider-a", ProviderType: llm.ProviderFake, APIKey: "provider-secret", Model: "model-a"},
		Messages: []llm.Message{{Role: "user", Content: []llm.ContentPart{{Type: "text", Text: "question"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.FinishReason != "stop" || response.Usage.TotalTokens != 3 || response.Message.Content[0].Text != "answer" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestHTTPModelClientRoundTripsGenerateStream(t *testing.T) {
	deltas := []llm.Delta{
		{Index: 0, Kind: llm.DeltaKindText, Text: "answer"},
		{Index: 1, Kind: llm.DeltaKindReasoning, Text: "reason"},
		{Index: 2, Kind: llm.DeltaKindToolCall, ToolCall: &llm.ToolCallDelta{Index: 0, ID: "call_1", Type: "function", Name: "lookup", Arguments: `{"q":"value"}`}},
		{Index: 3, Kind: llm.DeltaKindUsage, Usage: &llm.Usage{InputTokens: 4, OutputTokens: 2, TotalTokens: 6}},
		{Index: 4, Kind: llm.DeltaKindStop, FinishReason: "tool_calls"},
	}
	wantResponse := llm.Response{
		Message: llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: "answer"}}, ToolCalls: []llm.ToolCall{{
			ID: "call_1", Type: "function", Function: llm.ToolCallFunction{Name: "lookup", Arguments: []byte(`{"q":"value"}`)},
		}}},
		Reasoning: []llm.ReasoningPart{{Type: "text", Text: "reason"}},
		Usage:     llm.Usage{InputTokens: 4, OutputTokens: 2, TotalTokens: 6}, FinishReason: "tool_calls",
	}
	handler, err := NewHandler(HandlerConfig{AuthToken: "runtime-secret", Executor: stubExecutor{
		stream: func(_ context.Context, request GenerateRequest, sink func(llm.Delta) error) (llm.Response, error) {
			if request.Route.ProviderID != "provider-a" || request.Route.ProviderType != "openai" || request.Route.APIKey != "provider-secret" || request.Route.BaseURL != "https://provider.example/v1" || request.Route.Model != "model-a" {
				t.Fatalf("unexpected route: %+v", request.Route)
			}
			if request.ThinkingMode != "disabled" || request.MaxOutputTokens != 4096 || request.MaxAttempts != 4 || request.RetryBaseDelayMS != 125 || len(request.Messages) != 1 || len(request.Tools) != 1 {
				t.Fatalf("unexpected stream request: %+v", request)
			}
			for _, delta := range deltas {
				if err := sink(delta); err != nil {
					return llm.Response{}, err
				}
			}
			return wantResponse, nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client, err := NewHTTPModelClient(server.URL, "runtime-secret", server.Client(), 4, 125*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	var received []llm.Delta
	response, err := client.GenerateStream(context.Background(), llm.Request{
		Provider: "provider-a", ProviderType: "openai", BaseURL: "https://provider.example/v1", APIKey: "provider-secret", Model: "model-a",
		ThinkingMode: "disabled", MaxOutputTokens: 4096,
		Messages: []llm.Message{{Role: "user", Content: []llm.ContentPart{{Type: "text", Text: "question"}}}},
		Tools:    []llm.Tool{{Type: "function", Function: llm.ToolFunction{Name: "lookup", Parameters: []byte(`{"type":"object"}`)}}},
	}, func(delta llm.Delta) error {
		received = append(received, delta)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(received, deltas) {
		t.Fatalf("stream deltas = %#v, want %#v", received, deltas)
	}
	if !reflect.DeepEqual(response, wantResponse) {
		t.Fatalf("stream response = %#v, want %#v", response, wantResponse)
	}
}

func TestHTTPModelClientAllowsExistingVisionPayloadLimit(t *testing.T) {
	largeImage := "data:image/png;base64," + strings.Repeat("A", (2<<20)+1)
	handler, err := NewHandler(HandlerConfig{AuthToken: "runtime-secret", Executor: stubExecutor{
		generate: func(_ context.Context, request GenerateRequest) (llm.Response, error) {
			if got := request.Messages[0].Content[0].ImageURL.URL; got != largeImage {
				t.Fatalf("vision payload length = %d, want %d", len(got), len(largeImage))
			}
			return llm.Response{Message: llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: "described"}}}}, nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client, err := NewHTTPModelClient(server.URL, "runtime-secret", server.Client(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Generate(context.Background(), llm.Request{
		Provider: "vision", Model: "vision-model",
		Messages: []llm.Message{{Role: "user", Content: []llm.ContentPart{{Type: "image_url", ImageURL: &llm.ImageURL{URL: largeImage}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Content[0].Text != "described" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestHTTPModelClientPreservesErrorAfterStreamStarts(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{AuthToken: "runtime-secret", Executor: stubExecutor{
		stream: func(_ context.Context, _ GenerateRequest, sink func(llm.Delta) error) (llm.Response, error) {
			if err := sink(llm.Delta{Kind: llm.DeltaKindText, Text: "partial"}); err != nil {
				return llm.Response{}, err
			}
			return llm.Response{}, &llm.ProviderError{
				Class: llm.ErrorClassRateLimit, StatusCode: http.StatusTooManyRequests,
				Retryable: true, RetryAfter: 3 * time.Second, Message: "provider throttled",
			}
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client, err := NewHTTPModelClient(server.URL, "runtime-secret", server.Client(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var received []llm.Delta
	_, err = client.GenerateStream(context.Background(), llm.Request{Provider: "p", Model: "m"}, func(delta llm.Delta) error {
		received = append(received, delta)
		return nil
	})
	var providerError *llm.ProviderError
	if !errors.As(err, &providerError) || providerError.Class != llm.ErrorClassRateLimit || providerError.StatusCode != http.StatusTooManyRequests || providerError.RetryAfter != 3*time.Second || !providerError.Retryable {
		t.Fatalf("unexpected stream error: %T %+v", err, err)
	}
	if len(received) != 1 || received[0].Text != "partial" {
		t.Fatalf("stream deltas = %#v", received)
	}
}

func TestHTTPModelClientSinkErrorCancelsRuntimeRequest(t *testing.T) {
	runtimeCanceled := make(chan struct{})
	handler, err := NewHandler(HandlerConfig{AuthToken: "runtime-secret", Executor: stubExecutor{
		stream: func(ctx context.Context, _ GenerateRequest, sink func(llm.Delta) error) (llm.Response, error) {
			if err := sink(llm.Delta{Kind: llm.DeltaKindText, Text: "first"}); err != nil {
				return llm.Response{}, err
			}
			<-ctx.Done()
			close(runtimeCanceled)
			return llm.Response{}, ctx.Err()
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client, err := NewHTTPModelClient(server.URL, "runtime-secret", server.Client(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	sinkError := errors.New("stop consuming stream")
	_, err = client.GenerateStream(context.Background(), llm.Request{Provider: "p", Model: "m"}, func(llm.Delta) error {
		return sinkError
	})
	if !errors.Is(err, sinkError) {
		t.Fatalf("GenerateStream() error = %v, want sink error", err)
	}
	select {
	case <-runtimeCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("closing the stream did not cancel the runtime request")
	}
}

func TestHTTPModelClientCallerCancellationReachesRuntime(t *testing.T) {
	started := make(chan struct{})
	runtimeCanceled := make(chan struct{})
	handler, err := NewHandler(HandlerConfig{AuthToken: "runtime-secret", Executor: stubExecutor{
		stream: func(ctx context.Context, _ GenerateRequest, _ func(llm.Delta) error) (llm.Response, error) {
			close(started)
			<-ctx.Done()
			close(runtimeCanceled)
			return llm.Response{}, ctx.Err()
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client, err := NewHTTPModelClient(server.URL, "runtime-secret", server.Client(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, callErr := client.GenerateStream(ctx, llm.Request{Provider: "p", Model: "m"}, nil)
		result <- callErr
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("runtime request did not start")
	}
	cancel()
	select {
	case callErr := <-result:
		if !errors.Is(callErr, context.Canceled) {
			t.Fatalf("GenerateStream() error = %v, want context canceled", callErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled model request did not return")
	}
	select {
	case <-runtimeCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("caller cancellation did not reach runtime")
	}
}

func TestHTTPModelClientTreatsRuntimeAuthenticationAsUpstreamFailure(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{AuthToken: "expected-secret", Executor: stubExecutor{}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client, err := NewHTTPModelClient(server.URL, "wrong-secret", server.Client(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GenerateStream(context.Background(), llm.Request{Provider: "p", Model: "m"}, nil)
	var providerError *llm.ProviderError
	if !errors.As(err, &providerError) || providerError.StatusCode != http.StatusBadGateway || providerError.Class != llm.ErrorClassServer {
		t.Fatalf("expected upstream authentication failure, got %T: %+v", err, err)
	}
}

func TestHTTPExecutorPreservesProviderError(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{AuthToken: "runtime-secret", Executor: stubExecutor{
		embed: func(context.Context, EmbeddingRequest) (llm.EmbeddingResponse, error) {
			return llm.EmbeddingResponse{}, &llm.ProviderError{
				Class: llm.ErrorClassRateLimit, StatusCode: http.StatusTooManyRequests,
				Retryable: true, RetryAfter: 3 * time.Second, Message: "provider throttled",
			}
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	executor, err := NewHTTPExecutor(server.URL, "runtime-secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Embed(context.Background(), EmbeddingRequest{Route: Route{ProviderID: "p", Model: "m"}, Inputs: []string{"value"}})
	var providerError *llm.ProviderError
	if !errors.As(err, &providerError) {
		t.Fatalf("expected provider error, got %T: %v", err, err)
	}
	if providerError.Class != llm.ErrorClassRateLimit || providerError.StatusCode != http.StatusTooManyRequests || providerError.RetryAfter != 3*time.Second || !providerError.Retryable {
		t.Fatalf("unexpected provider error: %+v", providerError)
	}
}

func TestHandlerRequiresBearerToken(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{AuthToken: "runtime-secret", Executor: stubExecutor{}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/generate", strings.NewReader(`{}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "authentication failed") {
		t.Fatalf("unexpected response: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestHTTPExecutorTreatsRuntimeAuthenticationAsUpstreamFailure(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{AuthToken: "expected-secret", Executor: stubExecutor{}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	executor, err := NewHTTPExecutor(server.URL, "wrong-secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Generate(context.Background(), GenerateRequest{Route: Route{ProviderID: "p", Model: "m"}})
	var providerError *llm.ProviderError
	if !errors.As(err, &providerError) || providerError.StatusCode != http.StatusBadGateway || providerError.Class != llm.ErrorClassServer {
		t.Fatalf("expected upstream authentication failure, got %T: %+v", err, err)
	}
}

func TestHandlerRequiresAuthConfiguration(t *testing.T) {
	if _, err := NewHandler(HandlerConfig{}); err == nil || !strings.Contains(err.Error(), "auth token") {
		t.Fatalf("expected auth token error, got %v", err)
	}
}

func TestNewHTTPExecutorValidatesConfiguration(t *testing.T) {
	tests := []struct {
		endpoint string
		token    string
	}{
		{endpoint: "", token: "token"},
		{endpoint: "unix:///runtime.sock", token: "token"},
		{endpoint: "http://runtime.example?secret=value", token: "token"},
		{endpoint: "http://runtime.example", token: ""},
	}
	for _, test := range tests {
		if _, err := NewHTTPExecutor(test.endpoint, test.token, nil); err == nil {
			t.Fatalf("expected invalid configuration for endpoint=%q token=%q", test.endpoint, test.token)
		}
	}
}
