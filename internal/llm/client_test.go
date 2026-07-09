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

func TestFakeClientGeneratesToolVerificationCall(t *testing.T) {
	client := FakeClient{}

	response, err := client.Generate(t.Context(), Request{
		Messages: []Message{{
			Role: "user",
			Content: []ContentPart{{
				Type: "text",
				Text: "please run tma.verify_tool_call",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", response.Message.ToolCalls)
	}
	call := response.Message.ToolCalls[0]
	if call.ID != "call_verify_tool" || call.Function.Name != "default.run_command" {
		t.Fatalf("unexpected tool call: %#v", call)
	}
}

func TestFakeClientSummarizesToolVerificationResult(t *testing.T) {
	client := FakeClient{}

	response, err := client.Generate(t.Context(), Request{
		Messages: []Message{{
			Role:       "tool",
			ToolCallID: "call_verify_tool",
			Content: []ContentPart{{
				Type: "text",
				Text: "/workspace\n\ntma-session-tool-ok",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if len(response.Message.Content) != 1 || !strings.Contains(response.Message.Content[0].Text, "tma-session-tool-ok") {
		t.Fatalf("unexpected response content: %#v", response.Message.Content)
	}
}

func TestFakeClientGeneratesUploadedFileVerificationCalls(t *testing.T) {
	client := FakeClient{}

	seedResponse, err := client.Generate(t.Context(), Request{
		Messages: []Message{{
			Role: "user",
			Content: []ContentPart{{
				Type: "text",
				Text: "please run tma.verify_uploaded_file_seed",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("seed generate: %v", err)
	}
	if len(seedResponse.Message.ToolCalls) != 1 || seedResponse.Message.ToolCalls[0].Function.Name != "default.run_command" {
		t.Fatalf("unexpected seed tool call: %#v", seedResponse.Message.ToolCalls)
	}

	readResponse, err := client.Generate(t.Context(), Request{
		Messages: []Message{{
			Role: "user",
			Content: []ContentPart{{
				Type: "text",
				Text: "please run tma.verify_uploaded_file_read",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("read generate: %v", err)
	}
	if len(readResponse.Message.ToolCalls) != 1 || readResponse.Message.ToolCalls[0].Function.Name != "default.run_command" {
		t.Fatalf("unexpected read tool call: %#v", readResponse.Message.ToolCalls)
	}
}

func TestFakeClientGeneratesWebCrawlVerificationCall(t *testing.T) {
	client := FakeClient{}

	response, err := client.Generate(t.Context(), Request{
		Messages: []Message{{
			Role: "user",
			Content: []ContentPart{{
				Type: "text",
				Text: "please run tma.verify_web_crawl http://127.0.0.1:18084/fixture.html",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", response.Message.ToolCalls)
	}
	call := response.Message.ToolCalls[0]
	if call.ID != "call_verify_web_crawl" || call.Function.Name != "web.crawl" {
		t.Fatalf("unexpected web crawl tool call: %#v", call)
	}
	if !strings.Contains(string(call.Function.Arguments), "http://127.0.0.1:18084/fixture.html") {
		t.Fatalf("expected crawl URL in arguments, got %s", string(call.Function.Arguments))
	}
}

func TestFakeClientGeneratesWebSearchVerificationCall(t *testing.T) {
	client := FakeClient{}

	response, err := client.Generate(t.Context(), Request{
		Messages: []Message{{
			Role: "user",
			Content: []ContentPart{{
				Type: "text",
				Text: "please run tma.verify_web_search tma-web-search-ok",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", response.Message.ToolCalls)
	}
	call := response.Message.ToolCalls[0]
	if call.ID != "call_verify_web_search" || call.Function.Name != "web.search" {
		t.Fatalf("unexpected web search tool call: %#v", call)
	}
	if !strings.Contains(string(call.Function.Arguments), "tma-web-search-ok") {
		t.Fatalf("expected search query in arguments, got %s", string(call.Function.Arguments))
	}
}

func TestFakeClientGeneratesNetworkDownloadVerificationCall(t *testing.T) {
	client := FakeClient{}

	response, err := client.Generate(t.Context(), Request{
		Messages: []Message{{
			Role: "user",
			Content: []ContentPart{{
				Type: "text",
				Text: "please run tma.verify_network_download https://example.com/test.txt",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", response.Message.ToolCalls)
	}
	call := response.Message.ToolCalls[0]
	if call.ID != "call_verify_network_download" || call.Function.Name != "default.execute_code" {
		t.Fatalf("unexpected network download tool call: %#v", call)
	}
	arguments := string(call.Function.Arguments)
	if !strings.Contains(arguments, "https://example.com/test.txt") || !strings.Contains(arguments, "tma-network-download-ok") {
		t.Fatalf("expected download URL and marker in arguments, got %s", arguments)
	}
}

func TestFakeClientSummarizesNetworkDownloadVerificationResult(t *testing.T) {
	client := FakeClient{}

	response, err := client.Generate(t.Context(), Request{
		Messages: []Message{{
			Role:       "tool",
			ToolCallID: "call_verify_network_download",
			Content: []ContentPart{{
				Type: "text",
				Text: `{"id":"call_verify_network_download","content":"network unreachable"}`,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(response.Message.Content) != 1 || !strings.Contains(response.Message.Content[0].Text, "network unreachable") {
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
			Body:       io.NopCloser(bytes.NewBufferString(`{"choices":[{"message":{"role":"assistant","content":"real-ish response"}}],"usage":{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19,"prompt_tokens_details":{"cached_tokens":3},"completion_tokens_details":{"reasoning_tokens":2}}}`)),
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
	if response.Usage.InputTokens != 12 || response.Usage.OutputTokens != 7 || response.Usage.TotalTokens != 19 || response.Usage.CachedInputTokens != 3 || response.Usage.ReasoningTokens != 2 {
		t.Fatalf("unexpected usage: %#v", response.Usage)
	}
}

func TestOpenAICompatibleClientSendsToolsAndParsesToolCalls(t *testing.T) {
	var captured struct {
		Tools []Tool `json:"tools"`
	}

	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body: io.NopCloser(bytes.NewBufferString(`{
				"choices":[{
					"message":{
						"role":"assistant",
						"content":null,
						"tool_calls":[{
							"id":"call_1",
							"type":"function",
							"function":{
								"name":"default.run_command",
								"arguments":"{\"command\":\"sh\",\"args\":[\"-c\",\"pwd\"]}"
							}
						}]
					}
				}]
			}`)),
		}, nil
	})}

	client := OpenAICompatibleClient{
		BaseURL: "http://llm.example/v1",
		APIKey:  "test-key",
		Client:  httpClient,
	}

	response, err := client.Generate(t.Context(), Request{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: []ContentPart{{Type: "text", Text: "inspect"}}}},
		Tools: []Tool{{
			Type: "function",
			Function: ToolFunction{
				Name:        "default.run_command",
				Description: "Run a command.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`),
			},
		}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if len(captured.Tools) != 1 || captured.Tools[0].Function.Name != "default.run_command" {
		t.Fatalf("unexpected captured tools: %#v", captured.Tools)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", response.Message.ToolCalls)
	}
	call := response.Message.ToolCalls[0]
	if call.ID != "call_1" || call.Function.Name != "default.run_command" {
		t.Fatalf("unexpected tool call: %#v", call)
	}
	var args map[string]any
	if err := json.Unmarshal(call.Function.Arguments, &args); err != nil {
		t.Fatalf("decode tool call arguments: %v", err)
	}
	if args["command"] != "sh" {
		t.Fatalf("unexpected arguments: %#v", args)
	}
}

func TestOpenAICompatibleClientStreamsAssistantMessage(t *testing.T) {
	var captured struct {
		Stream        bool `json:"stream"`
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		body := strings.Join([]string{
			`data: {"choices":[{"delta":{"role":"assistant"}}]}`,
			`data: {"choices":[{"delta":{"content":"hello"}}]}`,
			`data: {"choices":[{"delta":{"content":" world"}}]}`,
			`data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":4,"total_tokens":9}}`,
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
	if !captured.StreamOptions.IncludeUsage {
		t.Fatal("expected stream_options.include_usage=true request")
	}
	if len(deltas) != 2 || deltas[0].Text != "hello" || deltas[1].Text != " world" {
		t.Fatalf("unexpected deltas: %#v", deltas)
	}
	if response.Message.Role != "assistant" || response.Message.Content[0].Text != "hello world" {
		t.Fatalf("unexpected response: %#v", response)
	}
	if response.Usage.InputTokens != 5 || response.Usage.OutputTokens != 4 || response.Usage.TotalTokens != 9 {
		t.Fatalf("unexpected usage: %#v", response.Usage)
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
