package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

func TestTaskPlanCompletionGatePassesWithoutActivePlan(t *testing.T) {
	gate := TaskPlanCompletionGate{Reader: taskPlanCompletionReader{err: managedagents.ErrNotFound}}
	verdict, err := gate.Validate(t.Context(), CompletionCandidate{SessionID: "session"})
	if err != nil || verdict.Outcome != CompletionOutcomePass || verdict.Validator != taskPlanCompletionValidator {
		t.Fatalf("unexpected verdict=%#v err=%v", verdict, err)
	}
	if verdict.Evidence["active_plan"] != false {
		t.Fatalf("expected no active plan evidence, got %#v", verdict.Evidence)
	}
}

func TestTaskPlanCompletionGateBlocksIncompletePlanWithActionableFeedback(t *testing.T) {
	gate := TaskPlanCompletionGate{Reader: taskPlanCompletionReader{plan: managedagents.SessionTaskPlan{
		ID: "plan_1", Status: managedagents.TaskPlanStatusActive,
		Items: []managedagents.SessionTaskItem{
			{ID: "item_done", Description: "Implement storage", Status: managedagents.TaskItemStatusCompleted, Evidence: "tests passed", EvidenceRefs: verifiedTestEvidenceRefs()},
			{ID: "item_work", Description: "Wire runtime", Status: managedagents.TaskItemStatusInProgress},
			{ID: "item_evidence", Description: "Verify rollout", Status: managedagents.TaskItemStatusCompleted},
		},
	}}}

	verdict, err := gate.Validate(t.Context(), CompletionCandidate{SessionID: "session"})
	if err != nil || verdict.Outcome != CompletionOutcomeRetry {
		t.Fatalf("unexpected verdict=%#v err=%v", verdict, err)
	}
	for _, expected := range []string{"plan_1", "item_work", "status=in_progress", "item_evidence", "missing evidence", "task_update_items"} {
		if !strings.Contains(verdict.Feedback, expected) {
			t.Fatalf("feedback missing %q: %s", expected, verdict.Feedback)
		}
	}
	if verdict.Evidence["completed_with_evidence"] != 1 || verdict.Evidence["remaining_count"] != 2 || verdict.Evidence["ready_to_complete"] != false {
		t.Fatalf("unexpected evidence %#v", verdict.Evidence)
	}
}

func TestTaskPlanCompletionGateRequiresExplicitPlanCompletion(t *testing.T) {
	gate := TaskPlanCompletionGate{Reader: taskPlanCompletionReader{plan: managedagents.SessionTaskPlan{
		ID: "plan_ready", Status: managedagents.TaskPlanStatusActive,
		Items: []managedagents.SessionTaskItem{{ID: "item_1", Status: managedagents.TaskItemStatusCompleted, Evidence: "go test passed", EvidenceRefs: verifiedTestEvidenceRefs()}},
	}}}

	verdict, err := gate.Validate(t.Context(), CompletionCandidate{SessionID: "session"})
	if err != nil || verdict.Outcome != CompletionOutcomeRetry {
		t.Fatalf("unexpected verdict=%#v err=%v", verdict, err)
	}
	if !strings.Contains(verdict.Feedback, "task_complete_plan") || verdict.Evidence["ready_to_complete"] != true {
		t.Fatalf("expected explicit completion guidance, got %#v", verdict)
	}
}

func TestTaskPlanCompletionGateFailsClosedWhenPlanReadFails(t *testing.T) {
	gate := TaskPlanCompletionGate{Reader: taskPlanCompletionReader{err: errors.New("database unavailable")}}
	verdict, err := gate.Validate(t.Context(), CompletionCandidate{SessionID: "session"})
	if err == nil || !strings.Contains(err.Error(), "database unavailable") {
		t.Fatalf("expected plan read failure, got %v", err)
	}
	if verdict.Validator != taskPlanCompletionValidator {
		t.Fatalf("expected task plan validator attribution, got %#v", verdict)
	}
}

type taskPlanCompletionReader struct {
	plan managedagents.SessionTaskPlan
	err  error
}

func (reader taskPlanCompletionReader) GetCurrentSessionTaskPlanContext(context.Context, string) (managedagents.SessionTaskPlan, error) {
	return reader.plan, reader.err
}

func (reader taskPlanCompletionReader) ListSessionTaskPlansContext(context.Context, string) ([]managedagents.SessionTaskPlan, error) {
	return nil, nil
}

type taskPlanCompletionState struct {
	plan managedagents.SessionTaskPlan
}

func (state *taskPlanCompletionState) GetCurrentSessionTaskPlanContext(context.Context, string) (managedagents.SessionTaskPlan, error) {
	if state.plan.Status != managedagents.TaskPlanStatusActive {
		return managedagents.SessionTaskPlan{}, managedagents.ErrNotFound
	}
	return state.plan, nil
}

