package tools

import (
	"context"
	"encoding/json"
	"testing"

	"tiggy-manage-agent/internal/managedagents"
)

type stubAgentToolService struct {
	spawnResponse AgentSpawnResponse
	waitResponse  AgentWaitResponse
	spawnError    error
	groupResponse AgentTaskGroupCreateResponse
	groupRequests *[]AgentTaskGroupCreateRequest
}

func (s stubAgentToolService) Spawn(context.Context, AgentSpawnRequest) (AgentSpawnResponse, error) {
	if s.spawnError != nil {
		return AgentSpawnResponse{}, s.spawnError
	}
	return s.spawnResponse, nil
}

func (s stubAgentToolService) SendMessage(context.Context, AgentSendMessageRequest) (AgentSendMessageResponse, error) {
	return AgentSendMessageResponse{}, nil
}

func (s stubAgentToolService) GetSession(context.Context, AgentSessionRequest) (AgentSessionResponse, error) {
	return AgentSessionResponse{}, nil
}

func (s stubAgentToolService) Wait(context.Context, AgentWaitRequest) (AgentWaitResponse, error) {
	return s.waitResponse, nil
}

func (s stubAgentToolService) CollectResult(context.Context, AgentCollectResultRequest) (AgentCollectResultResponse, error) {
	return AgentCollectResultResponse{}, nil
}

func (s stubAgentToolService) ListEvents(context.Context, AgentListEventsRequest) (AgentListEventsResponse, error) {
	return AgentListEventsResponse{}, nil
}

func (s stubAgentToolService) StreamEvents(context.Context, AgentStreamEventsRequest) (AgentStreamEventsResponse, error) {
	return AgentStreamEventsResponse{}, nil
}

func (s stubAgentToolService) ApproveTool(context.Context, AgentInterventionDecisionRequest) (AgentInterventionDecisionResponse, error) {
	return AgentInterventionDecisionResponse{}, nil
}

func (s stubAgentToolService) RejectTool(context.Context, AgentInterventionDecisionRequest) (AgentInterventionDecisionResponse, error) {
	return AgentInterventionDecisionResponse{}, nil
}

func (s stubAgentToolService) ArchiveSession(context.Context, AgentArchiveSessionRequest) (AgentArchiveSessionResponse, error) {
	return AgentArchiveSessionResponse{}, nil
}

func (s stubAgentToolService) CancelStart(context.Context, AgentCancelStartRequest) (AgentCancelStartResponse, error) {
	return AgentCancelStartResponse{}, nil
}

func (s stubAgentToolService) CreateTaskGroup(_ context.Context, request AgentTaskGroupCreateRequest) (AgentTaskGroupCreateResponse, error) {
	if s.groupRequests != nil {
		*s.groupRequests = append(*s.groupRequests, request)
	}
	return s.groupResponse, nil
}

func (s stubAgentToolService) GetTaskGroup(context.Context, AgentTaskGroupRequest) (AgentTaskGroupResponse, error) {
	return AgentTaskGroupResponse{}, nil
}

func (s stubAgentToolService) WaitTaskGroup(context.Context, AgentTaskGroupWaitRequest) (AgentTaskGroupWaitResponse, error) {
	return AgentTaskGroupWaitResponse{}, nil
}

func (s stubAgentToolService) CollectTaskGroup(context.Context, AgentTaskGroupCollectRequest) (AgentTaskGroupCollectResponse, error) {
	return AgentTaskGroupCollectResponse{}, nil
}

func (s stubAgentToolService) CancelTaskGroup(context.Context, AgentTaskGroupCancelRequest) (AgentTaskGroupCancelResponse, error) {
	return AgentTaskGroupCancelResponse{}, nil
}

func (s stubAgentToolService) RetryTaskGroupItem(context.Context, AgentTaskGroupRetryItemRequest) (AgentTaskGroupRetryResponse, error) {
	return AgentTaskGroupRetryResponse{}, nil
}

func (s stubAgentToolService) RetryTaskGroup(context.Context, AgentTaskGroupRetryRequest) (AgentTaskGroupRetryResponse, error) {
	return AgentTaskGroupRetryResponse{}, nil
}

