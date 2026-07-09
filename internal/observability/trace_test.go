package observability

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
)

func TestProjectTurnTraceBuildsToolSummary(t *testing.T) {
	now := time.Now().UTC()
	events := []managedagents.Event{
		{
			Seq:       1,
			Type:      managedagents.EventUserMessage,
			Payload:   json.RawMessage(`{"turn_id":"turn_1","content":[{"type":"text","text":"please edit"}]}`),
			CreatedAt: now,
		},
		{
			Seq:       2,
			Type:      managedagents.EventRuntimeToolInterventionRejected,
			Payload:   json.RawMessage(`{"turn_id":"turn_1","message":"Tool call rejected by user.","data":{"id":"call_edit","identifier":"default","api_name":"edit_file","decision_reason":"unsafe"}}`),
			CreatedAt: now,
		},
		{
			Seq:       3,
			Type:      managedagents.EventRuntimeToolResult,
			Payload:   json.RawMessage(`{"turn_id":"turn_1","message":"Rejected tool result.","data":{"id":"call_edit","identifier":"default","api_name":"edit_file","success":false,"decision_reason":"unsafe","artifact_error":"object store unavailable","artifacts":[{"artifact_id":"art_000001","name":"edit.patch","artifact_type":"asset","download_path":"/v1/sessions/sesn_1/artifacts/art_000001/download"}]}}`),
			CreatedAt: now,
		},
	}

	trace := ProjectTurnTrace("sesn_1", "turn_1", events)
	if trace.SessionID != "sesn_1" || trace.TurnID != "turn_1" {
		t.Fatalf("unexpected trace identity: %+v", trace)
	}
	if len(trace.Steps) != 3 {
		t.Fatalf("expected 3 trace steps, got %+v", trace.Steps)
	}
	if !strings.Contains(trace.Summary, "approval rejected: default.edit_file reason=unsafe") {
		t.Fatalf("expected rejection summary, got %q", trace.Summary)
	}
	if !strings.Contains(trace.Summary, "tool result: default.edit_file error reason=unsafe artifacts=1 artifact_error") {
		t.Fatalf("expected tool result summary, got %q", trace.Summary)
	}
	if len(trace.Steps[2].Artifacts) != 1 || trace.Steps[2].Artifacts[0].ArtifactID != "art_000001" {
		t.Fatalf("expected projected trace artifact, got %#v", trace.Steps[2].Artifacts)
	}
	if trace.Steps[2].ArtifactError != "object store unavailable" {
		t.Fatalf("expected projected artifact error, got %#v", trace.Steps[2])
	}
	if trace.TraceID == "" || len(trace.Spans) != 4 {
		t.Fatalf("expected root span plus 3 step spans, got trace_id=%q spans=%#v", trace.TraceID, trace.Spans)
	}
	if trace.Spans[0].Name != "tma.interaction" || trace.Spans[1].ParentSpanID != trace.Spans[0].SpanID {
		t.Fatalf("unexpected span tree: %#v", trace.Spans)
	}
	if trace.Stats.StepCount != 3 || trace.Stats.Errors != 2 || trace.Stats.ArtifactCount != 1 {
		t.Fatalf("unexpected trace stats: %#v", trace.Stats)
	}
	if len(trace.Turns) != 1 || trace.Turns[0].TurnID != "turn_1" || trace.Turns[0].Status != managedagents.TurnStatusFailed {
		t.Fatalf("expected turn catalog entry, got %#v", trace.Turns)
	}
	perfetto := ExportPerfetto(trace)
	if _, ok := perfetto["traceEvents"]; !ok {
		t.Fatalf("expected perfetto traceEvents, got %#v", perfetto)
	}
	if _, ok := perfetto["metadata"]; !ok {
		t.Fatalf("expected perfetto metadata, got %#v", perfetto)
	}
	otel := ExportOTel(trace)
	if _, ok := otel["resourceSpans"]; !ok {
		t.Fatalf("expected otel resourceSpans, got %#v", otel)
	}
	if _, ok := otel["metadata"]; !ok {
		t.Fatalf("expected otel metadata, got %#v", otel)
	}
}

func TestRefreshSessionSummaryAppendsTurnTrace(t *testing.T) {
	store := &stubSummaryStore{
		events: []managedagents.Event{
			{Seq: 1, Type: managedagents.EventUserMessage, Payload: json.RawMessage(`{"turn_id":"turn_2","content":[{"type":"text","text":"please read"}]}`)},
			{Seq: 2, Type: managedagents.EventRuntimeToolCall, Payload: json.RawMessage(`{"turn_id":"turn_2","message":"Received tool call request.","data":{"id":"call_read","identifier":"default","api_name":"read_file"}}`)},
			{Seq: 3, Type: managedagents.EventRuntimeToolResult, Payload: json.RawMessage(`{"turn_id":"turn_2","message":"Received tool result.","data":{"id":"call_read","identifier":"default","api_name":"read_file","success":true}}`)},
		},
		summary: managedagents.SessionSummary{
			SessionID:      "sesn_2",
			SummaryText:    "Conversation summary:\nOlder context",
			SourceUntilSeq: 1,
		},
	}

	if err := RefreshSessionSummary(store, "sesn_2", "turn_2"); err != nil {
		t.Fatalf("refresh summary: %v", err)
	}
	if store.saved.SummaryText == "" || !strings.Contains(store.saved.SummaryText, "Turn turn_2:") {
		t.Fatalf("expected turn trace appended to summary, got %+v", store.saved)
	}
	if store.saved.SourceUntilSeq != 3 {
		t.Fatalf("expected source_until_seq to advance, got %+v", store.saved)
	}
}

type stubSummaryStore struct {
	events   []managedagents.Event
	summary  managedagents.SessionSummary
	saved    managedagents.UpsertSessionSummaryInput
	notFound bool
}

func (s *stubSummaryStore) GetSessionSummary(string) (managedagents.SessionSummary, error) {
	if s.notFound {
		return managedagents.SessionSummary{}, managedagents.ErrNotFound
	}
	return s.summary, nil
}

func (s *stubSummaryStore) SaveSessionSummary(_ string, input managedagents.UpsertSessionSummaryInput) (managedagents.SessionSummary, error) {
	s.saved = input
	s.summary.SummaryText = input.SummaryText
	s.summary.SourceUntilSeq = input.SourceUntilSeq
	return s.summary, nil
}

func (s *stubSummaryStore) ListEvents(string, int64) ([]managedagents.Event, error) {
	return append([]managedagents.Event(nil), s.events...), nil
}
