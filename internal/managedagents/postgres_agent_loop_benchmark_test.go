package managedagents

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/model"
	"tiggy-manage-agent/internal/modeltest"
)

func BenchmarkPostgresAgentLoopFastCommit(b *testing.B) {
	cases := []struct {
		name        string
		eventCount  int
		toolResults bool
	}{
		{name: "events_1", eventCount: 1},
		{name: "events_10", eventCount: 10},
		{name: "events_20", eventCount: 20},
		{name: "tool_results_10", eventCount: 10, toolResults: true},
	}
	for _, benchmark := range cases {
		benchmark := benchmark
		b.Run(benchmark.name, func(b *testing.B) {
			benchmarkPostgresAgentLoopFastCommit(b, benchmark.eventCount, benchmark.toolResults)
		})
	}
}

func benchmarkPostgresAgentLoopFastCommit(b *testing.B, eventCount int, toolResults bool) {
	store := newPostgresAgentLoopIntegrationStore(b)
	session := createPostgresIntegrationSession(b, store)
	started, err := store.StartSessionRunContext(b.Context(), session.ID, StartSessionRunInput{
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"benchmark"}]}`),
	})
	if err != nil {
		b.Fatalf("start session run: %v", err)
	}
	ctx, err := ContextWithDatabaseAccessScope(b.Context(), AccessScope{WorkspaceID: session.WorkspaceID, OwnerID: session.OwnerID})
	if err != nil {
		b.Fatalf("scope context: %v", err)
	}
	durability := store.AgentLoopDurability(leasePostgresAgentLoopTurn(b, store, session.ID, started.Run.ID, "benchmark-fast-commit"))
	state := postgresAgentLoopInitialState(session.ID, started.Run.ID)
	state.Phase = agentcore.PhaseAwaitingModel
	state, err = durability.Commit(ctx, agentcore.Transition{ExpectedRevision: 0, Next: state})
	if err != nil {
		b.Fatalf("seed durable state: %v", err)
	}

	latencies := make([]time.Duration, b.N)
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		next := state.Clone()
		next.ControlCursor++
		events := make([]agentcore.RuntimeEvent, eventCount)
		for index := range events {
			events[index] = agentcore.RuntimeEvent{
				Type: agentcore.EventModelRequested, Message: "Benchmark fast commit.",
				Payload: map[string]any{"iteration": iteration, "event": index},
			}
			if toolResults {
				events[index] = agentcore.RuntimeEvent{
					Type: agentcore.EventToolCallResult, Message: "Benchmark Tool result.",
					Payload: agentcore.ToolCallJournalEntry{
						CallID: fmt.Sprintf("call_%d", index), Status: agentcore.ToolCallSucceeded,
					},
				}
			}
		}
		startedAt := time.Now()
		state, err = durability.Commit(ctx, agentcore.Transition{
			ExpectedRevision: state.Revision,
			Next:             next,
			Events:           events,
		})
		latencies[iteration] = time.Since(startedAt)
		if err != nil {
			b.Fatalf("fast commit %d: %v", iteration, err)
		}
	}
	b.StopTimer()

	reportLatencyPercentiles(b, latencies)
	b.ReportMetric(float64(eventCount), "events/op")
	b.ReportMetric(1, "transactions/op")
}

func BenchmarkPostgresSessionEventAppend(b *testing.B) {
	cases := []struct {
		name    string
		workers int
		sharded bool
	}{
		{name: "same_session_workers_1", workers: 1},
		{name: "same_session_workers_10", workers: 10},
		{name: "same_session_workers_50", workers: 50},
		{name: "sharded_sessions_workers_10", workers: 10, sharded: true},
		{name: "sharded_sessions_workers_50", workers: 50, sharded: true},
	}
	for _, benchmark := range cases {
		benchmark := benchmark
		b.Run(benchmark.name, func(b *testing.B) {
			benchmarkPostgresSessionEventAppend(b, benchmark.workers, benchmark.sharded)
		})
	}
}

func BenchmarkPostgresAgentLoopEndToEnd(b *testing.B) {
	cases := []struct {
		name        string
		toolCalls   int
		sideEffect  string
		idempotency string
		approval    bool
	}{
		{name: "no_tools"},
		{name: "safe_reads_10", toolCalls: 10, sideEffect: "read", idempotency: "safe"},
		{name: "unsafe_writes_10", toolCalls: 10, sideEffect: "write", idempotency: "unsafe"},
		{name: "approval_pause_resume", toolCalls: 1, sideEffect: "write", idempotency: "unsafe", approval: true},
	}
	for _, benchmark := range cases {
		benchmark := benchmark
		b.Run(benchmark.name, func(b *testing.B) {
			benchmarkPostgresAgentLoopEndToEnd(b, benchmark.toolCalls, benchmark.sideEffect, benchmark.idempotency, benchmark.approval)
		})
	}
}

type postgresAgentLoopBenchmarkRun struct {
	session       Session
	turnID        string
	ctx           context.Context
	state         agentcore.State
	fence         AgentLoopFence
	modelPort     agentcore.ModelPort
	toolPort      agentcore.ToolPort
	interactionID string
}

func benchmarkPostgresAgentLoopEndToEnd(b *testing.B, toolCallCount int, sideEffect, idempotency string, approval bool) {
	b.StopTimer()
	store := newPostgresAgentLoopIntegrationStore(b)
	sessions := createPostgresBenchmarkSessions(b, store, b.N)
	runs := make([]postgresAgentLoopBenchmarkRun, b.N)
	for index, session := range sessions {
		started, err := store.StartSessionRunContext(b.Context(), session.ID, StartSessionRunInput{
			Payload: json.RawMessage(`{"content":[{"type":"text","text":"end-to-end benchmark"}]}`),
		})
		if err != nil {
			b.Fatalf("start benchmark session %d: %v", index, err)
		}
		ctx, err := ContextWithDatabaseAccessScope(b.Context(), AccessScope{WorkspaceID: session.WorkspaceID, OwnerID: session.OwnerID})
		if err != nil {
			b.Fatalf("scope benchmark session %d: %v", index, err)
		}
		calls := benchmarkAgentLoopToolCalls(index, toolCallCount)
		state := postgresAgentLoopInitialState(session.ID, started.Run.ID)
		state.Budget.Limit.MaxToolCalls = 100
		toolPort, interactionID := benchmarkAgentLoopTools(calls, sideEffect, idempotency, approval)
		if len(calls) == 0 {
			toolPort = nil
		}
		runs[index] = postgresAgentLoopBenchmarkRun{
			session: session, turnID: started.Run.ID, ctx: ctx, state: state,
			fence:     leasePostgresAgentLoopTurn(b, store, session.ID, started.Run.ID, fmt.Sprintf("e2e-benchmark-%d", index)),
			modelPort: benchmarkAgentLoopModel(calls), toolPort: toolPort, interactionID: interactionID,
		}
	}

	latencies := make([]time.Duration, b.N)
	var totalRevisions int64
	b.ReportAllocs()
	b.ResetTimer()
	b.StartTimer()
	for index := range runs {
		run := &runs[index]
		startedAt := time.Now()
		engine := newPostgresAgentLoopEngine(b, store, run.fence, run.modelPort, run.toolPort)
		outcome, err := engine.Run(run.ctx, run.state)
		if err != nil {
			b.Fatalf("run benchmark session %d: %v", index, err)
		}
		if approval {
			if outcome.Status != agentcore.OutcomePaused || outcome.Pause == nil {
				b.Fatalf("benchmark session %d status=%q, want paused", index, outcome.Status)
			}
			callID := outcome.State.PendingToolBatch.Calls[0].Call.ID
			if _, err := store.DecideSessionInterventionContext(run.ctx, run.session.ID, DecideSessionInterventionInput{
				TurnID: run.turnID, CallID: callID, Status: InterventionStatusApproved,
				DecisionReason: "approved by benchmark",
			}); err != nil {
				b.Fatalf("approve benchmark session %d: %v", index, err)
			}
			resumeFence := leasePostgresAgentLoopTurn(b, store, run.session.ID, run.turnID, fmt.Sprintf("e2e-benchmark-resume-%d", index))
			engine = newPostgresAgentLoopEngine(b, store, resumeFence, run.modelPort, run.toolPort)
			resumed, err := engine.Resume(run.ctx, outcome.State, []agentcore.InteractionDecision{{
				InteractionID: run.interactionID, Status: "approved",
			}})
			if err != nil {
				b.Fatalf("resume benchmark session %d: %v", index, err)
			}
			outcome, err = engine.Run(run.ctx, resumed)
			if err != nil {
				b.Fatalf("run resumed benchmark session %d: %v", index, err)
			}
		}
		latencies[index] = time.Since(startedAt)
		if outcome.Status != agentcore.OutcomeCompleted {
			b.Fatalf("benchmark session %d status=%q, want completed", index, outcome.Status)
		}
		totalRevisions += outcome.State.Revision
	}
	b.StopTimer()

	var totalEvents int64
	var totalStateBytes int64
	for _, run := range runs {
		var events int64
		if err := store.db.QueryRowContext(b.Context(), `SELECT count(*) FROM session_events WHERE session_id = $1 AND turn_id = $2`, run.session.ID, run.turnID).Scan(&events); err != nil {
			b.Fatalf("count benchmark events: %v", err)
		}
		totalEvents += events
		var stateBytes int64
		if err := store.db.QueryRowContext(b.Context(), `SELECT octet_length(state_json::text) FROM agent_loop_states WHERE session_id = $1 AND turn_id = $2`, run.session.ID, run.turnID).Scan(&stateBytes); err != nil {
			b.Fatalf("measure benchmark state: %v", err)
		}
		totalStateBytes += stateBytes
	}
	reportLatencyPercentiles(b, latencies)
	b.ReportMetric(float64(totalRevisions)/float64(b.N), "revisions/op")
	b.ReportMetric(float64(totalEvents)/float64(b.N), "events/op")
	b.ReportMetric(float64(totalStateBytes)/float64(b.N), "state_bytes/op")
}

func benchmarkAgentLoopToolCalls(runIndex, count int) []model.ToolCall {
	calls := make([]model.ToolCall, count)
	for index := range calls {
		calls[index] = model.ToolCall{
			ID: fmt.Sprintf("call_%d_%d", runIndex, index), Name: "benchmark_tool",
			Arguments: json.RawMessage(`{"value":"benchmark"}`),
		}
	}
	return calls
}

func benchmarkAgentLoopModel(calls []model.ToolCall) agentcore.ModelPort {
	completed := model.Response{
		Message:    model.Message{ID: "answer", Content: []model.Content{{Type: model.ContentText, Text: "done"}}},
		StopReason: model.StopReasonComplete,
	}
	if len(calls) == 0 {
		return modeltest.NewScriptedModel(modeltest.ModelStep{Response: completed})
	}
	content := make([]model.Content, len(calls))
	for index := range calls {
		call := calls[index]
		content[index] = model.Content{Type: model.ContentToolCall, ToolCall: &call}
	}
	return modeltest.NewScriptedModel(
		modeltest.ModelStep{Response: model.Response{
			Message: model.Message{ID: "tool_request", Content: content}, StopReason: model.StopReasonToolCall,
		}},
		modeltest.ModelStep{Response: completed},
	)
}

func benchmarkAgentLoopTools(calls []model.ToolCall, sideEffect, idempotency string, approval bool) (agentcore.ToolPort, string) {
	interactionID := "tool_approval:benchmark"
	if approval && len(calls) > 0 {
		interactionID = "tool_approval:" + calls[0].ID
	}
	tools := &modeltest.ScriptedTools{PreflightFunc: func(_ context.Context, state agentcore.State, source []model.ToolCall) (agentcore.ToolBatchPlan, error) {
		planned := make([]agentcore.PlannedToolCall, len(source))
		for index, call := range source {
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
		plan := agentcore.ToolBatchPlan{Calls: planned}
		if approval {
			plan.Calls[0].ApprovalState = agentcore.ToolApprovalPending
			plan.Calls[0].ApprovalSource = agentcore.ToolApprovalSourceHuman
			plan.Interactions = []agentcore.RequiredInteraction{{
				ID: interactionID, Kind: "tool_approval", CallID: source[0].ID,
				Request: json.RawMessage(`{"risk":"write"}`),
			}}
		}
		return plan, nil
	}}
	return tools, interactionID
}

func benchmarkPostgresSessionEventAppend(b *testing.B, workers int, sharded bool) {
	store := newPostgresAgentLoopIntegrationStore(b)
	sessionCount := 1
	if sharded {
		sessionCount = workers
	}
	sessions := createPostgresBenchmarkSessions(b, store, sessionCount)
	scope := AccessScope{WorkspaceID: sessions[0].WorkspaceID, OwnerID: sessions[0].OwnerID}
	for _, session := range sessions {
		if err := appendBenchmarkSessionEvent(b.Context(), store, scope, session.ID, -1); err != nil {
			b.Fatalf("seed session event counter: %v", err)
		}
	}

	latencies := make([]time.Duration, b.N)
	var next atomic.Int64
	var wait sync.WaitGroup
	var firstErr error
	var errOnce sync.Once

	b.ReportAllocs()
	b.ResetTimer()
	wait.Add(workers)
	for worker := 0; worker < workers; worker++ {
		sessionID := sessions[worker%len(sessions)].ID
		go func() {
			defer wait.Done()
			for {
				iteration := int(next.Add(1)) - 1
				if iteration >= b.N {
					return
				}
				startedAt := time.Now()
				err := appendBenchmarkSessionEvent(b.Context(), store, scope, sessionID, iteration)
				latencies[iteration] = time.Since(startedAt)
				if err != nil {
					errOnce.Do(func() { firstErr = err })
					return
				}
			}
		}()
	}
	wait.Wait()
	b.StopTimer()
	if firstErr != nil {
		b.Fatalf("append session event: %v", firstErr)
	}

	reportLatencyPercentiles(b, latencies)
	b.ReportMetric(float64(workers), "workers")
	b.ReportMetric(float64(len(sessions)), "sessions")
	b.ReportMetric(1, "transactions/op")
}

func createPostgresBenchmarkSessions(b *testing.B, store *PostgresStore, count int) []Session {
	first := createPostgresIntegrationSession(b, store)
	sessions := []Session{first}
	for index := 1; index < count; index++ {
		session, err := store.CreateSession(CreateSessionInput{
			AgentID: first.AgentID, EnvironmentID: first.EnvironmentID,
			Title: fmt.Sprintf("Postgres benchmark %d", index), CreatedBy: "benchmark",
		})
		if err != nil {
			b.Fatalf("create benchmark session %d: %v", index, err)
		}
		sessions = append(sessions, session)
		sessionID := session.ID
		b.Cleanup(func() {
			if _, err := store.db.ExecContext(context.Background(), `DELETE FROM sessions WHERE id = $1`, sessionID); err != nil {
				b.Errorf("cleanup benchmark session %s: %v", sessionID, err)
			}
		})
	}
	return sessions
}

func appendBenchmarkSessionEvent(ctx context.Context, store *PostgresStore, scope AccessScope, sessionID string, iteration int) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := setDatabaseAccessScope(ctx, tx, scope.WorkspaceID); err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"iteration": iteration})
	if err != nil {
		return err
	}
	if _, err := store.appendEventTx(ctx, tx, sessionID, "benchmark.session_event", payload, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

func reportLatencyPercentiles(b *testing.B, values []time.Duration) {
	if len(values) == 0 {
		return
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(left, right int) bool { return sorted[left] < sorted[right] })
	percentile := func(value float64) time.Duration {
		index := int(float64(len(sorted)-1) * value)
		return sorted[index]
	}
	b.ReportMetric(float64(percentile(0.50).Nanoseconds()), "p50-ns/op")
	b.ReportMetric(float64(percentile(0.95).Nanoseconds()), "p95-ns/op")
	b.ReportMetric(float64(percentile(0.99).Nanoseconds()), "p99-ns/op")
}
