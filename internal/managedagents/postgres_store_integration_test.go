package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgresStoreCompletesSessionTurn(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)

	startEvents, err := store.AppendEvents(session.ID, []AppendEventInput{{
		Type:    EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"complete me"}]}`),
	}})
	if err != nil {
		t.Fatalf("append user.message: %v", err)
	}
	turnID := payloadString(startEvents[1].Payload, "turn_id")
	if turnID == "" {
		t.Fatal("expected user.message to include turn_id")
	}

	completedEvents, err := store.CompleteSessionTurn(session.ID, turnID, json.RawMessage(`{"content":[{"type":"text","text":"done"}]}`))
	if err != nil {
		t.Fatalf("complete turn: %v", err)
	}
	if len(completedEvents) != 2 {
		t.Fatalf("expected 2 completion events, got %d", len(completedEvents))
	}
	if completedEvents[0].Type != EventAgentMessage {
		t.Fatalf("expected first completion event %q, got %q", EventAgentMessage, completedEvents[0].Type)
	}
	if completedEvents[1].Type != EventSessionStatusIdle {
		t.Fatalf("expected second completion event %q, got %q", EventSessionStatusIdle, completedEvents[1].Type)
	}
	if got := payloadString(completedEvents[0].Payload, "turn_id"); got != turnID {
		t.Fatalf("expected agent.message turn_id %q, got %q", turnID, got)
	}

	assertPostgresSessionStatus(t, store, session.ID, SessionStatusIdle)
	status, errorMessage := postgresTurnState(t, store, session.ID, turnID)
	if status != "completed" {
		t.Fatalf("expected turn status completed, got %q", status)
	}
	if errorMessage != "" {
		t.Fatalf("expected empty error_message, got %q", errorMessage)
	}
}

func TestPostgresStoreAppendsRuntimeEventForCurrentTurn(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)

	startEvents, err := store.AppendEvents(session.ID, []AppendEventInput{{
		Type:    EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"runtime step"}]}`),
	}})
	if err != nil {
		t.Fatalf("append user.message: %v", err)
	}
	turnID := payloadString(startEvents[1].Payload, "turn_id")

	runtimeEvents, err := store.AppendRuntimeEvent(session.ID, turnID, AppendEventInput{
		Type:    EventRuntimeStarted,
		Payload: json.RawMessage(`{"message":"runtime started"}`),
	})
	if err != nil {
		t.Fatalf("append runtime event: %v", err)
	}
	if len(runtimeEvents) != 1 {
		t.Fatalf("expected 1 runtime event, got %d", len(runtimeEvents))
	}
	if runtimeEvents[0].Type != EventRuntimeStarted {
		t.Fatalf("expected runtime event %q, got %q", EventRuntimeStarted, runtimeEvents[0].Type)
	}
	if got := payloadString(runtimeEvents[0].Payload, "turn_id"); got != turnID {
		t.Fatalf("expected runtime event turn_id %q, got %q", turnID, got)
	}

	if _, err := store.CompleteSessionTurn(session.ID, turnID, json.RawMessage(`{"content":[{"type":"text","text":"done"}]}`)); err != nil {
		t.Fatalf("complete turn: %v", err)
	}
	lateEvents, err := store.AppendRuntimeEvent(session.ID, turnID, AppendEventInput{
		Type:    EventRuntimeThinking,
		Payload: json.RawMessage(`{"message":"too late"}`),
	})
	if err != nil {
		t.Fatalf("append late runtime event: %v", err)
	}
	if len(lateEvents) != 0 {
		t.Fatalf("expected late runtime event to append no events, got %d", len(lateEvents))
	}
}

