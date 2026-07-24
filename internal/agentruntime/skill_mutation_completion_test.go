package agentruntime

import (
	"context"
	"testing"

	"tiggy-manage-agent/internal/llm"
)

func TestSkillMutationCompletionGateRetriesUnsupportedSuccessClaim(t *testing.T) {
	candidate := skillMutationCandidate("Skill 已更新至 image-to-editable-ppt-v4 v1，当前启用。")
	verdict, err := (SkillMutationCompletionGate{}).Validate(context.Background(), candidate)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if verdict.Outcome != CompletionOutcomeRetry || verdict.Validator != skillMutationCompletionValidator {
		t.Fatalf("unexpected verdict: %+v", verdict)
	}
}

func TestSkillMutationCompletionGateRequiresEveryClaimedMutation(t *testing.T) {
	candidate := skillMutationCandidate("Skill 已安装并已启用。")
	candidate.ToolExecutions = []CompletionToolExecution{{Name: skillsInstallToolName, Status: "succeeded"}}
	verdict, err := (SkillMutationCompletionGate{}).Validate(context.Background(), candidate)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if verdict.Outcome != CompletionOutcomeRetry {
		t.Fatalf("expected missing enable call to retry, got %+v", verdict)
	}
}

func TestSkillMutationCompletionGatePassesMatchingSuccessfulMutations(t *testing.T) {
	candidate := skillMutationCandidate("Skill 已更新并已启用。")
	candidate.ToolExecutions = []CompletionToolExecution{
		{Name: skillsInstallToolName, Status: "succeeded"},
		{Name: skillsEnableToolName, Status: "succeeded"},
	}
	verdict, err := (SkillMutationCompletionGate{}).Validate(context.Background(), candidate)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if verdict.Outcome != CompletionOutcomePass {
		t.Fatalf("expected matching executions to pass, got %+v", verdict)
	}
}

func TestSkillMutationCompletionGateRejectsFailedMutation(t *testing.T) {
	candidate := skillMutationCandidate("Skill 安装成功。")
	candidate.ToolExecutions = []CompletionToolExecution{{Name: skillsInstallToolName, Status: "failed", IsError: true}}
	verdict, err := (SkillMutationCompletionGate{}).Validate(context.Background(), candidate)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if verdict.Outcome != CompletionOutcomeRetry {
		t.Fatalf("expected failed execution to retry, got %+v", verdict)
	}
}

func TestSkillMutationCompletionGateAllowsCorrectionAndDocumentation(t *testing.T) {
	for _, text := range []string{
		"我之前声称 Skill 已更新，但这是错误的；实际上没有调用 skills_install。",
		"skills_enable 用于启用已经安装的 Skill。",
		"image-to-editable-ppt-v4 根本不存在，尚未安装。",
	} {
		verdict, err := (SkillMutationCompletionGate{}).Validate(context.Background(), skillMutationCandidate(text))
		if err != nil {
			t.Fatalf("Validate(%q) error = %v", text, err)
		}
		if verdict.Outcome != CompletionOutcomePass {
			t.Fatalf("expected truthful non-success response to pass for %q, got %+v", text, verdict)
		}
	}
}

func skillMutationCandidate(text string) CompletionCandidate {
	return CompletionCandidate{Response: llm.Response{Message: llm.Message{
		Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: text}},
	}}}
}
