package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
)

func TestCompletionGateAllowsNonEmptyResponseByDefault(t *testing.T) {
	client := &completionScriptClient{responses: []llm.Response{textResponse("done")}}
	var steps []Step

	result, err := (DemoRuntime{Client: client}).RunTurn(t.Context(), TurnRequest{
		SessionID: "session", TurnID: "turn", UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"finish"}]}`),
		EmitStep: collectCompletionSteps(&steps),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}
	if got := payloadText(result.AgentPayload); got != "done" {
		t.Fatalf("unexpected response %q", got)
	}
	if !hasStepType(steps, managedagents.EventRuntimeTurnCompleting) || !hasStepType(steps, managedagents.EventRuntimeCompletionValidated) {
		t.Fatalf("missing completion events: %#v", steps)
	}
}

func TestCompletionGateRetriesEmptyResponseInSameLoop(t *testing.T) {
	client := &completionScriptClient{responses: []llm.Response{textResponse("  "), textResponse("now done")}}
	var steps []Step

	result, err := (DemoRuntime{Client: client}).RunTurn(t.Context(), TurnRequest{
		SessionID: "session", TurnID: "turn", UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"finish"}]}`),
		EmitStep: collectCompletionSteps(&steps),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}
	if got := payloadText(result.AgentPayload); got != "now done" {
		t.Fatalf("unexpected response %q", got)
	}
	if len(client.requests) != 2 || !strings.Contains(messagesText(client.requests[1].Messages), "response because it was empty") {
		t.Fatalf("empty-response feedback did not enter the same loop: %#v", client.requests)
	}
	if countCompletionSteps(steps, managedagents.EventRuntimeCompletionBlocked) != 1 || countCompletionSteps(steps, managedagents.EventRuntimeCompletionValidated) != 1 {
		t.Fatalf("unexpected completion events: %#v", steps)
	}
}

func TestCustomCompletionGateBlocksOnceThenPasses(t *testing.T) {
	client := &completionScriptClient{responses: []llm.Response{textResponse("first"), textResponse("verified")}}
	gateCalls := 0
	var steps []Step
	gate := completionGateFunc(func(_ context.Context, candidate CompletionCandidate) (CompletionVerdict, error) {
		gateCalls++
		if candidate.Attempt == 1 {
			return CompletionVerdict{Outcome: CompletionOutcomeRetry, Validator: "test.acceptance", Reason: "missing evidence", Feedback: "Run the required verification first."}, nil
		}
		return CompletionVerdict{Outcome: CompletionOutcomePass, Validator: "test.acceptance", Evidence: map[string]any{"checked": true}}, nil
	})

	result, err := (DemoRuntime{Client: client, CompletionGate: gate}).RunTurn(t.Context(), TurnRequest{
		SessionID: "session", TurnID: "turn", UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"finish"}]}`),
		EmitStep: collectCompletionSteps(&steps),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}
	if gateCalls != 2 || payloadText(result.AgentPayload) != "verified" {
		t.Fatalf("gate calls=%d result=%q", gateCalls, payloadText(result.AgentPayload))
	}
	if !strings.Contains(messagesText(client.requests[1].Messages), "Run the required verification first.") {
		t.Fatalf("custom feedback missing from retry request: %#v", client.requests[1].Messages)
	}
	blockedData, marshalErr := json.Marshal(firstStepType(steps, managedagents.EventRuntimeCompletionBlocked).Data)
	if marshalErr != nil || strings.Contains(string(blockedData), "first") || strings.Contains(string(blockedData), "Run the required verification first.") {
		t.Fatalf("completion event persisted candidate or feedback text: %s (marshal err: %v)", blockedData, marshalErr)
	}
}

func TestCompletionGateStopsAtConfiguredRetryLimit(t *testing.T) {
	client := &completionScriptClient{responses: []llm.Response{textResponse("first"), textResponse("second")}}
	gate := completionGateFunc(func(context.Context, CompletionCandidate) (CompletionVerdict, error) {
		return CompletionVerdict{Outcome: CompletionOutcomeRetry, Validator: "test.always_block", Reason: "not complete", Feedback: "Continue."}, nil
	})
	var steps []Step

	_, err := (DemoRuntime{Client: client, CompletionGate: gate}).RunTurn(t.Context(), TurnRequest{
		SessionID: "session", TurnID: "turn", UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"finish"}]}`),
		Config: Config{RuntimeSettings: json.RawMessage(`{"completion_gate":{"max_retries":1}}`)}, EmitStep: collectCompletionSteps(&steps),
	})
	if err == nil || !strings.Contains(err.Error(), "retry limit reached (max_retries=1)") {
		t.Fatalf("expected retry limit error, got %v", err)
	}
	if len(client.requests) != 2 || countCompletionSteps(steps, managedagents.EventRuntimeCompletionBlocked) != 1 || countCompletionSteps(steps, managedagents.EventRuntimeCompletionFailed) != 1 {
		t.Fatalf("unexpected retry behavior, requests=%d steps=%#v", len(client.requests), steps)
	}
}

