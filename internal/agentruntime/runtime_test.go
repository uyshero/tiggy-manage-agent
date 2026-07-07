package agentruntime

import (
	"context"
	"encoding/json"
	"testing"

	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
)

func TestDemoRuntimeReturnsAgentPayload(t *testing.T) {
	runtime := DemoRuntime{}

	payload, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"hello"}]}`),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	if got := payloadText(payload); got != "Agent runtime received: hello" {
		t.Fatalf("expected demo runtime payload, got %q", got)
	}
	if got := payloadString(payload, "protocol_version"); got != DemoProtocolVersion {
		t.Fatalf("expected protocol version %q, got %q", DemoProtocolVersion, got)
	}
}

func TestDemoRuntimeEmitsLLMDeltaForStreamingClient(t *testing.T) {
	runtime := DemoRuntime{Client: streamingTestClient{}}
	var steps []Step

	payload, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"hello"}]}`),
		EmitStep: func(ctx context.Context, step Step) error {
			steps = append(steps, step)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	if got := payloadText(payload); got != "streamed response" {
		t.Fatalf("expected streamed payload, got %q", got)
	}
	if !hasStepType(steps, managedagents.EventRuntimeLLMDelta) {
		t.Fatalf("expected runtime.llm_delta in steps: %#v", steps)
	}
}

func TestDemoRuntimePassesProviderConfigToLLMClient(t *testing.T) {
	client := &captureLLMClient{}
	runtime := DemoRuntime{Client: client}

	_, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"hello"}]}`),
		Config: Config{
			LLMProvider:     "volcengine-agent-plan",
			LLMProviderType: llm.ProviderTypeOpenAI,
			LLMModel:        "doubao-test",
			LLMBaseURL:      "http://llm.example/v1",
			LLMAPIKey:       "test-key",
		},
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	if client.request.Provider != "volcengine-agent-plan" {
		t.Fatalf("expected provider from config, got %q", client.request.Provider)
	}
	if client.request.ProviderType != llm.ProviderTypeOpenAI {
		t.Fatalf("expected provider type from config, got %q", client.request.ProviderType)
	}
	if client.request.Model != "doubao-test" {
		t.Fatalf("expected model from config, got %q", client.request.Model)
	}
	if client.request.BaseURL != "http://llm.example/v1" || client.request.APIKey != "test-key" {
		t.Fatalf("expected transport config, got base_url=%q api_key=%q", client.request.BaseURL, client.request.APIKey)
	}
}

func TestDemoRuntimePassesConversationHistoryToLLMClient(t *testing.T) {
	client := &captureLLMClient{}
	runtime := DemoRuntime{Client: client}

	_, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000002",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"what is my name?"}]}`),
		History: []managedagents.ConversationMessage{
			{
				Seq:     3,
				Role:    "user",
				Payload: json.RawMessage(`{"content":[{"type":"text","text":"my name is Alice"}],"turn_id":"turn_000001"}`),
			},
			{
				Seq:     4,
				Role:    "assistant",
				Payload: json.RawMessage(`{"content":[{"type":"text","text":"Nice to meet you, Alice."}],"turn_id":"turn_000001"}`),
			},
		},
		Config: Config{System: "remember user facts"},
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	if len(client.request.Messages) != 4 {
		t.Fatalf("expected 4 LLM messages, got %#v", client.request.Messages)
	}
	assertLLMMessage(t, client.request.Messages[0], "system", "remember user facts")
	assertLLMMessage(t, client.request.Messages[1], "user", "my name is Alice")
	assertLLMMessage(t, client.request.Messages[2], "assistant", "Nice to meet you, Alice.")
	assertLLMMessage(t, client.request.Messages[3], "user", "what is my name?")
}

func assertLLMMessage(t *testing.T, message llm.Message, role string, text string) {
	t.Helper()
	if message.Role != role {
		t.Fatalf("expected role %q, got %q", role, message.Role)
	}
	if len(message.Content) != 1 || message.Content[0].Text != text {
		t.Fatalf("expected text %q, got %#v", text, message.Content)
	}
}

type captureLLMClient struct {
	request llm.Request
}

func (c *captureLLMClient) Generate(ctx context.Context, request llm.Request) (llm.Response, error) {
	c.request = request
	return llm.Response{
		Message: llm.Message{
			Role: "assistant",
			Content: []llm.ContentPart{{
				Type: "text",
				Text: "captured",
			}},
		},
	}, nil
}

type streamingTestClient struct{}

func (streamingTestClient) Generate(ctx context.Context, request llm.Request) (llm.Response, error) {
	return llm.Response{}, nil
}

func (streamingTestClient) GenerateStream(ctx context.Context, request llm.Request, onDelta func(llm.Delta) error) (llm.Response, error) {
	if err := onDelta(llm.Delta{Index: 1, Text: "streamed "}); err != nil {
		return llm.Response{}, err
	}
	if err := onDelta(llm.Delta{Index: 2, Text: "response"}); err != nil {
		return llm.Response{}, err
	}
	return llm.Response{
		Message: llm.Message{
			Role: "assistant",
			Content: []llm.ContentPart{{
				Type: "text",
				Text: "streamed response",
			}},
		},
	}, nil
}

func hasStepType(steps []Step, stepType string) bool {
	for _, step := range steps {
		if step.Type == stepType {
			return true
		}
	}
	return false
}

func payloadText(payload json.RawMessage) string {
	var object struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(payload, &object); err != nil {
		return ""
	}
	for _, content := range object.Content {
		if content.Type == "text" {
			return content.Text
		}
	}
	return ""
}

func payloadString(payload json.RawMessage, key string) string {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil {
		return ""
	}
	var value string
	if err := json.Unmarshal(object[key], &value); err != nil {
		return ""
	}
	return value
}