func TestPostgresStoreInterruptedTurnSkipsLateCompletion(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)

	startEvents, err := store.AppendEvents(session.ID, []AppendEventInput{{
		Type:    EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"interrupt me"}]}`),
	}})
	if err != nil {
		t.Fatalf("append user.message: %v", err)
	}
	turnID := payloadString(startEvents[1].Payload, "turn_id")

	interruptEvents, err := store.AppendEvents(session.ID, []AppendEventInput{{Type: EventUserInterrupt}})
	if err != nil {
		t.Fatalf("append user.interrupt: %v", err)
	}
	if len(interruptEvents) != 3 {
		t.Fatalf("expected 3 interrupt events, got %d", len(interruptEvents))
	}

	lateEvents, err := store.CompleteSessionTurn(session.ID, turnID, json.RawMessage(`{"content":[{"type":"text","text":"too late"}]}`))
	if err != nil {
		t.Fatalf("late complete turn: %v", err)
	}
	if len(lateEvents) != 0 {
		t.Fatalf("expected late completion to append no events, got %d", len(lateEvents))
	}

	assertPostgresSessionStatus(t, store, session.ID, SessionStatusIdle)
	status, _ := postgresTurnState(t, store, session.ID, turnID)
	if status != "interrupted" {
		t.Fatalf("expected turn status interrupted, got %q", status)
	}
	assertNoPostgresAgentMessageForTurn(t, store, session.ID, turnID)
}

func TestPostgresStoreFailedTurnReturnsSessionToIdle(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)

	startEvents, err := store.AppendEvents(session.ID, []AppendEventInput{{
		Type:    EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"fail me"}]}`),
	}})
	if err != nil {
		t.Fatalf("append user.message: %v", err)
	}
	turnID := payloadString(startEvents[1].Payload, "turn_id")

	failedEvents, err := store.FailSessionTurn(session.ID, turnID, "command turn failed")
	if err != nil {
		t.Fatalf("fail turn: %v", err)
	}
	if len(failedEvents) != 1 {
		t.Fatalf("expected 1 failure event, got %d", len(failedEvents))
	}
	if failedEvents[0].Type != EventSessionStatusIdle {
		t.Fatalf("expected failure to append %q, got %q", EventSessionStatusIdle, failedEvents[0].Type)
	}
	if got := payloadString(failedEvents[0].Payload, "last_turn_status"); got != "failed" {
		t.Fatalf("expected last_turn_status failed, got %q", got)
	}
	if got := payloadString(failedEvents[0].Payload, "reason"); got != "command turn failed" {
		t.Fatalf("expected failure reason, got %q", got)
	}

	assertPostgresSessionStatus(t, store, session.ID, SessionStatusIdle)
	status, errorMessage := postgresTurnState(t, store, session.ID, turnID)
	if status != "failed" {
		t.Fatalf("expected turn status failed, got %q", status)
	}
	if !strings.Contains(errorMessage, "command turn failed") {
		t.Fatalf("expected error_message to contain failure reason, got %q", errorMessage)
	}

	retryEvents, err := store.AppendEvents(session.ID, []AppendEventInput{{
		Type:    EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"retry"}]}`),
	}})
	if err != nil {
		t.Fatalf("append retry user.message after failed turn: %v", err)
	}
	if len(retryEvents) != 2 {
		t.Fatalf("expected retry to append 2 events, got %d", len(retryEvents))
	}
}

func TestPostgresStoreObjectRefsAndSessionArtifacts(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)

	object, err := store.CreateObjectRef(CreateObjectRefInput{
		WorkspaceID:    session.WorkspaceID,
		Bucket:         "tma-integration",
		ObjectKey:      "integration/" + session.ID + "/artifact.txt",
		ContentType:    "text/plain",
		SizeBytes:      12,
		ChecksumSHA256: "abc123",
		Metadata:       json.RawMessage(`{"source":"integration"}`),
		CreatedBy:      "integration-test",
	})
	if err != nil {
		t.Fatalf("create object ref: %v", err)
	}
	t.Cleanup(func() {
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM session_artifacts WHERE object_ref_id = $1`, object.ID); err != nil {
			t.Fatalf("cleanup session artifacts for object %s: %v", object.ID, err)
		}
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM object_refs WHERE id = $1`, object.ID); err != nil {
			t.Fatalf("cleanup object ref %s: %v", object.ID, err)
		}
	})

	fetched, err := store.GetObjectRef(object.ID)
	if err != nil {
		t.Fatalf("get object ref: %v", err)
	}
	if fetched.Bucket != object.Bucket || fetched.ObjectKey != object.ObjectKey || fetched.Visibility != ObjectVisibilityWorkspace {
		t.Fatalf("unexpected object ref: %+v", fetched)
	}

	artifact, err := store.CreateSessionArtifact(CreateSessionArtifactInput{
		SessionID:    session.ID,
		ObjectRefID:  object.ID,
		TurnID:       "turn_000001",
		ToolCallID:   "call_write",
		Name:         "artifact.txt",
		ArtifactType: ArtifactTypeFile,
		Metadata:     json.RawMessage(`{"preview":"hello"}`),
		CreatedBy:    "integration-test",
	})
	if err != nil {
		t.Fatalf("create session artifact: %v", err)
	}
	if artifact.WorkspaceID != session.WorkspaceID || artifact.EnvironmentID != session.EnvironmentID {
		t.Fatalf("unexpected artifact: %+v", artifact)
	}

	artifacts, err := store.ListSessionArtifacts(session.ID)
	if err != nil {
		t.Fatalf("list session artifacts: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].ID != artifact.ID || artifacts[0].ObjectRefID != object.ID {
		t.Fatalf("unexpected artifacts: %+v", artifacts)
	}
}

func TestPostgresStoreReapsExpiredWorkerWork(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	worker, err := store.RegisterWorker(RegisterWorkerInput{
		Name:         "integration-worker-" + time.Now().UTC().Format("20060102150405.000000000"),
		WorkerType:   WorkerTypeLocal,
		RegisteredBy: "integration-test",
		LeaseSeconds: 30,
	})
	if err != nil {
		t.Fatalf("register worker: %v", err)
	}
	t.Cleanup(func() {
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM worker_work WHERE worker_id = $1`, worker.ID); err != nil {
			t.Fatalf("cleanup worker work for %s: %v", worker.ID, err)
		}
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM workers WHERE id = $1`, worker.ID); err != nil {
			t.Fatalf("cleanup worker %s: %v", worker.ID, err)
		}
	})

	queued, err := store.EnqueueWorkerWork(EnqueueWorkerWorkInput{
		WorkspaceID: DefaultWorkspaceID,
		WorkerID:    worker.ID,
		WorkType:    WorkerWorkTypeSandboxCommand,
		Payload:     json.RawMessage(`{"command":"sh","args":["-c","sleep 100"]}`),
	})
	if err != nil {
		t.Fatalf("enqueue worker work: %v", err)
	}
	polled, err := store.PollWorkerWork(worker.ID, PollWorkerWorkInput{LeaseSeconds: 1})
	if err != nil {
		t.Fatalf("poll worker work: %v", err)
	}
	if polled == nil || polled.ID != queued.ID || polled.Status != WorkerWorkStatusLeased {
		t.Fatalf("expected leased work, got %+v", polled)
	}

	expiredAt := time.Unix(0, 0).UTC()
	if _, err := store.db.ExecContext(context.Background(), `UPDATE worker_work SET lease_expires_at = $1 WHERE id = $2`, expiredAt, queued.ID); err != nil {
		t.Fatalf("force expired lease: %v", err)
	}
	expired, err := store.ReapExpiredWorkerWork(ReapExpiredWorkerWorkInput{Limit: 1})
	if err != nil {
		t.Fatalf("reap expired worker work: %v", err)
	}
	if len(expired) != 1 || expired[0].ID != queued.ID {
		t.Fatalf("expected only test work to expire, got %+v", expired)
	}
	if expired[0].Status != WorkerWorkStatusFailed || expired[0].CompletedAt == nil || !strings.Contains(expired[0].ErrorMessage, "worker work lease expired") {
		t.Fatalf("unexpected expired work: %+v", expired[0])
	}
}

func newPostgresIntegrationStore(t *testing.T) *PostgresStore {
	t.Helper()

	if os.Getenv("TMA_RUN_POSTGRES_TESTS") != "1" {
		t.Skip("set TMA_RUN_POSTGRES_TESTS=1 to run Postgres integration tests")
	}
	databaseURL := os.Getenv("TMA_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TMA_DATABASE_URL to run Postgres integration tests")
	}

	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatalf("open postgres store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close postgres store: %v", err)
		}
	})

	requirePostgresIntegrationSchema(t, store)
	return store
}

func requirePostgresIntegrationSchema(t *testing.T, store *PostgresStore) {
	t.Helper()

	var table sql.NullString
	if err := store.db.QueryRowContext(context.Background(), `SELECT to_regclass('public.session_turns')`).Scan(&table); err != nil {
		t.Fatalf("check postgres schema: %v", err)
	}
	if !table.Valid || table.String == "" {
		t.Fatal("session_turns table missing; run make migrate-up before integration tests")
	}
}

func createPostgresIntegrationSession(t *testing.T, store *PostgresStore) Session {
	t.Helper()

	suffix := time.Now().UTC().Format("20060102150405.000000000")
	agent, err := store.CreateAgent(CreateAgentInput{
		Name:   "integration-agent-" + suffix,
		Model:  "test-model",
		System: "integration test",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	environment, err := store.CreateEnvironment(CreateEnvironmentInput{
		Name:   "integration-env-" + suffix,
		Config: json.RawMessage(`{"type":"integration"}`),
	})
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}

	session, err := store.CreateSession(CreateSessionInput{
		AgentID:       agent.ID,
		EnvironmentID: environment.ID,
		Title:         "Postgres integration " + suffix,
		CreatedBy:     "integration-test",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	t.Cleanup(func() {
		cleanupPostgresIntegrationData(t, store, session.ID, agent.ID, environment.ID)
	})
	return session
}

func cleanupPostgresIntegrationData(t *testing.T, store *PostgresStore, sessionID string, agentID string, environmentID string) {
	t.Helper()

	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = $1`, sessionID); err != nil {
		t.Fatalf("cleanup session %s: %v", sessionID, err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM environments WHERE id = $1`, environmentID); err != nil {
		t.Fatalf("cleanup environment %s: %v", environmentID, err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM agents WHERE id = $1`, agentID); err != nil {
		t.Fatalf("cleanup agent %s: %v", agentID, err)
	}
}

func assertPostgresSessionStatus(t *testing.T, store *PostgresStore, sessionID string, expected string) {
	t.Helper()

	session, err := store.GetSession(sessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Status != expected {
		t.Fatalf("expected session status %q, got %q", expected, session.Status)
	}
}

func postgresTurnState(t *testing.T, store *PostgresStore, sessionID string, turnID string) (string, string) {
	t.Helper()

	var status string
	var errorMessage sql.NullString
	err := store.db.QueryRowContext(context.Background(), `
		SELECT status, error_message
		FROM session_turns
		WHERE session_id = $1 AND id = $2
	`, sessionID, turnID).Scan(&status, &errorMessage)
	if err != nil {
		t.Fatalf("query turn state: %v", err)
	}
	return status, errorMessage.String
}

func assertNoPostgresAgentMessageForTurn(t *testing.T, store *PostgresStore, sessionID string, turnID string) {
	t.Helper()

	events, err := store.ListEvents(sessionID, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	for _, event := range events {
		if event.Type == EventAgentMessage && payloadString(event.Payload, "turn_id") == turnID {
			t.Fatalf("did not expect agent.message for interrupted turn %s", turnID)
		}
	}
}
