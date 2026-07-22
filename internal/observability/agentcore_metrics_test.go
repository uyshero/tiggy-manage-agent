package observability

import (
	"strings"
	"testing"
)

func TestAgentCoreMetricsRecordBoundedLabels(t *testing.T) {
	resetAgentCoreMetricsForTest()
	t.Cleanup(resetAgentCoreMetricsForTest)

	RecordAgentCoreMetric(AgentCoreMetricInput{Event: AgentCoreMetricToolReplayed, Idempotency: "keyed", Count: 2})
	RecordAgentCoreMetric(AgentCoreMetricInput{Event: AgentCoreMetricToolIndeterminate, Idempotency: "tenant-value", Count: 1})
	RecordAgentCoreMetric(AgentCoreMetricInput{Event: AgentCoreMetricBudgetExhausted, Idempotency: "ignored", Count: 1})
	RecordAgentCoreMetric(AgentCoreMetricInput{Event: "tenant-event", Count: 10})
	RecordWorkerLeaseMetric(WorkerLeaseMetricLost)
	RecordWorkerLeaseMetric("tenant-event")

	core := AgentCoreMetricsSnapshot()
	if len(core) != 3 || core[0].Event != AgentCoreMetricBudgetExhausted || core[0].Idempotency != "none" || core[0].Count != 1 ||
		core[1].Event != AgentCoreMetricToolIndeterminate || core[1].Idempotency != "other" || core[1].Count != 1 ||
		core[2].Event != AgentCoreMetricToolReplayed || core[2].Idempotency != "keyed" || core[2].Count != 2 {
		t.Fatalf("agent core metrics = %+v", core)
	}
	leases := WorkerLeaseMetricsSnapshot()
	if len(leases) != 1 || leases[0].Event != WorkerLeaseMetricLost || leases[0].Count != 1 {
		t.Fatalf("worker lease metrics = %+v", leases)
	}
}

func TestPrometheusTextIncludesAgentCoreRuntimeMetrics(t *testing.T) {
	text := PrometheusText(MetricsSnapshot{
		AgentCore: []AgentCoreRuntimeMetric{
			{Event: AgentCoreMetricCompactionRecovered, Idempotency: "none", Count: 2},
			{Event: AgentCoreMetricToolReplayed, Idempotency: "safe", Count: 3},
		},
		WorkerLeases: []WorkerLeaseMetric{{Event: WorkerLeaseMetricLost, Count: 4}},
	})
	for _, expected := range []string{
		`tma_agent_core_events_total{event="compaction_recovered",idempotency="none"} 2`,
		`tma_agent_core_events_total{event="tool_replayed",idempotency="safe"} 3`,
		`tma_worker_lease_events_total{event="lease_lost"} 4`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected %q in metrics:\n%s", expected, text)
		}
	}
}
