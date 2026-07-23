package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
)

func TestArtifactCompletionGateBlocksUnregisteredWorkspaceFile(t *testing.T) {
	gate := ArtifactCompletionGate{Reader: artifactCompletionReader{artifacts: []managedagents.SessionArtifact{{
		Name: "generate_pig.py", Metadata: json.RawMessage(`{"protocol_version":"tma.tool_export.v1","path":"/workspace/generate_pig.py"}`),
	}}}}
	verdict, err := gate.Validate(t.Context(), completionCandidateText("图片已保存到：`/workspace/generated_pig.png`"))
	if err != nil || verdict.Outcome != CompletionOutcomeRetry {
		t.Fatalf("unexpected verdict=%#v err=%v", verdict, err)
	}
	for _, expected := range []string{"/workspace/generated_pig.png", "output_paths", "default_run_command"} {
		if !strings.Contains(verdict.Feedback, expected) {
			t.Fatalf("feedback missing %q: %s", expected, verdict.Feedback)
		}
	}
}

func TestArtifactCompletionGatePassesRegisteredWorkspaceFile(t *testing.T) {
	gate := ArtifactCompletionGate{Reader: artifactCompletionReader{artifacts: []managedagents.SessionArtifact{{
		Name: "generated_pig.png", Metadata: json.RawMessage(`{"protocol_version":"tma.tool_export.v1","path":"/workspace/generated_pig.png"}`),
	}}}}
	verdict, err := gate.Validate(t.Context(), completionCandidateText("图片已保存到 /workspace/generated_pig.png。"))
	if err != nil || verdict.Outcome != CompletionOutcomePass {
		t.Fatalf("unexpected verdict=%#v err=%v", verdict, err)
	}
}

func TestArtifactCompletionGateIgnoresWorkspaceDirectoriesAndOrdinaryReplies(t *testing.T) {
	gate := ArtifactCompletionGate{}
	verdict, err := gate.Validate(t.Context(), completionCandidateText("项目位于 /workspace/project，请继续操作。"))
	if err != nil || verdict.Outcome != CompletionOutcomePass {
		t.Fatalf("unexpected verdict=%#v err=%v", verdict, err)
	}
}

func TestArtifactCompletionGateFailsClosedWhenArtifactReadFails(t *testing.T) {
	gate := ArtifactCompletionGate{Reader: artifactCompletionReader{err: errors.New("database unavailable")}}
	verdict, err := gate.Validate(t.Context(), completionCandidateText("结果：/workspace/result.png"))
	if err == nil || !strings.Contains(err.Error(), "database unavailable") {
		t.Fatalf("expected artifact read failure, got verdict=%#v err=%v", verdict, err)
	}
}

func TestCompletionGateChainStopsAtFirstBlockedGate(t *testing.T) {
	first := &countingCompletionGate{verdict: CompletionVerdict{Outcome: CompletionOutcomeRetry, Validator: "first"}}
	second := &countingCompletionGate{verdict: CompletionVerdict{Outcome: CompletionOutcomePass, Validator: "second"}}
	chain := CompletionGateChain{Gates: []CompletionGate{
		first,
		second,
	}}
	verdict, err := chain.Validate(t.Context(), CompletionCandidate{})
	if err != nil || verdict.Validator != "first" || second.calls != 0 {
		t.Fatalf("unexpected verdict=%#v second_calls=%d err=%v", verdict, second.calls, err)
	}
}

type artifactCompletionReader struct {
	artifacts []managedagents.SessionArtifact
	err       error
}

func (reader artifactCompletionReader) ListSessionArtifacts(string) ([]managedagents.SessionArtifact, error) {
	return reader.artifacts, reader.err
}

type countingCompletionGate struct {
	verdict CompletionVerdict
	calls   int
}

func (gate *countingCompletionGate) Validate(context.Context, CompletionCandidate) (CompletionVerdict, error) {
	gate.calls++
	return gate.verdict, nil
}

func completionCandidateText(text string) CompletionCandidate {
	return CompletionCandidate{SessionID: "session", Response: llm.Response{Message: llm.Message{
		Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: text}},
	}}}
}
