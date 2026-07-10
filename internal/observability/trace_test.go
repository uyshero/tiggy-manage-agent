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
	if trace.Spans[1].Depth != 1 || !trace.Spans[0].Critical || trace.Spans[0].SelfDurationMillis < 0 {
		t.Fatalf("expected span graph annotations, got %#v", trace.Spans)
	}
	if len(trace.Spans[0].ChildSpanIDs) != 3 || len(trace.Spans[0].Events) != 3 {
		t.Fatalf("expected root span children and events, got %#v", trace.Spans[0])
	}
	if len(trace.Spans[1].Events) == 0 || trace.Spans[1].Events[0].Seq != 1 {
		t.Fatalf("expected span events to reference source steps, got %#v", trace.Spans[1].Events)
	}
	if trace.Stats.StepCount != 3 || trace.Stats.Errors != 2 || trace.Stats.ArtifactCount != 1 {
		t.Fatalf("unexpected trace stats: %#v", trace.Stats)
	}
	if len(trace.Turns) != 1 || trace.Turns[0].TurnID != "turn_1" || trace.Turns[0].Status != managedagents.TurnStatusFailed {
		t.Fatalf("expected turn catalog entry, got %#v", trace.Turns)
	}
	if len(trace.Graph.RootSpanIDs) != 1 || len(trace.Graph.Edges) != 3 || len(trace.Graph.CriticalSpanIDs) == 0 || trace.Graph.MaxDepth != 1 {
		t.Fatalf("expected trace graph metadata, got %#v", trace.Graph)
	}
	perfetto := ExportPerfetto(trace)
	if _, ok := perfetto["traceEvents"]; !ok {
		t.Fatalf("expected perfetto traceEvents, got %#v", perfetto)
	}
	if _, ok := perfetto["metadata"]; !ok {
		t.Fatalf("expected perfetto metadata, got %#v", perfetto)
	}
	encodedPerfetto, err := json.Marshal(perfetto)
	if err != nil {
		t.Fatalf("marshal perfetto: %v", err)
	}
	if !strings.Contains(string(encodedPerfetto), `"critical":true`) || !strings.Contains(string(encodedPerfetto), `"self_duration_ms"`) || !strings.Contains(string(encodedPerfetto), `"graph"`) {
		t.Fatalf("expected perfetto graph annotations, got %s", string(encodedPerfetto))
	}
	otel := ExportOTel(trace)
	if _, ok := otel["resourceSpans"]; !ok {
		t.Fatalf("expected otel resourceSpans, got %#v", otel)
	}
	if _, ok := otel["metadata"]; !ok {
		t.Fatalf("expected otel metadata, got %#v", otel)
	}
	encodedOTel, err := json.Marshal(otel)
	if err != nil {
		t.Fatalf("marshal otel: %v", err)
	}
	if !strings.Contains(string(encodedOTel), `"name":"tma.tool.result"`) ||
		!strings.Contains(string(encodedOTel), `"tma.event_seq"`) ||
		!strings.Contains(string(encodedOTel), `"tma.critical"`) ||
		!strings.Contains(string(encodedOTel), `"tma.self_duration_ms"`) ||
		!strings.Contains(string(encodedOTel), `"graph"`) {
		t.Fatalf("expected otel span events, got %s", string(encodedOTel))
	}
}

func TestProjectTurnTracePrefersNativeSpanFields(t *testing.T) {
	now := time.Now().UTC()
	events := []managedagents.Event{
		{
			Seq:       1,
			Type:      managedagents.EventRuntimeStarted,
			Payload:   json.RawMessage(`{"turn_id":"turn_1","trace_id":"trace_native","span_id":"span_root","span_name":"tma.interaction","span_kind":"interaction","span_status":"running","message":"started"}`),
			CreatedAt: now,
		},
		{
			Seq:       2,
			Type:      managedagents.EventRuntimeToolCall,
			Payload:   json.RawMessage(`{"turn_id":"turn_1","trace_id":"trace_native","span_id":"span_tool","parent_span_id":"span_root","span_name":"tma.tool.default.read_file","span_kind":"tool","span_status":"point","message":"tool call","data":{"id":"call_read","identifier":"default","api_name":"read_file"}}`),
			CreatedAt: now.Add(10 * time.Millisecond),
		},
		{
			Seq:       3,
			Type:      managedagents.EventRuntimeToolResult,
			Payload:   json.RawMessage(`{"turn_id":"turn_1","trace_id":"trace_native","span_id":"span_tool","parent_span_id":"span_root","span_name":"tma.tool.default.read_file","span_kind":"tool","span_status":"ok","duration_ms":42,"message":"tool result","data":{"id":"call_read","identifier":"default","api_name":"read_file","success":true}}`),
			CreatedAt: now.Add(20 * time.Millisecond),
		},
	}

	trace := ProjectTurnTrace("sesn_1", "turn_1", events)
	if len(trace.Spans) != 2 {
		t.Fatalf("expected native root and tool spans, got %#v", trace.Spans)
	}
	if trace.Spans[0].SpanID != "span_root" || trace.Spans[0].Name != "tma.interaction" {
		t.Fatalf("expected native root span, got %#v", trace.Spans[0])
	}
	if trace.Spans[1].SpanID != "span_tool" || trace.Spans[1].ParentSpanID != "span_root" || trace.Spans[1].DurationMillis != 42 {
		t.Fatalf("expected native tool span, got %#v", trace.Spans[1])
	}
	if len(trace.Spans[0].ChildSpanIDs) != 1 || trace.Spans[0].ChildSpanIDs[0] != "span_tool" {
		t.Fatalf("expected native child span linkage, got %#v", trace.Spans[0].ChildSpanIDs)
	}
	if len(trace.Spans[0].Events) != 1 || trace.Spans[0].Events[0].Seq != 1 {
		t.Fatalf("expected native root event, got %#v", trace.Spans[0].Events)
	}
	if len(trace.Spans[1].Events) != 2 || trace.Spans[1].Events[0].Seq != 2 || trace.Spans[1].Events[1].Seq != 3 {
		t.Fatalf("expected native tool span events, got %#v", trace.Spans[1].Events)
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
