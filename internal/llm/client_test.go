package llm

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFakeClientGeneratesAssistantMessage(t *testing.T) {
	client := FakeClient{}

	response, err := client.Generate(t.Context(), Request{
		Messages: []Message{{
			Role: "user",
			Content: []ContentPart{{
				Type: "text",
				Text: "hello",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if response.Message.Role != "assistant" {
		t.Fatalf("expected assistant role, got %q", response.Message.Role)
	}
	if len(response.Message.Content) != 1 || response.Message.Content[0].Text != "Agent runtime received: hello" {
		t.Fatalf("unexpected response content: %#v", response.Message.Content)
	}
}

func TestManagerDefaultsToFakeProvider(t *testing.T) {
	manager, err := NewManager("", "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	client, provider, model := manager.Current()
	if client == nil {
		t.Fatal("expected current client")
	}
	if provider != ProviderFake {
		t.Fatalf("expected provider %q, got %q", ProviderFake, provider)
	}
	if model != DefaultModel {
		t.Fatalf("expected model %q, got %q", DefaultModel, model)
	}
}

func TestManagerRejectsUnsupportedProvider(t *testing.T) {
	_, err := NewManager("unknown", "")
	if err == nil {
		t.Fatal("expected unsupported provider error")
	}
}

func TestManagerRejectsOpenAICompatibleWithoutAPIKey(t *testing.T) {
	_, err := NewManagerWithConfig(ManagerConfig{
		Provider: ProviderOpenAICompatible,
		Model:    "test-model",
	})
	if err == nil {
		t.Fatal("expected missing api key error")
	}
}

func TestManagerRegistersCustomProviderIDAsOpenAICompatible(t *testing.T) {
	manager, err := NewManagerWithConfig(ManagerConfig{
		Provider:     "volcengine-agent-plan",
		ProviderType: ProviderTypeOpenAI,
		Model:        "doubao-test",
		BaseURL:      "http://llm.example/v1",
		APIKey:       "test-key",
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	client, provider, model := manager.Current()
	if client == nil {
		t.Fatal("expected current client")
	}
	if provider != "volcengine-agent-plan" {
		t.Fatalf("expected custom provider id, got %q", provider)
	}
	if model != "doubao-test" {
		t.Fatalf("expected custom model, got %q", model)
	}
}

func TestManagerAcceptsOpenAICompatibleProviderTypeAlias(t *testing.T) {
	manager, err := NewManagerWithConfig(ManagerConfig{
		Provider:     "legacy-openai-compatible",
		ProviderType: ProviderOpenAICompatible,
		Model:        "test-model",
		BaseURL:      "http://llm.example/v1",
		APIKey:       "test-key",
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	_, provider, _ := manager.Current()
	if provider != "legacy-openai-compatible" {
		t.Fatalf("expected custom provider id, got %q", provider)
	}
}

func TestManagerInfersCustomProviderIDAsOpenAICompatible(t *testing.T) {
	manager, err := NewManagerWithConfig(ManagerConfig{
		Provider: "volcengine-agent-plan",
		Model:    "doubao-test",
		BaseURL:  "http://llm.example/v1",
		APIKey:   "test-key",
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	_, provider, _ := manager.Current()
	if provider != "volcengine-agent-plan" {
		t.Fatalf("expected custom provider id, got %q", provider)
	}
}

func TestManagerSwitchesProvider(t *testing.T) {
	manager, err := NewManager("", "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	if err := manager.Switch(ProviderFake, "fake-next"); err != nil {
		t.Fatalf("switch provider: %v", err)
	}
	_, provider, model := manager.Current()
	if provider != ProviderFake || model != "fake-next" {
		t.Fatalf("unexpected current config provider=%q model=%q", provider, model)
	}
}

func TestManagerGeneratesWithCurrentClient(t *testing.T) {
	manager, err := NewManager("", "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	response, err := manager.Generate(t.Context(), Request{
		Messages: []Message{{
			Role: "user",
			Content: []ContentPart{{
				Type: "text",
				Text: "through manager",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("generate through manager: %v", err)
	}

	if response.Message.Content[0].Text != "Agent runtime received: through manager" {
		t.Fatalf("unexpected response: %#v", response.Message.Content)
	}
}

func TestManagerGeneratesWithRequestProviderConfig(t *testing.T) {
	manager, err := NewManager("", "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	response, err := manager.Generate(t.Context(), Request{
		Provider:     "tenant-fake",
		ProviderType: ProviderFake,
		Model:        "fake-tenant",
		Messages: []Message{{
			Role: "user",
			Content: []ContentPart{{
				Type: "text",
				Text: "request scoped provider",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("generate with request provider config: %v", err)
	}

	if response.Message.Content[0].Text != "Agent runtime received: request scoped provider" {
		t.Fatalf("unexpected response: %#v", response.Message.Content)
	}
}

func TestManagerRejectsRequestOpenAIProviderWithoutAPIKey(t *testing.T) {
	manager, err := NewManager("", "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	_, err = manager.Generate(t.Context(), Request{
		Provider:     "tenant-openai",
		ProviderType: ProviderTypeOpenAI,
		Model:        "test-model",
		BaseURL:      "http://llm.example/v1",
		Messages:     []Message{{Role: "user", Content: []ContentPart{{Type: "text", Text: "hello"}}}},
	})
	if err == nil {
		t.Fatal("expected missing api key error")
	}
}

func TestOpenAICompatibleClientGeneratesAssistantMessage(t *testing.T) {
	var captured struct {
		Path          string
		Authorization string
		Model         string `json:"model"`
		Messages      []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}

	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		captured.Path = r.URL.Path
		captured.Authorization = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString(`{"choices":[{"message":{"role":"assistant","content":"real-ish response"}}]}`)),
		}, nil
	})}

	client := OpenAICompatibleClient{
		BaseURL: "http://llm.example/v1",
		APIKey:  "test-key",
		Client:  httpClient,
	}

	response, err := client.Generate(t.Context(), Request{
		Model: "test-model",
		Messages: []Message{
			{
				Role: "system",
				Content: []ContentPart{{
					Type: "text",
					Text: "You are helpful.",
				}},
			},
			{
				Role: "user",
				Content: []ContentPart{{
					Type: "text",
					Text: "hello",
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if captured.Path != "/v1/chat/completions" {
		t.Fatalf("expected chat completions path, got %q", captured.Path)
	}
	if captured.Authorization != "Bearer test-key" {
		t.Fatalf("unexpected authorization header %q", captured.Authorization)
	}
	if captured.Model != "test-model" {
		t.Fatalf("expected model test-model, got %q", captured.Model)
	}
	if len(captured.Messages) != 2 || captured.Messages[0].Role != "system" || captured.Messages[1].Content != "hello" {
		t.Fatalf("unexpected messages: %#v", captured.Messages)
	}
	if response.Message.Role != "assistant" || response.Message.Content[0].Text != "real-ish response" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestOpenAICompatibleClientStreamsAssistantMessage(t *testing.T) {
	var captured struct {
		Stream bool `json:"stream"`
	}
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		body := strings.Join([]string{
			`data: {"choices":[{"delta":{"role":"assistant"}}]}`,
			`data: {"choices":[{"delta":{"content":"hello"}}]}`,
			`data: {"choices":[{"delta":{"content":" world"}}]}`,
			`data: [DONE]`,
			``,
		}, "\n")
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString(body)),
		}, nil
	})}

	client := OpenAICompatibleClient{
		BaseURL: "http://llm.example/v1",
		APIKey:  "test-key",
		Client:  httpClient,
	}

	var deltas []Delta
	response, err := client.GenerateStream(t.Context(), Request{
		Model: "test-model",
		Messages: []Message{{
			Role: "user",
			Content: []ContentPart{{
				Type: "text",
				Text: "hello",
			}},
		}},
	}, func(delta Delta) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatalf("generate stream: %v", err)
	}

	if !captured.Stream {
		t.Fatal("expected stream=true request")
	}
	if len(deltas) != 2 || deltas[0].Text != "hello" || deltas[1].Text != " world" {
		t.Fatalf("unexpected deltas: %#v", deltas)
	}
	if response.Message.Role != "assistant" || response.Message.Content[0].Text != "hello world" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestOpenAICompatibleClientReturnsHTTPError(t *testing.T) {
	client := OpenAICompatibleClient{
		BaseURL: "http://llm.example/v1",
		APIKey:  "test-key",
		Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Status:     "401 Unauthorized",
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewBufferString("bad key")),
			}, nil
		})},
	}
	_, err := client.Generate(t.Context(), Request{Model: "test-model"})
	if err == nil {
		t.Fatal("expected http error")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
