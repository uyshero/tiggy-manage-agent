package toolruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/managedagents"
	coremodel "tiggy-manage-agent/internal/model"
	"tiggy-manage-agent/internal/tools"
)

type ToolRuntime struct {
	Snapshot         Snapshot
	Executor         tools.Executor
	ExecutionContext tools.ExecutionContext
}

var _ agentcore.ToolPort = ToolRuntime{}

func (r ToolRuntime) Preflight(ctx context.Context, state agentcore.State, calls []coremodel.ToolCall) (agentcore.ToolBatchPlan, error) {
	if len(calls) == 0 {
		return agentcore.ToolBatchPlan{}, errors.New("tool preflight requires calls")
	}
	if strings.TrimSpace(r.Snapshot.registryRevision) == "" || strings.TrimSpace(r.Snapshot.policyRevision) == "" {
		return agentcore.ToolBatchPlan{}, tools.NewToolContractError(
			"invalid_tool_runtime_snapshot", errors.New("tool runtime snapshot is uninitialized"),
		)
	}
	for _, source := range calls {
		call := tools.NormalizeCall(tools.Call{ID: source.ID, Name: source.Name, Arguments: append(json.RawMessage(nil), source.Arguments...)})
		if _, _, ok := r.Snapshot.registry.GetAPI(call.Identifier, call.APIName); !ok || source.ArgumentsError != "" {
			continue
		}
		if validationError := r.Snapshot.registry.ValidateCallArguments(call); validationError != nil {
			if validationError.Type == "invalid_tool_schema" {
				return agentcore.ToolBatchPlan{}, tools.NewToolContractError(
					validationError.Type, fmt.Errorf("%s: %s", validationError.Type, validationError.Message),
				)
			}
			continue
		}
		if tools.IsParkingInteractionCall(call) && len(calls) != 1 {
			return agentcore.ToolBatchPlan{}, fmt.Errorf("interaction.%s must be the only tool call in a model response", call.APIName)
		}
	}
	plan := agentcore.ToolBatchPlan{
		Calls:            make([]agentcore.PlannedToolCall, 0, len(calls)),
		RegistryRevision: r.Snapshot.registryRevision, PolicyRevision: r.Snapshot.policyRevision,
	}
	for _, source := range calls {
		call := tools.NormalizeCall(tools.Call{ID: source.ID, Name: source.Name, Arguments: append(json.RawMessage(nil), source.Arguments...)})
		manifest, api, ok := r.Snapshot.registry.GetAPI(call.Identifier, call.APIName)
		if !ok {
			validationState := agentcore.ToolValidationUnsupportedToolAPI
			if _, runtimeExists := r.Snapshot.registry.Get(call.Identifier); !runtimeExists {
				validationState = agentcore.ToolValidationUnsupportedTool
			}
			plan.Calls = append(plan.Calls, agentcore.PlannedToolCall{
				Call: source, ExecutionMode: "parallel", SideEffect: "none", Idempotency: "safe",
				IdempotencyKey: agentcore.StableToolIdempotencyKey(state.SessionID, state.TurnID, source),
				Disposition:    agentcore.ToolDispositionReturnError, ValidationState: validationState,
				ApprovalState: agentcore.ToolApprovalNotRequired,
			})
			continue
		}
		sideEffect := toolSideEffect(api)
		executionMode := toolExecutionMode(api)
		lockKey := toolLockKey(api, state, source, executionMode)
		planned := agentcore.PlannedToolCall{
			Call:            source,
			ExecutionMode:   executionMode,
			SideEffect:      sideEffect,
			Idempotency:     toolIdempotency(api),
			IdempotencyKey:  agentcore.StableToolIdempotencyKey(state.SessionID, state.TurnID, source),
			LockKey:         lockKey,
			Disposition:     agentcore.ToolDispositionExecute,
			ValidationState: agentcore.ToolValidationValid,
			ApprovalState:   agentcore.ToolApprovalNotRequired,
		}
		if source.ArgumentsError != "" {
			planned.Disposition = agentcore.ToolDispositionReturnError
			planned.ValidationState = agentcore.ToolValidationInvalidArguments
			plan.Calls = append(plan.Calls, planned)
			continue
		}
		if validationError := r.Snapshot.registry.ValidateCallArguments(call); validationError != nil {
			if validationError.Type == "invalid_tool_schema" {
				return agentcore.ToolBatchPlan{}, tools.NewToolContractError(
					validationError.Type, fmt.Errorf("%s: %s", validationError.Type, validationError.Message),
				)
			}
			planned.Disposition = agentcore.ToolDispositionReturnError
			planned.ValidationState = agentcore.ToolValidationInvalidArguments
			plan.Calls = append(plan.Calls, planned)
			continue
		}
		decision := r.Snapshot.policy.EvaluateCall(manifest, api, call, r.executionContext(state))
		planned.Permission = agentCoreToolPermissionDecision(decision)
		if !decision.Allowed && !decision.Required {
			planned.Disposition = agentcore.ToolDispositionDenied
		}
		_, persistedReplay := persistedSegmentEditReplay(state, source)
		fileReceipt, hasFileReceipt := persistedFileReceiptForEdit(state, source)
		if decision.Allowed && decision.Required {
			planned.ApprovalState = agentcore.ToolApprovalAuto
			planned.ApprovalSource = agentcore.ToolApprovalSourcePolicy
		}
		plan.Calls = append(plan.Calls, planned)
		if planned.Disposition == agentcore.ToolDispositionDenied {
			continue
		}
		interaction, ok, err := r.parkingInteraction(ctx, state, call)
		if err != nil {
			return agentcore.ToolBatchPlan{}, err
		}
		if ok {
			plan.Interactions = append(plan.Interactions, interaction)
			continue
		}
		if persistedReplay || (call.APIName == "edit_file" && !hasFileReceipt) {
			continue
		}
		if !decision.Allowed {
			requestData := map[string]any{
				"kind":                       "tool_approval",
				"call_id":                    source.ID,
				"tool":                       source.Name,
				"arguments":                  json.RawMessage(source.Arguments),
				"reason":                     decision.Reason,
				"policy_mode":                decision.Mode,
				"approval_policy":            decision.ApprovalPolicy,
				"matched_rule_id":            decision.MatchedRuleID,
				"rule_source":                decision.RuleSource,
				"risk":                       decision.Risk,
				"suggested_permission_rules": tools.SuggestedPermissionRules(call),
			}
			if call.Identifier == tools.DefaultIdentifier && call.APIName == "edit_file" {
				preview, ok := r.buildDurableEditPreview(ctx, source, fileReceipt)
				if !ok {
					continue
				}
				requestData["edit_preview"] = preview
			}
			request, err := json.Marshal(requestData)
			if err != nil {
				return agentcore.ToolBatchPlan{}, err
			}
			plan.Interactions = append(plan.Interactions, agentcore.RequiredInteraction{
				ID:      "tool_approval:" + source.ID,
				Kind:    "tool_approval",
				CallID:  source.ID,
				Request: request,
			})
			plan.Calls[len(plan.Calls)-1].ApprovalState = agentcore.ToolApprovalPending
			plan.Calls[len(plan.Calls)-1].ApprovalSource = agentcore.ToolApprovalSourceHuman
		}
	}
	return plan, nil
}

