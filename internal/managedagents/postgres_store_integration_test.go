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

func TestPostgresStoreStreamsCrossInstanceBurstWithoutLoss(t *testing.T) {
	storeA := newPostgresIntegrationStore(t)
	storeB := newPostgresIntegrationStore(t)
	session := createPostgresIntegrationSession(t, storeA)

	history, err := storeA.ListEvents(session.ID, 0)
	if err != nil {
		t.Fatalf("list initial events: %v", err)
	}
	afterSeq := history[len(history)-1].Seq
	gapEvents, err := storeB.AppendEvents(session.ID, []AppendEventInput{{
		Type:    EventRuntimeThinking,
		Payload: json.RawMessage(`{"message":"committed before subscribe"}`),
	}})
	if err != nil {
		t.Fatalf("append event between snapshot and subscribe: %v", err)
	}
	stream, cancel, err := storeA.SubscribeEvents(session.ID, afterSeq)
	if err != nil {
		t.Fatalf("subscribe from instance A: %v", err)
	}
	defer cancel()

	inputs := make([]AppendEventInput, 64)
	for index := range inputs {
		inputs[index] = AppendEventInput{
			Type:    EventRuntimeThinking,
			Payload: json.RawMessage(`{"message":"burst"}`),
		}
	}
	appended, err := storeB.AppendEvents(session.ID, inputs)
	if err != nil {
		t.Fatalf("append burst from instance B: %v", err)
	}
	expected := append(gapEvents, appended...)

	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for index, want := range expected {
		select {
		case got, ok := <-stream:
			if !ok {
				t.Fatalf("stream closed after %d of %d events", index, len(expected))
			}
			if got.ID != want.ID || got.Seq != want.Seq {
				t.Fatalf("event %d mismatch: got %+v, want %+v", index, got, want)
			}
		case <-timeout.C:
			t.Fatalf("timed out after receiving %d of %d cross-instance events", index, len(expected))
		}
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

func TestPostgresStoreSessionTurnLeaseRecoveryAndRemoteInterrupt(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)

	events, err := store.AppendEvents(session.ID, []AppendEventInput{{
		Type:    EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"lease me"}]}`),
	}})
	if err != nil {
		t.Fatalf("append user message: %v", err)
	}
	turnID := payloadString(events[len(events)-1].Payload, "turn_id")

	first, err := store.ClaimSessionTurns(ClaimSessionTurnsInput{LeaseOwner: "instance-a", LeaseDuration: time.Minute, Limit: 1})
	if err != nil {
		t.Fatalf("claim first lease: %v", err)
	}
	if len(first) != 1 || first[0].SessionID != session.ID || first[0].TurnID != turnID || first[0].Attempt != 1 {
		t.Fatalf("unexpected first claim: %+v", first)
	}
	second, err := store.ClaimSessionTurns(ClaimSessionTurnsInput{LeaseOwner: "instance-b", LeaseDuration: time.Minute, Limit: 1})
	if err != nil {
		t.Fatalf("claim while leased: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("expected active lease to exclude second instance, got %+v", second)
	}

	if _, err := store.db.ExecContext(context.Background(), `UPDATE session_turns SET lease_expires_at = $3 WHERE session_id = $1 AND id = $2`, session.ID, turnID, time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatalf("expire first lease: %v", err)
	}
	recovered, err := store.ClaimSessionTurns(ClaimSessionTurnsInput{LeaseOwner: "instance-b", LeaseDuration: time.Minute, Limit: 1})
	if err != nil {
		t.Fatalf("recover expired lease: %v", err)
	}
	if len(recovered) != 1 || recovered[0].Attempt != 2 {
		t.Fatalf("expected expired turn to be recovered on attempt 2, got %+v", recovered)
	}
	active, err := store.RenewSessionTurnLease(RenewSessionTurnLeaseInput{SessionID: session.ID, TurnID: turnID, LeaseOwner: "instance-a", LeaseDuration: time.Minute})
	if err != nil {
		t.Fatalf("renew stale owner: %v", err)
	}
	if active {
		t.Fatal("expected stale lease owner to be fenced out")
	}

	if _, err := store.AppendEvents(session.ID, []AppendEventInput{{Type: EventUserInterrupt}}); err != nil {
		t.Fatalf("append remote interrupt: %v", err)
	}
	active, err = store.RenewSessionTurnLease(RenewSessionTurnLeaseInput{SessionID: session.ID, TurnID: turnID, LeaseOwner: "instance-b", LeaseDuration: time.Minute})
	if err != nil {
		t.Fatalf("renew interrupted turn: %v", err)
	}
	if active {
		t.Fatal("expected interrupt persisted by another instance to stop lease renewal")
	}
}

func TestPostgresStoreSessionTurnClaimRestoresApprovedIntervention(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)
	events, err := store.AppendEvents(session.ID, []AppendEventInput{{
		Type:    EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"approve me"}]}`),
	}})
	if err != nil {
		t.Fatalf("append user message: %v", err)
	}
	turnID := payloadString(events[len(events)-1].Payload, "turn_id")
	if _, err := store.SaveSessionIntervention(session.ID, SaveSessionInterventionInput{
		TurnID:            turnID,
		CallID:            "call_approved",
		ToolIdentifier:    "standard.read_file",
		APIName:           "read_file",
		Arguments:         json.RawMessage(`{"path":"README.md"}`),
		InterventionMode:  "request_approval",
		Reason:            "test",
		Continuation:      json.RawMessage(`[{"role":"assistant","content":[]}]`),
		ContinuationRound: 2,
	}); err != nil {
		t.Fatalf("save intervention: %v", err)
	}
	if err := store.MarkSessionTurnWaitingApproval(session.ID, turnID); err != nil {
		t.Fatalf("mark waiting approval: %v", err)
	}
	if _, err := store.DecideSessionIntervention(session.ID, DecideSessionInterventionInput{
		TurnID: turnID, CallID: "call_approved", Status: InterventionStatusApproved, DecisionReason: "safe",
	}); err != nil {
		t.Fatalf("approve intervention: %v", err)
	}
	retried, err := store.DecideSessionIntervention(session.ID, DecideSessionInterventionInput{
		TurnID: turnID, CallID: "call_approved", Status: InterventionStatusApproved, DecisionReason: "retry",
	})
	if err != nil {
		t.Fatalf("retry approved intervention: %v", err)
	}
	if retried.Intervention.Status != InterventionStatusApproved || len(retried.Events) != 0 {
		t.Fatalf("expected idempotent approved retry without duplicate event, got %+v", retried)
	}

	claimed, err := store.ClaimSessionTurns(ClaimSessionTurnsInput{LeaseOwner: "instance-resume", LeaseDuration: time.Minute, Limit: 1})
	if err != nil {
		t.Fatalf("claim resumed turn: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ResumeIntervention == nil {
		t.Fatalf("expected claimed intervention resume, got %+v", claimed)
	}
	resume := claimed[0].ResumeIntervention
	if resume.CallID != "call_approved" || resume.Status != InterventionStatusApproved || resume.DecisionReason != "safe" || resume.ContinuationRound != 2 {
		t.Fatalf("unexpected claimed intervention: %+v", resume)
	}
	if _, err := store.AppendEvents(session.ID, []AppendEventInput{{Type: EventUserInterrupt}}); err != nil {
		t.Fatalf("cleanup interrupt: %v", err)
	}
}

