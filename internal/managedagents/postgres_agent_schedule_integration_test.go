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

func TestPostgresAgentScheduleExistingSessionFIFOLeaseAndInvalidation(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	workspaceID := createPostgresIntegrationWorkspace(t, store, "agent-schedule-queue")
	scope := AccessScope{WorkspaceID: workspaceID, OwnerID: "schedule-queue-owner"}
	ctx, err := ContextWithDatabaseAccessScope(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := store.CreateAgentContext(ctx, CreateAgentInput{
		WorkspaceID: workspaceID, OwnerType: AgentOwnerUser, OwnerID: scope.OwnerID,
		Visibility: AgentVisibilityPrivate, Name: "Schedule queue agent",
		LLMProvider: "fake", LLMModel: "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := store.CreateEnvironmentContext(ctx, CreateEnvironmentInput{
		WorkspaceID: workspaceID, Name: "Schedule queue environment", Config: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSessionContext(ctx, CreateSessionInput{
		WorkspaceID: workspaceID, OwnerID: scope.OwnerID, AgentID: agent.ID,
		EnvironmentID: environment.ID, Title: "Queue target", CreatedBy: scope.OwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	var overlapSessionID string
	t.Cleanup(func() {
		tx, txErr := store.db.BeginTx(context.Background(), nil)
		if txErr != nil {
			return
		}
		defer tx.Rollback()
		if _, txErr = setDatabaseAccessScope(context.Background(), tx, workspaceID); txErr != nil {
			return
		}
		_, _ = tx.Exec(`DELETE FROM agent_schedules WHERE agent_id=$1`, agent.ID)
		if overlapSessionID != "" {
			_, _ = tx.Exec(`DELETE FROM sessions WHERE id=$1`, overlapSessionID)
		}
		_, _ = tx.Exec(`DELETE FROM sessions WHERE id=$1`, session.ID)
		_, _ = tx.Exec(`DELETE FROM environments WHERE id=$1`, environment.ID)
		_, _ = tx.Exec(`DELETE FROM agents WHERE id=$1`, agent.ID)
		_ = tx.Commit()
	})

	createSchedule := func(name string) AgentSchedule {
		t.Helper()
		item, createErr := store.CreateAgentSchedule(ctx, CreateAgentScheduleInput{
			WorkspaceID: workspaceID, OwnerID: scope.OwnerID, AgentID: agent.ID,
			SessionMode: AgentScheduleSessionExisting, TargetSessionID: session.ID,
			ApprovalMode: AgentScheduleApprovalApproveForMe, Name: name, Prompt: name,
			CronExpression: "0 9 * * *", Timezone: "UTC", CreatedBy: scope.OwnerID,
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return item
	}
	firstSchedule := createSchedule("First bound schedule")
	secondSchedule := createSchedule("Second bound schedule")
	base := time.Now().UTC().Truncate(time.Millisecond)
	firstRun, err := store.StartAgentScheduleNow(ctx, firstSchedule.ID, base)
	if err != nil {
		t.Fatal(err)
	}
	secondRun, err := store.StartAgentScheduleNow(ctx, secondSchedule.ID, base.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, ready, err := store.ClaimAgentScheduleRun(ctx, secondRun.RunID, base.Add(2*time.Second), time.Minute); err != nil || ready {
		t.Fatalf("later run must wait for FIFO predecessor: ready=%v err=%v", ready, err)
	}
	claimedFirst, ready, err := store.ClaimAgentScheduleRun(ctx, firstRun.RunID, base.Add(2*time.Second), time.Minute)
	if err != nil || !ready || claimedFirst.RunID != firstRun.RunID {
		t.Fatalf("claim first run: invocation=%+v ready=%v err=%v", claimedFirst, ready, err)
	}
	if _, ready, err := store.ClaimAgentScheduleRun(ctx, secondRun.RunID, base.Add(3*time.Second), time.Minute); err != nil || ready {
		t.Fatalf("dispatching predecessor must keep later run queued: ready=%v err=%v", ready, err)
	}
	if err := store.CompleteAgentScheduleRun(ctx, CompleteAgentScheduleRunInput{
		RunID: firstRun.RunID, ScheduleID: firstSchedule.ID, SessionID: session.ID, Status: AgentScheduleRunDispatched,
	}); err != nil {
		t.Fatal(err)
	}
	claimedSecond, ready, err := store.ClaimAgentScheduleRun(ctx, secondRun.RunID, base.Add(4*time.Second), time.Second)
	if err != nil || !ready || claimedSecond.RunID != secondRun.RunID {
		t.Fatalf("claim second run after predecessor: invocation=%+v ready=%v err=%v", claimedSecond, ready, err)
	}
	if _, ready, err := store.ClaimAgentScheduleRun(ctx, secondRun.RunID, base.Add(4500*time.Millisecond), time.Second); err != nil || ready {
		t.Fatalf("active lease must prevent duplicate claim: ready=%v err=%v", ready, err)
	}
	if _, ready, err := store.ClaimAgentScheduleRun(ctx, secondRun.RunID, base.Add(6*time.Second), time.Second); err != nil || !ready {
		t.Fatalf("expired lease must be reclaimable: ready=%v err=%v", ready, err)
	}
	if err := store.CompleteAgentScheduleRun(ctx, CompleteAgentScheduleRunInput{
		RunID: secondRun.RunID, ScheduleID: secondSchedule.ID, SessionID: session.ID, Status: AgentScheduleRunDispatched,
	}); err != nil {
		t.Fatal(err)
	}

	newSessionSchedule, err := store.CreateAgentSchedule(ctx, CreateAgentScheduleInput{
		WorkspaceID: workspaceID, OwnerID: scope.OwnerID, AgentID: agent.ID, EnvironmentID: environment.ID,
		SessionMode: AgentScheduleSessionNew, ApprovalMode: AgentScheduleApprovalApproveForMe,
		Name: "Non-overlapping new sessions", Prompt: "Do not overlap", CronExpression: "0 10 * * *", Timezone: "UTC", CreatedBy: scope.OwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	overlapFirst, err := store.StartAgentScheduleNow(ctx, newSessionSchedule.ID, base.Add(8*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, ready, err := store.ClaimAgentScheduleRun(ctx, overlapFirst.RunID, base.Add(8*time.Second), time.Minute); err != nil || !ready {
		t.Fatalf("claim first new-session run: ready=%v err=%v", ready, err)
	}
	overlapSession, err := store.CreateSessionContext(ctx, CreateSessionInput{
		WorkspaceID: workspaceID, OwnerID: scope.OwnerID, AgentID: agent.ID,
		EnvironmentID: environment.ID, Title: "Active scheduled session", CreatedBy: scope.OwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	overlapSessionID = overlapSession.ID
	tx, _, err := store.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE sessions SET status='running' WHERE id=$1`, overlapSession.ID); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteAgentScheduleRun(ctx, CompleteAgentScheduleRunInput{
		RunID: overlapFirst.RunID, ScheduleID: newSessionSchedule.ID, SessionID: overlapSession.ID, Status: AgentScheduleRunDispatched,
	}); err != nil {
		t.Fatal(err)
	}
	overlapSecond, err := store.StartAgentScheduleNow(ctx, newSessionSchedule.ID, base.Add(9*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, ready, err := store.ClaimAgentScheduleRun(ctx, overlapSecond.RunID, base.Add(9*time.Second), time.Minute); err != nil || ready {
		t.Fatalf("new-session schedule must not overlap active predecessor: ready=%v err=%v", ready, err)
	}
	tx, _, err = store.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE sessions SET status='idle' WHERE id=$1`, overlapSession.ID); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, ready, err := store.ClaimAgentScheduleRun(ctx, overlapSecond.RunID, base.Add(10*time.Second), time.Minute); err != nil || !ready {
		t.Fatalf("new-session successor should run after predecessor is idle: ready=%v err=%v", ready, err)
	}
	invalidRun, err := store.StartAgentScheduleNow(ctx, secondSchedule.ID, base.Add(11*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, ready, err := store.ClaimAgentScheduleRun(ctx, invalidRun.RunID, base.Add(11*time.Second), time.Minute); err != nil || !ready {
		t.Fatalf("claim run before target invalidation: ready=%v err=%v", ready, err)
	}

	tx, _, err = store.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE sessions SET archived_at=now() WHERE id=$1`, session.ID); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if count, err := store.ReconcileInvalidAgentScheduleRuns(t.Context(), base.Add(7*time.Second)); err != nil || count != 2 {
		t.Fatalf("reconcile invalid target: count=%d err=%v", count, err)
	}
	for _, scheduleID := range []string{firstSchedule.ID, secondSchedule.ID} {
		item, getErr := store.GetAgentSchedule(ctx, scheduleID)
		if getErr != nil || item.Enabled || item.LastRunStatus != AgentScheduleRunFailed {
			t.Fatalf("invalid target schedule must pause: item=%+v err=%v", item, getErr)
		}
	}
	var runStatus string
	tx, _, err = store.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(`SELECT status FROM agent_schedule_runs WHERE id=$1`, invalidRun.RunID).Scan(&runStatus); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if runStatus != AgentScheduleRunFailed {
		t.Fatalf("queued run must fail after target invalidation, got %s", runStatus)
	}
}