func agentCoreToolPermissionDecision(decision tools.InterventionDecision) *agentcore.ToolPermissionDecision {
	name := "deny"
	if decision.Allowed {
		name = "allow"
	} else if decision.Required {
		name = "ask"
	}
	return &agentcore.ToolPermissionDecision{
		Decision: name, Allowed: decision.Allowed, Required: decision.Required,
		Mode: decision.Mode, ApprovalPolicy: decision.ApprovalPolicy,
		Reason: decision.Reason, Risk: decision.Risk,
		MatchedRuleID: decision.MatchedRuleID, RuleSource: decision.RuleSource,
	}
}

func (r ToolRuntime) ValidateExecution(_ context.Context, state agentcore.State, plan agentcore.ToolBatchPlan) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(plan.RegistryRevision) == "" {
		return agentcore.NewToolFatalError("tool_registry_changed", errors.New("tool registry revision is missing from durable preflight"))
	}
	if r.Snapshot.registryRevision == "" || r.Snapshot.registryRevision != plan.RegistryRevision {
		return agentcore.NewToolFatalError("tool_registry_changed", errors.New("tool registry changed after durable preflight"))
	}
	if strings.TrimSpace(plan.PolicyRevision) == "" || strings.TrimSpace(r.Snapshot.policyRevision) == "" {
		return agentcore.NewToolFatalError("tool_policy_changed", errors.New("tool policy revision is missing from durable execution"))
	}
	for _, planned := range plan.Calls {
		if planned.ApprovalState == agentcore.ToolApprovalRejected {
			continue
		}
		call := tools.NormalizeCall(tools.Call{
			ID: planned.Call.ID, Name: planned.Call.Name,
			Arguments: append(json.RawMessage(nil), planned.Call.Arguments...),
		})
		if planned.ValidationState == agentcore.ToolValidationUnsupportedTool || planned.ValidationState == agentcore.ToolValidationUnsupportedToolAPI {
			executionError := unsupportedToolError(r.Snapshot.registry, call)
			expectedType := "unsupported_tool_api"
			if planned.ValidationState == agentcore.ToolValidationUnsupportedTool {
				expectedType = "unsupported_tool"
			}
			if executionError.Type != expectedType {
				return agentcore.NewToolFatalError("tool_registry_changed", errors.New("tool registry changed after durable preflight"))
			}
			continue
		}
		validationError := r.Snapshot.registry.ValidateCallArguments(call)
		if planned.ValidationState == agentcore.ToolValidationInvalidArguments {
			if planned.Call.ArgumentsError != "" {
				continue
			}
			if validationError == nil || validationError.Type != "invalid_tool_arguments" {
				return agentcore.NewToolFatalError("tool_registry_changed", errors.New("tool validation changed after durable preflight"))
			}
			continue
		}
		if validationError != nil {
			return agentcore.NewToolFatalError("tool_registry_changed", fmt.Errorf("%s: %s", validationError.Type, validationError.Message))
		}
		manifest, api, ok := r.Snapshot.registry.GetAPI(call.Identifier, call.APIName)
		if !ok {
			return agentcore.NewToolFatalError("tool_registry_changed", fmt.Errorf("tool registry changed after durable preflight: unsupported tool %q", planned.Call.Name))
		}
		decision := r.Snapshot.policy.EvaluateCall(manifest, api, call, r.executionContext(state))
		if plan.PolicyRevision == r.Snapshot.policyRevision && !sameToolPermissionDecision(planned.Permission, agentCoreToolPermissionDecision(decision)) {
			return agentcore.NewToolFatalError("tool_policy_changed", errors.New("tool permission decision changed after durable preflight"))
		}
	}
	return nil
}

