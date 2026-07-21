package managedagents

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestPostgresAgentScheduleCRUDAndClaim(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	workspaceID := createPostgresIntegrationWorkspace(t, store, "agent-schedule")
	scope := AccessScope{WorkspaceID: workspaceID, OwnerID: "schedule-owner"}
	ctx, err := ContextWithDatabaseAccessScope(t.Context(), scope)
	if err != nil {
		t.Fatalf("build scoped context: %v", err)
	}
	agent, err := store.CreateAgentContext(ctx, CreateAgentInput{
		WorkspaceID: workspaceID, OwnerType: AgentOwnerUser, OwnerID: scope.OwnerID,
		Visibility: AgentVisibilityPrivate, Name: "Schedule integration agent",
		LLMProvider: "fake", LLMModel: "test-model",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	environment, err := store.CreateEnvironmentContext(ctx, CreateEnvironmentInput{
		WorkspaceID: workspaceID, Name: "Schedule integration environment", Config: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	t.Cleanup(func() {
		tx, txErr := store.db.BeginTx(context.Background(), nil)
		if txErr != nil {
			t.Errorf("begin cleanup: %v", txErr)
			return
		}
		defer tx.Rollback()
		if _, txErr = setDatabaseAccessScope(context.Background(), tx, workspaceID); txErr != nil {
			t.Errorf("scope cleanup: %v", txErr)
			return
		}
		if _, txErr = tx.Exec(`DELETE FROM agent_schedules WHERE agent_id=$1`, agent.ID); txErr != nil {
			t.Errorf("delete schedules: %v", txErr)
			return
		}
		if _, txErr = tx.Exec(`DELETE FROM environments WHERE id=$1`, environment.ID); txErr != nil {
			t.Errorf("delete environment: %v", txErr)
			return
		}
		if _, txErr = tx.Exec(`DELETE FROM agents WHERE id=$1`, agent.ID); txErr != nil {
			t.Errorf("delete agent: %v", txErr)
			return
		}
		if txErr = tx.Commit(); txErr != nil {
			t.Errorf("commit cleanup: %v", txErr)
		}
	})

	created, err := store.CreateAgentSchedule(ctx, CreateAgentScheduleInput{
		WorkspaceID: workspaceID, OwnerID: scope.OwnerID, AgentID: agent.ID, EnvironmentID: environment.ID,
		Name: "Integration schedule", Prompt: "Run integration task", CronExpression: "*/5 * * * *",
		Timezone: "Asia/Shanghai", CreatedBy: scope.OwnerID,
	})
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	if created.NextRunAt == nil {
		t.Fatal("expected next run")
	}
	items, err := store.ListAgentSchedules(ctx, agent.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("list schedules: items=%+v err=%v", items, err)
	}

	tx, _, err := store.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		t.Fatalf("scope due update: %v", err)
	}
	if _, err = tx.Exec(`UPDATE agent_schedules SET next_run_at=$2 WHERE id=$1`, created.ID, time.Now().UTC().Add(-time.Minute)); err != nil {
		tx.Rollback()
		t.Fatalf("make schedule due: %v", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatalf("commit due update: %v", err)
	}
	claimed, err := store.ClaimDueAgentSchedules(t.Context(), time.Now().UTC(), 10)
	if err != nil {
		t.Fatalf("claim schedules: %v", err)
	}
	if len(claimed) != 1 || claimed[0].Schedule.ID != created.ID {
		t.Fatalf("unexpected claimed schedules: %+v", claimed)
	}
	if err := store.CompleteAgentScheduleRun(ctx, CompleteAgentScheduleRunInput{
		RunID: claimed[0].RunID, ScheduleID: created.ID, Status: AgentScheduleRunDispatched,
	}); err != nil {
		t.Fatalf("complete schedule run: %v", err)
	}
	updated, err := store.GetAgentSchedule(ctx, created.ID)
	if err != nil || updated.LastRunStatus != AgentScheduleRunDispatched {
		t.Fatalf("unexpected updated schedule: %+v err=%v", updated, err)
	}
}
