package observability

import (
	"sort"
	"strings"
	"sync"
)

const (
	AgentCoreMetricCompactionCompleted = "compaction_completed"
	AgentCoreMetricCompactionRecovered = "compaction_recovered"
	AgentCoreMetricToolReplayed        = "tool_replayed"
	AgentCoreMetricToolIndeterminate   = "tool_indeterminate"
	AgentCoreMetricBudgetExhausted     = "budget_exhausted"

	WorkerLeaseMetricLost            = "lease_lost"
	WorkerLeaseMetricRenewalInactive = "renewal_inactive"
	WorkerLeaseMetricRenewalFailed   = "renewal_failed"
)

type AgentCoreMetricInput struct {
	Event       string
	Idempotency string
	Count       int64
}

type AgentCoreRuntimeMetric struct {
	Event       string
	Idempotency string
	Count       int64
}

type WorkerLeaseMetric struct {
	Event string
	Count int64
}

var agentCoreRuntimeCounters = struct {
	sync.Mutex
	counts map[string]int64
}{counts: map[string]int64{}}

var workerLeaseCounters = struct {
	sync.Mutex
	counts map[string]int64
}{counts: map[string]int64{}}

func RecordAgentCoreMetric(input AgentCoreMetricInput) {
	event, ok := boundedAgentCoreMetricEvent(input.Event)
	if !ok || input.Count <= 0 {
		return
	}
	idempotency := "none"
	if event == AgentCoreMetricToolReplayed || event == AgentCoreMetricToolIndeterminate {
		idempotency = boundedAgentCoreIdempotency(input.Idempotency)
	}
	agentCoreRuntimeCounters.Lock()
	agentCoreRuntimeCounters.counts[event+"\x00"+idempotency] += input.Count
	agentCoreRuntimeCounters.Unlock()
}

func AgentCoreMetricsSnapshot() []AgentCoreRuntimeMetric {
	agentCoreRuntimeCounters.Lock()
	defer agentCoreRuntimeCounters.Unlock()
	metrics := make([]AgentCoreRuntimeMetric, 0, len(agentCoreRuntimeCounters.counts))
	for key, count := range agentCoreRuntimeCounters.counts {
		parts := strings.SplitN(key, "\x00", 2)
		metrics = append(metrics, AgentCoreRuntimeMetric{Event: parts[0], Idempotency: parts[1], Count: count})
	}
	sort.Slice(metrics, func(i, j int) bool {
		return metrics[i].Event+"\x00"+metrics[i].Idempotency < metrics[j].Event+"\x00"+metrics[j].Idempotency
	})
	return metrics
}

func RecordWorkerLeaseMetric(event string) {
	event, ok := boundedWorkerLeaseMetricEvent(event)
	if !ok {
		return
	}
	workerLeaseCounters.Lock()
	workerLeaseCounters.counts[event]++
	workerLeaseCounters.Unlock()
}

func WorkerLeaseMetricsSnapshot() []WorkerLeaseMetric {
	workerLeaseCounters.Lock()
	defer workerLeaseCounters.Unlock()
	events := make([]string, 0, len(workerLeaseCounters.counts))
	for event := range workerLeaseCounters.counts {
		events = append(events, event)
	}
	sort.Strings(events)
	metrics := make([]WorkerLeaseMetric, 0, len(events))
	for _, event := range events {
		metrics = append(metrics, WorkerLeaseMetric{Event: event, Count: workerLeaseCounters.counts[event]})
	}
	return metrics
}

func boundedAgentCoreMetricEvent(value string) (string, bool) {
	switch strings.TrimSpace(value) {
	case AgentCoreMetricCompactionCompleted, AgentCoreMetricCompactionRecovered, AgentCoreMetricToolReplayed,
		AgentCoreMetricToolIndeterminate, AgentCoreMetricBudgetExhausted:
		return strings.TrimSpace(value), true
	default:
		return "", false
	}
}

func boundedAgentCoreIdempotency(value string) string {
	switch value = strings.ToLower(strings.TrimSpace(value)); value {
	case "safe", "keyed", "unsafe", "unknown", "idempotent":
		return value
	default:
		return "other"
	}
}

func boundedWorkerLeaseMetricEvent(value string) (string, bool) {
	switch strings.TrimSpace(value) {
	case WorkerLeaseMetricLost, WorkerLeaseMetricRenewalInactive, WorkerLeaseMetricRenewalFailed:
		return strings.TrimSpace(value), true
	default:
		return "", false
	}
}

func resetAgentCoreMetricsForTest() {
	agentCoreRuntimeCounters.Lock()
	agentCoreRuntimeCounters.counts = map[string]int64{}
	agentCoreRuntimeCounters.Unlock()
	workerLeaseCounters.Lock()
	workerLeaseCounters.counts = map[string]int64{}
	workerLeaseCounters.Unlock()
}
