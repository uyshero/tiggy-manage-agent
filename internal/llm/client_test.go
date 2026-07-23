package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
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
	if call.ID != "call_verify_tool" || call.Function.Name != "default_run_command" {
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

func TestFakeClientSummarizesComputerCUAVerificationResult(t *testing.T) {
	client := FakeClient{}

	response, err := client.Generate(t.Context(), Request{
		Messages: []Message{{
			Role:       "tool",
			ToolCallID: "call_verify_computer_plugin",
			Content: []ContentPart{{
				Type: "text",
				Text: `{"id":"call_verify_computer_plugin","content":"computer.get_state completed via cua"}`,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(response.Message.Content) != 1 || !strings.Contains(response.Message.Content[0].Text, "computer.get_state completed via cua") {
		t.Fatalf("unexpected response content: %#v", response.Message.Content)
	}
}

func TestFakeClientGeneratesMCPVerificationCall(t *testing.T) {
	client := FakeClient{}

	response, err := client.Generate(t.Context(), Request{
		Messages: []Message{{
			Role: "user",
			Content: []ContentPart{{
				Type: "text",
				Text: "please run tma.verify_mcp_tool",
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
	if call.ID != "call_verify_mcp_tool" || call.Function.Name != "filesystem_read_file" {
		t.Fatalf("unexpected mcp tool call: %#v", call)
	}
}

func TestFakeClientSummarizesMCPVerificationResult(t *testing.T) {
	client := FakeClient{}

	response, err := client.Generate(t.Context(), Request{
		Messages: []Message{{
			Role:       "tool",
			ToolCallID: "call_verify_mcp_tool",
			Content: []ContentPart{{
				Type: "text",
				Text: `{"id":"call_verify_mcp_tool","content":"tma-mcp-filesystem-ok"}`,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(response.Message.Content) != 1 || !strings.Contains(response.Message.Content[0].Text, "tma-mcp-filesystem-ok") {
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
	if len(seedResponse.Message.ToolCalls) != 1 || seedResponse.Message.ToolCalls[0].Function.Name != "default_run_command" {
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
	if len(readResponse.Message.ToolCalls) != 1 || readResponse.Message.ToolCalls[0].Function.Name != "default_run_command" {
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
	if call.ID != "call_verify_web_crawl" || call.Function.Name != "web_crawl" {
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
	if call.ID != "call_verify_web_search" || call.Function.Name != "web_search" {
		t.Fatalf("unexpected web search tool call: %#v", call)
	}
	if !strings.Contains(string(call.Function.Arguments), "tma-web-search-ok") {
		t.Fatalf("expected search query in arguments, got %s", string(call.Function.Arguments))
	}
}

func TestFakeClientGeneratesBrowserFlowVerificationCalls(t *testing.T) {
	client := FakeClient{}

	response, err := client.Generate(t.Context(), Request{
		Messages: []Message{{
			Role: "user",
			Content: []ContentPart{{
				Type: "text",
				Text: "please run tma.verify_browser_flow data:text/html,browser",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(response.Message.ToolCalls) != 4 {
		t.Fatalf("expected four browser tool calls, got %#v", response.Message.ToolCalls)
	}
	names := make(map[string]bool)
	for _, call := range response.Message.ToolCalls {
		names[call.Function.Name] = true
	}
	for _, expected := range []string{"browser_open", "browser_screenshot", "browser_type", "browser_click"} {
		if !names[expected] {
			t.Fatalf("missing %s in browser tool calls: %#v", expected, response.Message.ToolCalls)
		}
	}
}

func TestFakeClientGeneratesBrowserTakeoverVerificationCall(t *testing.T) {
	client := FakeClient{}

	response, err := client.Generate(t.Context(), Request{
		Messages: []Message{{
			Role: "user",
			Content: []ContentPart{{
				Type: "text",
				Text: "please run tma.verify_browser_takeover data:text/html,takeover",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("expected one browser takeover tool call, got %#v", response.Message.ToolCalls)
	}
	call := response.Message.ToolCalls[0]
	if call.ID != "call_verify_browser_takeover" || call.Function.Name != "browser_takeover" {
		t.Fatalf("unexpected browser takeover tool call: %#v", call)
	}
	if !strings.Contains(string(call.Function.Arguments), "data:text/html,takeover") {
		t.Fatalf("expected takeover URL in arguments, got %s", string(call.Function.Arguments))
	}
}

func TestFakeClientGeneratesBrowserCloseVerificationCall(t *testing.T) {
	client := FakeClient{}

	response, err := client.Generate(t.Context(), Request{
		Messages: []Message{{
			Role: "user",
			Content: []ContentPart{{
				Type: "text",
				Text: "please run tma.verify_browser_close",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("expected one browser close tool call, got %#v", response.Message.ToolCalls)
	}
	call := response.Message.ToolCalls[0]
	if call.ID != "call_verify_browser_close" || call.Function.Name != "browser_close" {
		t.Fatalf("unexpected browser close tool call: %#v", call)
	}
}

func TestFakeClientGeneratesComputerPluginVerificationCall(t *testing.T) {
	client := FakeClient{}

	response, err := client.Generate(t.Context(), Request{
		Messages: []Message{{
			Role: "user",
			Content: []ContentPart{{
				Type: "text",
				Text: "please run tma.verify_computer_plugin_tool",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("expected one computer tool call, got %#v", response.Message.ToolCalls)
	}
	call := response.Message.ToolCalls[0]
	if call.ID != "call_verify_computer_plugin" || call.Function.Name != "computer_get_state" {
		t.Fatalf("unexpected computer tool call: %#v", call)
	}
	if !strings.Contains(string(call.Function.Arguments), `"capture_mode":"ax"`) {
		t.Fatalf("expected ax capture mode in arguments, got %s", string(call.Function.Arguments))
	}
}

func TestFakeClientGeneratesComputerPluginScreenshotVerificationCall(t *testing.T) {
	client := FakeClient{}

	response, err := client.Generate(t.Context(), Request{
		Messages: []Message{{
			Role: "user",
			Content: []ContentPart{{
				Type: "text",
				Text: "please run tma.verify_computer_plugin_screenshot",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("expected one computer screenshot tool call, got %#v", response.Message.ToolCalls)
	}
	call := response.Message.ToolCalls[0]
	if call.ID != "call_verify_computer_plugin_screenshot" || call.Function.Name != "computer_screenshot" {
		t.Fatalf("unexpected computer screenshot tool call: %#v", call)
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
	if call.ID != "call_verify_network_download" || call.Function.Name != "default_execute_code" {
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
		Provider:       "volcengine-agent-plan",
		ProviderType:   ProviderTypeOpenAI,
		Model:          "doubao-test",
		BaseURL:        "http://llm.example/v1",
		APIKey:         "test-key",
		MaxAttempts:    5,
		RetryBaseDelay: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	client, provider, model := manager.Current()
	if client == nil {
		t.Fatal("expected current client")
	}
	openAIClient, ok := client.(OpenAICompatibleClient)
	if !ok || openAIClient.MaxAttempts != 5 || openAIClient.RetryBaseDelay != 2*time.Second {
		t.Fatalf("expected configured retry policy, got %T %#v", client, client)
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

func TestManagerAppliesRetryPolicyToRequestScopedProvider(t *testing.T) {
	manager, err := NewManagerWithConfig(ManagerConfig{
		Provider:       ProviderFake,
		Model:          "fake-demo",
		MaxAttempts:    4,
		RetryBaseDelay: 900 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	client, _, _, err := manager.clientForRequest(Request{
		Provider:     "tenant-openai",
		ProviderType: ProviderTypeOpenAI,
		Model:        "tenant-model",
		BaseURL:      "http://llm.example/v1",
		APIKey:       "tenant-key",
	})
	if err != nil {
		t.Fatalf("resolve request-scoped client: %v", err)
	}
	openAIClient, ok := client.(OpenAICompatibleClient)
	if !ok || openAIClient.MaxAttempts != 4 || openAIClient.RetryBaseDelay != 900*time.Millisecond {
		t.Fatalf("unexpected request-scoped retry policy: %T %#v", client, client)
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
		MaxTokens     int    `json:"max_tokens"`
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
		Model: "test-model", MaxOutputTokens: 16384,
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
	if captured.MaxTokens != 16384 {
		t.Fatalf("expected max_tokens=16384, got %d", captured.MaxTokens)
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

func TestOpenAICompatibleClientSendsMultimodalImageContent(t *testing.T) {
	var captured struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewBufferString(`{"choices":[{"message":{"role":"assistant","content":"image understood"}}]}`))}, nil
	})}
	client := OpenAICompatibleClient{BaseURL: "http://llm.example/v1", APIKey: "test-key", Client: httpClient}
	_, err := client.Generate(t.Context(), Request{Model: "vision-model", Messages: []Message{{Role: "user", Content: []ContentPart{
		{Type: "text", Text: "describe"},
		{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,cG5n", Detail: "auto"}},
	}}}})
	if err != nil {
		t.Fatalf("generate multimodal request: %v", err)
	}
	if len(captured.Messages) != 1 {
		t.Fatalf("unexpected messages: %#v", captured.Messages)
	}
	var parts []ContentPart
	if err := json.Unmarshal(captured.Messages[0].Content, &parts); err != nil {
		t.Fatalf("decode multimodal content: %v (%s)", err, captured.Messages[0].Content)
	}
	if len(parts) != 2 || parts[1].Type != "image_url" || parts[1].ImageURL == nil || parts[1].ImageURL.URL != "data:image/png;base64,cG5n" {
		t.Fatalf("unexpected multimodal parts: %#v", parts)
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
								"name":"default_run_command",
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
				Name:        "default_run_command",
				Description: "Run a command.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`),
			},
		}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if len(captured.Tools) != 1 || captured.Tools[0].Function.Name != "default_run_command" {
		t.Fatalf("unexpected captured tools: %#v", captured.Tools)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", response.Message.ToolCalls)
	}
	call := response.Message.ToolCalls[0]
	if call.ID != "call_1" || call.Function.Name != "default_run_command" {
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
	if len(deltas) != 4 || deltas[0].Text != "hello" || deltas[1].Text != " world" {
		t.Fatalf("unexpected deltas: %#v", deltas)
	}
	if deltas[0].Kind != DeltaKindText || deltas[1].Kind != DeltaKindText {
		t.Fatalf("expected text delta kinds, got %#v", deltas)
	}
	if deltas[2].Kind != DeltaKindUsage || deltas[2].Usage == nil || deltas[2].Usage.TotalTokens != 9 {
		t.Fatalf("expected typed usage delta, got %#v", deltas[2])
	}
	if deltas[3].Kind != DeltaKindStop || deltas[3].FinishReason != "done" {
		t.Fatalf("expected typed stop delta, got %#v", deltas[3])
	}
	if response.Message.Role != "assistant" || response.Message.Content[0].Text != "hello world" {
		t.Fatalf("unexpected response: %#v", response)
	}
	if response.Usage.InputTokens != 5 || response.Usage.OutputTokens != 4 || response.Usage.TotalTokens != 9 {
		t.Fatalf("unexpected usage: %#v", response.Usage)
	}
}

func TestDecodeOpenAIStreamEmitsPresentZeroUsage(t *testing.T) {
	stream := strings.NewReader("data: {\"choices\":[],\"usage\":{}}\n\ndata: [DONE]\n\n")
	var deltas []Delta
	response, err := decodeOpenAIStream(stream, func(delta Delta) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatalf("decode stream: %v", err)
	}
	if len(deltas) != 2 || deltas[0].Kind != DeltaKindUsage || deltas[0].Usage == nil || deltas[1].Kind != DeltaKindStop {
		t.Fatalf("expected zero usage and stop deltas, got %#v", deltas)
	}
	if response.Usage.TotalTokens != 0 {
		t.Fatalf("unexpected zero usage response: %#v", response.Usage)
	}
}

func TestOpenAICompatibleClientReturnsReasoningContent(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body: io.NopCloser(bytes.NewBufferString(
				`{"choices":[{"message":{"role":"assistant","reasoning_content":"check the evidence","content":"final answer"}}]}`,
			)),
		}, nil
	})}
	client := OpenAICompatibleClient{BaseURL: "http://llm.example/v1", APIKey: "test-key", Client: httpClient}

	response, err := client.Generate(t.Context(), Request{Model: "test-model"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(response.Reasoning) != 1 || response.Reasoning[0].Text != "check the evidence" {
		t.Fatalf("unexpected reasoning: %#v", response.Reasoning)
	}
	if response.Message.Content[0].Text != "final answer" {
		t.Fatalf("unexpected response content: %#v", response.Message.Content)
	}
}

func TestOpenAICompatibleClientStreamsReasoningDetails(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := strings.Join([]string{
			`data: {"choices":[{"delta":{"role":"assistant","reasoning_details":[{"type":"reasoning.summary","summary":"check "}]}}]}`,
			`data: {"choices":[{"delta":{"reasoning_details":[{"type":"reasoning.text","text":"evidence"}]}}]}`,
			`data: {"choices":[{"delta":{"content":"final answer"}}]}`,
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
	client := OpenAICompatibleClient{BaseURL: "http://llm.example/v1", APIKey: "test-key", Client: httpClient}
	var deltas []Delta

	response, err := client.GenerateStream(t.Context(), Request{Model: "test-model"}, func(delta Delta) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatalf("generate stream: %v", err)
	}
	if len(deltas) != 3 || deltas[0].Kind != DeltaKindReasoning || deltas[0].Text != "check evidence" {
		t.Fatalf("unexpected reasoning deltas: %#v", deltas)
	}
	if deltas[1].Kind != DeltaKindText || deltas[1].Text != "final answer" {
		t.Fatalf("unexpected text delta: %#v", deltas[1])
	}
	if deltas[2].Kind != DeltaKindStop || deltas[2].FinishReason != "done" {
		t.Fatalf("unexpected stop delta: %#v", deltas[2])
	}
	if len(response.Reasoning) != 1 || response.Reasoning[0].Text != "check evidence" {
		t.Fatalf("unexpected final reasoning: %#v", response.Reasoning)
	}
}

func TestOpenAICompatibleClientStreamsToolCallFragments(t *testing.T) {
	var captured struct {
		Stream bool         `json:"stream"`
		Tools  []openAITool `json:"tools"`
	}
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		body := strings.Join([]string{
			`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"default.read_file","arguments":"{"}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}}]},"finish_reason":"tool_calls"}]}`,
			`data: {"choices":[],"usage":{"prompt_tokens":8,"completion_tokens":5,"total_tokens":13}}`,
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
			Role:    "user",
			Content: []ContentPart{{Type: "text", Text: "read the file"}},
		}},
		Tools: []Tool{{
			Type: "function",
			Function: ToolFunction{
				Name:       "default.read_file",
				Parameters: json.RawMessage(`{"type":"object"}`),
			},
		}},
	}, func(delta Delta) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatalf("generate stream: %v", err)
	}
	if !captured.Stream || len(captured.Tools) != 1 {
		t.Fatalf("expected streamed request with tools, got stream=%v tools=%#v", captured.Stream, captured.Tools)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("expected one streamed tool call, got %#v", response.Message.ToolCalls)
	}
	call := response.Message.ToolCalls[0]
	if call.ID != "call_1" || call.Function.Name != "default.read_file" {
		t.Fatalf("unexpected streamed tool call: %#v", call)
	}
	var arguments map[string]any
	if err := json.Unmarshal(call.Function.Arguments, &arguments); err != nil {
		t.Fatalf("decode streamed tool arguments: %v", err)
	}
	if len(arguments) != 0 {
		t.Fatalf("unexpected streamed tool arguments: %#v", arguments)
	}
	if response.Usage.TotalTokens != 13 {
		t.Fatalf("unexpected streamed usage: %#v", response.Usage)
	}
	if len(deltas) != 4 || deltas[0].Kind != DeltaKindToolCall || deltas[1].Kind != DeltaKindToolCall || deltas[2].Kind != DeltaKindStop || deltas[3].Kind != DeltaKindUsage {
		t.Fatalf("unexpected typed tool stream deltas: %#v", deltas)
	}
	if deltas[0].ToolCall == nil || deltas[0].ToolCall.ID != "call_1" || deltas[0].ToolCall.Name != "default.read_file" || deltas[0].ToolCall.Arguments != "{" {
		t.Fatalf("unexpected first tool call delta: %#v", deltas[0])
	}
	if deltas[1].ToolCall == nil || deltas[1].ToolCall.Arguments != "}" || deltas[2].FinishReason != "tool_calls" {
		t.Fatalf("unexpected final tool call deltas: %#v", deltas)
	}
}

func TestOpenAICompatibleClientEmitsTypedStreamError(t *testing.T) {
	client := OpenAICompatibleClient{
		BaseURL: "http://llm.example/v1",
		APIKey:  "test-key",
		Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(strings.Join([]string{
					`data: {"choices":[{"delta":{"reasoning_content":"last thought"}}]}`,
					`data: {"error":{"message":"service unavailable","type":"server_error","code":"unavailable"}}`,
					``,
				}, "\n"))),
			}, nil
		})},
	}
	var deltas []Delta

	_, err := client.GenerateStream(t.Context(), Request{Model: "test-model"}, func(delta Delta) error {
		deltas = append(deltas, delta)
		return nil
	})
	var providerError *ProviderError
	if !errors.As(err, &providerError) || providerError.Class != ErrorClassServer || !providerError.Retryable || providerError.Attempts != 1 {
		t.Fatalf("expected typed provider stream error, got %T %#v", err, err)
	}
	if len(deltas) != 2 || deltas[0].Kind != DeltaKindReasoning || deltas[0].Text != "last thought" || deltas[1].Kind != DeltaKindError || deltas[1].Error == nil || deltas[1].Error.Class != ErrorClassServer || deltas[1].Error.Message != "service unavailable" {
		t.Fatalf("unexpected stream error delta: %#v", deltas)
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
				Body:       io.NopCloser(bytes.NewBufferString(`{"error":{"message":"bad key","type":"authentication_error"}}`)),
			}, nil
		})},
	}
	_, err := client.Generate(t.Context(), Request{Model: "test-model"})
	if err == nil {
		t.Fatal("expected http error")
	}
	var providerError *ProviderError
	if !errors.As(err, &providerError) {
		t.Fatalf("expected ProviderError, got %T: %v", err, err)
	}
	if providerError.Class != ErrorClassAuth || providerError.StatusCode != http.StatusUnauthorized || providerError.Retryable || providerError.Message != "bad key" {
		t.Fatalf("unexpected provider error: %#v", providerError)
	}
}

func TestOpenAICompatibleClientClassifiesProviderErrors(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		class      ErrorClass
		retryable  bool
	}{
		{name: "rate limit", statusCode: http.StatusTooManyRequests, body: `{"error":{"message":"quota reached","type":"rate_limit_error"}}`, class: ErrorClassRateLimit, retryable: true},
		{name: "context length", statusCode: http.StatusBadRequest, body: `{"error":{"message":"maximum context length exceeded","code":"context_length_exceeded"}}`, class: ErrorClassContextLength},
		{name: "invalid request", statusCode: http.StatusUnprocessableEntity, body: `{"error":{"message":"invalid tool schema","type":"invalid_request_error"}}`, class: ErrorClassInvalidRequest},
		{name: "timeout", statusCode: http.StatusGatewayTimeout, body: "upstream timeout", class: ErrorClassTimeout, retryable: true},
		{name: "server", statusCode: http.StatusServiceUnavailable, body: "temporarily unavailable", class: ErrorClassServer, retryable: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := OpenAICompatibleClient{
				BaseURL:     "http://llm.example/v1",
				APIKey:      "test-key",
				MaxAttempts: 1,
				Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: test.statusCode,
						Status:     http.StatusText(test.statusCode),
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader(test.body)),
					}, nil
				})},
			}
			_, err := client.Generate(t.Context(), Request{Model: "test-model"})
			var providerError *ProviderError
			if !errors.As(err, &providerError) {
				t.Fatalf("expected ProviderError, got %T: %v", err, err)
			}
			if providerError.Class != test.class || providerError.StatusCode != test.statusCode || providerError.Retryable != test.retryable {
				t.Fatalf("unexpected provider error: %#v", providerError)
			}
		})
	}
}

func TestOpenAICompatibleClientClassifiesTransportErrors(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		class     ErrorClass
		retryable bool
	}{
		{name: "timeout", err: context.DeadlineExceeded, class: ErrorClassTimeout, retryable: true},
		{name: "network", err: errors.New("connection reset by peer"), class: ErrorClassServer, retryable: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := OpenAICompatibleClient{
				BaseURL:     "http://llm.example/v1",
				APIKey:      "test-key",
				MaxAttempts: 1,
				Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					return nil, test.err
				})},
			}
			_, err := client.Generate(t.Context(), Request{Model: "test-model"})
			var providerError *ProviderError
			if !errors.As(err, &providerError) {
				t.Fatalf("expected ProviderError, got %T: %v", err, err)
			}
			if providerError.Class != test.class || providerError.Retryable != test.retryable {
				t.Fatalf("unexpected provider error: %#v", providerError)
			}
		})
	}
}

func TestOpenAICompatibleClientRetriesTransientErrors(t *testing.T) {
	attempts := 0
	client := OpenAICompatibleClient{
		BaseURL:        "http://llm.example/v1",
		APIKey:         "test-key",
		MaxAttempts:    3,
		RetryBaseDelay: time.Nanosecond,
		Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			attempts++
			var request openAIChatRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode retry request: %v", err)
			}
			if request.Model != "test-model" {
				t.Fatalf("unexpected retry model: %q", request.Model)
			}
			switch attempts {
			case 1:
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Header:     http.Header{"Retry-After": []string{"0"}},
					Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"slow down"}}`)),
				}, nil
			case 2:
				return &http.Response{
					StatusCode: http.StatusServiceUnavailable,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"try again"}}`)),
				}, nil
			default:
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"recovered"}}]}`)),
				}, nil
			}
		})},
	}

	response, err := client.Generate(t.Context(), Request{Model: "test-model"})
	if err != nil {
		t.Fatalf("generate after transient errors: %v", err)
	}
	if attempts != 3 || response.Message.Content[0].Text != "recovered" {
		t.Fatalf("unexpected retry result attempts=%d response=%#v", attempts, response)
	}
}

func TestOpenAICompatibleClientReportsExhaustedRetryAttempts(t *testing.T) {
	attempts := 0
	client := OpenAICompatibleClient{
		BaseURL:        "http://llm.example/v1",
		APIKey:         "test-key",
		MaxAttempts:    2,
		RetryBaseDelay: time.Nanosecond,
		Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			attempts++
			header := make(http.Header)
			if attempts == 2 {
				header.Set("Retry-After", "2")
			}
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Header:     header,
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"still unavailable"}}`)),
			}, nil
		})},
	}

	_, err := client.Generate(t.Context(), Request{Model: "test-model"})
	var providerError *ProviderError
	if !errors.As(err, &providerError) {
		t.Fatalf("expected ProviderError, got %T: %v", err, err)
	}
	if attempts != 2 || providerError.Attempts != 2 || providerError.RetryAfter != 2*time.Second || !providerError.Retryable || providerError.Class != ErrorClassServer {
		t.Fatalf("unexpected exhausted retry error attempts=%d error=%#v", attempts, providerError)
	}
}

func TestOpenAICompatibleClientCancelsRetryBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	attempts := 0
	client := OpenAICompatibleClient{
		BaseURL:        "http://llm.example/v1",
		APIKey:         "test-key",
		MaxAttempts:    3,
		RetryBaseDelay: time.Hour,
		Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			attempts++
			cancel()
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"slow down"}}`)),
			}, nil
		})},
	}

	_, err := client.Generate(ctx, Request{Model: "test-model"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled retry backoff, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected cancellation before retry, got %d attempts", attempts)
	}
}

func TestOpenAICompatibleClientDoesNotRetryAfterStreamStarts(t *testing.T) {
	attempts := 0
	var deltas []Delta
	client := OpenAICompatibleClient{
		BaseURL:        "http://llm.example/v1",
		APIKey:         "test-key",
		MaxAttempts:    3,
		RetryBaseDelay: time.Nanosecond,
		Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			attempts++
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("data: {not-json}\n\n")),
			}, nil
		})},
	}

	_, err := client.GenerateStream(t.Context(), Request{Model: "test-model"}, func(delta Delta) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err == nil {
		t.Fatal("expected stream decode error")
	}
	if attempts != 1 {
		t.Fatalf("expected no retry after successful stream response, got %d attempts", attempts)
	}
	if len(deltas) != 1 || deltas[0].Kind != DeltaKindError || deltas[0].Error == nil || deltas[0].Error.Class != ErrorClassUnknown {
		t.Fatalf("expected typed stream decode error, got %#v", deltas)
	}
}

func TestOpenAICompatibleToolParametersStripUnsupportedCombinatorsRecursively(t *testing.T) {
	raw := json.RawMessage(`{
		"type":"object",
		"properties":{
			"source":{
				"type":"object",
				"properties":{"provider":{"type":"string","const":"catalog"}},
				"allOf":[{"if":{"required":["provider"]},"then":{"required":["catalog_entry_id"]}}]
			}
		},
		"anyOf":[{"required":["source"]}],
		"oneOf":[{"required":["source"]}]
	}`)
	compatible := openAICompatibleToolParameters(raw)
	for _, keyword := range []string{`"anyOf"`, `"oneOf"`, `"allOf"`, `"if"`, `"then"`, `"else"`, `"const"`} {
		if strings.Contains(string(compatible), keyword) {
			t.Fatalf("compatible schema retained %s: %s", keyword, compatible)
		}
	}
	for _, preserved := range []string{`"type":"object"`, `"properties"`, `"provider"`} {
		if !strings.Contains(string(compatible), preserved) {
			t.Fatalf("compatible schema lost %s: %s", preserved, compatible)
		}
	}
}

func TestParseProviderRetryAfter(t *testing.T) {
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	if got, ok := parseProviderRetryAfter("3", now); !ok || got != 3*time.Second {
		t.Fatalf("expected 3 second Retry-After, got %s", got)
	}
	if got, ok := parseProviderRetryAfter(now.Add(5*time.Second).Format(http.TimeFormat), now); !ok || got != 5*time.Second {
		t.Fatalf("expected HTTP-date Retry-After, got %s", got)
	}
	if got, ok := parseProviderRetryAfter("120", now); !ok || got != MaxProviderRetryDelay {
		t.Fatalf("expected capped Retry-After, got %s", got)
	}
	if got, ok := parseProviderRetryAfter("0", now); !ok || got != 0 {
		t.Fatalf("expected immediate Retry-After, got %s ok=%v", got, ok)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