func TestAgentRuntimeExecuteSpawn(t *testing.T) {
	runtime := AgentRuntime{
		Service: stubAgentToolService{
			spawnResponse: AgentSpawnResponse{
				Session: managedagents.Session{ID: "sesn_child", Status: managedagents.SessionStatusRunning},
				Started: true,
			},
		},
	}

	result, err := runtime.Execute(t.Context(), Call{
		ID:         "call_spawn",
		Identifier: AgentIdentifier,
		APIName:    "spawn",
		Arguments:  json.RawMessage(`{"message":"research auth flow"}`),
	}, ExecutionContext{
		SessionID: "sesn_parent",
		TurnID:    "turn_000001",
	})
	if err != nil {
		t.Fatalf("execute spawn: %v", err)
	}
	if result.Identifier != AgentIdentifier || result.APIName != "spawn" {
		t.Fatalf("unexpected result metadata: %#v", result)
	}
	var state AgentSpawnResponse
	if err := json.Unmarshal(result.State, &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if state.Session.ID != "sesn_child" || !state.Started {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestAgentRuntimeExecuteWait(t *testing.T) {
	runtime := AgentRuntime{
		Service: stubAgentToolService{
			waitResponse: AgentWaitResponse{
				Session:        managedagents.Session{ID: "sesn_child", Status: managedagents.SessionStatusIdle},
				Status:         managedagents.SessionStatusIdle,
				LastTurnStatus: managedagents.TurnStatusCompleted,
			},
		},
	}

	result, err := runtime.Execute(t.Context(), Call{
		ID:         "call_wait",
		Identifier: AgentIdentifier,
		APIName:    "wait",
		Arguments:  json.RawMessage(`{"session_id":"sesn_child"}`),
	}, ExecutionContext{
		SessionID: "sesn_parent",
		TurnID:    "turn_000001",
	})
	if err != nil {
		t.Fatalf("execute wait: %v", err)
	}
	if result.APIName != "wait" || result.Content == "" {
		t.Fatalf("unexpected wait result: %#v", result)
	}
}

func TestAgentRuntimeExecuteSpawnReturnsStructuredQuotaError(t *testing.T) {
	runtime := AgentRuntime{
		Service: stubAgentToolService{
			spawnError: AgentToolError{
				Type:    "subagent_workspace_active_limit",
				Message: "workspace subagent active limit reached",
				State: map[string]any{
					"category":       "quota",
					"scope":          "workspace",
					"policy":         "workspace_active_limit",
					"current_active": 12,
					"limit":          10,
				},
			},
		},
	}

	result, err := runtime.Execute(t.Context(), Call{
		ID:         "call_spawn",
		Identifier: AgentIdentifier,
		APIName:    "spawn",
		Arguments:  json.RawMessage(`{"message":"spawn child"}`),
	}, ExecutionContext{
		SessionID: "sesn_parent",
		TurnID:    "turn_000001",
	})
	if err != nil {
		t.Fatalf("execute spawn: %v", err)
	}
	if result.Error == nil || result.Error.Type != "subagent_workspace_active_limit" {
		t.Fatalf("expected structured quota error, got %#v", result)
	}
	var state map[string]any
	if err := json.Unmarshal(result.State, &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if state["category"] != "quota" || state["policy"] != "workspace_active_limit" {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestAgentRuntimeExecuteRunGroup(t *testing.T) {
	var requests []AgentTaskGroupCreateRequest
	runtime := AgentRuntime{
		Service: stubAgentToolService{
			groupResponse: AgentTaskGroupCreateResponse{
				Group:  managedagents.SubagentTaskGroup{ID: "sgrp_000001", Strategy: managedagents.SubagentTaskGroupStrategyAllCompleted},
				Status: "running",
				Items: []AgentTaskGroupItemState{
					{Status: managedagents.SessionStatusRunning},
				},
			},
			groupRequests: &requests,
		},
	}

	result, err := runtime.Execute(t.Context(), Call{
		ID:         "call_group",
		Identifier: AgentIdentifier,
		APIName:    "run_group",
		Arguments:  json.RawMessage(`{"template_id":"module_risk_audit","items":[{"message":"inspect auth flow"}]}`),
	}, ExecutionContext{
		SessionID: "sesn_parent",
		TurnID:    "turn_000001",
	})
	if err != nil {
		t.Fatalf("execute run_group: %v", err)
	}
	if result.APIName != "run_group" || result.Content == "" {
		t.Fatalf("unexpected run_group result: %#v", result)
	}
	if len(requests) != 1 || requests[0].TemplateID != "module_risk_audit" || requests[0].ParentSessionID != "sesn_parent" || requests[0].ParentTurnID != "turn_000001" {
		t.Fatalf("expected template-aware group request, got %#v", requests)
	}
}

func TestAgentRuntimeExecuteListGroupTemplates(t *testing.T) {
	runtime := AgentRuntime{}

	result, err := runtime.Execute(t.Context(), Call{
		ID:         "call_group_templates",
		Identifier: AgentIdentifier,
		APIName:    "list_group_templates",
		Arguments:  json.RawMessage(`{}`),
	}, ExecutionContext{
		SessionID: "sesn_parent",
		TurnID:    "turn_000001",
	})
	if err != nil {
		t.Fatalf("execute list_group_templates: %v", err)
	}
	var state AgentTaskGroupTemplateListResponse
	if err := json.Unmarshal(result.State, &state); err != nil {
		t.Fatalf("decode template list state: %v", err)
	}
	if len(state.Templates) == 0 || state.Templates[0].ID == "" {
		t.Fatalf("expected builtin templates, got %#v", state)
	}
}
