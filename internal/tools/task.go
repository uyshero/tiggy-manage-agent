package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"tiggy-manage-agent/internal/managedagents"
)

const TaskIdentifier = NamespaceTask

const (
	TaskAPICreatePlan   = "create_plan"
	TaskAPIUpdateItems  = "update_items"
	TaskAPIGetPlan      = "get_plan"
	TaskAPICompletePlan = "complete_plan"
	TaskAPICancelPlan   = "cancel_plan"
)

type TaskToolService interface {
	CreatePlan(context.Context, string, managedagents.CreateSessionTaskPlanInput) (managedagents.SessionTaskPlanResult, error)
	GetPlan(context.Context, string) (managedagents.SessionTaskPlan, error)
	UpdateItems(context.Context, string, managedagents.UpdateSessionTaskItemsInput) (managedagents.SessionTaskPlanResult, error)
	CompletePlan(context.Context, string, managedagents.FinishSessionTaskPlanInput) (managedagents.SessionTaskPlanResult, error)
	CancelPlan(context.Context, string, managedagents.FinishSessionTaskPlanInput) (managedagents.SessionTaskPlanResult, error)
}

type TaskRuntime struct{}

func IsTaskCall(call Call) bool {
	call = NormalizeCall(call)
	return call.Identifier == TaskIdentifier
}