func (state *taskPlanCompletionState) ListSessionTaskPlansContext(context.Context, string) ([]managedagents.SessionTaskPlan, error) {
	return []managedagents.SessionTaskPlan{state.plan}, nil
}

func (state *taskPlanCompletionState) CreatePlan(context.Context, string, managedagents.CreateSessionTaskPlanInput) (managedagents.SessionTaskPlanResult, error) {
	return managedagents.SessionTaskPlanResult{Plan: state.plan}, nil
}

func (state *taskPlanCompletionState) GetPlan(context.Context, string) (managedagents.SessionTaskPlan, error) {
	return state.GetCurrentSessionTaskPlanContext(context.Background(), state.plan.SessionID)
}

func (state *taskPlanCompletionState) UpdateItems(_ context.Context, _ string, input managedagents.UpdateSessionTaskItemsInput) (managedagents.SessionTaskPlanResult, error) {
	for _, update := range input.Items {
		for index := range state.plan.Items {
			if state.plan.Items[index].ID == update.ItemID {
				state.plan.Items[index].Status = update.Status
				state.plan.Items[index].Evidence = update.Evidence
				state.plan.Items[index].EvidenceRefs = make([]managedagents.TaskEvidenceRef, 0, len(update.EvidenceRefs))
				for _, ref := range update.EvidenceRefs {
					state.plan.Items[index].EvidenceRefs = append(state.plan.Items[index].EvidenceRefs, managedagents.TaskEvidenceRef{
						Kind: managedagents.TaskEvidenceKindToolResult, TurnID: "turn", ToolCallID: ref.ToolCallID, Tool: "verify.check",
					})
				}
			}
		}
	}
	return managedagents.SessionTaskPlanResult{Plan: state.plan}, nil
}

func (state *taskPlanCompletionState) CompletePlan(context.Context, string, managedagents.FinishSessionTaskPlanInput) (managedagents.SessionTaskPlanResult, error) {
	state.plan.Status = managedagents.TaskPlanStatusCompleted
	return managedagents.SessionTaskPlanResult{Plan: state.plan}, nil
}

func (state *taskPlanCompletionState) CancelPlan(context.Context, string, managedagents.FinishSessionTaskPlanInput) (managedagents.SessionTaskPlanResult, error) {
	state.plan.Status = managedagents.TaskPlanStatusCanceled
	return managedagents.SessionTaskPlanResult{Plan: state.plan}, nil
}

type taskPlanCompletionClient struct {
	calls    int
	requests []llm.Request
}

func (client *taskPlanCompletionClient) Generate(_ context.Context, request llm.Request) (llm.Response, error) {
	client.calls++
	client.requests = append(client.requests, request)
	switch client.calls {
	case 1:
		return textResponse("premature final response"), nil
	case 2:
		return taskPlanToolResponse("call_verify", "verify.check", `{}`), nil
	case 3:
		return taskPlanToolResponse("call_update", tools.TaskIdentifier+"."+tools.TaskAPIUpdateItems, `{"items":[{"item_id":"item_loop","status":"completed","evidence":"verification tool passed","evidence_refs":[{"tool_call_id":"call_verify"}]}]}`), nil
	case 4:
		return taskPlanToolResponse("call_complete", tools.TaskAPICompletePlan, `{"plan_id":"plan_loop"}`), nil
	default:
		return textResponse("verified final response"), nil
	}
}

func (client *taskPlanCompletionClient) Provider() string { return llm.ProviderFake }

func taskPlanToolResponse(id, apiName, arguments string) llm.Response {
	if !strings.Contains(apiName, ".") {
		apiName = tools.TaskIdentifier + "." + apiName
	}
	return llm.Response{Message: llm.Message{
		Role: "assistant",
		ToolCalls: []llm.ToolCall{{
			ID: id, Type: "function",
			Function: llm.ToolCallFunction{Name: apiName, Arguments: json.RawMessage(arguments)},
		}},
	}}
}

type taskPlanEvidenceRuntime struct{}

func (taskPlanEvidenceRuntime) Manifest() tools.Manifest {
	return tools.Manifest{
		Identifier: "verify", Type: "builtin", Executors: []string{tools.ExecutorServer},
		API: []tools.API{{
			Name: "check", Namespace: "verify", APIName: "check", Description: "Run a deterministic verification check.",
			Parameters: json.RawMessage(`{"type":"object","additionalProperties":false}`), Risk: tools.ToolRiskRead,
		}},
	}
}

func (taskPlanEvidenceRuntime) Execute(context.Context, tools.Call, tools.ExecutionContext) (tools.ExecutionResult, error) {
	return tools.ExecutionResult{Identifier: "verify", APIName: "check", Content: "verification passed"}, nil
}

func verifiedTestEvidenceRefs() []managedagents.TaskEvidenceRef {
	return []managedagents.TaskEvidenceRef{{Kind: managedagents.TaskEvidenceKindToolResult, TurnID: "turn", ToolCallID: "call_verify", Tool: "verify.check"}}
}
