package modelruntime

import (
	"context"
	"testing"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/agentruntime"
	coremodel "tiggy-manage-agent/internal/model"
)

func TestCompletionGatePropagatesToolJournalEvidence(t *testing.T) {
	capture := &completionCaptureGate{}
	gate := CompletionGate{Gate: capture, MaxRetries: 3}
	result := coremodel.ToolResult{CallID: "call_install", Name: "skills_install"}
	candidate := agentcore.CompletionCandidate{
		Message: coremodel.Message{ID: "final", Role: coremodel.RoleAssistant, Visibility: coremodel.VisibilityPublic, Content: []coremodel.Content{{Type: coremodel.ContentText, Text: "Skill installed."}}},
		State: agentcore.State{
			SessionID: "session", TurnID: "turn", Round: 4,
			ToolJournal: []agentcore.ToolCallJournalEntry{{
				CallID: "call_install", Name: "skills_install", Status: agentcore.ToolCallSucceeded, Result: &result,
			}},
		},
	}

	if _, err := gate.Validate(context.Background(), candidate); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if capture.candidate == nil || len(capture.candidate.ToolExecutions) != 1 {
		t.Fatalf("expected one propagated tool execution, got %+v", capture.candidate)
	}
	execution := capture.candidate.ToolExecutions[0]
	if execution.CallID != "call_install" || execution.Name != "skills_install" || execution.Status != "succeeded" || execution.IsError {
		t.Fatalf("unexpected propagated execution: %+v", execution)
	}
}

type completionCaptureGate struct {
	candidate *agentruntime.CompletionCandidate
}

func (gate *completionCaptureGate) Validate(_ context.Context, candidate agentruntime.CompletionCandidate) (agentruntime.CompletionVerdict, error) {
	cloned := candidate
	gate.candidate = &cloned
	return agentruntime.CompletionVerdict{Outcome: agentruntime.CompletionOutcomePass, Validator: "capture"}, nil
}
