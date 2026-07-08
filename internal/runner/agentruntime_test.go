package runner

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"tiggy-manage-agent/internal/agentruntime"
	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
)

func TestAgentRuntimeTurnExecutorReturnsRuntimePayload(t *testing.T) {
	store := &mockStore{}
	executor := AgentRuntimeTurnExecutor{
		Runtime: agentruntime.DemoRuntime{},
		Store:   store,
	}

	result, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"hello"}]}`),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	if got := payloadText(result.AgentPayload); got != "Agent runtime received: hello" {
		t.Fatalf("expected runtime payload, got %q", got)
	}
	if result.Usage == nil {
		t.Fatal("expected usage record")
	}
	if result.Usage.WorkspaceID != "wksp_default" || result.Usage.AgentID != "agt_000001" || result.Usage.AgentConfigVersion != 1 || result.Usage.ProviderID != "fake" || result.Usage.Model != "fake-demo" {
		t.Fatalf("unexpected usage record: %#v", result.Usage)
	}
	if got := store.runtimeEventTypes(); len(got) != 5 ||
		got[0] != "runtime.started" ||
		got[1] != "runtime.thinking" ||
		got[2] != "runtime.llm_request" ||
		got[3] != "runtime.llm_response" ||
		got[4] != "runtime.completed" {
		t.Fatalf("unexpected runtime events: %#v", got)
	}
}

func TestAgentRuntimeTurnExecutorRequiresRuntime(t *testing.T) {
	executor := AgentRuntimeTurnExecutor{}

	_, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[]}`),
	})
	if err == nil {
		t.Fatal("expected missing runtime error")
	}
}

func TestAgentRuntimeTurnExecutorReturnsFailedUsageWhenRuntimeFailsAfterLLM(t *testing.T) {
	executor := AgentRuntimeTurnExecutor{
		Runtime: failedAfterLLMRuntime{},
		Store:   &mockStore{},
	}

	result, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[]}`),
	})
	if err == nil {
		t.Fatal("expected runtime error")
	}
	if result.Usage == nil {
		t.Fatal("expected failed usage record")
	}
	if result.Usage.Status != "failed" || result.Usage.ErrorMessage != "runtime event write failed" {
		t.Fatalf("unexpected failed usage record: %#v", result.Usage)
	}
	if result.Usage.ProviderID != "fake" || result.Usage.Model != "fake-demo" || result.Usage.TotalTokens != 12 {
		t.Fatalf("unexpected usage dimensions: %#v", result.Usage)
	}
}

func TestAgentRuntimeTurnExecutorPassesConversationHistory(t *testing.T) {
	store := &mockStore{
		history: []managedagents.ConversationMessage{{
			Seq:     3,
			Role:    "user",
			Payload: json.RawMessage(`{"content":[{"type":"text","text":"my name is Alice"}]}`),
		}},
	}
	runtime := &captureRuntime{}
	executor := AgentRuntimeTurnExecutor{
		Runtime: runtime,
		Store:   store,
	}

	_, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:    "sesn_000001",
		TurnID:       "turn_000002",
		UserEventSeq: 5,
		UserPayload:  json.RawMessage(`{"content":[{"type":"text","text":"what is my name?"}]}`),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	if len(runtime.request.History) != 1 {
		t.Fatalf("expected 1 history message, got %#v", runtime.request.History)
	}
	if runtime.request.History[0].Role != "user" || runtime.request.History[0].Seq != 3 {
		t.Fatalf("unexpected history message: %#v", runtime.request.History[0])
	}
}

func TestAgentRuntimeTurnExecutorPassesSessionInterventionMode(t *testing.T) {
	store := &mockStore{
		runtimeSettings: json.RawMessage(`{"intervention_mode":"approve_for_me"}`),
	}
	runtime := &captureRuntime{}
	executor := AgentRuntimeTurnExecutor{
		Runtime: runtime,
		Store:   store,
	}

	_, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000002",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"go"}]}`),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}
	if runtime.request.Config.InterventionMode != "approve_for_me" {
		t.Fatalf("expected session intervention mode to reach runtime, got %q", runtime.request.Config.InterventionMode)
	}
}

