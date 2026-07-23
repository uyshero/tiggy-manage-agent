package agentruntime

import (
	"context"
	"testing"

	"tiggy-manage-agent/internal/llm"
)

func TestToolMarkupCompletionGateBlocksSerializedSeedCall(t *testing.T) {
	gate := ToolMarkupCompletionGate{}
	verdict, err := gate.Validate(context.Background(), CompletionCandidate{Response: llm.Response{Message: llm.Message{
		Content: []llm.ContentPart{{Type: "text", Text: `<seed:tool_call><function name="default_run_command">`}},
	}}})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if verdict.Outcome != CompletionOutcomeRetry || verdict.Validator != toolMarkupCompletionValidator {
		t.Fatalf("unexpected verdict: %#v", verdict)
	}
}

func TestToolMarkupCompletionGatePassesOrdinaryFinalText(t *testing.T) {
	gate := ToolMarkupCompletionGate{}
	verdict, err := gate.Validate(context.Background(), CompletionCandidate{Response: llm.Response{Message: llm.Message{
		Content: []llm.ContentPart{{Type: "text", Text: "The requested file is ready."}},
	}}})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if verdict.Outcome != CompletionOutcomePass {
		t.Fatalf("unexpected verdict: %#v", verdict)
	}
}
