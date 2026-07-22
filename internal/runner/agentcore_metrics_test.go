package runner

import (
	"reflect"
	"testing"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/observability"
)

func TestAgentCoreOutcomeMetricsProjectsRecoveryAndToolSafety(t *testing.T) {
	outcome := agentcore.Outcome{
		Status: agentcore.OutcomeCompleted,
		State: agentcore.State{
			CompactionAttempts: 2,
			Context:            agentcore.ContextState{CompactionCount: 1},
			ToolJournal: []agentcore.ToolCallJournalEntry{
				{Idempotency: "safe", Attempt: 2, Status: agentcore.ToolCallSucceeded},
				{Idempotency: "unsafe", Attempt: 1, Status: agentcore.ToolCallSucceeded, Reconciliation: &agentcore.ToolReconciliation{Outcome: agentcore.ToolReconciliationExecuted}},
			},
		},
	}
	want := []observability.AgentCoreMetricInput{
		{Event: observability.AgentCoreMetricCompactionCompleted, Count: 1},
		{Event: observability.AgentCoreMetricCompactionRecovered, Count: 1},
		{Event: observability.AgentCoreMetricToolReplayed, Idempotency: "safe", Count: 1},
		{Event: observability.AgentCoreMetricToolIndeterminate, Idempotency: "unsafe", Count: 1},
	}
	if got := agentCoreOutcomeMetrics(outcome); !reflect.DeepEqual(got, want) {
		t.Fatalf("agent core outcome metrics = %+v, want %+v", got, want)
	}
}

func TestAgentCoreOutcomeMetricsProjectsBudgetExhaustion(t *testing.T) {
	outcome := agentcore.Outcome{
		Status:  agentcore.OutcomeFailed,
		Failure: &agentcore.Failure{Code: "budget_exhausted"},
	}
	want := []observability.AgentCoreMetricInput{{Event: observability.AgentCoreMetricBudgetExhausted, Count: 1}}
	if got := agentCoreOutcomeMetrics(outcome); !reflect.DeepEqual(got, want) {
		t.Fatalf("agent core outcome metrics = %+v, want %+v", got, want)
	}
}
