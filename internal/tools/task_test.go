package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
)

type recordingTaskToolService struct {
	createdSessionID string
	createdInput     managedagents.CreateSessionTaskPlanInput
	plan             managedagents.SessionTaskPlan
}

func (s *recordingTaskToolService) CreatePlan(_ context.Context, sessionID string, input managedagents.CreateSessionTaskPlanInput) (managedagents.SessionTaskPlanResult, error) {
	s.createdSessionID = sessionID
	s.createdInput = input
	return managedagents.SessionTaskPlanResult{Plan: s.plan}, nil
}

func (s *recordingTaskToolService) GetPlan(context.Context, string) (managedagents.SessionTaskPlan, error) {
	return s.plan, nil
}

func (s *recordingTaskToolService) UpdateItems(context.Context, string, managedagents.UpdateSessionTaskItemsInput) (managedagents.SessionTaskPlanResult, error) {
	return managedagents.SessionTaskPlanResult{Plan: s.plan}, nil
}

func (s *recordingTaskToolService) CompletePlan(context.Context, string, managedagents.FinishSessionTaskPlanInput) (managedagents.SessionTaskPlanResult, error) {
	return managedagents.SessionTaskPlanResult{Plan: s.plan}, nil
}

func (s *recordingTaskToolService) CancelPlan(context.Context, string, managedagents.FinishSessionTaskPlanInput) (managedagents.SessionTaskPlanResult, error) {
	return managedagents.SessionTaskPlanResult{Plan: s.plan}, nil
}

func TestTaskRuntimeManifestDefinesComplexityAndEvidenceRules(t *testing.T) {
	manifest := (TaskRuntime{}).Manifest()
	for _, expected := range []string{"one or two tool calls", "3-4 related steps", "5 or more dependent steps", "at most one in_progress", "completed items require"} {
		if !strings.Contains(manifest.SystemRole, expected) {
			t.Fatalf("task system role is missing %q: %s", expected, manifest.SystemRole)
		}
	}
	if len(manifest.API) != 5 {
		t.Fatalf("expected five task APIs, got %+v", manifest.API)
	}
}

func TestTaskRuntimeCreatePlanUsesCurrentSessionAndTurn(t *testing.T) {
	now := time.Now().UTC()
	service := &recordingTaskToolService{plan: managedagents.SessionTaskPlan{
		ID: "plan_000001", SessionID: "sesn_000001", Goal: "Verify task tools", HandlingMode: managedagents.TaskPlanModeTracked,
		Status: managedagents.TaskPlanStatusActive, CreatedAt: now, UpdatedAt: now,
		Items: []managedagents.SessionTaskItem{{ID: "task_000001"}, {ID: "task_000002"}, {ID: "task_000003"}},
	}}
	result, err := (TaskRuntime{}).Execute(context.Background(), Call{
		ID: "call_plan", Identifier: TaskIdentifier, APIName: TaskAPICreatePlan,
		Arguments: json.RawMessage(`{"goal":"Verify task tools","items":["Create","Run","Check"]}`),
	}, ExecutionContext{SessionID: "sesn_000001", TurnID: "turn_000001", TaskService: service})
	if err != nil {
		t.Fatalf("execute create_plan: %v", err)
	}
	if result.Error != nil || service.createdSessionID != "sesn_000001" || service.createdInput.TurnID != "turn_000001" {
		t.Fatalf("unexpected create_plan execution: result=%+v service=%+v", result, service)
	}
	if !json.Valid(result.State) || !strings.Contains(result.Content, "plan_000001") {
		t.Fatalf("expected structured plan result, got %+v", result)
	}
}

func TestTaskRuntimeHidesMissingServiceAsToolError(t *testing.T) {
	result, err := (TaskRuntime{}).Execute(context.Background(), Call{ID: "call_plan", Identifier: TaskIdentifier, APIName: TaskAPIGetPlan}, ExecutionContext{SessionID: "sesn_000001"})
	if err != nil {
		t.Fatalf("execute get_plan without service: %v", err)
	}
	if result.Error == nil || result.Error.Type != "task_service_unavailable" {
		t.Fatalf("expected task service error, got %+v", result)
	}
}

func TestIsTaskCallNormalizesQualifiedName(t *testing.T) {
	if !IsTaskCall(Call{Name: "task.update_items"}) || IsTaskCall(Call{Name: "default.edit_file"}) {
		t.Fatal("expected only task.* calls to use the internal state path")
	}
}
