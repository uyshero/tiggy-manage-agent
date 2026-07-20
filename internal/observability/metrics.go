package observability

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/mcp"
	"tiggy-manage-agent/internal/skillmarketplace"
	"tiggy-manage-agent/internal/skillretention"
)

type MetricsSnapshot struct {
	Usage                  managedagents.LLMUsageAggregateReport
	Workers                []managedagents.Worker
	Subagents              managedagents.SubagentMetrics
	TaskGroups             managedagents.SubagentTaskGroupMetrics
	Observability          Status
	Trace                  *TurnTrace
	Events                 []managedagents.Event
	Interventions          []managedagents.SessionIntervention
	BinaryScans            []skillmarketplace.BinaryScanMetric
	SkillAssetGC           skillretention.MetricsSnapshot
	MCPHost                mcp.StdioHostStats
	MCPHTTPHost            mcp.StreamableHTTPHostStats
	MCPRuntimeGuard        mcp.RuntimeGuardStats
	AuthorizationDecisions []AuthorizationDecisionMetric
	SecurityAuditExporter  SecurityAuditExporterMetrics
	CompletionValidations  []CompletionValidationMetric
}

type AuthorizationDecisionMetric struct {
	Outcome  string
	Reason   string
	AuthType string
	Count    int64
}

func PrometheusText(snapshot MetricsSnapshot) string {
	var builder strings.Builder
	writeUsageMetrics(&builder, snapshot.Usage)
	writeWorkerMetrics(&builder, snapshot.Workers)
	writeSubagentMetrics(&builder, snapshot.Subagents)
	writeTaskGroupMetrics(&builder, snapshot.TaskGroups)
	writeObservabilityMetrics(&builder, snapshot.Observability)
	writeSkillBinaryScanMetrics(&builder, snapshot.BinaryScans)
	writeSkillAssetGCMetrics(&builder, snapshot.SkillAssetGC)
	writeMCPHostMetrics(&builder, snapshot.MCPHost)
	writeMCPHTTPHostMetrics(&builder, snapshot.MCPHTTPHost)
	writeMCPRuntimeGuardMetrics(&builder, snapshot.MCPRuntimeGuard)
	writeAuthorizationDecisionMetrics(&builder, snapshot.AuthorizationDecisions)
	writeSecurityAuditExporterMetrics(&builder, snapshot.SecurityAuditExporter)
	writeCompletionValidationCounterMetrics(&builder, snapshot.CompletionValidations)
	writeTraceMetrics(&builder, snapshot.Trace, snapshot.Events, snapshot.Interventions)
	return builder.String()
}

func writeCompletionValidationCounterMetrics(builder *strings.Builder, metrics []CompletionValidationMetric) {
	writeMetricHelp(builder, "tma_completion_validation_events_total", "Process-local completion validation events by bounded validator and outcome.")
	writeMetricType(builder, "tma_completion_validation_events_total", "counter")
	for _, metric := range metrics {
		writeMetric(builder, "tma_completion_validation_events_total", map[string]string{
			"outcome": metric.Outcome, "validator": completionMetricValidator(metric.Validator),
		}, metric.Count)
	}
}

func writeSecurityAuditExporterMetrics(builder *strings.Builder, metrics SecurityAuditExporterMetrics) {
	writeMetricHelp(builder, "tma_security_audit_exporter_enabled", "Whether the OTLP security audit log exporter is enabled.")
	writeMetricType(builder, "tma_security_audit_exporter_enabled", "gauge")
	writeMetric(builder, "tma_security_audit_exporter_enabled", nil, boolMetric(metrics.Enabled))
	writeMetricHelp(builder, "tma_security_audit_exporter_durable", "Whether authorization audits use the PostgreSQL durable outbox.")
	writeMetricType(builder, "tma_security_audit_exporter_durable", "gauge")
	writeMetric(builder, "tma_security_audit_exporter_durable", nil, boolMetric(metrics.Durable))
	writeMetricHelp(builder, "tma_security_audit_exporter_queue_depth", "Authorization audit events currently waiting for OTLP export.")
	writeMetricType(builder, "tma_security_audit_exporter_queue_depth", "gauge")
	writeMetric(builder, "tma_security_audit_exporter_queue_depth", nil, metrics.QueueDepth)
	writeMetricHelp(builder, "tma_security_audit_exporter_queue_capacity", "Maximum queued authorization audit events.")
	writeMetricType(builder, "tma_security_audit_exporter_queue_capacity", "gauge")
	writeMetric(builder, "tma_security_audit_exporter_queue_capacity", nil, metrics.QueueCapacity)
	writeMetricHelp(builder, "tma_security_audit_export_events_total", "Authorization audit OTLP export events by final outcome.")
	writeMetricType(builder, "tma_security_audit_export_events_total", "counter")
	writeMetric(builder, "tma_security_audit_export_events_total", map[string]string{"outcome": "sent"}, metrics.Sent)
	writeMetric(builder, "tma_security_audit_export_events_total", map[string]string{"outcome": "failed"}, metrics.Failed)
	writeMetric(builder, "tma_security_audit_export_events_total", map[string]string{"outcome": "dropped"}, metrics.Dropped)
	writeMetric(builder, "tma_security_audit_export_events_total", map[string]string{"outcome": "persistence_failed"}, metrics.PersistenceFailed)
	writeMetricHelp(builder, "tma_security_audit_outbox_events", "Durable authorization audit outbox events by status.")
	writeMetricType(builder, "tma_security_audit_outbox_events", "gauge")
	writeMetric(builder, "tma_security_audit_outbox_events", map[string]string{"status": "pending"}, metrics.Pending)
	writeMetric(builder, "tma_security_audit_outbox_events", map[string]string{"status": "delivering"}, metrics.Delivering)
	writeMetric(builder, "tma_security_audit_outbox_events", map[string]string{"status": "delivered"}, metrics.Delivered)
	writeMetric(builder, "tma_security_audit_outbox_events", map[string]string{"status": "dead_letter"}, metrics.DeadLetter)
	writeMetricHelp(builder, "tma_security_audit_outbox_oldest_pending_seconds", "Age of the oldest pending or delivering authorization audit event.")
	writeMetricType(builder, "tma_security_audit_outbox_oldest_pending_seconds", "gauge")
	writeMetric(builder, "tma_security_audit_outbox_oldest_pending_seconds", nil, metrics.OldestPendingSeconds)
	writeMetricHelp(builder, "tma_security_audit_integrity_status_available", "Whether durable HMAC integrity key lifecycle status was read successfully.")
	writeMetricType(builder, "tma_security_audit_integrity_status_available", "gauge")
	writeMetric(builder, "tma_security_audit_integrity_status_available", nil, boolMetric(metrics.IntegrityStatusAvailable))
	writeMetricHelp(builder, "tma_security_audit_integrity_blocking_events", "Non-terminal HMAC audit events blocking integrity key lifecycle operations by bounded reason.")
	writeMetricType(builder, "tma_security_audit_integrity_blocking_events", "gauge")
	writeMetric(builder, "tma_security_audit_integrity_blocking_events", map[string]string{"reason": "unconfigured_key"}, metrics.IntegrityUnconfiguredBlocking)
	writeMetric(builder, "tma_security_audit_integrity_blocking_events", map[string]string{"reason": "historical_unidentified"}, metrics.IntegrityHistoricalUnidentifiedBlocking)
	writeMetric(builder, "tma_security_audit_integrity_blocking_events", map[string]string{"reason": "inactive_key"}, metrics.IntegrityInactiveKeyBlocking)
	writeMetricHelp(builder, "tma_security_audit_integrity_keys", "Configured inactive HMAC integrity keys by removal readiness state.")
	writeMetricType(builder, "tma_security_audit_integrity_keys", "gauge")
	writeMetric(builder, "tma_security_audit_integrity_keys", map[string]string{"state": "ready_to_remove"}, metrics.IntegrityKeysReadyToRemove)
	writeMetric(builder, "tma_security_audit_integrity_keys", map[string]string{"state": "removal_blocked"}, metrics.IntegrityKeysRemovalBlocked)
}

