package observability

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/mcp"
	"tiggy-manage-agent/internal/skillmarketplace"
	"tiggy-manage-agent/internal/skillretention"
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
		Subagents: managedagents.SubagentMetrics{
			Queued:      2,
			Running:     3,
			Rejected:    4,
			WaitSeconds: 9,
		},
		TaskGroups: managedagents.SubagentTaskGroupMetrics{
			Pending:      5,
			Running:      6,
			Completed:    7,
			Failed:       8,
			Canceled:     9,
			ItemCreated:  10,
			ItemStarted:  11,
			ItemQueued:   12,
			ItemRejected: 13,
		},
	})
	for _, expected := range []string{
		`tma_llm_usage_records_total{model="fake-demo",provider="fake"} 2`,
		`tma_llm_tokens_total{kind="total",model="fake-demo",provider="fake"} 14`,
		`tma_llm_latency_milliseconds_total{model="fake-demo",provider="fake"} 123`,
		`tma_workers_total{status="online",type="local"} 1`,
		`tma_subagent_status_total{status="queued"} 2`,
		`tma_subagent_status_total{status="running"} 3`,
		`tma_subagent_status_total{status="rejected"} 4`,
		`tma_subagent_wait_seconds 9`,
		`tma_subagent_group_status_total{status="pending"} 5`,
		`tma_subagent_group_status_total{status="running"} 6`,
		`tma_subagent_group_status_total{status="completed"} 7`,
		`tma_subagent_group_status_total{status="failed"} 8`,
		`tma_subagent_group_status_total{status="canceled"} 9`,
		`tma_subagent_group_items_total{status="created"} 10`,
		`tma_subagent_group_items_total{status="started"} 11`,
		`tma_subagent_group_items_total{status="queued"} 12`,
		`tma_subagent_group_items_total{status="rejected"} 13`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected metrics to contain %q, got:\n%s", expected, text)
		}
	}
}

func TestPrometheusTextIncludesCompletionValidationMetrics(t *testing.T) {
	events := []managedagents.Event{
		{SessionID: "session_1", TurnID: "turn_1", Type: managedagents.EventRuntimeCompletionBlocked, Payload: json.RawMessage(`{"data":{"validator":"builtin.task_plan","outcome":"retry"}}`)},
		{SessionID: "session_1", TurnID: "turn_1", Type: managedagents.EventRuntimeCompletionValidated, Payload: json.RawMessage(`{"data":{"validator":"builtin.task_plan","outcome":"pass"}}`)},
		{SessionID: "session_1", TurnID: "turn_1", Type: managedagents.EventRuntimeCompletionFailed, Payload: json.RawMessage(`{"data":{"validator":"a-secret-high-cardinality-validator-name-that-should-not-be-exported"}}`)},
		{SessionID: "session_1", TurnID: "turn_other", Type: managedagents.EventRuntimeCompletionValidated, Payload: json.RawMessage(`{"data":{"validator":"builtin.task_plan"}}`)},
	}
	text := PrometheusText(MetricsSnapshot{Trace: &TurnTrace{SessionID: "session_1", TurnID: "turn_1", Status: "completed"}, Events: events})
	for _, expected := range []string{
		`tma_completion_validation_total{outcome="fail",session_id="session_1",turn_id="turn_1",validator="other"} 1`,
		`tma_completion_validation_total{outcome="retry",session_id="session_1",turn_id="turn_1",validator="builtin.task_plan"} 1`,
		`tma_completion_validation_total{outcome="pass",session_id="session_1",turn_id="turn_1",validator="builtin.task_plan"} 1`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected completion metric %q, got:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "a-secret-high-cardinality") {
		t.Fatalf("completion validator leaked high-cardinality value: %s", text)
	}
}

func TestPrometheusTextIncludesGlobalCompletionValidationCounters(t *testing.T) {
	text := PrometheusText(MetricsSnapshot{CompletionValidations: []CompletionValidationMetric{
		{Outcome: "retry", Validator: "builtin.task_plan", Count: 7},
		{Outcome: "fail", Validator: "unbounded-validator-name", Count: 2},
	}})
	for _, expected := range []string{
		`tma_completion_validation_events_total{outcome="retry",validator="builtin.task_plan"} 7`,
		`tma_completion_validation_events_total{outcome="fail",validator="other"} 2`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected global completion metric %q, got:\n%s", expected, text)
		}
	}
}

