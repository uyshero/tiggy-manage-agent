package llm

import "testing"

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
