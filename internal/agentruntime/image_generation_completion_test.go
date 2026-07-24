package agentruntime

import (
	"context"
	"encoding/json"
	"testing"

	"tiggy-manage-agent/internal/llm"
)

func TestImageGenerationCompletionGateRetriesTextOnlyPromise(t *testing.T) {
	gate := ImageGenerationCompletionGate{}
	verdict, err := gate.Validate(context.Background(), imageGenerationCandidate("画个大象"))
	if err != nil || verdict.Outcome != CompletionOutcomeRetry || verdict.Validator != imageGenerationCompletionValidator {
		t.Fatalf("unexpected verdict=%+v err=%v", verdict, err)
	}
	if verdict.Evidence["image_generation_intent"] != true || verdict.Evidence["image_generate_attempted"] != false {
		t.Fatalf("unexpected evidence: %#v", verdict.Evidence)
	}
}

func TestImageGenerationCompletionGatePassesAfterToolAttempt(t *testing.T) {
	candidate := imageGenerationCandidate("请帮我画一只大象")
	candidate.Messages = append(candidate.Messages, llm.Message{
		Role: "assistant",
		ToolCalls: []llm.ToolCall{{
			ID: "call_image", Type: "function",
			Function: llm.ToolCallFunction{Name: imageGenerateToolName, Arguments: json.RawMessage(`{"prompt":"an elephant","use_case":"illustration-story"}`)},
		}},
	}, llm.Message{Role: "tool", ToolCallID: "call_image", Content: []llm.ContentPart{{Type: "text", Text: "Generated 1 image."}}})
	verdict, err := ImageGenerationCompletionGate{}.Validate(context.Background(), candidate)
	if err != nil || verdict.Outcome != CompletionOutcomePass || verdict.Evidence["image_generate_attempted"] != true {
		t.Fatalf("unexpected verdict=%+v err=%v", verdict, err)
	}
}

func TestImageGenerationCompletionGateDoesNotForceInformationalQuestions(t *testing.T) {
	for _, prompt := range []string{"你会画图吗", "画图工具叫啥", "如何画图", "what tools can generate images?"} {
		verdict, err := ImageGenerationCompletionGate{}.Validate(context.Background(), imageGenerationCandidate(prompt))
		if err != nil || verdict.Outcome != CompletionOutcomePass {
			t.Fatalf("prompt=%q verdict=%+v err=%v", prompt, verdict, err)
		}
	}
}

func TestImageGenerationCompletionGatePassesWhenToolUnavailable(t *testing.T) {
	candidate := imageGenerationCandidate("画个大象")
	candidate.ActiveTools = []string{"default_read_file"}
	verdict, err := ImageGenerationCompletionGate{}.Validate(context.Background(), candidate)
	if err != nil || verdict.Outcome != CompletionOutcomePass {
		t.Fatalf("unexpected verdict=%+v err=%v", verdict, err)
	}
}

func TestRequestsImageGenerationRecognizesCommonRequestsAndFollowups(t *testing.T) {
	for _, prompt := range []string{
		"画个大象", "请画大象", "请帮我画一只大象", "生成一张产品图片", "马上画", "画了吗",
		"draw an elephant image", "create a product photo",
	} {
		if !requestsImageGeneration(prompt) {
			t.Fatalf("expected image generation intent for %q", prompt)
		}
	}
}

func imageGenerationCandidate(userText string) CompletionCandidate {
	return CompletionCandidate{
		SessionID: "session", TurnID: "turn", ActiveTools: []string{imageGenerateToolName},
		Messages: []llm.Message{{Role: "user", Content: []llm.ContentPart{{Type: "text", Text: userText}}}},
		Response: llm.Response{Message: llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: "好的，我来画。"}}}},
	}
}