func TestPrometheusTextIncludesSkillBinaryScanMetrics(t *testing.T) {
	text := PrometheusText(MetricsSnapshot{BinaryScans: []skillmarketplace.BinaryScanMetric{
		{Provider: "clamav_http", Outcome: "passed", Count: 3, DurationMillis: 125},
		{Provider: "clamav_http", Outcome: "blocked", Count: 1, DurationMillis: 40},
	}})
	for _, expected := range []string{
		`tma_skill_binary_scans_total{outcome="passed",provider="clamav_http"} 3`,
		`tma_skill_binary_scans_total{outcome="blocked",provider="clamav_http"} 1`,
		`tma_skill_binary_scan_duration_milliseconds_total{outcome="passed",provider="clamav_http"} 125`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected %q in metrics:\n%s", expected, text)
		}
	}
}

func TestPrometheusTextIncludesSecurityAuditExporterMetrics(t *testing.T) {
	text := PrometheusText(MetricsSnapshot{SecurityAuditExporter: SecurityAuditExporterMetrics{
		Enabled: true, Durable: true, QueueDepth: 7, QueueCapacity: 100, Sent: 25, Failed: 2, Dropped: 1,
		PersistenceFailed: 3, Pending: 4, Delivering: 2, Delivered: 20, DeadLetter: 1, OldestPendingSeconds: 90,
		IntegrityStatusAvailable: true, IntegrityUnconfiguredBlocking: 2,
		IntegrityHistoricalUnidentifiedBlocking: 3, IntegrityInactiveKeyBlocking: 4,
		IntegrityKeysReadyToRemove: 1, IntegrityKeysRemovalBlocked: 2,
	}})
	for _, expected := range []string{
		`tma_security_audit_exporter_enabled 1`,
		`tma_security_audit_exporter_durable 1`,
		`tma_security_audit_exporter_queue_depth 7`,
		`tma_security_audit_exporter_queue_capacity 100`,
		`tma_security_audit_export_events_total{outcome="sent"} 25`,
		`tma_security_audit_export_events_total{outcome="failed"} 2`,
		`tma_security_audit_export_events_total{outcome="dropped"} 1`,
		`tma_security_audit_export_events_total{outcome="persistence_failed"} 3`,
		`tma_security_audit_outbox_events{status="pending"} 4`,
		`tma_security_audit_outbox_events{status="dead_letter"} 1`,
		`tma_security_audit_outbox_oldest_pending_seconds 90`,
		`tma_security_audit_integrity_status_available 1`,
		`tma_security_audit_integrity_blocking_events{reason="unconfigured_key"} 2`,
		`tma_security_audit_integrity_blocking_events{reason="historical_unidentified"} 3`,
		`tma_security_audit_integrity_blocking_events{reason="inactive_key"} 4`,
		`tma_security_audit_integrity_keys{state="ready_to_remove"} 1`,
		`tma_security_audit_integrity_keys{state="removal_blocked"} 2`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected %q in metrics:\n%s", expected, text)
		}
	}
}

func TestPrometheusTextIncludesSkillAssetGCMetrics(t *testing.T) {
	text := PrometheusText(MetricsSnapshot{SkillAssetGC: skillretention.MetricsSnapshot{
		Runs:       []skillretention.RunMetric{{Outcome: "succeeded", Count: 2}},
		Objects:    []skillretention.ObjectMetric{{Outcome: "deleted", Count: 3, Bytes: 4096}},
		Candidates: 4,
	}})
	for _, expected := range []string{
		`tma_skill_asset_gc_runs_total{dry_run="false",outcome="succeeded"} 2`,
		`tma_skill_asset_gc_objects_total{outcome="deleted"} 3`,
		`tma_skill_asset_gc_bytes_total{outcome="deleted"} 4096`,
		`tma_skill_asset_gc_candidates 4`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected %q in metrics:\n%s", expected, text)
		}
	}
}

