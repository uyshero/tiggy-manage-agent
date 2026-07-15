package observability

import (
	"testing"

	"tiggy-manage-agent/internal/managedagents"
)

func TestEventTraceFieldsClosesInteractionWithStableSpanID(t *testing.T) {
	started := EventTraceFields(EventTraceFieldsInput{
		SessionID: "sesn_1", TurnID: "turn_1", EventType: managedagents.EventRuntimeStarted, InteractionRoot: true,
	})
	completed := EventTraceFields(EventTraceFieldsInput{
		SessionID: "sesn_1", TurnID: "turn_1", EventType: managedagents.EventRuntimeCompleted, Status: "ok",
	})
	failed := EventTraceFields(EventTraceFieldsInput{
		SessionID: "sesn_1", TurnID: "turn_1", EventType: managedagents.EventRuntimeFailed, Status: "error",
	})

	if started["span_id"] == "" || started["span_id"] != completed["span_id"] || started["span_id"] != failed["span_id"] {
		t.Fatalf("expected stable interaction span id, started=%#v completed=%#v failed=%#v", started, completed, failed)
	}
	if completed["span_name"] != "tma.interaction" || completed["span_kind"] != "interaction" || completed["span_status"] != "ok" {
		t.Fatalf("unexpected completed interaction fields: %#v", completed)
	}
	if failed["span_status"] != "error" || failed["parent_span_id"] != nil {
		t.Fatalf("unexpected failed interaction fields: %#v", failed)
	}
}
