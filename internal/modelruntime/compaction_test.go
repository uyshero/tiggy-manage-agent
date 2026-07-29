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

func TestEstimateCoreMessagesIgnoresDurableToolState(t *testing.T) {
	visible := "bounded result"
	resultName := "default_run_command"
	messages := []coremodel.Message{{
		ID: "tool_result", Role: coremodel.RoleTool, Visibility: coremodel.VisibilityInternal,
		Content: []coremodel.Content{{Type: coremodel.ContentToolResult, ToolResult: &coremodel.ToolResult{
			CallID: "call_1", Name: resultName,
			Content: []coremodel.Content{{Type: coremodel.ContentText, Text: visible}},
			State:   []byte(`{"stdout":"` + strings.Repeat("durable-only", 10000) + `"}`),
		}}},
	}}
	want := 4 + 8 + tokenestimate.Text(resultName) + tokenestimate.Text(visible)
	if got := estimateCoreMessages(messages); got != want {
		t.Fatalf("estimateCoreMessages() counted durable ToolResult State: got=%d want=%d", got, want)
	}
}

func TestCompactionPreservesArtifactReferencesInsteadOfCopyingLargeResults(t *testing.T) {
	model := &recordingCompactionModel{summary: "Continue work without repeating any Artifact ID."}
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
	if len(model.request.Messages) != 2 {
		t.Fatalf("unexpected compaction result=%#v request=%#v", result, model.request)
	}
	for _, expected := range []string{compactionArtifactReferenceProtocol, "art_123", "next_offset_bytes", "4096", "artifact_read"} {
		if !strings.Contains(result.Summary, expected) {
			t.Fatalf("runtime-preserved summary is missing %q: %s", expected, result.Summary)
		}
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

func TestCompactionPreservesLatestArtifactReadCursorFromDurableState(t *testing.T) {
	model := &recordingCompactionModel{summary: strings.Repeat("Detailed model summary. ", 200)}
	state := agentcore.State{
		SessionID: "session_1", TurnID: "turn_1",
		Messages: []coremodel.Message{
			{
				ID: "tool_result_1", Role: coremodel.RoleTool, Visibility: coremodel.VisibilityInternal,
				Content: []coremodel.Content{{Type: coremodel.ContentToolResult, ToolResult: &coremodel.ToolResult{
					CallID: "call_1", Name: "default_run_command",
					Content: []coremodel.Content{{Type: coremodel.ContentText, Text: `{"context":{"content_artifact_id":"art_page","next_offset_bytes":8192}}`}},
				}}},
			},
			{
				ID: "tool_result_2", Role: coremodel.RoleTool, Visibility: coremodel.VisibilityInternal,
				Content: []coremodel.Content{{Type: coremodel.ContentToolResult, ToolResult: &coremodel.ToolResult{
					CallID: "call_2", Name: "artifact_read",
					Content: []coremodel.Content{{Type: coremodel.ContentText, Text: "bounded page"}},
					State:   []byte(`{"artifact":{"artifact_id":"art_page"},"offset_bytes":8192,"returned_bytes":8192,"next_offset_bytes":16384,"eof":false}`),
				}}},
			},
		},
	}
	result, err := (LLMCompactor{Model: model, SummaryMaxChars: 1200}).Compact(t.Context(), state, "compact_cursor")
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if len([]rune(result.Summary)) > 1200 {
		t.Fatalf("summary exceeded configured bound: chars=%d", len([]rune(result.Summary)))
	}
	for _, expected := range []string{`"artifact_id":"art_page"`, `"next_offset_bytes":16384`, `"eof":false`, `"tool":"artifact_read"`, `"call_id":"call_2"`} {
		if !strings.Contains(result.Summary, expected) {
			t.Fatalf("summary lost latest Artifact cursor field %s: %s", expected, result.Summary)
		}
	}
}

func TestTruncateCompactionSummaryHonorsMaximumIncludingMarker(t *testing.T) {
	for _, maximum := range []int{8, 64, 128} {
		result := truncateCompactionSummary(strings.Repeat("x", 1000), maximum)
		if len([]rune(result)) > maximum {
			t.Fatalf("truncateCompactionSummary exceeded %d characters: %d", maximum, len([]rune(result)))
		}
	}
}

func TestCompactionProjectsLargeRecoverableToolResults(t *testing.T) {
	largeMarker := strings.Repeat("UNPROJECTED_TOOL_OUTPUT_", 400)
	envelope := `{"protocol_version":"tma.tool_result.v1","id":"call_1","identifier":"default","api_name":"run_command","content":"` + largeMarker + `","state":{"stdout":"` + largeMarker + `"},"artifacts":[{"artifact_id":"art_200","name":"run_command.json"}],"context":{"content_artifact_id":"art_200","state_artifact_id":"art_200"},"success":true}`
	largeState := []byte(`{"stdout":"` + largeMarker + `"}`)
	state := agentcore.State{
		SessionID: "session_1", TurnID: "turn_1",
		Messages: []coremodel.Message{{
			ID: "tool_result_1", Role: coremodel.RoleTool, Visibility: coremodel.VisibilityInternal,
			Content: []coremodel.Content{{Type: coremodel.ContentToolResult, ToolResult: &coremodel.ToolResult{
				CallID: "call_1", Name: "default_run_command", State: largeState,
				Content: []coremodel.Content{{Type: coremodel.ContentText, Text: envelope}},
			}}},
		}},
	}
	model := &recordingCompactionModel{}
	if _, err := (LLMCompactor{Model: model}).Compact(t.Context(), state, "compact_projection"); err != nil {
		t.Fatalf("compact: %v", err)
	}
	input := model.request.Messages[1].Content[0].Text
	if strings.Contains(input, "UNPROJECTED_TOOL_OUTPUT") {
		t.Fatalf("compaction input retained recoverable large output: %s", input)
	}
	for _, expected := range []string{"art_200", "artifact_read", "original_state_bytes", "content_artifact_id"} {
		if !strings.Contains(input, expected) {
			t.Fatalf("compaction projection lost %q: %s", expected, input)
		}
	}
	if !strings.Contains(state.Messages[0].Content[0].ToolResult.Content[0].Text, "UNPROJECTED_TOOL_OUTPUT") || !strings.Contains(string(state.Messages[0].Content[0].ToolResult.State), "UNPROJECTED_TOOL_OUTPUT") {
		t.Fatal("compaction projection mutated durable Agent Core state")
	}
}

func TestCompactionKeepsLargeToolResultsWithoutArtifactReferences(t *testing.T) {
	largeMarker := strings.Repeat("ONLY_COPY_OF_RESULT_", 400)
	messages := []coremodel.Message{{
		ID: "tool_result_1", Role: coremodel.RoleTool, Visibility: coremodel.VisibilityInternal,
		Content: []coremodel.Content{{Type: coremodel.ContentToolResult, ToolResult: &coremodel.ToolResult{
			CallID: "call_1", Name: "custom_lookup", State: []byte(`{"value":"` + largeMarker + `"}`),
			Content: []coremodel.Content{{Type: coremodel.ContentText, Text: `{"protocol_version":"tma.tool_result.v1","content":"` + largeMarker + `","success":true}`}},
		}}},
	}}
	projected := projectCompactionMessages(messages)
	if !strings.Contains(projected[0].Content[0].ToolResult.Content[0].Text, "ONLY_COPY_OF_RESULT") || !strings.Contains(string(projected[0].Content[0].ToolResult.State), "ONLY_COPY_OF_RESULT") {
		t.Fatal("compaction removed a large result that had no durable recovery reference")
	}
}

type recordingCompactionModel struct {
	request coremodel.Request
	summary string
}

func (m *recordingCompactionModel) Generate(_ context.Context, request coremodel.Request, _ agentcore.DeltaSink) (coremodel.Response, error) {
	m.request = request
	summary := m.summary
	if summary == "" {
		summary = "Continue from Artifact art_123."
	}
	return coremodel.Response{
		Message:    coremodel.Message{Role: coremodel.RoleAssistant, Content: []coremodel.Content{{Type: coremodel.ContentText, Text: summary}}},
		StopReason: coremodel.StopReasonComplete,
	}, nil
}

type stubCompactionModel struct{}

func (stubCompactionModel) Generate(_ context.Context, _ coremodel.Request, _ agentcore.DeltaSink) (coremodel.Response, error) {
	return coremodel.Response{}, nil
}
