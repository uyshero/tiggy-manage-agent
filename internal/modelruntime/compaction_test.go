package modelruntime

import (
	"context"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/agentcore"
	coremodel "tiggy-manage-agent/internal/model"
	"tiggy-manage-agent/internal/tokenestimate"
)

func TestEstimateCoreMessagesCountsDecodedContentInsteadOfJSONEscaping(t *testing.T) {
	text := strings.Repeat(`{"name":"tool","description":"value"}`, 200)
	messages := []coremodel.Message{{
		ID: "system", Role: coremodel.RoleSystem, Visibility: coremodel.VisibilityInternal,
		Content: []coremodel.Content{{Type: coremodel.ContentText, Text: text}},
	}}

	want := 4 + tokenestimate.Text(text)
	if got := estimateCoreMessages(messages); got != want {
		t.Fatalf("estimateCoreMessages() = %d, want %d", got, want)
	}

	compactor := LLMCompactor{Model: stubCompactionModel{}, ThresholdTokens: want + 1}
	if compactor.NeedsCompaction(agentcore.State{Messages: messages}) {
		t.Fatal("JSON transport escaping must not trigger context compaction")
	}
}

func TestCompactionPreservesArtifactReferencesInsteadOfCopyingLargeResults(t *testing.T) {
	model := &recordingCompactionModel{}
	compactor := LLMCompactor{Model: model, SummaryMaxChars: 12000}
	state := agentcore.State{
		SessionID: "session_1", TurnID: "turn_1",
		Messages: []coremodel.Message{{
			ID: "tool_result_1", Role: coremodel.RoleTool, Visibility: coremodel.VisibilityInternal,
			Content: []coremodel.Content{{Type: coremodel.ContentToolResult, ToolResult: &coremodel.ToolResult{
				CallID: "call_1", Name: "default_run_command",
				Content: []coremodel.Content{{Type: coremodel.ContentText, Text: `{"context":{"content_artifact_id":"art_123","next_offset_bytes":4096}}`}},
			}}},
		}},
	}
	result, err := compactor.Compact(t.Context(), state, "compact_1")
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if result.Summary != "Continue from Artifact art_123." || len(model.request.Messages) != 2 {
		t.Fatalf("unexpected compaction result=%#v request=%#v", result, model.request)
	}
	instruction := model.request.Messages[0].Content[0].Text
	for _, expected := range []string{"Preserve Artifact IDs", "paging cursors", "instead of copying"} {
		if !strings.Contains(instruction, expected) {
			t.Fatalf("compaction instruction is missing %q: %s", expected, instruction)
		}
	}
	if !strings.Contains(model.request.Messages[1].Content[0].Text, "art_123") {
		t.Fatalf("compaction input lost Artifact reference: %#v", model.request.Messages[1])
	}
}

type recordingCompactionModel struct {
	request coremodel.Request
}

func (m *recordingCompactionModel) Generate(_ context.Context, request coremodel.Request, _ agentcore.DeltaSink) (coremodel.Response, error) {
	m.request = request
	return coremodel.Response{
		Message:    coremodel.Message{Role: coremodel.RoleAssistant, Content: []coremodel.Content{{Type: coremodel.ContentText, Text: "Continue from Artifact art_123."}}},
		StopReason: coremodel.StopReasonComplete,
	}, nil
}

type stubCompactionModel struct{}

func (stubCompactionModel) Generate(_ context.Context, _ coremodel.Request, _ agentcore.DeltaSink) (coremodel.Response, error) {
	return coremodel.Response{}, nil
}