func (r ToolRuntime) Execute(ctx context.Context, state agentcore.State, plan agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error) {
	if err := r.ValidateExecution(ctx, state, plan); err != nil {
		return agentcore.ToolBatchResult{}, err
	}
	executor := snapshotExecutor(r.Snapshot.registry, r.Executor)
	results := make([]coremodel.ToolResult, 0, len(plan.Calls))
	for _, planned := range plan.Calls {
		if planned.ApprovalState == agentcore.ToolApprovalRejected {
			return agentcore.ToolBatchResult{}, fmt.Errorf("rejected tool %q reached executor", planned.Call.Name)
		}
		call := tools.NormalizeCall(tools.Call{
			ID:        planned.Call.ID,
			Name:      planned.Call.Name,
			Arguments: append(json.RawMessage(nil), planned.Call.Arguments...),
		})
		if planned.ValidationState == agentcore.ToolValidationUnsupportedTool || planned.ValidationState == agentcore.ToolValidationUnsupportedToolAPI {
			executionError := unsupportedToolError(r.Snapshot.registry, call)
			expectedType := "unsupported_tool_api"
			if planned.ValidationState == agentcore.ToolValidationUnsupportedTool {
				expectedType = "unsupported_tool"
			}
			if executionError.Type != expectedType {
				return agentcore.ToolBatchResult{}, agentcore.NewToolFatalError("tool_registry_changed", errors.New("tool registry changed after durable preflight"))
			}
			results = append(results, recoverableToolResult(planned.Call, executionError))
			continue
		}
		validationError := r.Snapshot.registry.ValidateCallArguments(call)
		if planned.ValidationState == agentcore.ToolValidationInvalidArguments {
			if planned.Call.ArgumentsError != "" {
				results = append(results, recoverableToolResult(planned.Call, &tools.ExecutionError{
					Type: "invalid_tool_arguments", Message: planned.Call.ArgumentsError,
				}))
				continue
			}
			if validationError == nil || validationError.Type != "invalid_tool_arguments" {
				return agentcore.ToolBatchResult{}, agentcore.NewToolFatalError("tool_registry_changed", errors.New("tool validation changed after durable preflight"))
			}
			results = append(results, recoverableToolResult(planned.Call, validationError))
			continue
		}
		if validationError != nil {
			return agentcore.ToolBatchResult{}, agentcore.NewToolFatalError("tool_registry_changed", fmt.Errorf("%s: %s", validationError.Type, validationError.Message))
		}
		if result, ok, err := resolvedParkingInteraction(call, plan.Interactions); ok {
			if err != nil {
				return agentcore.ToolBatchResult{}, err
			}
			results = append(results, result)
			continue
		}
		manifest, api, ok := r.Snapshot.registry.GetAPI(call.Identifier, call.APIName)
		if !ok {
			return agentcore.ToolBatchResult{}, agentcore.NewToolFatalError("tool_registry_changed", fmt.Errorf("tool registry changed after durable preflight: unsupported tool %q", planned.Call.Name))
		}
		decision := r.Snapshot.policy.EvaluateCall(manifest, api, call, r.executionContext(state))
		currentPermission := agentCoreToolPermissionDecision(decision)
		if plan.PolicyRevision == r.Snapshot.policyRevision && !sameToolPermissionDecision(planned.Permission, currentPermission) {
			return agentcore.ToolBatchResult{}, agentcore.NewToolFatalError("tool_policy_changed", errors.New("tool permission decision changed after durable preflight"))
		}
		if planned.Disposition == agentcore.ToolDispositionDenied || (!decision.Allowed && !decision.Required) {
			denied := tools.PermissionDeniedResult(call, decision)
			results = append(results, coremodel.ToolResult{
				CallID: planned.Call.ID, Name: planned.Call.Name,
				Content: []coremodel.Content{{Type: coremodel.ContentText, Text: tools.ResultMessage(denied)}},
				State:   append(json.RawMessage(nil), denied.State...), IsError: true,
			})
			continue
		}
		if replay, ok := persistedSegmentEditReplay(state, planned.Call); ok {
			results = append(results, replay)
			continue
		}
		fileReceipt, hasFileReceipt := persistedFileReceiptForEdit(state, planned.Call)
		if call.APIName == "edit_file" && !hasFileReceipt {
			results = append(results, fileReadRequiredToolResult(planned.Call))
			continue
		}
		approved := hasApprovedToolInteraction(plan.Interactions, planned.Call.ID)
		if call.Identifier == tools.DefaultIdentifier && call.APIName == "edit_file" && (approved || !decision.Allowed) {
			currentPreview, previewErr := r.previewEditCall(ctx, planned.Call, fileReceipt)
			if previewErr != nil || !currentPreview.Success {
				results = append(results, editPreviewFailureToolResult(planned.Call, currentPreview, previewErr))
				continue
			}
			if approved {
				persistedPreview, ok := approvedDurableEditPreview(plan.Interactions, planned.Call, fileReceipt)
				if !ok || !sameEditPreview(persistedPreview, currentPreview) {
					results = append(results, staleEditPreviewToolResult(planned.Call))
					continue
				}
			}
		}
		if !decision.Allowed && !approved {
			results = append(results, approvalRequiredToolResult(planned.Call, decision))
			continue
		}
		executionContext := r.executionContext(state)
		executionContext.IdempotencyKey = planned.IdempotencyKey
		if call.APIName == "edit_file" {
			executionContext.ExpectedFileRevision = fileReceipt.revision
			executionContext.ExpectedFileContentSHA256 = fileReceipt.contentSHA256
		}
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

func snapshotExecutor(registry tools.Registry, executor tools.Executor) tools.Executor {
	if executor == nil {
		return tools.RegistryExecutor{Registry: registry}
	}
	switch executor.(type) {
	case tools.RegistryExecutor, *tools.RegistryExecutor:
		return tools.RegistryExecutor{Registry: registry}
	default:
		return executor
	}
}

func unsupportedToolError(registry tools.Registry, call tools.Call) *tools.ExecutionError {
	if _, ok := registry.Get(call.Identifier); !ok {
		return &tools.ExecutionError{Type: "unsupported_tool", Message: fmt.Sprintf("unsupported tool %q", call.Identifier)}
	}
	return &tools.ExecutionError{Type: "unsupported_tool_api", Message: fmt.Sprintf("unsupported tool api %q", call.Identifier+"."+call.APIName)}
}

func recoverableToolResult(call coremodel.ToolCall, executionError *tools.ExecutionError) coremodel.ToolResult {
	state, _ := json.Marshal(map[string]any{"status": "failed", "error_type": executionError.Type})
	normalized := tools.NormalizeCall(tools.Call{ID: call.ID, Name: call.Name, Arguments: call.Arguments})
	failed := tools.ExecutionResult{
		ID: normalized.ID, Identifier: normalized.Identifier, APIName: normalized.APIName,
		Content: executionError.Message, State: state, Error: executionError,
	}
	return coremodel.ToolResult{
		CallID: call.ID, Name: call.Name,
		Content: []coremodel.Content{{Type: coremodel.ContentText, Text: tools.ResultMessage(failed)}},
		State:   state, IsError: true, Retryable: true,
	}
}

func approvalRequiredToolResult(call coremodel.ToolCall, decision tools.InterventionDecision) coremodel.ToolResult {
	message := "Tool call requires an approved human interaction under the current permission policy."
	if reason := strings.TrimSpace(decision.Reason); reason != "" {
		message += " Reason: " + reason + "."
	}
	return recoverableToolResult(call, &tools.ExecutionError{Type: "tool_approval_required", Message: message})
}

func hasApprovedToolInteraction(interactions []agentcore.RequiredInteraction, callID string) bool {
	for _, interaction := range interactions {
		if interaction.CallID == callID && interaction.Kind == managedagents.InterventionKindToolApproval &&
			interaction.Decision != nil && interaction.Decision.Status == managedagents.InterventionStatusApproved {
			return true
		}
	}
	return false
}

func sameToolPermissionDecision(left, right *agentcore.ToolPermissionDecision) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func (r ToolRuntime) parkingInteraction(ctx context.Context, state agentcore.State, call tools.Call) (agentcore.RequiredInteraction, bool, error) {
	kind := ""
	var request any
	switch {
	case tools.IsAskUserCall(call):
		parsed, err := tools.ParseAskUserRequest(call.Arguments)
		if err != nil {
			return agentcore.RequiredInteraction{}, true, err
		}
		kind = managedagents.InterventionKindClarification
		request = parsed
	case tools.IsUploadRequestCall(call):
		parsed, err := tools.ParseUploadRequest(call.Arguments)
		if err != nil {
			return agentcore.RequiredInteraction{}, true, err
		}
		kind = managedagents.InterventionKindUploadRequest
		request = parsed
	case tools.IsPlanApprovalCall(call):
		parsed, err := tools.ParsePlanApprovalRequest(call.Arguments)
		if err != nil {
			return agentcore.RequiredInteraction{}, true, err
		}
		if r.ExecutionContext.TaskService == nil {
			return agentcore.RequiredInteraction{}, true, errors.New("request_plan_approval requires task planning in this runtime")
		}
		plan, err := r.ExecutionContext.TaskService.GetPlan(ctx, state.SessionID)
		if err != nil {
			return agentcore.RequiredInteraction{}, true, fmt.Errorf("load active task plan for approval: %w", err)
		}
		if parsed.PlanID != "" && parsed.PlanID != plan.ID {
			return agentcore.RequiredInteraction{}, true, fmt.Errorf("request_plan_approval plan_id %q does not match current active plan %q", parsed.PlanID, plan.ID)
		}
		parsed.PlanID = plan.ID
		kind = managedagents.InterventionKindPlanApproval
		request = tools.PlanApprovalSnapshot{Plan: plan, Summary: parsed.Summary}
	default:
		return agentcore.RequiredInteraction{}, false, nil
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return agentcore.RequiredInteraction{}, true, err
	}
	return agentcore.RequiredInteraction{
		ID: kind + ":" + call.ID, Kind: kind, CallID: call.ID, Request: raw,
	}, true, nil
}

func resolvedParkingInteraction(call tools.Call, interactions []agentcore.RequiredInteraction) (coremodel.ToolResult, bool, error) {
	if !tools.IsParkingInteractionCall(call) {
		return coremodel.ToolResult{}, false, nil
	}
	for _, interaction := range interactions {
		if interaction.CallID != call.ID {
			continue
		}
		if interaction.Decision == nil || interaction.Decision.Status != managedagents.InterventionStatusApproved {
			return coremodel.ToolResult{}, true, errors.New("parking interaction reached execution without an approved decision")
		}
		response := interaction.Decision.Response
		if len(response) == 0 {
			response = json.RawMessage(`{}`)
		}
		state, err := json.Marshal(map[string]any{
			"status": "resolved", "kind": interaction.Kind, "response": json.RawMessage(response), "reason": interaction.Decision.Reason,
		})
		if err != nil {
			return coremodel.ToolResult{}, true, err
		}
		return coremodel.ToolResult{
			CallID: call.ID, Name: call.Name,
			Content: []coremodel.Content{{Type: coremodel.ContentText, Text: "User interaction resolved: " + string(response)}},
			State:   state,
		}, true, nil
	}
	return coremodel.ToolResult{}, true, errors.New("parking interaction is missing its durable decision")
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
	if toolIsReadOnly(api) {
		return "safe"
	}
	return "unknown"
}

func toolExecutionMode(api tools.API) string {
	if value := strings.ToLower(strings.TrimSpace(api.ConcurrencyClass)); value == "parallel" || value == "sequential" {
		return value
	}
	if toolIsReadOnly(api) {
		return "parallel"
	}
	return "sequential"
}

func toolIsReadOnly(api tools.API) bool {
	if risk := strings.ToLower(strings.TrimSpace(api.Risk)); risk != "" {
		return risk == tools.ToolRiskRead
	}
	return toolSideEffect(api) == "none"
}

func toolLockKey(api tools.API, state agentcore.State, call coremodel.ToolCall, executionMode string) string {
	if value := strings.TrimSpace(api.LockKey); value != "" {
		if strings.Contains(value, "{path}") {
			var arguments map[string]json.RawMessage
			var path string
			if json.Unmarshal(call.Arguments, &arguments) == nil {
				_ = json.Unmarshal(arguments["path"], &path)
			}
			path = strings.TrimSpace(path)
			if path == "" {
				return state.SessionID + ":" + call.Name
			}
			value = strings.ReplaceAll(value, "{path}", filepath.ToSlash(filepath.Clean(path)))
		}
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