func writeAuthorizationDecisionMetrics(builder *strings.Builder, metrics []AuthorizationDecisionMetric) {
	writeMetricHelp(builder, "tma_authorization_decisions_total", "HTTP authorization decisions by authentication type, outcome, and reason.")
	writeMetricType(builder, "tma_authorization_decisions_total", "counter")
	metrics = append([]AuthorizationDecisionMetric(nil), metrics...)
	sort.Slice(metrics, func(i, j int) bool {
		left := metrics[i].AuthType + "\x00" + metrics[i].Outcome + "\x00" + metrics[i].Reason
		right := metrics[j].AuthType + "\x00" + metrics[j].Outcome + "\x00" + metrics[j].Reason
		return left < right
	})
	for _, metric := range metrics {
		writeMetric(builder, "tma_authorization_decisions_total", map[string]string{
			"auth_type": metric.AuthType,
			"outcome":   metric.Outcome,
			"reason":    metric.Reason,
		}, metric.Count)
	}
}

func writeMCPHostMetrics(builder *strings.Builder, stats mcp.StdioHostStats) {
	writeMetricHelp(builder, "tma_mcp_stdio_host_sessions", "Current server-hosted MCP stdio session entries.")
	writeMetricType(builder, "tma_mcp_stdio_host_sessions", "gauge")
	writeMetric(builder, "tma_mcp_stdio_host_sessions", nil, int64(stats.Sessions))
	writeMetricHelp(builder, "tma_mcp_stdio_host_in_use_sessions", "Current server-hosted MCP stdio session entries with active or waiting requests.")
	writeMetricType(builder, "tma_mcp_stdio_host_in_use_sessions", "gauge")
	writeMetric(builder, "tma_mcp_stdio_host_in_use_sessions", nil, int64(stats.InUseSessions))
	writeMetricHelp(builder, "tma_mcp_stdio_host_max_sessions", "Configured maximum server-hosted MCP stdio session entries.")
	writeMetricType(builder, "tma_mcp_stdio_host_max_sessions", "gauge")
	writeMetric(builder, "tma_mcp_stdio_host_max_sessions", nil, int64(stats.MaxSessions))
	writeMetricHelp(builder, "tma_mcp_stdio_host_idle_timeout_seconds", "Configured idle timeout for server-hosted MCP stdio sessions.")
	writeMetricType(builder, "tma_mcp_stdio_host_idle_timeout_seconds", "gauge")
	writeMetric(builder, "tma_mcp_stdio_host_idle_timeout_seconds", nil, stats.IdleTimeoutSeconds)
	writeMetricHelp(builder, "tma_mcp_stdio_host_sweep_interval_seconds", "Configured sweep interval for server-hosted MCP stdio sessions.")
	writeMetricType(builder, "tma_mcp_stdio_host_sweep_interval_seconds", "gauge")
	writeMetric(builder, "tma_mcp_stdio_host_sweep_interval_seconds", nil, stats.SweepIntervalSeconds)
	writeMetricHelp(builder, "tma_mcp_stdio_host_events_total", "Server-hosted MCP stdio lifecycle and catalog-change events by event type.")
	writeMetricType(builder, "tma_mcp_stdio_host_events_total", "counter")
	for _, event := range []struct {
		name  string
		value int64
	}{
		{name: "start", value: stats.StartsTotal},
		{name: "stop", value: stats.StopsTotal},
		{name: "discard", value: stats.DiscardsTotal},
		{name: "reap", value: stats.ReapedTotal},
		{name: "evict", value: stats.EvictionsTotal},
		{name: "reject", value: stats.RejectionsTotal},
		{name: "tools_list_changed", value: stats.ToolsListChangedTotal},
		{name: "resources_list_changed", value: stats.ResourcesListChangedTotal},
		{name: "prompts_list_changed", value: stats.PromptsListChangedTotal},
	} {
		writeMetric(builder, "tma_mcp_stdio_host_events_total", map[string]string{"event": event.name}, event.value)
	}
	writeMCPNotificationMetrics(builder, "tma_mcp_stdio_host", stats.ProgressNotificationsTotal, stats.LogMessagesTotal, stats.InvalidNotificationsTotal, stats.LogMessagesByLevel)
}

