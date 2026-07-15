package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type runtimeGuardTestClient struct {
	callTool func(context.Context, string, json.RawMessage) (ToolCallResult, error)
}

func (c runtimeGuardTestClient) ListTools(context.Context) (InitializeResult, []ToolDefinition, error) {
	return InitializeResult{}, nil, nil
}

func (c runtimeGuardTestClient) ListResources(context.Context) (InitializeResult, []ResourceDefinition, error) {
	return InitializeResult{}, nil, nil
}

func (c runtimeGuardTestClient) ListResourceTemplates(context.Context) (InitializeResult, []ResourceTemplate, error) {
	return InitializeResult{}, nil, nil
}

func (c runtimeGuardTestClient) ReadResource(context.Context, string) (ResourceReadResult, error) {
	return ResourceReadResult{}, nil
}

func (c runtimeGuardTestClient) ListPrompts(context.Context) (InitializeResult, []PromptDefinition, error) {
	return InitializeResult{}, nil, nil
}

func (c runtimeGuardTestClient) GetPrompt(context.Context, string, json.RawMessage) (PromptGetResult, error) {
	return PromptGetResult{}, nil
}

func (c runtimeGuardTestClient) Complete(context.Context, CompletionReference, CompletionArgument, CompletionContext) (CompletionResult, error) {
	return CompletionResult{}, nil
}

func (c runtimeGuardTestClient) CallTool(ctx context.Context, name string, arguments json.RawMessage) (ToolCallResult, error) {
	return c.callTool(ctx, name, arguments)
}

func TestRuntimeGuardTimesOutWithoutReplayingToolCall(t *testing.T) {
	guard := NewRuntimeGuard(RuntimeGuardOptions{})
	var calls atomic.Int64
	client := guardedRuntimeClient{
		guard: guard,
		key:   "wksp/mcps/v1",
		policy: EffectiveRuntimePolicy{
			Timeout: 30 * time.Millisecond, MaxConcurrency: 1,
			FailureThreshold: 2, Cooldown: time.Minute,
		},
		client: runtimeGuardTestClient{callTool: func(ctx context.Context, _ string, _ json.RawMessage) (ToolCallResult, error) {
			calls.Add(1)
			<-ctx.Done()
			return ToolCallResult{}, ctx.Err()
		}},
	}

	_, err := client.CallTool(t.Context(), "write", json.RawMessage(`{"value":1}`))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("tool call must not be replayed, got %d calls", calls.Load())
	}
	stats := guard.Stats()
	if stats.CallsTotal != 1 || stats.FailuresByClass["timeout"] != 1 {
		t.Fatalf("unexpected timeout stats: %#v", stats)
	}
}

func TestRuntimeGuardCatalogSuccessDoesNotResetToolCallFailures(t *testing.T) {
	guard := NewRuntimeGuard(RuntimeGuardOptions{})
	policy := EffectiveRuntimePolicy{Timeout: time.Second, MaxConcurrency: 1, FailureThreshold: 2, Cooldown: time.Minute}
	client := guardedRuntimeClient{
		guard: guard, key: "wksp/mcps/v1", policy: policy,
		client: runtimeGuardTestClient{callTool: func(context.Context, string, json.RawMessage) (ToolCallResult, error) {
			return ToolCallResult{}, errors.New("connection reset")
		}},
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, _, err := client.ListTools(t.Context()); err != nil {
			t.Fatalf("catalog call %d: %v", attempt+1, err)
		}
		if _, err := client.CallTool(t.Context(), "write", json.RawMessage(`{}`)); err == nil {
			t.Fatalf("expected tool failure %d", attempt+1)
		}
	}
	if _, _, err := client.ListTools(t.Context()); !errors.Is(err, ErrRuntimeCircuitOpen) {
		t.Fatalf("expected tool failures to open circuit despite catalog success, got %v", err)
	}
	states := guard.States()
	if len(states) != 1 || states[0].State != "open" || states[0].ConsecutiveFailures != 2 {
		t.Fatalf("unexpected circuit state: %#v", states)
	}
}

