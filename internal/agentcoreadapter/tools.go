package agentcoreadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"tiggy-manage-agent/internal/agentcore"
	coremodel "tiggy-manage-agent/internal/model"
	"tiggy-manage-agent/internal/tools"
)

type ToolRuntime struct {
	Registry         tools.Registry
	Executor         tools.Executor
	Policy           tools.InterventionPolicy
	ExecutionContext tools.ExecutionContext
}

var _ agentcore.ToolPort = ToolRuntime{}

func (r ToolRuntime) Preflight(_ context.Context, state agentcore.State, calls []coremodel.ToolCall) (agentcore.ToolBatchPlan, error) {
	if len(calls) == 0 {
		return agentcore.ToolBatchPlan{}, errors.New("tool preflight requires calls")
	}
	plan := agentcore.ToolBatchPlan{Calls: make([]agentcore.PlannedToolCall, 0, len(calls))}
	for _, source := range calls {
		call := tools.NormalizeCall(tools.Call{ID: source.ID, Name: source.Name, Arguments: append(json.RawMessage(nil), source.Arguments...)})
		manifest, api, ok := r.Registry.GetAPI(call.Identifier, call.APIName)
		if !ok {
			return agentcore.ToolBatchPlan{}, fmt.Errorf("unsupported tool %q", source.Name)
		}
		if validationError := r.Registry.ValidateCallArguments(call); validationError != nil {
			return agentcore.ToolBatchPlan{}, fmt.Errorf("%s: %s", validationError.Type, validationError.Message)
		}
		decision := r.Policy.EvaluateCall(manifest, api, call, r.executionContext(state))
		sideEffect := toolSideEffect(api)
		executionMode := toolExecutionMode(api)
		lockKey := toolLockKey(api, state, source, executionMode)
		planned := agentcore.PlannedToolCall{
			Call:           source,
			ExecutionMode:  executionMode,
			SideEffect:     sideEffect,
			Idempotency:    toolIdempotency(api),
			IdempotencyKey: agentcore.StableToolIdempotencyKey(state.SessionID, state.TurnID, source),
			LockKey:        lockKey,
			ApprovalStatus: "",
		}
		if decision.Allowed && decision.Required {
			planned.ApprovalStatus = "approved"
		}
		plan.Calls = append(plan.Calls, planned)
		if !decision.Allowed {
			if !decision.Required {
				return agentcore.ToolBatchPlan{}, fmt.Errorf("tool policy denied %q without an intervention", source.Name)
			}
			request, err := json.Marshal(map[string]any{
				"kind":        "tool_approval",
				"call_id":     source.ID,
				"tool":        source.Name,
				"arguments":   json.RawMessage(source.Arguments),
				"reason":      decision.Reason,
				"policy_mode": decision.Mode,
			})
			if err != nil {
				return agentcore.ToolBatchPlan{}, err
			}
			plan.Interactions = append(plan.Interactions, agentcore.RequiredInteraction{
				ID:      "tool_approval:" + source.ID,
				Kind:    "tool_approval",
				CallID:  source.ID,
				Request: request,
			})
		}
	}
	return plan, nil
}