func writeMCPHTTPHostMetrics(builder *strings.Builder, stats mcp.StreamableHTTPHostStats) {
	writeMetricHelp(builder, "tma_mcp_streamable_http_host_sessions", "Current server-hosted MCP Streamable HTTP session entries.")
	writeMetricType(builder, "tma_mcp_streamable_http_host_sessions", "gauge")
	writeMetric(builder, "tma_mcp_streamable_http_host_sessions", nil, int64(stats.Sessions))
	writeMetricHelp(builder, "tma_mcp_streamable_http_host_in_use_sessions", "Current server-hosted MCP Streamable HTTP session entries with active or waiting requests.")
	writeMetricType(builder, "tma_mcp_streamable_http_host_in_use_sessions", "gauge")
	writeMetric(builder, "tma_mcp_streamable_http_host_in_use_sessions", nil, int64(stats.InUseSessions))
	writeMetricHelp(builder, "tma_mcp_streamable_http_host_max_sessions", "Configured maximum server-hosted MCP Streamable HTTP session entries.")
	writeMetricType(builder, "tma_mcp_streamable_http_host_max_sessions", "gauge")
	writeMetric(builder, "tma_mcp_streamable_http_host_max_sessions", nil, int64(stats.MaxSessions))
	writeMetricHelp(builder, "tma_mcp_streamable_http_host_idle_timeout_seconds", "Configured idle timeout for server-hosted MCP Streamable HTTP sessions.")
	writeMetricType(builder, "tma_mcp_streamable_http_host_idle_timeout_seconds", "gauge")
	writeMetric(builder, "tma_mcp_streamable_http_host_idle_timeout_seconds", nil, stats.IdleTimeoutSeconds)
	writeMetricHelp(builder, "tma_mcp_streamable_http_host_sweep_interval_seconds", "Configured sweep interval for server-hosted MCP Streamable HTTP sessions.")
	writeMetricType(builder, "tma_mcp_streamable_http_host_sweep_interval_seconds", "gauge")
	writeMetric(builder, "tma_mcp_streamable_http_host_sweep_interval_seconds", nil, stats.SweepIntervalSeconds)
	writeMetricHelp(builder, "tma_mcp_streamable_http_host_egress_policy_enabled", "Whether the MCP Streamable HTTP egress policy is enabled.")
	writeMetricType(builder, "tma_mcp_streamable_http_host_egress_policy_enabled", "gauge")
	writeMetric(builder, "tma_mcp_streamable_http_host_egress_policy_enabled", nil, boolMetric(stats.EgressPolicyEnabled))
	writeMetricHelp(builder, "tma_mcp_streamable_http_host_egress_allow_http", "Whether plain HTTP MCP egress is allowed.")
	writeMetricType(builder, "tma_mcp_streamable_http_host_egress_allow_http", "gauge")
	writeMetric(builder, "tma_mcp_streamable_http_host_egress_allow_http", nil, boolMetric(stats.EgressAllowHTTP))
	writeMetricHelp(builder, "tma_mcp_streamable_http_host_egress_allow_private_networks", "Whether RFC1918 and ULA MCP egress is allowed.")
	writeMetricType(builder, "tma_mcp_streamable_http_host_egress_allow_private_networks", "gauge")
	writeMetric(builder, "tma_mcp_streamable_http_host_egress_allow_private_networks", nil, boolMetric(stats.EgressAllowPrivateNetworks))
	writeMetricHelp(builder, "tma_mcp_streamable_http_host_egress_allowed_hosts", "Configured MCP egress host allowlist entries without exposing values.")
	writeMetricType(builder, "tma_mcp_streamable_http_host_egress_allowed_hosts", "gauge")
	writeMetric(builder, "tma_mcp_streamable_http_host_egress_allowed_hosts", nil, int64(stats.EgressAllowedHostCount))
	writeMetricHelp(builder, "tma_mcp_streamable_http_host_egress_allowed_cidrs", "Configured MCP egress CIDR allowlist entries without exposing values.")
	writeMetricType(builder, "tma_mcp_streamable_http_host_egress_allowed_cidrs", "gauge")
	writeMetric(builder, "tma_mcp_streamable_http_host_egress_allowed_cidrs", nil, int64(stats.EgressAllowedCIDRCount))
	writeMetricHelp(builder, "tma_mcp_streamable_http_host_egress_blocked_total", "MCP Streamable HTTP requests blocked by the egress policy.")
	writeMetricType(builder, "tma_mcp_streamable_http_host_egress_blocked_total", "counter")
	writeMetric(builder, "tma_mcp_streamable_http_host_egress_blocked_total", nil, stats.EgressBlockedTotal)
	writeMetricHelp(builder, "tma_mcp_streamable_http_host_events_total", "Server-hosted MCP Streamable HTTP lifecycle and catalog-change events by event type.")
	writeMetricType(builder, "tma_mcp_streamable_http_host_events_total", "counter")
	for _, event := range []struct {
		name  string
		value int64
	}{
		{name: "start", value: stats.StartsTotal},
		{name: "stop", value: stats.StopsTotal},
		{name: "discard", value: stats.DiscardsTotal},
		{name: "reap", value: stats.ReapedTotal},
		{name: "evict", value: stats.EvictionsTotal},
		{name: "reject", value: stats.RejectionsTotal},
		{name: "delete_error", value: stats.DeleteErrorsTotal},
		{name: "tools_list_changed", value: stats.ToolsListChangedTotal},
		{name: "resources_list_changed", value: stats.ResourcesListChangedTotal},
		{name: "prompts_list_changed", value: stats.PromptsListChangedTotal},
	} {
		writeMetric(builder, "tma_mcp_streamable_http_host_events_total", map[string]string{"event": event.name}, event.value)
	}
	writeMCPNotificationMetrics(builder, "tma_mcp_streamable_http_host", stats.ProgressNotificationsTotal, stats.LogMessagesTotal, stats.InvalidNotificationsTotal, stats.LogMessagesByLevel)
}

func writeMCPNotificationMetrics(builder *strings.Builder, prefix string, progress int64, logging int64, invalid int64, levels map[string]int64) {
	notificationsMetric := prefix + "_notifications_total"
	writeMetricHelp(builder, notificationsMetric, "MCP server notifications by sanitized notification type.")
	writeMetricType(builder, notificationsMetric, "counter")
	writeMetric(builder, notificationsMetric, map[string]string{"type": "progress"}, progress)
	writeMetric(builder, notificationsMetric, map[string]string{"type": "logging"}, logging)
	writeMetric(builder, notificationsMetric, map[string]string{"type": "invalid"}, invalid)
	logMetric := prefix + "_log_messages_total"
	writeMetricHelp(builder, logMetric, "MCP logging notifications by normalized level; message data is not exported.")
	writeMetricType(builder, logMetric, "counter")
	keys := make([]string, 0, len(levels))
	for level := range levels {
		keys = append(keys, level)
	}
	sort.Strings(keys)
	for _, level := range keys {
		writeMetric(builder, logMetric, map[string]string{"level": level}, levels[level])
	}
}