func TestRuntimeGuardLimitsConcurrencyAndHonorsWaitingContext(t *testing.T) {
	guard := NewRuntimeGuard(RuntimeGuardOptions{})
	policy := EffectiveRuntimePolicy{Timeout: time.Second, MaxConcurrency: 1, FailureThreshold: 3, Cooldown: time.Minute}
	first, err := guard.acquire(t.Context(), "wksp/mcps/v1", policy)
	if err != nil {
		t.Fatal(err)
	}

	waitCtx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err = guard.acquire(waitCtx, "wksp/mcps/v1", policy)
	if !errors.Is(err, ErrRuntimeConcurrencyFull) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected bounded concurrency wait error, got %v", err)
	}
	first.finish(nil)
	stats := guard.Stats()
	if stats.WaitCanceledTotal != 1 || stats.SuccessesTotal != 1 || stats.InFlight != 0 {
		t.Fatalf("unexpected concurrency stats: %#v", stats)
	}
}

func TestRuntimeGuardCircuitHalfOpenProbeAndRecovery(t *testing.T) {
	now := time.Unix(100, 0)
	guard := NewRuntimeGuard(RuntimeGuardOptions{Now: func() time.Time { return now }})
	policy := EffectiveRuntimePolicy{Timeout: time.Second, MaxConcurrency: 2, FailureThreshold: 2, Cooldown: 10 * time.Second}
	for range 2 {
		permit, err := guard.acquire(t.Context(), "wksp/mcps/v1", policy)
		if err != nil {
			t.Fatal(err)
		}
		permit.finish(errors.New("connection refused"))
	}
	if _, err := guard.acquire(t.Context(), "wksp/mcps/v1", policy); !errors.Is(err, ErrRuntimeCircuitOpen) {
		t.Fatalf("expected open circuit rejection, got %v", err)
	}

	now = now.Add(11 * time.Second)
	probe, err := guard.acquire(t.Context(), "wksp/mcps/v1", policy)
	if err != nil {
		t.Fatalf("expected half-open probe, got %v", err)
	}
	if _, err := guard.acquire(t.Context(), "wksp/mcps/v1", policy); !errors.Is(err, ErrRuntimeCircuitOpen) {
		t.Fatalf("expected concurrent half-open rejection, got %v", err)
	}
	probe.finish(nil)

	next, err := guard.acquire(t.Context(), "wksp/mcps/v1", policy)
	if err != nil {
		t.Fatalf("expected closed circuit after successful probe, got %v", err)
	}
	next.finish(nil)
	states := guard.States()
	if len(states) != 1 || states[0].State != "closed" || states[0].ConsecutiveFailures != 0 {
		t.Fatalf("unexpected recovered state: %#v", states)
	}
	stats := guard.Stats()
	if stats.CircuitRejectedTotal != 2 || stats.OpenCircuits != 0 || stats.SuccessesTotal != 2 {
		t.Fatalf("unexpected circuit stats: %#v", stats)
	}
}

func TestRuntimeGuardAllowsOnlyOneHalfOpenProbeConcurrently(t *testing.T) {
	now := time.Unix(200, 0)
	guard := NewRuntimeGuard(RuntimeGuardOptions{Now: func() time.Time { return now }})
	policy := EffectiveRuntimePolicy{Timeout: time.Second, MaxConcurrency: 4, FailureThreshold: 1, Cooldown: time.Second}
	permit, err := guard.acquire(t.Context(), "key", policy)
	if err != nil {
		t.Fatal(err)
	}
	permit.finish(errors.New("503 unavailable"))
	now = now.Add(2 * time.Second)

	var wg sync.WaitGroup
	probeReady := make(chan *runtimePermit, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		probe, acquireErr := guard.acquire(t.Context(), "key", policy)
		if acquireErr != nil {
			t.Errorf("probe acquire: %v", acquireErr)
			return
		}
		probeReady <- probe
	}()
	probe := <-probeReady
	if _, err := guard.acquire(t.Context(), "key", policy); !errors.Is(err, ErrRuntimeCircuitOpen) {
		t.Fatalf("expected second half-open call to be rejected, got %v", err)
	}
	probe.finish(nil)
	wg.Wait()
}

