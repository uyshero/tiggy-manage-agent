package agentruntime

import (
	"encoding/json"
	"testing"
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
