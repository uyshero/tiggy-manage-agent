package observability

import (
	"fmt"
	"sort"
	"strings"

	"tiggy-manage-agent/internal/managedagents"
)

type MetricsSnapshot struct {
	Usage         managedagents.LLMUsageAggregateReport
	Workers       []managedagents.Worker
	Observability Status
	Trace         *TurnTrace
	Events        []managedagents.Event
	Interventions []managedagents.SessionIntervention
}

func PrometheusText(snapshot MetricsSnapshot) string {
	var builder strings.Builder
	writeUsageMetrics(&builder, snapshot.Usage)
	writeWorkerMetrics(&builder, snapshot.Workers)
	writeObservabilityMetrics(&builder, snapshot.Observability)
	writeTraceMetrics(&builder, snapshot.Trace, snapshot.Events, snapshot.Interventions)
	return builder.String()
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