func writeMCPRuntimeGuardMetrics(builder *strings.Builder, stats mcp.RuntimeGuardStats) {
	writeMetricHelp(builder, "tma_mcp_runtime_guard_servers", "MCP server-version runtime budgets currently tracked by this process.")
	writeMetricType(builder, "tma_mcp_runtime_guard_servers", "gauge")
	writeMetric(builder, "tma_mcp_runtime_guard_servers", nil, int64(stats.TrackedServers))
	writeMetricHelp(builder, "tma_mcp_runtime_guard_in_flight", "MCP requests currently executing across guarded server versions.")
	writeMetricType(builder, "tma_mcp_runtime_guard_in_flight", "gauge")
	writeMetric(builder, "tma_mcp_runtime_guard_in_flight", nil, int64(stats.InFlight))
	writeMetricHelp(builder, "tma_mcp_runtime_guard_open_circuits", "MCP server-version circuits currently open or half-open.")
	writeMetricType(builder, "tma_mcp_runtime_guard_open_circuits", "gauge")
	writeMetric(builder, "tma_mcp_runtime_guard_open_circuits", nil, int64(stats.OpenCircuits))
	writeMetricHelp(builder, "tma_mcp_runtime_guard_calls_total", "MCP calls admitted by the runtime guard.")
	writeMetricType(builder, "tma_mcp_runtime_guard_calls_total", "counter")
	writeMetric(builder, "tma_mcp_runtime_guard_calls_total", nil, stats.CallsTotal)
	writeMetricHelp(builder, "tma_mcp_runtime_guard_results_total", "MCP guarded calls by final outcome.")
	writeMetricType(builder, "tma_mcp_runtime_guard_results_total", "counter")
	writeMetric(builder, "tma_mcp_runtime_guard_results_total", map[string]string{"outcome": "success"}, stats.SuccessesTotal)
	writeMetric(builder, "tma_mcp_runtime_guard_results_total", map[string]string{"outcome": "failure"}, stats.FailuresTotal)
	writeMetricHelp(builder, "tma_mcp_runtime_guard_rejections_total", "MCP runtime guard rejections by bounded reason.")
	writeMetricType(builder, "tma_mcp_runtime_guard_rejections_total", "counter")
	writeMetric(builder, "tma_mcp_runtime_guard_rejections_total", map[string]string{"reason": "circuit_open"}, stats.CircuitRejectedTotal)
	writeMetric(builder, "tma_mcp_runtime_guard_rejections_total", map[string]string{"reason": "wait_canceled"}, stats.WaitCanceledTotal)
	writeMetricHelp(builder, "tma_mcp_runtime_guard_failures_total", "MCP guarded call failures by bounded classification.")
	writeMetricType(builder, "tma_mcp_runtime_guard_failures_total", "counter")
	classes := make([]string, 0, len(stats.FailuresByClass))
	for class := range stats.FailuresByClass {
		classes = append(classes, class)
	}
	sort.Strings(classes)
	for _, class := range classes {
		writeMetric(builder, "tma_mcp_runtime_guard_failures_total", map[string]string{"class": class}, stats.FailuresByClass[class])
	}
}

func writeSkillAssetGCMetrics(builder *strings.Builder, metrics skillretention.MetricsSnapshot) {
	writeMetricHelp(builder, "tma_skill_asset_gc_runs_total", "Skill asset garbage collection runs by final outcome.")
	writeMetricType(builder, "tma_skill_asset_gc_runs_total", "counter")
	writeMetricHelp(builder, "tma_skill_asset_gc_objects_total", "Skill asset garbage collection objects by final outcome.")
	writeMetricType(builder, "tma_skill_asset_gc_objects_total", "counter")
	writeMetricHelp(builder, "tma_skill_asset_gc_bytes_total", "Skill asset garbage collection bytes by final outcome.")
	writeMetricType(builder, "tma_skill_asset_gc_bytes_total", "counter")
	writeMetricHelp(builder, "tma_skill_asset_gc_candidates", "Candidates found by the latest Skill asset GC preview.")
	writeMetricType(builder, "tma_skill_asset_gc_candidates", "gauge")
	writeMetric(builder, "tma_skill_asset_gc_candidates", nil, metrics.Candidates)
	runs := append([]skillretention.RunMetric(nil), metrics.Runs...)
	sort.Slice(runs, func(i, j int) bool { return runs[i].Outcome < runs[j].Outcome })
	for _, metric := range runs {
		writeMetric(builder, "tma_skill_asset_gc_runs_total", map[string]string{"outcome": metric.Outcome, "dry_run": "false"}, int64(metric.Count))
	}
	objects := append([]skillretention.ObjectMetric(nil), metrics.Objects...)
	sort.Slice(objects, func(i, j int) bool { return objects[i].Outcome < objects[j].Outcome })
	for _, metric := range objects {
		labels := map[string]string{"outcome": metric.Outcome}
		writeMetric(builder, "tma_skill_asset_gc_objects_total", labels, int64(metric.Count))
		writeMetric(builder, "tma_skill_asset_gc_bytes_total", labels, int64(metric.Bytes))
	}
}

func writeSkillBinaryScanMetrics(builder *strings.Builder, metrics []skillmarketplace.BinaryScanMetric) {
	writeMetricHelp(builder, "tma_skill_binary_scans_total", "External Skill binary scans by provider and final outcome.")
	writeMetricType(builder, "tma_skill_binary_scans_total", "counter")
	writeMetricHelp(builder, "tma_skill_binary_scan_duration_milliseconds_total", "External Skill binary scan duration milliseconds by provider and final outcome.")
	writeMetricType(builder, "tma_skill_binary_scan_duration_milliseconds_total", "counter")
	metrics = append([]skillmarketplace.BinaryScanMetric(nil), metrics...)
	sort.Slice(metrics, func(left int, right int) bool {
		return metrics[left].Provider+"\x00"+metrics[left].Outcome < metrics[right].Provider+"\x00"+metrics[right].Outcome
	})
	for _, metric := range metrics {
		labels := map[string]string{"provider": metric.Provider, "outcome": metric.Outcome}
		writeMetric(builder, "tma_skill_binary_scans_total", labels, int64(metric.Count))
		writeMetric(builder, "tma_skill_binary_scan_duration_milliseconds_total", labels, int64(metric.DurationMillis))
	}
}

