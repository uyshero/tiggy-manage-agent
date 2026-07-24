package agentruntime

import (
	"context"
	"testing"

	"tiggy-manage-agent/internal/llm"
)

func TestFinalResponseCompletionGateRetriesEmptyVisibleResponse(t *testing.T) {
	gate := FinalResponseCompletionGate{}
	for _, content := range [][]llm.ContentPart{
		nil,
		{{Type: "text", Text: " \n\t "}},
	} {
		verdict, err := gate.Validate(context.Background(), CompletionCandidate{Response: llm.Response{Message: llm.Message{Content: content}}})
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		if verdict.Outcome != CompletionOutcomeRetry || verdict.Validator != finalResponseCompletionValidator {
			t.Fatalf("unexpected verdict: %#v", verdict)
		}
		if verdict.Feedback == "" {
			t.Fatal("expected actionable retry feedback")
		}
	}
}

func TestFinalResponseCompletionGatePassesVisibleResponse(t *testing.T) {
	gate := FinalResponseCompletionGate{}
	verdict, err := gate.Validate(context.Background(), CompletionCandidate{Response: llm.Response{Message: llm.Message{
		Content: []llm.ContentPart{{Type: "text", Text: "PPT 已生成并完成校验。"}},
	}}})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if verdict.Outcome != CompletionOutcomePass {
		t.Fatalf("unexpected verdict: %#v", verdict)
	}
}
