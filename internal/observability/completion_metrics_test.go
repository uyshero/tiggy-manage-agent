package observability

import (
	"testing"

	"tiggy-manage-agent/internal/managedagents"
)

func TestCompletionValidationMetricsSnapshotRecordsBoundedOutcomes(t *testing.T) {
	completionValidationCounters.Lock()
	previous := completionValidationCounters.counts
	completionValidationCounters.counts = map[string]int64{}
	completionValidationCounters.Unlock()
	t.Cleanup(func() {
		completionValidationCounters.Lock()
		completionValidationCounters.counts = previous
		completionValidationCounters.Unlock()
	})

	RecordCompletionValidation(managedagents.EventRuntimeCompletionBlocked, "builtin.task_plan")
	RecordCompletionValidation(managedagents.EventRuntimeCompletionBlocked, "builtin.task_plan")
	RecordCompletionValidation(managedagents.EventRuntimeCompletionFailed, "tenant-specific-validator")
	RecordCompletionValidation(managedagents.EventRuntimeToolResult, "builtin.task_plan")

	metrics := CompletionValidationMetricsSnapshot()
	if len(metrics) != 2 || metrics[0].Outcome != "fail" || metrics[0].Validator != "other" || metrics[0].Count != 1 || metrics[1].Outcome != "retry" || metrics[1].Validator != "builtin.task_plan" || metrics[1].Count != 2 {
		t.Fatalf("unexpected completion validation counters: %+v", metrics)
	}
}