func writeUsageMetrics(builder *strings.Builder, usage managedagents.LLMUsageAggregateReport) {
	writeMetricHelp(builder, "tma_llm_usage_records_total", "Total LLM usage records by provider/model.")
	writeMetricType(builder, "tma_llm_usage_records_total", "counter")
	writeMetricHelp(builder, "tma_llm_tokens_total", "Total LLM tokens by provider/model/token kind.")
	writeMetricType(builder, "tma_llm_tokens_total", "counter")
	writeMetricHelp(builder, "tma_llm_latency_milliseconds_total", "Total LLM latency milliseconds by provider/model.")
	writeMetricType(builder, "tma_llm_latency_milliseconds_total", "counter")

	groups := append([]managedagents.LLMUsageAggregate(nil), usage.Groups...)
	sort.Slice(groups, func(i int, j int) bool {
		left := groups[i].ProviderID + "\x00" + groups[i].Model
		right := groups[j].ProviderID + "\x00" + groups[j].Model
		return left < right
	})
	for _, group := range groups {
		labels := map[string]string{
			"provider": group.ProviderID,
			"model":    group.Model,
		}
		writeMetric(builder, "tma_llm_usage_records_total", labels, group.Summary.RecordCount)
		writeMetric(builder, "tma_llm_tokens_total", withLabel(labels, "kind", "input"), group.Summary.InputTokens)
		writeMetric(builder, "tma_llm_tokens_total", withLabel(labels, "kind", "output"), group.Summary.OutputTokens)
		writeMetric(builder, "tma_llm_tokens_total", withLabel(labels, "kind", "total"), group.Summary.TotalTokens)
		writeMetric(builder, "tma_llm_tokens_total", withLabel(labels, "kind", "cached_input"), group.Summary.CachedInputTokens)
		writeMetric(builder, "tma_llm_tokens_total", withLabel(labels, "kind", "reasoning"), group.Summary.ReasoningTokens)
		writeMetric(builder, "tma_llm_latency_milliseconds_total", labels, group.Summary.LatencyMillis)
	}
}

func writeWorkerMetrics(builder *strings.Builder, workers []managedagents.Worker) {
	writeMetricHelp(builder, "tma_workers_total", "Workers by status and type.")
	writeMetricType(builder, "tma_workers_total", "gauge")
	workerCounts := map[string]int64{}
	for _, worker := range workers {
		key := worker.Status + "\x00" + worker.WorkerType
		workerCounts[key]++
	}
	keys := make([]string, 0, len(workerCounts))
	for key := range workerCounts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		status, workerType, _ := strings.Cut(key, "\x00")
		writeMetric(builder, "tma_workers_total", map[string]string{
			"status": status,
			"type":   workerType,
		}, workerCounts[key])
	}
}

func writeSubagentMetrics(builder *strings.Builder, metrics managedagents.SubagentMetrics) {
	writeMetricHelp(builder, "tma_subagent_status_total", "Subagent queue and runtime totals by status.")
	writeMetricType(builder, "tma_subagent_status_total", "gauge")
	baseLabels := map[string]string{}
	if metrics.WorkspaceID != "" {
		baseLabels["workspace_id"] = metrics.WorkspaceID
	}
	writeMetric(builder, "tma_subagent_status_total", withLabel(baseLabels, "status", "queued"), metrics.Queued)
	writeMetric(builder, "tma_subagent_status_total", withLabel(baseLabels, "status", "running"), metrics.Running)
	writeMetric(builder, "tma_subagent_status_total", withLabel(baseLabels, "status", "rejected"), metrics.Rejected)

	writeMetricHelp(builder, "tma_subagent_wait_seconds", "Oldest pending subagent queue wait in seconds.")
	writeMetricType(builder, "tma_subagent_wait_seconds", "gauge")
	writeMetric(builder, "tma_subagent_wait_seconds", baseLabels, metrics.WaitSeconds)
}

func writeTaskGroupMetrics(builder *strings.Builder, metrics managedagents.SubagentTaskGroupMetrics) {
	writeMetricHelp(builder, "tma_subagent_group_status_total", "Task group totals by status.")
	writeMetricType(builder, "tma_subagent_group_status_total", "gauge")
	baseLabels := map[string]string{}
	if metrics.WorkspaceID != "" {
		baseLabels["workspace_id"] = metrics.WorkspaceID
	}
	writeMetric(builder, "tma_subagent_group_status_total", withLabel(baseLabels, "status", "pending"), metrics.Pending)
	writeMetric(builder, "tma_subagent_group_status_total", withLabel(baseLabels, "status", "running"), metrics.Running)
	writeMetric(builder, "tma_subagent_group_status_total", withLabel(baseLabels, "status", "completed"), metrics.Completed)
	writeMetric(builder, "tma_subagent_group_status_total", withLabel(baseLabels, "status", "failed"), metrics.Failed)
	writeMetric(builder, "tma_subagent_group_status_total", withLabel(baseLabels, "status", "canceled"), metrics.Canceled)

	writeMetricHelp(builder, "tma_subagent_group_items_total", "Task group item totals by initial orchestration state.")
	writeMetricType(builder, "tma_subagent_group_items_total", "gauge")
	writeMetric(builder, "tma_subagent_group_items_total", withLabel(baseLabels, "status", "created"), metrics.ItemCreated)
	writeMetric(builder, "tma_subagent_group_items_total", withLabel(baseLabels, "status", "started"), metrics.ItemStarted)
	writeMetric(builder, "tma_subagent_group_items_total", withLabel(baseLabels, "status", "queued"), metrics.ItemQueued)
	writeMetric(builder, "tma_subagent_group_items_total", withLabel(baseLabels, "status", "rejected"), metrics.ItemRejected)
}