func (r ToolRuntime) Execute(ctx context.Context, state agentcore.State, plan agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error) {
	if err := plan.Validate(); err != nil {
		return agentcore.ToolBatchResult{}, err
	}
	executor := r.Executor
	if executor == nil {
		executor = tools.RegistryExecutor{Registry: r.Registry}
	}
	results := make([]coremodel.ToolResult, 0, len(plan.Calls))
	for _, planned := range plan.Calls {
		if planned.ApprovalStatus == "rejected" {
			return agentcore.ToolBatchResult{}, fmt.Errorf("rejected tool %q reached executor", planned.Call.Name)
		}
		call := tools.NormalizeCall(tools.Call{
			ID:        planned.Call.ID,
			Name:      planned.Call.Name,
			Arguments: append(json.RawMessage(nil), planned.Call.Arguments...),
		})
		manifest, api, ok := r.Registry.GetAPI(call.Identifier, call.APIName)
		if !ok {
			return agentcore.ToolBatchResult{}, fmt.Errorf("unsupported tool %q", planned.Call.Name)
		}
		if validationError := r.Registry.ValidateCallArguments(call); validationError != nil {
			return agentcore.ToolBatchResult{}, fmt.Errorf("%s: %s", validationError.Type, validationError.Message)
		}
		decision := r.Policy.EvaluateCall(manifest, api, call, r.executionContext(state))
		if !decision.Allowed && planned.ApprovalStatus != "approved" {
			return agentcore.ToolBatchResult{}, fmt.Errorf("tool %q requires approval", planned.Call.Name)
		}
		executionContext := r.executionContext(state)
		executionContext.IdempotencyKey = planned.IdempotencyKey
		result, err := executor.Execute(ctx, call, executionContext)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return agentcore.ToolBatchResult{}, err
			}
			message := tools.RedactEnvironmentText(err.Error(), r.ExecutionContext.Environment)
			failed := tools.ExecutionResult{
				ID: call.ID, Identifier: call.Identifier, APIName: call.APIName,
				Content: message,
				State:   json.RawMessage(`{"status":"failed","error_type":"tool_execution_failed"}`),
				Error:   &tools.ExecutionError{Type: "tool_execution_failed", Message: message},
			}
			results = append(results, coremodel.ToolResult{
				CallID: planned.Call.ID, Name: planned.Call.Name,
				Content: []coremodel.Content{{Type: coremodel.ContentText, Text: tools.ResultMessage(failed)}},
				State:   append(json.RawMessage(nil), failed.State...),
				IsError: true,
			})
			continue
		}
		if result.PendingIntervention {
			return agentcore.ToolBatchResult{}, fmt.Errorf("tool %q requested an intervention after preflight", planned.Call.Name)
		}
		results = append(results, coremodel.ToolResult{
			CallID:  planned.Call.ID,
			Name:    planned.Call.Name,
			Content: []coremodel.Content{{Type: coremodel.ContentText, Text: tools.ResultMessage(result)}},
			State:   append(json.RawMessage(nil), result.State...),
			IsError: result.Error != nil,
		})
	}
	return agentcore.ToolBatchResult{Results: results}, nil
}

func (r ToolRuntime) executionContext(state agentcore.State) tools.ExecutionContext {
	cloned := r.ExecutionContext
	cloned.SessionID = state.SessionID
	cloned.TurnID = state.TurnID
	if cloned.Environment != nil {
		cloned.Environment = cloneStringMap(cloned.Environment)
	}
	return cloned
}

func toolSideEffect(api tools.API) string {
	if strings.TrimSpace(api.Risk) != "" {
		return strings.TrimSpace(api.Risk)
	}
	for _, capability := range api.Capabilities {
		switch capability {
		case tools.CapabilityFilesystemWrite, tools.CapabilityProcessExec, tools.CapabilityCodeExec:
			return "write"
		}
	}
	return "none"
}

func toolIdempotency(api tools.API) string {
	if value := strings.ToLower(strings.TrimSpace(api.Idempotency)); value != "" {
		switch value {
		case "safe", "keyed", "idempotent", "unknown", "unsafe":
			return value
		}
	}
	if toolSideEffect(api) == "none" {
		return "safe"
	}
	return "unknown"
}

func toolExecutionMode(api tools.API) string {
	if value := strings.ToLower(strings.TrimSpace(api.ConcurrencyClass)); value == "parallel" || value == "sequential" {
		return value
	}
	if toolSideEffect(api) == "none" {
		return "parallel"
	}
	return "sequential"
}

func toolLockKey(api tools.API, state agentcore.State, call coremodel.ToolCall, executionMode string) string {
	if value := strings.TrimSpace(api.LockKey); value != "" {
		return state.SessionID + ":" + value
	}
	if executionMode == "sequential" {
		return state.SessionID + ":" + call.Name
	}
	return ""
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