func (TaskRuntime) Manifest() Manifest {
	return Manifest{
		Identifier: TaskIdentifier,
		Type:       "builtin",
		Meta: Meta{
			Title:       "Task Planning",
			Description: "Persist and update the current Session's execution plan and todo state.",
		},
		SystemRole:     `Use task.* to track genuinely multi-step work; there is no separate complexity classifier. Work directly when one or two tool calls are likely enough. Create a tracked plan for roughly 3-4 related steps and a planned plan for 5 or more dependent steps, cross-turn work, multiple deliverables, or when the user asks for a plan. Keep 3-10 outcome-oriented items and at most one in_progress item. Update status as work changes; completed items require concise evidence text plus evidence_refs naming one or more successful non-task tool_call_id values from the current turn; task tools cannot prove their own work. Read the current plan after context changes. Complete the plan only when every item is completed with verified evidence refs. Cancel it when the user changes or abandons the goal. Do not use task tools for subagent fan-out, clarification, tool approval, or plan approval.`,
		Executors:      []string{ExecutorServer},
		ApprovalPolicy: ApprovalPolicyNever,
		API: []API{
			{
				Name: TaskAPICreatePlan, Namespace: NamespaceTask, APIName: TaskAPICreatePlan,
				Description: "Create a persistent 3-10 item execution plan for multi-step work. A new plan supersedes the current active plan.",
				Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"title":{"type":"string","maxLength":200},"goal":{"type":"string","minLength":1,"maxLength":2000},"handling_mode":{"type":"string","enum":["tracked","planned"]},"items":{"type":"array","minItems":3,"maxItems":10,"items":{"type":"string","minLength":1,"maxLength":500}}},"required":["goal","items"]}`),
				Risk:        ToolRiskWrite, Runtime: taskRuntimePolicy(), Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name: TaskAPIUpdateItems, Namespace: NamespaceTask, APIName: TaskAPIUpdateItems,
				Description: "Update one or more current plan items. A completed item needs evidence text and evidence_refs containing successful non-task tool call IDs from this turn; only one item may be in_progress.",
				Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"plan_id":{"type":"string"},"items":{"type":"array","minItems":1,"maxItems":10,"items":{"type":"object","additionalProperties":false,"properties":{"item_id":{"type":"string"},"status":{"type":"string","enum":["pending","in_progress","completed","blocked"]},"evidence":{"type":"string","maxLength":2000},"evidence_refs":{"type":"array","maxItems":10,"items":{"type":"object","additionalProperties":false,"properties":{"tool_call_id":{"type":"string","minLength":1}},"required":["tool_call_id"]}}},"required":["item_id","status"]}}},"required":["items"]}`),
				Risk:        ToolRiskWrite, Runtime: taskRuntimePolicy(), Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name: TaskAPIGetPlan, Namespace: NamespaceTask, APIName: TaskAPIGetPlan,
				Description: "Read the current active plan and all item states for this Session.",
				Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`),
				Risk:        ToolRiskRead, Runtime: taskRuntimePolicy(), Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name: TaskAPICompletePlan, Namespace: NamespaceTask, APIName: TaskAPICompletePlan,
				Description: "Complete the current plan after every item is completed with evidence text and verified tool result references.",
				Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"plan_id":{"type":"string"},"reason":{"type":"string","maxLength":1000}}}`),
				Risk:        ToolRiskWrite, Runtime: taskRuntimePolicy(), Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name: TaskAPICancelPlan, Namespace: NamespaceTask, APIName: TaskAPICancelPlan,
				Description: "Cancel the current plan when its goal is abandoned or replaced.",
				Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"plan_id":{"type":"string"},"reason":{"type":"string","maxLength":1000}}}`),
				Risk:        ToolRiskWrite, Runtime: taskRuntimePolicy(), Implementation: ToolImplementationServerBuiltin,
			},
		},
	}
}

func (TaskRuntime) Execute(ctx context.Context, call Call, executionContext ExecutionContext) (ExecutionResult, error) {
	service := executionContext.TaskService
	if service == nil {
		return failedResult(call, "task_service_unavailable", "task planning is unavailable in this runtime"), nil
	}
	sessionID := strings.TrimSpace(executionContext.SessionID)
	if sessionID == "" {
		return failedResult(call, "invalid_task_context", "task planning requires a Session"), nil
	}
	switch strings.ToLower(strings.TrimSpace(call.APIName)) {
	case TaskAPICreatePlan:
		var input managedagents.CreateSessionTaskPlanInput
		if err := json.Unmarshal(call.Arguments, &input); err != nil {
			return failedResult(call, "invalid_arguments", fmt.Sprintf("decode create_plan arguments: %v", err)), nil
		}
		input.TurnID = executionContext.TurnID
		result, err := service.CreatePlan(ctx, sessionID, input)
		if err != nil {
			return taskFailure(call, err), nil
		}
		return taskResult(call, result, fmt.Sprintf("Created %s task plan %s with %d items.", result.Plan.HandlingMode, result.Plan.ID, len(result.Plan.Items)))
	case TaskAPIUpdateItems:
		var input managedagents.UpdateSessionTaskItemsInput
		if err := json.Unmarshal(call.Arguments, &input); err != nil {
			return failedResult(call, "invalid_arguments", fmt.Sprintf("decode update_items arguments: %v", err)), nil
		}
		input.TurnID = executionContext.TurnID
		result, err := service.UpdateItems(ctx, sessionID, input)
		if err != nil {
			return taskFailure(call, err), nil
		}
		return taskResult(call, result, fmt.Sprintf("Updated %d items in task plan %s.", len(input.Items), result.Plan.ID))
	case TaskAPIGetPlan:
		plan, err := service.GetPlan(ctx, sessionID)
		if err != nil {
			return taskFailure(call, err), nil
		}
		return taskResult(call, plan, fmt.Sprintf("Loaded active task plan %s.", plan.ID))
	case TaskAPICompletePlan, TaskAPICancelPlan:
		var input managedagents.FinishSessionTaskPlanInput
		if err := json.Unmarshal(call.Arguments, &input); err != nil {
			return failedResult(call, "invalid_arguments", fmt.Sprintf("decode %s arguments: %v", call.APIName, err)), nil
		}
		input.TurnID = executionContext.TurnID
		var result managedagents.SessionTaskPlanResult
		var err error
		if call.APIName == TaskAPICompletePlan {
			result, err = service.CompletePlan(ctx, sessionID, input)
		} else {
			result, err = service.CancelPlan(ctx, sessionID, input)
		}
		if err != nil {
			return taskFailure(call, err), nil
		}
		return taskResult(call, result, fmt.Sprintf("Task plan %s is now %s.", result.Plan.ID, result.Plan.Status))
	default:
		return failedResult(call, "unknown_task_api", fmt.Sprintf("unsupported task api %q", call.APIName)), nil
	}
}

func taskRuntimePolicy() *RuntimePolicy {
	return &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto}
}

func taskFailure(call Call, err error) ExecutionResult {
	errorType := "task_operation_failed"
	switch {
	case errors.Is(err, managedagents.ErrNotFound):
		errorType = "task_plan_not_found"
	case errors.Is(err, managedagents.ErrInvalid):
		errorType = "invalid_task_operation"
	case errors.Is(err, managedagents.ErrConflict):
		errorType = "task_plan_conflict"
	case errors.Is(err, managedagents.ErrForbidden):
		errorType = "task_plan_forbidden"
	}
	return failedResult(call, errorType, err.Error())
}

func taskResult(call Call, state any, content string) (ExecutionResult, error) {
	encoded, err := json.Marshal(state)
	if err != nil {
		return ExecutionResult{}, err
	}
	call = NormalizeCall(call)
	return ExecutionResult{ID: call.ID, Identifier: TaskIdentifier, APIName: call.APIName, Content: content, State: encoded}, nil
}
