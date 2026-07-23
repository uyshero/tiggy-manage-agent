package agentcore_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/model"
	"tiggy-manage-agent/internal/modeltest"
)

func BenchmarkAgentLoop(b *testing.B) {
	cases := []struct {
		name        string
		toolCalls   int
		sideEffect  string
		idempotency string
	}{
		{name: "no_tools"},
		{name: "safe_reads_10", toolCalls: 10, sideEffect: "read", idempotency: "safe"},
		{name: "unsafe_writes_10", toolCalls: 10, sideEffect: "write", idempotency: "unsafe"},
	}

	for _, benchmark := range cases {
		benchmark := benchmark
		b.Run(benchmark.name, func(b *testing.B) {
			calls := benchmarkToolCalls(benchmark.toolCalls)
			var totalTransitions int
			var totalEvents int

			b.ReportAllocs()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				transitions, events, err := runAgentLoopBenchmark(calls, benchmark.sideEffect, benchmark.idempotency)
				if err != nil {
					b.Fatal(err)
				}
				totalTransitions += transitions
				totalEvents += events
			}
			b.StopTimer()

			b.ReportMetric(float64(totalTransitions)/float64(b.N), "transitions/op")
			b.ReportMetric(float64(totalEvents)/float64(b.N), "events/op")
			b.ReportMetric(float64(benchmark.toolCalls), "tool_calls/op")
		})
	}
}

func TestAgentLoopDurabilityTransitionCounts(t *testing.T) {
	cases := []struct {
		name                string
		toolCalls           int
		sideEffect          string
		idempotency         string
		expectedTransitions int
	}{
		{name: "no tools", expectedTransitions: 3},
		{name: "one safe read", toolCalls: 1, sideEffect: "read", idempotency: "safe", expectedTransitions: 8},
		{name: "ten safe reads", toolCalls: 10, sideEffect: "read", idempotency: "safe", expectedTransitions: 8},
		{name: "ten unsafe writes", toolCalls: 10, sideEffect: "write", idempotency: "unsafe", expectedTransitions: 17},
	}
	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			transitions, _, err := runAgentLoopBenchmark(benchmarkToolCalls(test.toolCalls), test.sideEffect, test.idempotency)
			if err != nil {
				t.Fatal(err)
			}
			if transitions != test.expectedTransitions {
				t.Fatalf("durability transitions = %d, want %d", transitions, test.expectedTransitions)
			}
		})
	}
}

func runAgentLoopBenchmark(calls []model.ToolCall, sideEffect, idempotency string) (int, int, error) {
	state := initialState(1_000)
	state.Budget.Limit.MaxToolCalls = 100
	var toolPort agentcore.ToolPort
	if len(calls) > 0 {
		toolPort = benchmarkTools(sideEffect, idempotency)
	}
	durability := modeltest.NewMemoryDurability(state)
	engine, err := agentcore.NewEngine(agentcore.Ports{
		Model: benchmarkModel(calls), Context: testContext(), Tools: toolPort,
		Durability: durability, Clock: modeltest.FixedClock{Time: testNow}, IDs: modeltest.NewSequenceIDs(),
	})
	if err != nil {
		return 0, 0, err
	}
	outcome, err := engine.Run(context.Background(), state)
	if err != nil {
		return 0, 0, err
	}
	if outcome.Status != agentcore.OutcomeCompleted {
		return 0, 0, fmt.Errorf("agent loop status = %q", outcome.Status)
	}
	return len(durability.Transitions()), len(durability.Events()), nil
}

func benchmarkToolCalls(count int) []model.ToolCall {
	calls := make([]model.ToolCall, count)
	for index := range calls {
		calls[index] = model.ToolCall{
			ID:        fmt.Sprintf("call_%02d", index+1),
			Name:      "benchmark_tool",
			Arguments: json.RawMessage(`{"value":"benchmark"}`),
		}
	}
	return calls
}

func benchmarkModel(calls []model.ToolCall) agentcore.ModelPort {
	if len(calls) == 0 {
		return modeltest.NewScriptedModel(modeltest.ModelStep{Response: textResponse("answer_1", "done", model.Usage{})})
	}
	return modeltest.NewScriptedModel(
		modeltest.ModelStep{Response: toolResponse("assistant_tools", calls)},
		modeltest.ModelStep{Response: textResponse("answer_2", "done", model.Usage{})},
	)
}

func benchmarkTools(sideEffect, idempotency string) agentcore.ToolPort {
	return &modeltest.ScriptedTools{
		PreflightFunc: func(_ context.Context, state agentcore.State, calls []model.ToolCall) (agentcore.ToolBatchPlan, error) {
			planned := make([]agentcore.PlannedToolCall, len(calls))
			for index, call := range calls {
				executionMode := "parallel"
				if sideEffect == "write" {
					executionMode = "sequential"
				}
				planned[index] = agentcore.PlannedToolCall{
					Call: call, ExecutionMode: executionMode, SideEffect: sideEffect, Idempotency: idempotency,
					IdempotencyKey: agentcore.StableToolIdempotencyKey(state.SessionID, state.TurnID, call),
					Disposition:    agentcore.ToolDispositionExecute, ValidationState: agentcore.ToolValidationValid,
					ApprovalState: agentcore.ToolApprovalNotRequired,
				}
			}
			return agentcore.ToolBatchPlan{Calls: planned}, nil
		},
	}
}
