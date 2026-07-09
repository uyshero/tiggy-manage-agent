package observability

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
)

func TestPrometheusTextIncludesUsageAndWorkers(t *testing.T) {
	text := PrometheusText(MetricsSnapshot{
		Usage: managedagents.LLMUsageAggregateReport{
			Groups: []managedagents.LLMUsageAggregate{{
				ProviderID: "fake",
				Model:      "fake-demo",
				Summary: managedagents.LLMUsageSummary{
					RecordCount:   2,
					InputTokens:   10,
					OutputTokens:  4,
					TotalTokens:   14,
					LatencyMillis: 123,
				},
			}},
		},
		Workers: []managedagents.Worker{{
			Status:     managedagents.WorkerStatusOnline,
			WorkerType: managedagents.WorkerTypeLocal,
		}},
	})
	for _, expected := range []string{
		`tma_llm_usage_records_total{model="fake-demo",provider="fake"} 2`,
		`tma_llm_tokens_total{kind="total",model="fake-demo",provider="fake"} 14`,
		`tma_llm_latency_milliseconds_total{model="fake-demo",provider="fake"} 123`,
		`tma_workers_total{status="online",type="local"} 1`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected metrics to contain %q, got:\n%s", expected, text)
		}
	}
}

func TestPrometheusTextIncludesSessionTraceMetrics(t *testing.T) {
	now := time.Now().UTC()
	trace := ProjectTurnTrace("sesn_1", "turn_1", []managedagents.Event{
		{
			Seq:       1,
			Type:      managedagents.EventUserMessage,
			Payload:   json.RawMessage(`{"turn_id":"turn_1","content":[{"type":"text","text":"please read"}]}`),
			CreatedAt: now,
		},
		{
			Seq:       2,
			Type:      managedagents.EventRuntimeToolCall,
			Payload:   json.RawMessage(`{"turn_id":"turn_1","message":"tool call","data":{"id":"call_read","identifier":"default","api_name":"read_file"}}`),
			CreatedAt: now.Add(50 * time.Millisecond),
		},
		{
			Seq:       3,
			Type:      managedagents.EventRuntimeToolResult,
			Payload:   json.RawMessage(`{"turn_id":"turn_1","message":"tool result","data":{"id":"call_read","identifier":"default","api_name":"read_file","success":true}}`),
			CreatedAt: now.Add(120 * time.Millisecond),
		},
	})

	text := PrometheusText(MetricsSnapshot{
		Trace: &trace,
		Events: []managedagents.Event{
			{Type: managedagents.EventUserMessage},
			{Type: managedagents.EventRuntimeToolCall},
			{Type: managedagents.EventRuntimeToolResult},
		},
		Interventions: []managedagents.SessionIntervention{{
			SessionID:      "sesn_1",
			TurnID:         "turn_1",
			ToolIdentifier: "default",
			APIName:        "read_file",
			Status:         managedagents.InterventionStatusApproved,
		}},
	})

	for _, expected := range []string{
		`tma_session_events_total{event_type="runtime.tool_call",session_id="sesn_1"} 1`,
		`tma_trace_duration_milliseconds{session_id="sesn_1",status="running",turn_id="turn_1"} 120`,
		`tma_trace_spans_total{kind="tool",session_id="sesn_1",turn_id="turn_1"} 1`,
		`tma_tool_calls_total{api_name="read_file",outcome="success",session_id="sesn_1",tool_identifier="default",turn_id="turn_1"} 1`,
		`tma_tool_approvals_total{api_name="read_file",decision="approved",session_id="sesn_1",tool_identifier="default",turn_id="turn_1"} 1`,
		`tma_pending_interventions_total{session_id="sesn_1"} 0`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected session metrics to contain %q, got:\n%s", expected, text)
		}
	}
}