func writeObservabilityMetrics(builder *strings.Builder, status Status) {
	writeMetricHelp(builder, "tma_observability_exporter_enabled", "Whether an observability exporter is enabled.")
	writeMetricType(builder, "tma_observability_exporter_enabled", "gauge")
	writeMetric(builder, "tma_observability_exporter_enabled", map[string]string{"exporter": "perfetto"}, boolMetric(status.Perfetto.Enabled))
	writeMetric(builder, "tma_observability_exporter_enabled", map[string]string{"exporter": "otlp"}, boolMetric(status.OTLP.Enabled))

	writeMetricHelp(builder, "tma_observability_exporter_sample_rate", "Automatic observability exporter sampling rate.")
	writeMetricType(builder, "tma_observability_exporter_sample_rate", "gauge")
	writeFloatMetric(builder, "tma_observability_exporter_sample_rate", nil, status.Sampling.SampleRate)

	writeMetricHelp(builder, "tma_observability_exporter_retry_max_attempts", "Maximum exporter attempts including the original try.")
	writeMetricType(builder, "tma_observability_exporter_retry_max_attempts", "gauge")
	writeMetric(builder, "tma_observability_exporter_retry_max_attempts", nil, int64(status.Retry.MaxAttempts))

	writeMetricHelp(builder, "tma_observability_exporter_pending_recent_retries", "Recent failed exporter runs with a scheduled retry.")
	writeMetricType(builder, "tma_observability_exporter_pending_recent_retries", "gauge")
	writeMetric(builder, "tma_observability_exporter_pending_recent_retries", nil, int64(status.Retry.PendingRecentRetries))

	writeMetricHelp(builder, "tma_observability_exporter_last_success_timestamp_seconds", "Unix timestamp of the last successful exporter run.")
	writeMetricType(builder, "tma_observability_exporter_last_success_timestamp_seconds", "gauge")
	writeMetric(builder, "tma_observability_exporter_last_success_timestamp_seconds", map[string]string{"exporter": "perfetto"}, healthTimestamp(status.Perfetto.LastSuccess))
	writeMetric(builder, "tma_observability_exporter_last_success_timestamp_seconds", map[string]string{"exporter": "otlp"}, healthTimestamp(status.OTLP.LastSuccess))

	writeMetricHelp(builder, "tma_observability_exporter_last_failure_timestamp_seconds", "Unix timestamp of the last failed exporter run.")
	writeMetricType(builder, "tma_observability_exporter_last_failure_timestamp_seconds", "gauge")
	writeMetric(builder, "tma_observability_exporter_last_failure_timestamp_seconds", map[string]string{"exporter": "perfetto"}, healthTimestamp(status.Perfetto.LastFailure))
	writeMetric(builder, "tma_observability_exporter_last_failure_timestamp_seconds", map[string]string{"exporter": "otlp"}, healthTimestamp(status.OTLP.LastFailure))

	writeMetricHelp(builder, "tma_observability_exporter_last_attempt_timestamp_seconds", "Unix timestamp of the last exporter run attempt.")
	writeMetricType(builder, "tma_observability_exporter_last_attempt_timestamp_seconds", "gauge")
	writeMetric(builder, "tma_observability_exporter_last_attempt_timestamp_seconds", map[string]string{"exporter": "perfetto"}, healthTimestamp(status.Perfetto.LastAttempt))
	writeMetric(builder, "tma_observability_exporter_last_attempt_timestamp_seconds", map[string]string{"exporter": "otlp"}, healthTimestamp(status.OTLP.LastAttempt))

	writeMetricHelp(builder, "tma_observability_exporter_recent_runs_total", "Recent persisted exporter runs by exporter and status.")
	writeMetricType(builder, "tma_observability_exporter_recent_runs_total", "gauge")
	runCounts := map[string]int64{}
	for _, run := range status.RecentRuns {
		if run.Exporter == "" || run.Status == "" {
			continue
		}
		runCounts[run.Exporter+"\x00"+run.Status]++
	}
	runKeys := make([]string, 0, len(runCounts))
	for key := range runCounts {
		runKeys = append(runKeys, key)
	}
	sort.Strings(runKeys)
	for _, key := range runKeys {
		exporter, runStatus, _ := strings.Cut(key, "\x00")
		writeMetric(builder, "tma_observability_exporter_recent_runs_total", map[string]string{"exporter": exporter, "status": runStatus}, runCounts[key])
	}
}