func TestRuntimeGuardRegistryStatesAreWorkspaceScopedAndVersioned(t *testing.T) {
	now := time.Unix(300, 0)
	guard := NewRuntimeGuard(RuntimeGuardOptions{Now: func() time.Time { return now }})
	openPolicy := EffectiveRuntimePolicy{Timeout: time.Second, MaxConcurrency: 2, FailureThreshold: 1, Cooldown: 10 * time.Second}
	openPermit, err := guard.acquirePartition(t.Context(), "alpha/server/v1", RuntimePartition{WorkspaceID: "wksp_alpha", ServerID: "mcps_1", Version: 1}, openPolicy)
	if err != nil {
		t.Fatal(err)
	}
	openPermit.finish(errors.New("connection refused"))

	saturatedPolicy := EffectiveRuntimePolicy{Timeout: time.Second, MaxConcurrency: 1, FailureThreshold: 3, Cooldown: time.Minute}
	saturatedPermit, err := guard.acquirePartition(t.Context(), "alpha/server/v2", RuntimePartition{WorkspaceID: "wksp_alpha", ServerID: "mcps_1", Version: 2}, saturatedPolicy)
	if err != nil {
		t.Fatal(err)
	}
	betaPermit, err := guard.acquirePartition(t.Context(), "beta/server/v1", RuntimePartition{WorkspaceID: "wksp_beta", ServerID: "mcps_beta", Version: 1}, saturatedPolicy)
	if err != nil {
		t.Fatal(err)
	}
	betaPermit.finish(nil)
	embeddedPermit, err := guard.acquirePartition(t.Context(), "alpha/embedded", RuntimePartition{WorkspaceID: "wksp_alpha", Identifier: "local"}, saturatedPolicy)
	if err != nil {
		t.Fatal(err)
	}
	embeddedPermit.finish(nil)

	states := guard.RegistryStates("wksp_alpha")
	if len(states) != 2 {
		t.Fatalf("expected only alpha Registry versions, got %#v", states)
	}
	if states[0].ServerID != "mcps_1" || states[0].Version != 2 || states[0].State != "saturated" || states[0].InFlight != 1 || states[0].MaxConcurrency != 1 {
		t.Fatalf("unexpected saturated v2 state: %#v", states[0])
	}
	if states[1].Version != 1 || states[1].State != "open" || states[1].ConsecutiveFailures != 1 || states[1].FailureThreshold != 1 || states[1].LastFailureClass != "transport" || states[1].CooldownRemaining != 10 {
		t.Fatalf("unexpected open v1 state: %#v", states[1])
	}
	if states[1].LastFailureAt == nil || states[1].OpenUntil == nil || !states[1].LastFailureAt.Equal(now.UTC()) || !states[1].OpenUntil.Equal(now.Add(10*time.Second)) {
		t.Fatalf("unexpected failure timestamps: %#v", states[1])
	}
	if beta := guard.RegistryStates("wksp_beta"); len(beta) != 1 || beta[0].ServerID != "mcps_beta" {
		t.Fatalf("unexpected beta state projection: %#v", beta)
	}
	now = now.Add(11 * time.Second)
	cooled := guard.RegistryStates("wksp_alpha")
	if len(cooled) != 2 || cooled[1].State != "half_open" || guard.Stats().OpenCircuits != 1 {
		t.Fatalf("cooled circuit must remain half-open until a probe succeeds: %#v", cooled)
	}
	saturatedPermit.finish(nil)
}

func TestClassifyRuntimeError(t *testing.T) {
	tests := []struct {
		err    error
		class  string
		counts bool
	}{
		{context.Canceled, "canceled", false},
		{context.DeadlineExceeded, "timeout", true},
		{errors.New("HTTP 401 unauthorized"), "authentication", true},
		{errors.New("HTTP 429 rate limit"), "rate_limited", true},
		{errors.New("connection reset by peer"), "transport", true},
		{errors.New("invalid JSON-RPC response"), "protocol", true},
		{errors.New("HTTP 503 unavailable"), "unavailable", true},
	}
	for _, test := range tests {
		class, counts := ClassifyRuntimeError(test.err)
		if class != test.class || counts != test.counts {
			t.Fatalf("classify %q: got %s/%t want %s/%t", test.err, class, counts, test.class, test.counts)
		}
	}
}