func TestCompletionGateHardFailAndErrorFailClosed(t *testing.T) {
	tests := []struct {
		name string
		gate CompletionGate
		want string
	}{
		{name: "hard fail", gate: completionGateFunc(func(context.Context, CompletionCandidate) (CompletionVerdict, error) {
			return CompletionVerdict{Outcome: CompletionOutcomeFail, Validator: "test.policy", Reason: "policy rejected completion"}, nil
		}), want: "policy rejected completion"},
		{name: "validator error", gate: completionGateFunc(func(context.Context, CompletionCandidate) (CompletionVerdict, error) {
			return CompletionVerdict{}, errors.New("validator unavailable: original detail")
		}), want: "validator unavailable: original detail"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var steps []Step
			_, err := (DemoRuntime{Client: &completionScriptClient{responses: []llm.Response{textResponse("candidate")}}, CompletionGate: test.gate}).RunTurn(t.Context(), TurnRequest{
				SessionID: "session", TurnID: "turn", UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"finish"}]}`), EmitStep: collectCompletionSteps(&steps),
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected fail-closed error containing %q, got %v", test.want, err)
			}
			if countCompletionSteps(steps, managedagents.EventRuntimeCompletionFailed) != 1 {
				t.Fatalf("missing validation failure event: %#v", steps)
			}
		})
	}
}

func TestCompletionGateHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	gate := completionGateFunc(func(context.Context, CompletionCandidate) (CompletionVerdict, error) {
		cancel()
		return CompletionVerdict{}, context.Canceled
	})
	_, err := (DemoRuntime{Client: &completionScriptClient{responses: []llm.Response{textResponse("candidate")}}, CompletionGate: gate}).RunTurn(ctx, TurnRequest{
		SessionID: "session", TurnID: "turn", UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"finish"}]}`),
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func TestCompletionGateRetrySettingClamps(t *testing.T) {
	if got := completionGateMaxRetries(nil); got != 3 {
		t.Fatalf("default retries=%d", got)
	}
	if got := completionGateMaxRetries(json.RawMessage(`{"completion_gate":{"max_retries":-5}}`)); got != 1 {
		t.Fatalf("low clamp=%d", got)
	}
	if got := completionGateMaxRetries(json.RawMessage(`{"completion_gate":{"max_retries":99}}`)); got != 10 {
		t.Fatalf("high clamp=%d", got)
	}
}

type completionGateFunc func(context.Context, CompletionCandidate) (CompletionVerdict, error)

func (fn completionGateFunc) Validate(ctx context.Context, candidate CompletionCandidate) (CompletionVerdict, error) {
	return fn(ctx, candidate)
}

type completionScriptClient struct {
	responses []llm.Response
	requests  []llm.Request
}

func (client *completionScriptClient) Generate(_ context.Context, request llm.Request) (llm.Response, error) {
	client.requests = append(client.requests, request)
	if len(client.responses) == 0 {
		return llm.Response{}, errors.New("completion test client has no response")
	}
	response := client.responses[0]
	client.responses = client.responses[1:]
	return response, nil
}

func (client *completionScriptClient) Provider() string { return llm.ProviderFake }

func collectCompletionSteps(steps *[]Step) func(context.Context, Step) error {
	return func(_ context.Context, step Step) error {
		*steps = append(*steps, step)
		return nil
	}
}

func countCompletionSteps(steps []Step, stepType string) int {
	count := 0
	for _, step := range steps {
		if step.Type == stepType {
			count++
		}
	}
	return count
}