func boolMetric(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func healthTimestamp(health *ExporterHealth) int64 {
	if health == nil || health.At.IsZero() {
		return 0
	}
	return health.At.Unix()
}

func writeTraceMetrics(builder *strings.Builder, trace *TurnTrace, events []managedagents.Event, interventions []managedagents.SessionIntervention) {
	if trace == nil || trace.SessionID == "" {
		return
	}
	sessionLabels := map[string]string{
		"session_id": trace.SessionID,
	}
	writeMetricHelp(builder, "tma_session_events_total", "Session event totals by event type.")
	writeMetricType(builder, "tma_session_events_total", "gauge")
	eventCounts := map[string]int64{}
	for _, event := range events {
		eventCounts[event.Type]++
	}
	eventTypes := make([]string, 0, len(eventCounts))
	for eventType := range eventCounts {
		eventTypes = append(eventTypes, eventType)
	}
	sort.Strings(eventTypes)
	for _, eventType := range eventTypes {
		writeMetric(builder, "tma_session_events_total", withLabel(sessionLabels, "event_type", eventType), eventCounts[eventType])
	}
	writeCompletionValidationMetrics(builder, sessionLabels, trace.TurnID, events)
	writeLLMStreamMetrics(builder, sessionLabels, trace.TurnID, events)

	writeMetricHelp(builder, "tma_pending_interventions_total", "Pending intervention count for the selected session.")
	writeMetricType(builder, "tma_pending_interventions_total", "gauge")
	var pending int64
	for _, intervention := range interventions {
		if intervention.Status == managedagents.InterventionStatusPending {
			pending++
		}
	}
	writeMetric(builder, "tma_pending_interventions_total", sessionLabels, pending)

	if trace.TurnID == "" {
		return
	}

	writeMetricHelp(builder, "tma_trace_duration_milliseconds", "Projected turn duration in milliseconds.")
	writeMetricType(builder, "tma_trace_duration_milliseconds", "gauge")
	writeMetric(builder, "tma_trace_duration_milliseconds", mergeLabels(sessionLabels, map[string]string{
		"turn_id": trace.TurnID,
		"status":  defaultTraceLabel(trace.Status, "running"),
	}), trace.Stats.DurationMillis)

	writeMetricHelp(builder, "tma_trace_steps_total", "Projected trace step totals by turn.")
	writeMetricType(builder, "tma_trace_steps_total", "gauge")
	writeMetric(builder, "tma_trace_steps_total", mergeLabels(sessionLabels, map[string]string{
		"turn_id": trace.TurnID,
	}), int64(trace.Stats.StepCount))

	writeMetricHelp(builder, "tma_trace_critical_path_duration_milliseconds", "Projected critical path duration in milliseconds.")
	writeMetricType(builder, "tma_trace_critical_path_duration_milliseconds", "gauge")
	writeMetric(builder, "tma_trace_critical_path_duration_milliseconds", mergeLabels(sessionLabels, map[string]string{
		"turn_id": trace.TurnID,
		"status":  defaultTraceLabel(trace.Status, "running"),
	}), trace.Graph.CriticalPathDurationMillis)

	writeMetricHelp(builder, "tma_trace_max_span_depth", "Maximum projected span tree depth by turn.")
	writeMetricType(builder, "tma_trace_max_span_depth", "gauge")
	writeMetric(builder, "tma_trace_max_span_depth", mergeLabels(sessionLabels, map[string]string{
		"turn_id": trace.TurnID,
	}), int64(trace.Graph.MaxDepth))

	writeMetricHelp(builder, "tma_trace_critical_spans_total", "Projected critical path span count by turn.")
	writeMetricType(builder, "tma_trace_critical_spans_total", "gauge")
	writeMetric(builder, "tma_trace_critical_spans_total", mergeLabels(sessionLabels, map[string]string{
		"turn_id": trace.TurnID,
	}), int64(len(trace.Graph.CriticalSpanIDs)))

	writeMetricHelp(builder, "tma_trace_spans_total", "Projected span totals by turn and kind.")
	writeMetricType(builder, "tma_trace_spans_total", "gauge")
	spanCounts := map[string]int64{}
	spanMaxDuration := map[string]int64{}
	spanMaxSelfDuration := map[string]int64{}
	for _, span := range trace.Spans {
		kind := defaultTraceLabel(span.Kind, "unknown")
		spanCounts[kind]++
		if span.DurationMillis > spanMaxDuration[kind] {
			spanMaxDuration[kind] = span.DurationMillis
		}
		if span.SelfDurationMillis > spanMaxSelfDuration[kind] {
			spanMaxSelfDuration[kind] = span.SelfDurationMillis
		}
	}
	spanKinds := make([]string, 0, len(spanCounts))
	for kind := range spanCounts {
		spanKinds = append(spanKinds, kind)
	}
	sort.Strings(spanKinds)
	for _, kind := range spanKinds {
		writeMetric(builder, "tma_trace_spans_total", mergeLabels(sessionLabels, map[string]string{
			"turn_id": trace.TurnID,
			"kind":    kind,
		}), spanCounts[kind])
	}
	writeMetricHelp(builder, "tma_trace_span_duration_milliseconds_max", "Maximum projected span duration by turn and kind.")
	writeMetricType(builder, "tma_trace_span_duration_milliseconds_max", "gauge")
	writeMetricHelp(builder, "tma_trace_span_self_duration_milliseconds_max", "Maximum projected span self duration by turn and kind.")
	writeMetricType(builder, "tma_trace_span_self_duration_milliseconds_max", "gauge")
	for _, kind := range spanKinds {
		labels := mergeLabels(sessionLabels, map[string]string{
			"turn_id": trace.TurnID,
			"kind":    kind,
		})
		writeMetric(builder, "tma_trace_span_duration_milliseconds_max", labels, spanMaxDuration[kind])
		writeMetric(builder, "tma_trace_span_self_duration_milliseconds_max", labels, spanMaxSelfDuration[kind])
	}

	writeMetricHelp(builder, "tma_tool_calls_total", "Tool call totals by tool and outcome for the selected session turn.")
	writeMetricType(builder, "tma_tool_calls_total", "gauge")
	toolCounts := map[string]int64{}
	for _, step := range trace.Steps {
		if step.Type != managedagents.EventRuntimeToolResult {
			continue
		}
		key := strings.Join([]string{defaultTraceLabel(step.Identifier, "default"), defaultTraceLabel(step.APIName, "unknown"), defaultTraceLabel(step.Outcome, "unknown")}, "\x00")
		toolCounts[key]++
	}
	toolKeys := make([]string, 0, len(toolCounts))
	for key := range toolCounts {
		toolKeys = append(toolKeys, key)
	}
	sort.Strings(toolKeys)
	for _, key := range toolKeys {
		parts := strings.Split(key, "\x00")
		writeMetric(builder, "tma_tool_calls_total", mergeLabels(sessionLabels, map[string]string{
			"turn_id":         trace.TurnID,
			"tool_identifier": parts[0],
			"api_name":        parts[1],
			"outcome":         parts[2],
		}), toolCounts[key])
	}

	fileGeneration := projectFileGenerationMetrics(events, trace.TurnID)
	fileGenerationLabels := mergeLabels(sessionLabels, map[string]string{"turn_id": trace.TurnID})
	writeMetricHelp(builder, "tma_file_generation_oversized_calls_total", "Oversized segmented file mutation calls rejected in the selected turn.")
	writeMetricType(builder, "tma_file_generation_oversized_calls_total", "gauge")
	writeMetric(builder, "tma_file_generation_oversized_calls_total", fileGenerationLabels, fileGeneration.OversizedCalls)
	writeMetricHelp(builder, "tma_file_generation_segments_total", "Semantic file segments successfully written in the selected turn.")
	writeMetricType(builder, "tma_file_generation_segments_total", "gauge")
	writeMetric(builder, "tma_file_generation_segments_total", fileGenerationLabels, fileGeneration.Segments)
	writeMetricHelp(builder, "tma_file_generation_idempotent_replays_total", "Hash-verified idempotent segment replays in the selected turn.")
	writeMetricType(builder, "tma_file_generation_idempotent_replays_total", "gauge")
	writeMetric(builder, "tma_file_generation_idempotent_replays_total", fileGenerationLabels, fileGeneration.IdempotentReplays)
	writeMetricHelp(builder, "tma_file_generation_remaining_placeholders", "Segment placeholders remaining at the latest observed state in the selected turn.")
	writeMetricType(builder, "tma_file_generation_remaining_placeholders", "gauge")
	writeMetric(builder, "tma_file_generation_remaining_placeholders", fileGenerationLabels, fileGeneration.RemainingPlaceholders)
	writeMetricHelp(builder, "tma_file_generation_duration_milliseconds", "Elapsed segmented file generation time observed in the selected turn.")
	writeMetricType(builder, "tma_file_generation_duration_milliseconds", "gauge")
	writeMetric(builder, "tma_file_generation_duration_milliseconds", fileGenerationLabels, fileGeneration.DurationMillis)

	writeMetricHelp(builder, "tma_tool_approvals_total", "Approval decisions by tool for the selected session.")
	writeMetricType(builder, "tma_tool_approvals_total", "gauge")
	decisionCounts := map[string]int64{}
	for _, intervention := range interventions {
		key := strings.Join([]string{
			defaultTraceLabel(intervention.TurnID, "unknown"),
			defaultTraceLabel(intervention.ToolIdentifier, "default"),
			defaultTraceLabel(intervention.APIName, "unknown"),
			defaultTraceLabel(intervention.Status, "pending"),
		}, "\x00")
		decisionCounts[key]++
	}
	decisionKeys := make([]string, 0, len(decisionCounts))
	for key := range decisionCounts {
		decisionKeys = append(decisionKeys, key)
	}
	sort.Strings(decisionKeys)
	for _, key := range decisionKeys {
		parts := strings.Split(key, "\x00")
		writeMetric(builder, "tma_tool_approvals_total", mergeLabels(sessionLabels, map[string]string{
			"turn_id":         parts[0],
			"tool_identifier": parts[1],
			"api_name":        parts[2],
			"decision":        parts[3],
		}), decisionCounts[key])
	}
}

func writeLLMStreamMetrics(builder *strings.Builder, sessionLabels map[string]string, turnID string, events []managedagents.Event) {
	writeMetricHelp(builder, "tma_llm_stream_chunks", "Aggregated provider stream chunk count for a selected LLM request.")
	writeMetricType(builder, "tma_llm_stream_chunks", "gauge")
	writeMetricHelp(builder, "tma_llm_stream_output_characters", "Aggregated user-visible output characters for a selected LLM request.")
	writeMetricType(builder, "tma_llm_stream_output_characters", "gauge")
	writeMetricHelp(builder, "tma_llm_stream_reasoning_characters", "Aggregated reasoning characters without retaining reasoning content.")
	writeMetricType(builder, "tma_llm_stream_reasoning_characters", "gauge")
	writeMetricHelp(builder, "tma_llm_stream_ttft_milliseconds", "Time to first user-visible text chunk in milliseconds for a selected LLM request.")
	writeMetricType(builder, "tma_llm_stream_ttft_milliseconds", "gauge")

	for _, event := range events {
		if event.Type != managedagents.EventRuntimeLLMResponse || event.TurnID != turnID {
			continue
		}
		data := payloadData(event.Payload)
		stream, _ := data["stream"].(map[string]any)
		if !mapBool(stream, "streamed") {
			continue
		}
		labels := mergeLabels(sessionLabels, map[string]string{
			"turn_id":    turnID,
			"tool_round": fmt.Sprintf("%d", mapInt64(data, "tool_round")),
		})
		writeMetric(builder, "tma_llm_stream_chunks", labels, mapInt64(stream, "chunk_count"))
		writeMetric(builder, "tma_llm_stream_output_characters", labels, mapInt64(stream, "output_chars"))
		writeMetric(builder, "tma_llm_stream_reasoning_characters", labels, mapInt64(stream, "reasoning_chars"))
		writeMetric(builder, "tma_llm_stream_ttft_milliseconds", labels, mapInt64(stream, "ttft_ms"))
	}
}

func writeCompletionValidationMetrics(builder *strings.Builder, sessionLabels map[string]string, turnID string, events []managedagents.Event) {
	if strings.TrimSpace(turnID) == "" {
		return
	}
	counts := map[string]int64{}
	for _, event := range events {
		if event.TurnID != turnID {
			continue
		}
		outcome := completionMetricOutcome(event.Type)
		if outcome == "" {
			continue
		}
		validator := "unknown"
		var payload struct {
			Data struct {
				Validator string `json:"validator"`
			} `json:"data"`
		}
		if json.Unmarshal(event.Payload, &payload) == nil {
			validator = completionMetricValidator(payload.Data.Validator)
		}
		counts[outcome+"\x00"+validator]++
	}

	writeMetricHelp(builder, "tma_completion_validation_total", "Completion validation outcomes by bounded validator and outcome for the selected turn.")
	writeMetricType(builder, "tma_completion_validation_total", "gauge")
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts := strings.SplitN(key, "\x00", 2)
		writeMetric(builder, "tma_completion_validation_total", mergeLabels(sessionLabels, map[string]string{
			"turn_id":   turnID,
			"outcome":   parts[0],
			"validator": parts[1],
		}), counts[key])
	}
}

