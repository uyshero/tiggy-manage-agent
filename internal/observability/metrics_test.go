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

func TestPrometheusTextIncludesObservabilityExporterHealth(t *testing.T) {
	text := PrometheusText(MetricsSnapshot{
		Observability: Status{
			Sampling: SamplingStatus{
				SampleRate: 0.25,
			},
			Retry: RetryStatus{
				MaxAttempts:          3,
				PendingRecentRetries: 1,
			},
			Perfetto: ExporterStatus{
				Enabled: true,
				LastSuccess: &ExporterHealth{
					At: time.Unix(123, 0).UTC(),
				},
				LastAttempt: &ExporterHealth{
					At: time.Unix(125, 0).UTC(),
				},
			},
			OTLP: ExporterStatus{
				Enabled: true,
				LastFailure: &ExporterHealth{
					At: time.Unix(124, 0).UTC(),
				},
				LastAttempt: &ExporterHealth{
					At: time.Unix(124, 0).UTC(),
				},
			},
			RecentRuns: []managedagents.ObservabilityExporterRun{
				{Exporter: managedagents.ObservabilityExporterPerfetto, Status: managedagents.ObservabilityExporterRunSucceeded},
				{Exporter: managedagents.ObservabilityExporterOTLP, Status: managedagents.ObservabilityExporterRunFailed},
				{Exporter: managedagents.ObservabilityExporterOTLP, Status: managedagents.ObservabilityExporterRunSkipped},
			},
		},
	})
	for _, expected := range []string{
		`tma_observability_exporter_enabled{exporter="perfetto"} 1`,
		`tma_observability_exporter_enabled{exporter="otlp"} 1`,
		`tma_observability_exporter_sample_rate 0.25`,
		`tma_observability_exporter_retry_max_attempts 3`,
		`tma_observability_exporter_pending_recent_retries 1`,
		`tma_observability_exporter_last_success_timestamp_seconds{exporter="perfetto"} 123`,
		`tma_observability_exporter_last_failure_timestamp_seconds{exporter="otlp"} 124`,
		`tma_observability_exporter_last_attempt_timestamp_seconds{exporter="perfetto"} 125`,
		`tma_observability_exporter_last_attempt_timestamp_seconds{exporter="otlp"} 124`,
		`tma_observability_exporter_recent_runs_total{exporter="otlp",status="failed"} 1`,
		`tma_observability_exporter_recent_runs_total{exporter="otlp",status="skipped"} 1`,
		`tma_observability_exporter_recent_runs_total{exporter="perfetto",status="succeeded"} 1`,
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
		`tma_trace_critical_path_duration_milliseconds{session_id="sesn_1",status="running",turn_id="turn_1"}`,
		`tma_trace_max_span_depth{session_id="sesn_1",turn_id="turn_1"} 1`,
		`tma_trace_critical_spans_total{session_id="sesn_1",turn_id="turn_1"}`,
		`tma_trace_spans_total{kind="tool",session_id="sesn_1",turn_id="turn_1"} 1`,
		`tma_trace_span_duration_milliseconds_max{kind="tool",session_id="sesn_1",turn_id="turn_1"} 70`,
		`tma_trace_span_self_duration_milliseconds_max{kind="tool",session_id="sesn_1",turn_id="turn_1"} 70`,
		`tma_tool_calls_total{api_name="read_file",outcome="success",session_id="sesn_1",tool_identifier="default",turn_id="turn_1"} 1`,
		`tma_tool_approvals_total{api_name="read_file",decision="approved",session_id="sesn_1",tool_identifier="default",turn_id="turn_1"} 1`,
		`tma_pending_interventions_total{session_id="sesn_1"} 0`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected session metrics to contain %q, got:\n%s", expected, text)
		}
	}
}