func TestPostgresStoreRetryOldDecisionDoesNotOverrideNewPendingIntervention(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)
	events, err := store.AppendEvents(session.ID, []AppendEventInput{{
		Type:    EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"two approvals"}]}`),
	}})
	if err != nil {
		t.Fatalf("append user message: %v", err)
	}
	turnID := payloadString(events[len(events)-1].Payload, "turn_id")
	if _, err := store.SaveSessionIntervention(session.ID, SaveSessionInterventionInput{
		TurnID: turnID, CallID: "call_first", ToolIdentifier: "default", APIName: "read_file", InterventionMode: "request_approval",
	}); err != nil {
		t.Fatalf("save first intervention: %v", err)
	}
	if err := store.MarkSessionTurnWaitingApproval(session.ID, turnID); err != nil {
		t.Fatalf("mark first waiting approval: %v", err)
	}
	if _, err := store.DecideSessionIntervention(session.ID, DecideSessionInterventionInput{
		TurnID: turnID, CallID: "call_first", Status: InterventionStatusApproved,
	}); err != nil {
		t.Fatalf("approve first intervention: %v", err)
	}
	if _, err := store.SaveSessionIntervention(session.ID, SaveSessionInterventionInput{
		TurnID: turnID, CallID: "call_second", ToolIdentifier: "default", APIName: "edit_file", InterventionMode: "request_approval",
	}); err != nil {
		t.Fatalf("save second intervention: %v", err)
	}
	if err := store.MarkSessionTurnWaitingApproval(session.ID, turnID); err != nil {
		t.Fatalf("mark second waiting approval: %v", err)
	}
	if _, err := store.DecideSessionIntervention(session.ID, DecideSessionInterventionInput{
		TurnID: turnID, CallID: "call_first", Status: InterventionStatusApproved,
	}); err != nil {
		t.Fatalf("retry first intervention: %v", err)
	}

	claimed, err := store.ClaimSessionTurns(ClaimSessionTurnsInput{LeaseOwner: "instance-old-retry", LeaseDuration: time.Minute, Limit: 1})
	if err != nil {
		t.Fatalf("claim after old retry: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("expected second pending approval to block old resume, got %+v", claimed)
	}
	pending, err := store.ListSessionInterventions(session.ID, InterventionStatusPending)
	if err != nil {
		t.Fatalf("list pending interventions: %v", err)
	}
	if len(pending) != 1 || pending[0].CallID != "call_second" {
		t.Fatalf("expected second intervention to remain pending, got %+v", pending)
	}
	if _, err := store.AppendEvents(session.ID, []AppendEventInput{{Type: EventUserInterrupt}}); err != nil {
		t.Fatalf("cleanup interrupt: %v", err)
	}
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
	workspaceID := createPostgresIntegrationWorkspace(t, store, "reap-worker-work")
	worker, err := store.RegisterWorker(RegisterWorkerInput{
		WorkspaceID:  workspaceID,
		Name:         "integration-worker-" + time.Now().UTC().Format("20060102150405.000000000"),
		WorkerType:   WorkerTypeLocal,
		RegisteredBy: "integration-test",
		LeaseSeconds: 30,
	})
	if err != nil {
		t.Fatalf("register worker: %v", err)
	}
	t.Cleanup(func() {
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM worker_work WHERE workspace_id = $1`, workspaceID); err != nil {
			t.Fatalf("cleanup worker work for workspace %s: %v", workspaceID, err)
		}
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM workers WHERE id = $1`, worker.ID); err != nil {
			t.Fatalf("cleanup worker %s: %v", worker.ID, err)
		}
	})

	queued, err := store.EnqueueWorkerWork(EnqueueWorkerWorkInput{
		WorkspaceID: workspaceID,
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

func TestPostgresStoreCancelsWorkerWork(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	workspaceID := createPostgresIntegrationWorkspace(t, store, "cancel-worker-work")
	worker, err := store.RegisterWorker(RegisterWorkerInput{
		WorkspaceID:  workspaceID,
		Name:         "integration-cancel-worker-" + time.Now().UTC().Format("20060102150405.000000000"),
		WorkerType:   WorkerTypeLocal,
		RegisteredBy: "integration-test",
		LeaseSeconds: 30,
	})
	if err != nil {
		t.Fatalf("register worker: %v", err)
	}
	t.Cleanup(func() {
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM worker_work WHERE workspace_id = $1`, workspaceID); err != nil {
			t.Fatalf("cleanup worker work for workspace %s: %v", workspaceID, err)
		}
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM workers WHERE id = $1`, worker.ID); err != nil {
			t.Fatalf("cleanup worker %s: %v", worker.ID, err)
		}
	})

	queued, err := store.EnqueueWorkerWork(EnqueueWorkerWorkInput{
		WorkspaceID: workspaceID,
		WorkerID:    worker.ID,
		WorkType:    WorkerWorkTypeSandboxCommand,
		Payload:     json.RawMessage(`{"command":"sh","args":["-c","sleep 100"]}`),
	})
	if err != nil {
		t.Fatalf("enqueue worker work: %v", err)
	}
	polled, err := store.PollWorkerWork(worker.ID, PollWorkerWorkInput{LeaseSeconds: 30})
	if err != nil {
		t.Fatalf("poll worker work: %v", err)
	}
	if polled == nil || polled.ID != queued.ID || polled.Status != WorkerWorkStatusLeased {
		t.Fatalf("expected leased work, got %+v", polled)
	}

	canceled, err := store.CancelWorkerWork(queued.ID, CancelWorkerWorkInput{Reason: "integration canceled"})
	if err != nil {
		t.Fatalf("cancel worker work: %v", err)
	}
	if canceled.Status != WorkerWorkStatusCanceled || canceled.ErrorMessage != "integration canceled" || canceled.CompletedAt == nil {
		t.Fatalf("unexpected canceled work: %+v", canceled)
	}
	heartbeat, err := store.HeartbeatWorkerWork(worker.ID, queued.ID, WorkerWorkHeartbeatInput{LeaseSeconds: 30})
	if err != nil {
		t.Fatalf("heartbeat canceled worker work: %v", err)
	}
	if heartbeat.Status != WorkerWorkStatusCanceled {
		t.Fatalf("expected heartbeat to return canceled work, got %+v", heartbeat)
	}

	completed, err := store.CompleteWorkerWork(worker.ID, queued.ID, CompleteWorkerWorkInput{
		Success: true,
		Result:  json.RawMessage(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("complete canceled worker work: %v", err)
	}
	if completed.Status != WorkerWorkStatusCanceled || string(completed.Result) == `{"ok":true}` {
		t.Fatalf("expected canceled work result to be preserved, got %+v result=%s", completed, string(completed.Result))
	}

	again, err := store.CancelWorkerWork(queued.ID, CancelWorkerWorkInput{Reason: "second reason"})
	if err != nil {
		t.Fatalf("cancel terminal worker work: %v", err)
	}
	if again.ErrorMessage != "integration canceled" {
		t.Fatalf("expected terminal cancel to preserve reason, got %+v", again)
	}

	requeued, err := store.RequeueWorkerWork(queued.ID, RequeueWorkerWorkInput{ClearWorker: true})
	if err != nil {
		t.Fatalf("requeue canceled worker work: %v", err)
	}
	if requeued.ID == queued.ID || requeued.Status != WorkerWorkStatusPending || requeued.WorkerID != "" {
		t.Fatalf("unexpected requeued work: %+v", requeued)
	}
	if requeued.WorkspaceID != queued.WorkspaceID || requeued.WorkType != queued.WorkType || string(requeued.Payload) != string(queued.Payload) {
		t.Fatalf("requeued work did not preserve original data: original=%+v requeued=%+v", queued, requeued)
	}
	if string(requeued.Result) != `{}` || requeued.StartedAt != nil || requeued.CompletedAt != nil || requeued.LeaseExpiresAt != nil {
		t.Fatalf("requeued work did not reset execution fields: %+v result=%s", requeued, string(requeued.Result))
	}
}

func TestPostgresStoreReapsExpiredWorkers(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	worker, err := store.RegisterWorker(RegisterWorkerInput{
		Name:         "integration-expired-worker-" + time.Now().UTC().Format("20060102150405.000000000"),
		WorkerType:   WorkerTypeLocal,
		RegisteredBy: "integration-test",
		LeaseSeconds: 30,
	})
	if err != nil {
		t.Fatalf("register worker: %v", err)
	}
	t.Cleanup(func() {
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM workers WHERE id = $1`, worker.ID); err != nil {
			t.Fatalf("cleanup worker %s: %v", worker.ID, err)
		}
	})

	expiredAt := time.Unix(0, 0).UTC()
	if _, err := store.db.ExecContext(context.Background(), `UPDATE workers SET lease_expires_at = $1 WHERE id = $2`, expiredAt, worker.ID); err != nil {
		t.Fatalf("force expired lease: %v", err)
	}
	expired, err := store.ReapExpiredWorkers(ReapExpiredWorkersInput{Limit: 1})
	if err != nil {
		t.Fatalf("reap expired workers: %v", err)
	}
	if len(expired) != 1 || expired[0].ID != worker.ID {
		t.Fatalf("expected only test worker to expire, got %+v", expired)
	}
	if expired[0].Status != WorkerStatusOffline {
		t.Fatalf("expected expired worker offline, got %+v", expired[0])
	}

	fetched, err := store.GetWorker(worker.ID)
	if err != nil {
		t.Fatalf("get worker: %v", err)
	}
	if fetched.Status != WorkerStatusOffline {
		t.Fatalf("expected fetched worker offline, got %+v", fetched)
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

func createPostgresIntegrationWorkspace(t *testing.T, store *PostgresStore, prefix string) string {
	t.Helper()

	suffix := time.Now().UTC().Format("20060102150405.000000000")
	workspaceID := "wksp_integration_" + strings.ReplaceAll(prefix, "-", "_") + "_" + suffix
	if _, err := store.db.ExecContext(
		context.Background(),
		`INSERT INTO workspaces (id, org_id, name, created_at) VALUES ($1, 'org_default', $2, $3)`,
		workspaceID,
		prefix+" "+suffix,
		time.Now().UTC(),
	); err != nil {
		t.Fatalf("create integration workspace %s: %v", workspaceID, err)
	}
	t.Cleanup(func() {
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM workspaces WHERE id = $1`, workspaceID); err != nil {
			t.Fatalf("cleanup workspace %s: %v", workspaceID, err)
		}
	})
	return workspaceID
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