func completionMetricValidator(value string) string {
	value = strings.TrimSpace(value)
	if value == "custom" || strings.HasPrefix(value, "builtin.") {
		if len(value) <= 64 {
			return value
		}
	}
	return "other"
}

type fileGenerationMetricSnapshot struct {
	OversizedCalls        int64
	Segments              int64
	IdempotentReplays     int64
	RemainingPlaceholders int64
	DurationMillis        int64
}

func projectFileGenerationMetrics(events []managedagents.Event, turnID string) fileGenerationMetricSnapshot {
	var result fileGenerationMetricSnapshot
	var latestSeq int64
	for _, event := range events {
		if event.Type != managedagents.EventRuntimeToolResult {
			continue
		}
		var payload struct {
			TurnID string `json:"turn_id"`
			Data   struct {
				FileGeneration struct {
					OversizedCalls        int64 `json:"oversized_call_count"`
					Segments              int64 `json:"segment_count"`
					IdempotentReplays     int64 `json:"idempotent_replay_count"`
					RemainingPlaceholders int64 `json:"remaining_placeholder_count"`
					DurationMillis        int64 `json:"generation_duration_milliseconds"`
				} `json:"file_generation"`
			} `json:"data"`
		}
		if json.Unmarshal(event.Payload, &payload) != nil || (turnID != "" && payload.TurnID != turnID) {
			continue
		}
		metric := payload.Data.FileGeneration
		result.OversizedCalls = max(result.OversizedCalls, metric.OversizedCalls)
		result.Segments = max(result.Segments, metric.Segments)
		result.IdempotentReplays = max(result.IdempotentReplays, metric.IdempotentReplays)
		result.DurationMillis = max(result.DurationMillis, metric.DurationMillis)
		if event.Seq >= latestSeq {
			latestSeq = event.Seq
			result.RemainingPlaceholders = metric.RemainingPlaceholders
		}
	}
	return result
}

func writeMetricHelp(builder *strings.Builder, name string, help string) {
	fmt.Fprintf(builder, "# HELP %s %s\n", name, help)
}

func writeMetricType(builder *strings.Builder, name string, metricType string) {
	fmt.Fprintf(builder, "# TYPE %s %s\n", name, metricType)
}

func writeMetric(builder *strings.Builder, name string, labels map[string]string, value int64) {
	fmt.Fprintf(builder, "%s%s %d\n", name, prometheusLabels(labels), value)
}

func writeFloatMetric(builder *strings.Builder, name string, labels map[string]string, value float64) {
	fmt.Fprintf(builder, "%s%s %g\n", name, prometheusLabels(labels), value)
}

func prometheusLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf(`%s="%s"`, key, escapePrometheusLabel(labels[key])))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func escapePrometheusLabel(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	return strings.ReplaceAll(value, `"`, `\"`)
}

func withLabel(labels map[string]string, key string, value string) map[string]string {
	next := make(map[string]string, len(labels)+1)
	for labelKey, labelValue := range labels {
		next[labelKey] = labelValue
	}
	next[key] = value
	return next
}

func mergeLabels(left map[string]string, right map[string]string) map[string]string {
	merged := make(map[string]string, len(left)+len(right))
	for key, value := range left {
		merged[key] = value
	}
	for key, value := range right {
		merged[key] = value
	}
	return merged
}

func defaultTraceLabel(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
