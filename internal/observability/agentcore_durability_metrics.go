package observability

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/managedagents"
)

const (
	agentCoreDurabilityOutcomeSuccess          = "success"
	agentCoreDurabilityOutcomeError            = "error"
	agentCoreDurabilityOutcomeRevisionConflict = "revision_conflict"
	agentCoreDurabilityOutcomeLeaseLost        = "lease_lost"
)

var agentCoreDurabilityDurationBuckets = []int64{1, 2, 5, 10, 25, 50, 100, 250, 500, 1000}

type AgentCoreDurabilityMetric struct {
	Operation            string
	Outcome              string
	Count                int64
	DurationSumMillis    int64
	DurationBucketCounts []int64
	StateBytesSum        int64
	StateBytesMax        int64
	Events               int64
}

type instrumentedAgentCoreDurability struct {
	next agentcore.DurabilityPort
}

type agentCoreDurabilityObservation struct {
	operation  string
	duration   time.Duration
	stateBytes int64
	events     int64
	err        error
}

var agentCoreDurabilityMetrics = struct {
	sync.Mutex
	values map[string]*AgentCoreDurabilityMetric
}{values: map[string]*AgentCoreDurabilityMetric{}}

func InstrumentAgentCoreDurability(next agentcore.DurabilityPort) agentcore.DurabilityPort {
	if next == nil {
		return nil
	}
	return instrumentedAgentCoreDurability{next: next}
}

func (d instrumentedAgentCoreDurability) Commit(ctx context.Context, transition agentcore.Transition) (agentcore.State, error) {
	return d.observe(ctx, "commit", transition, func() (agentcore.State, error) {
		return d.next.Commit(ctx, transition)
	})
}

func (d instrumentedAgentCoreDurability) Park(ctx context.Context, transition agentcore.ParkTransition) (agentcore.State, error) {
	return d.observe(ctx, "park", transition.Transition, func() (agentcore.State, error) {
		return d.next.Park(ctx, transition)
	})
}

func (d instrumentedAgentCoreDurability) Complete(ctx context.Context, transition agentcore.CompleteTransition) (agentcore.State, error) {
	return d.observe(ctx, "complete", transition.Transition, func() (agentcore.State, error) {
		return d.next.Complete(ctx, transition)
	})
}

func (d instrumentedAgentCoreDurability) Fail(ctx context.Context, transition agentcore.TerminalTransition) (agentcore.State, error) {
	return d.observe(ctx, "fail", transition.Transition, func() (agentcore.State, error) {
		return d.next.Fail(ctx, transition)
	})
}

func (d instrumentedAgentCoreDurability) Cancel(ctx context.Context, transition agentcore.TerminalTransition) (agentcore.State, error) {
	return d.observe(ctx, "cancel", transition.Transition, func() (agentcore.State, error) {
		return d.next.Cancel(ctx, transition)
	})
}

func (d instrumentedAgentCoreDurability) observe(
	_ context.Context,
	operation string,
	transition agentcore.Transition,
	apply func() (agentcore.State, error),
) (agentcore.State, error) {
	raw, _ := json.Marshal(transition.Next)
	startedAt := time.Now()
	state, err := apply()
	recordAgentCoreDurability(agentCoreDurabilityObservation{
		operation: operation, duration: time.Since(startedAt), stateBytes: int64(len(raw)),
		events: int64(len(transition.Events)), err: err,
	})
	return state, err
}

func recordAgentCoreDurability(observation agentCoreDurabilityObservation) {
	operation := boundedAgentCoreDurabilityOperation(observation.operation)
	if operation == "" {
		return
	}
	outcome := agentCoreDurabilityOutcome(observation.err)
	key := operation + "\x00" + outcome
	durationMillis := observation.duration.Milliseconds()
	if durationMillis < 0 {
		durationMillis = 0
	}

	agentCoreDurabilityMetrics.Lock()
	defer agentCoreDurabilityMetrics.Unlock()
	metric := agentCoreDurabilityMetrics.values[key]
	if metric == nil {
		metric = &AgentCoreDurabilityMetric{
			Operation: operation, Outcome: outcome,
			DurationBucketCounts: make([]int64, len(agentCoreDurabilityDurationBuckets)+1),
		}
		agentCoreDurabilityMetrics.values[key] = metric
	}
	metric.Count++
	metric.DurationSumMillis += durationMillis
	metric.StateBytesSum += max(observation.stateBytes, 0)
	metric.StateBytesMax = max(metric.StateBytesMax, observation.stateBytes)
	metric.Events += max(observation.events, 0)
	for index, upperBound := range agentCoreDurabilityDurationBuckets {
		if durationMillis <= upperBound {
			for bucket := index; bucket < len(metric.DurationBucketCounts); bucket++ {
				metric.DurationBucketCounts[bucket]++
			}
			return
		}
	}
	metric.DurationBucketCounts[len(metric.DurationBucketCounts)-1]++
}

func AgentCoreDurabilityMetricsSnapshot() []AgentCoreDurabilityMetric {
	agentCoreDurabilityMetrics.Lock()
	defer agentCoreDurabilityMetrics.Unlock()
	keys := make([]string, 0, len(agentCoreDurabilityMetrics.values))
	for key := range agentCoreDurabilityMetrics.values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]AgentCoreDurabilityMetric, 0, len(keys))
	for _, key := range keys {
		metric := *agentCoreDurabilityMetrics.values[key]
		metric.DurationBucketCounts = append([]int64(nil), metric.DurationBucketCounts...)
		result = append(result, metric)
	}
	return result
}

func boundedAgentCoreDurabilityOperation(value string) string {
	switch value = strings.TrimSpace(value); value {
	case "commit", "park", "complete", "fail", "cancel":
		return value
	default:
		return ""
	}
}

func agentCoreDurabilityOutcome(err error) string {
	switch {
	case err == nil:
		return agentCoreDurabilityOutcomeSuccess
	case errors.Is(err, managedagents.ErrRevisionConflict):
		return agentCoreDurabilityOutcomeRevisionConflict
	case errors.Is(err, managedagents.ErrLeaseLost):
		return agentCoreDurabilityOutcomeLeaseLost
	default:
		return agentCoreDurabilityOutcomeError
	}
}

func resetAgentCoreDurabilityMetricsForTest() {
	agentCoreDurabilityMetrics.Lock()
	agentCoreDurabilityMetrics.values = map[string]*AgentCoreDurabilityMetric{}
	agentCoreDurabilityMetrics.Unlock()
}
