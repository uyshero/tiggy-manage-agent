package observability

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/managedagents"
)

func TestAgentCoreDurabilityMetricsRecordBoundedOperationsAndOutcomes(t *testing.T) {
	resetAgentCoreDurabilityMetricsForTest()
	t.Cleanup(resetAgentCoreDurabilityMetricsForTest)

	recordAgentCoreDurability(agentCoreDurabilityObservation{
		operation: "commit", duration: 7 * time.Millisecond, stateBytes: 1024, events: 2,
	})
	recordAgentCoreDurability(agentCoreDurabilityObservation{
		operation: "commit", duration: 12 * time.Millisecond, stateBytes: 2048, events: 1,
		err: managedagents.ErrRevisionConflict,
	})
	recordAgentCoreDurability(agentCoreDurabilityObservation{
		operation: "tenant-operation", duration: time.Second, stateBytes: 9999,
	})

	metrics := AgentCoreDurabilityMetricsSnapshot()
	if len(metrics) != 2 {
		t.Fatalf("durability metrics = %+v", metrics)
	}
	if metrics[0].Operation != "commit" || metrics[0].Outcome != "revision_conflict" || metrics[0].Count != 1 ||
		metrics[0].StateBytesSum != 2048 || metrics[0].StateBytesMax != 2048 || metrics[0].Events != 1 {
		t.Fatalf("revision conflict metric = %+v", metrics[0])
	}
	if metrics[1].Operation != "commit" || metrics[1].Outcome != "success" || metrics[1].Count != 1 ||
		metrics[1].DurationSumMillis != 7 || metrics[1].StateBytesSum != 1024 || metrics[1].Events != 2 {
		t.Fatalf("success metric = %+v", metrics[1])
	}
}

func TestInstrumentAgentCoreDurabilityRecordsSubmittedTransition(t *testing.T) {
	resetAgentCoreDurabilityMetricsForTest()
	t.Cleanup(resetAgentCoreDurabilityMetricsForTest)

	next := &captureDurability{}
	port := InstrumentAgentCoreDurability(next)
	transition := agentcore.Transition{
		Next: agentcore.State{
			Version: agentcore.StateVersion, SessionID: "session", TurnID: "turn",
			Phase: agentcore.PhaseAwaitingModel,
		},
		Events: []agentcore.RuntimeEvent{{Type: agentcore.EventRuntimeStarted}},
	}
	if _, err := port.Commit(t.Context(), transition); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if next.commits != 1 {
		t.Fatalf("downstream commits = %d", next.commits)
	}
	metrics := AgentCoreDurabilityMetricsSnapshot()
	if len(metrics) != 1 || metrics[0].Operation != "commit" || metrics[0].Outcome != "success" ||
		metrics[0].Count != 1 || metrics[0].Events != 1 || metrics[0].StateBytesSum <= 0 {
		t.Fatalf("durability metrics = %+v", metrics)
	}
}

func TestPrometheusTextIncludesAgentCoreDurabilityMetrics(t *testing.T) {
	text := PrometheusText(MetricsSnapshot{AgentCoreDurability: []AgentCoreDurabilityMetric{{
		Operation: "commit", Outcome: "success", Count: 2,
		DurationSumMillis: 17, DurationBucketCounts: []int64{0, 0, 1, 2, 2, 2, 2, 2, 2, 2, 2},
		StateBytesSum: 3072, StateBytesMax: 2048, Events: 3,
	}}})
	for _, expected := range []string{
		`tma_agent_core_durability_commits_total{operation="commit",outcome="success"} 2`,
		`tma_agent_core_durability_duration_milliseconds_bucket{le="10",operation="commit",outcome="success"} 2`,
		`tma_agent_core_durability_duration_milliseconds_sum{operation="commit",outcome="success"} 17`,
		`tma_agent_core_durability_state_bytes_total{operation="commit",outcome="success"} 3072`,
		`tma_agent_core_durability_state_bytes_max{operation="commit",outcome="success"} 2048`,
		`tma_agent_core_durability_events_total{operation="commit",outcome="success"} 3`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected %q in metrics:\n%s", expected, text)
		}
	}
}

type captureDurability struct {
	commits int
	err     error
}

func (d *captureDurability) Commit(_ context.Context, transition agentcore.Transition) (agentcore.State, error) {
	d.commits++
	if d.err != nil {
		return agentcore.State{}, d.err
	}
	next := transition.Next.Clone()
	next.Revision = transition.ExpectedRevision + 1
	return next, nil
}

func (d *captureDurability) Park(context.Context, agentcore.ParkTransition) (agentcore.State, error) {
	return agentcore.State{}, errors.New("unexpected Park")
}

func (d *captureDurability) Complete(context.Context, agentcore.CompleteTransition) (agentcore.State, error) {
	return agentcore.State{}, errors.New("unexpected Complete")
}

func (d *captureDurability) Fail(context.Context, agentcore.TerminalTransition) (agentcore.State, error) {
	return agentcore.State{}, errors.New("unexpected Fail")
}

func (d *captureDurability) Cancel(context.Context, agentcore.TerminalTransition) (agentcore.State, error) {
	return agentcore.State{}, errors.New("unexpected Cancel")
}