func TestPrometheusTextIncludesMCPStdioHostMetrics(t *testing.T) {
	text := PrometheusText(MetricsSnapshot{MCPHost: mcp.StdioHostStats{
		Sessions: 4, InUseSessions: 2, MaxSessions: 16,
		IdleTimeoutSeconds: 300, SweepIntervalSeconds: 30, StartsTotal: 9, StopsTotal: 5,
		DiscardsTotal: 2, ReapedTotal: 1, EvictionsTotal: 1, RejectionsTotal: 3,
		ToolsListChangedTotal: 4, ResourcesListChangedTotal: 2, PromptsListChangedTotal: 1,
		ProgressNotificationsTotal: 5, LogMessagesTotal: 4, InvalidNotificationsTotal: 1,
		LogMessagesByLevel: map[string]int64{"error": 1, "info": 3},
	}})
	for _, expected := range []string{
		`tma_mcp_stdio_host_sessions 4`,
		`tma_mcp_stdio_host_in_use_sessions 2`,
		`tma_mcp_stdio_host_max_sessions 16`,
		`tma_mcp_stdio_host_idle_timeout_seconds 300`,
		`tma_mcp_stdio_host_sweep_interval_seconds 30`,
		`tma_mcp_stdio_host_events_total{event="start"} 9`,
		`tma_mcp_stdio_host_events_total{event="discard"} 2`,
		`tma_mcp_stdio_host_events_total{event="reject"} 3`,
		`tma_mcp_stdio_host_events_total{event="tools_list_changed"} 4`,
		`tma_mcp_stdio_host_events_total{event="resources_list_changed"} 2`,
		`tma_mcp_stdio_host_events_total{event="prompts_list_changed"} 1`,
		`tma_mcp_stdio_host_notifications_total{type="progress"} 5`,
		`tma_mcp_stdio_host_notifications_total{type="logging"} 4`,
		`tma_mcp_stdio_host_notifications_total{type="invalid"} 1`,
		`tma_mcp_stdio_host_log_messages_total{level="error"} 1`,
		`tma_mcp_stdio_host_log_messages_total{level="info"} 3`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected %q in MCP host metrics:\n%s", expected, text)
		}
	}
}

func TestPrometheusTextIncludesMCPStreamableHTTPHostMetrics(t *testing.T) {
	text := PrometheusText(MetricsSnapshot{MCPHTTPHost: mcp.StreamableHTTPHostStats{
		Sessions: 3, InUseSessions: 1, MaxSessions: 12,
		IdleTimeoutSeconds: 600, SweepIntervalSeconds: 60, StartsTotal: 7, StopsTotal: 2,
		DiscardsTotal: 1, ReapedTotal: 1, RejectionsTotal: 2, DeleteErrorsTotal: 1, ToolsListChangedTotal: 4,
		ProgressNotificationsTotal: 6, LogMessagesTotal: 3, InvalidNotificationsTotal: 2,
		LogMessagesByLevel:  map[string]int64{"warning": 2, "unknown": 1},
		EgressPolicyEnabled: true, EgressAllowedHostCount: 2, EgressAllowedCIDRCount: 1, EgressBlockedTotal: 4,
	}})
	for _, expected := range []string{
		`tma_mcp_streamable_http_host_sessions 3`,
		`tma_mcp_streamable_http_host_in_use_sessions 1`,
		`tma_mcp_streamable_http_host_max_sessions 12`,
		`tma_mcp_streamable_http_host_idle_timeout_seconds 600`,
		`tma_mcp_streamable_http_host_egress_policy_enabled 1`,
		`tma_mcp_streamable_http_host_egress_allow_http 0`,
		`tma_mcp_streamable_http_host_egress_allowed_hosts 2`,
		`tma_mcp_streamable_http_host_egress_allowed_cidrs 1`,
		`tma_mcp_streamable_http_host_egress_blocked_total 4`,
		`tma_mcp_streamable_http_host_events_total{event="start"} 7`,
		`tma_mcp_streamable_http_host_events_total{event="reject"} 2`,
		`tma_mcp_streamable_http_host_events_total{event="delete_error"} 1`,
		`tma_mcp_streamable_http_host_events_total{event="tools_list_changed"} 4`,
		`tma_mcp_streamable_http_host_notifications_total{type="progress"} 6`,
		`tma_mcp_streamable_http_host_notifications_total{type="logging"} 3`,
		`tma_mcp_streamable_http_host_notifications_total{type="invalid"} 2`,
		`tma_mcp_streamable_http_host_log_messages_total{level="unknown"} 1`,
		`tma_mcp_streamable_http_host_log_messages_total{level="warning"} 2`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected %q in MCP HTTP host metrics:\n%s", expected, text)
		}
	}
}