func TestAgentRuntimeTurnExecutorSavesPendingInterventionSteps(t *testing.T) {
	store := &mockStore{}
	executor := AgentRuntimeTurnExecutor{
		Runtime: interventionStepRuntime{},
		Store:   store,
	}

	_, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000003",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"edit"}]}`),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	interventions := store.savedInterventions()
	if len(interventions) != 1 {
		t.Fatalf("expected 1 saved intervention, got %#v", interventions)
	}
	got := interventions[0]
	if got.TurnID != "turn_000003" || got.CallID != "call_edit" || got.ToolIdentifier != "tma.local_system" || got.APIName != "edit_file" {
		t.Fatalf("unexpected saved intervention: %#v", got)
	}
	if got.InterventionMode != "request_approval" || got.Reason != "optional" {
		t.Fatalf("unexpected intervention policy fields: %#v", got)
	}
	if string(got.Arguments) != `{"path":"README.md"}` {
		t.Fatalf("unexpected intervention arguments: %s", string(got.Arguments))
	}
	if string(got.Continuation) != `[{"role":"assistant","content":[{"type":"text","text":"calling tool"}]}]` || got.ContinuationRound != 2 {
		t.Fatalf("unexpected intervention continuation: round=%d messages=%s", got.ContinuationRound, string(got.Continuation))
	}
}

func TestAgentRuntimeTurnExecutorSavesRuntimeSummary(t *testing.T) {
	store := &mockStore{}
	executor := AgentRuntimeTurnExecutor{
		Runtime: summaryRuntime{},
		Store:   store,
	}

	_, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000004",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"hello"}]}`),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	summary, ok := store.summaries["sesn_000001"]
	if !ok {
		t.Fatal("expected summary to be saved")
	}
	if summary.SummaryText != "generated summary" || summary.SourceUntilSeq != 7 {
		t.Fatalf("unexpected saved summary: %#v", summary)
	}
}

type captureRuntime struct {
	request agentruntime.TurnRequest
}

func (r *captureRuntime) RunTurn(_ context.Context, request agentruntime.TurnRequest) (agentruntime.TurnResult, error) {
	r.request = request
	return agentruntime.TurnResult{
		AgentPayload: json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`),
	}, nil
}

type failedAfterLLMRuntime struct{}

func (failedAfterLLMRuntime) RunTurn(context.Context, agentruntime.TurnRequest) (agentruntime.TurnResult, error) {
	return agentruntime.TurnResult{
		Usage: llm.Usage{
			InputTokens:  8,
			OutputTokens: 4,
			TotalTokens:  12,
		},
		Provider:     "fake",
		ProviderType: "fake",
		Model:        "fake-demo",
	}, errors.New("runtime event write failed")
}

type summaryRuntime struct{}

func (summaryRuntime) RunTurn(context.Context, agentruntime.TurnRequest) (agentruntime.TurnResult, error) {
	return agentruntime.TurnResult{
		AgentPayload:          json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`),
		SummaryText:           "generated summary",
		SummarySourceUntilSeq: 7,
	}, nil
}

type interventionStepRuntime struct{}

func (interventionStepRuntime) RunTurn(ctx context.Context, request agentruntime.TurnRequest) (agentruntime.TurnResult, error) {
	if err := request.EmitStep(ctx, agentruntime.Step{
		Type:    managedagents.EventRuntimeToolInterventionRequired,
		Message: "Tool call requires approval before execution.",
		Data: map[string]any{
			"id":                "call_edit",
			"identifier":        "tma.local_system",
			"api_name":          "edit_file",
			"arguments":         map[string]any{"path": "README.md"},
			"intervention_mode": "request_approval",
			"reason":            "optional",
		},
		Private: map[string]any{
			"continuation_messages": []llm.Message{{
				Role:    "assistant",
				Content: []llm.ContentPart{{Type: "text", Text: "calling tool"}},
			}},
			"continuation_round": 2,
		},
	}); err != nil {
		return agentruntime.TurnResult{}, err
	}
	return agentruntime.TurnResult{
		AgentPayload: json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`),
	}, nil
}