func TestPrometheusTextIncludesMCPRuntimeGuardMetrics(t *testing.T) {
	text := PrometheusText(MetricsSnapshot{MCPRuntimeGuard: mcp.RuntimeGuardStats{
		TrackedServers: 3, InFlight: 2, OpenCircuits: 1,
		CallsTotal: 12, SuccessesTotal: 7, FailuresTotal: 5,
		CircuitRejectedTotal: 4, WaitCanceledTotal: 2,
		FailuresByClass: map[string]int64{"timeout": 3, "transport": 2},
	}})
	for _, expected := range []string{
		`tma_mcp_runtime_guard_servers 3`,
		`tma_mcp_runtime_guard_in_flight 2`,
		`tma_mcp_runtime_guard_open_circuits 1`,
		`tma_mcp_runtime_guard_calls_total 12`,
		`tma_mcp_runtime_guard_results_total{outcome="success"} 7`,
		`tma_mcp_runtime_guard_results_total{outcome="failure"} 5`,
		`tma_mcp_runtime_guard_rejections_total{reason="circuit_open"} 4`,
		`tma_mcp_runtime_guard_rejections_total{reason="wait_canceled"} 2`,
		`tma_mcp_runtime_guard_failures_total{class="timeout"} 3`,
		`tma_mcp_runtime_guard_failures_total{class="transport"} 2`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected %q in MCP runtime guard metrics:\n%s", expected, text)
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
			Payload:   json.RawMessage(`{"turn_id":"turn_1","message":"tool result","data":{"id":"call_read","identifier":"default","api_name":"read_file","success":true,"file_generation":{"oversized_call_count":2,"segment_count":3,"idempotent_replay_count":1,"remaining_placeholder_count":0,"generation_duration_milliseconds":450}}}`),
			CreatedAt: now.Add(120 * time.Millisecond),
		},
	})

	text := PrometheusText(MetricsSnapshot{
		Trace: &trace,
		Events: []managedagents.Event{
			{Type: managedagents.EventUserMessage},
			{Type: managedagents.EventRuntimeToolCall},
			{Seq: 3, Type: managedagents.EventRuntimeToolResult, Payload: json.RawMessage(`{"turn_id":"turn_1","data":{"file_generation":{"oversized_call_count":2,"segment_count":3,"idempotent_replay_count":1,"remaining_placeholder_count":0,"generation_duration_milliseconds":450}}}`)},
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
		`tma_file_generation_oversized_calls_total{session_id="sesn_1",turn_id="turn_1"} 2`,
		`tma_file_generation_segments_total{session_id="sesn_1",turn_id="turn_1"} 3`,
		`tma_file_generation_idempotent_replays_total{session_id="sesn_1",turn_id="turn_1"} 1`,
		`tma_file_generation_remaining_placeholders{session_id="sesn_1",turn_id="turn_1"} 0`,
		`tma_file_generation_duration_milliseconds{session_id="sesn_1",turn_id="turn_1"} 450`,
		`tma_tool_approvals_total{api_name="read_file",decision="approved",session_id="sesn_1",tool_identifier="default",turn_id="turn_1"} 1`,
		`tma_pending_interventions_total{session_id="sesn_1"} 0`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected session metrics to contain %q, got:\n%s", expected, text)
		}
	}
}
