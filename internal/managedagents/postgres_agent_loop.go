package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/model"
)

const agentLoopInterventionModeRequestApproval = "request_approval"

type PostgresAgentLoopDurability struct {
	store *PostgresStore
	fence AgentLoopFence
}

type AgentLoopFence struct {
	LeaseOwner string
	Attempt    int
}

var _ agentcore.DurabilityPort = (*PostgresAgentLoopDurability)(nil)
var _ agentcore.StateRepository = (*PostgresAgentLoopDurability)(nil)

func (s *PostgresStore) AgentLoopDurability(fence AgentLoopFence) *PostgresAgentLoopDurability {
	fence.LeaseOwner = strings.TrimSpace(fence.LeaseOwner)
	return &PostgresAgentLoopDurability{store: s, fence: fence}
}

func (s *PostgresStore) AgentLoopRepository(fence AgentLoopFence) agentcore.StateRepository {
	return s.AgentLoopDurability(fence)
}

func (d *PostgresAgentLoopDurability) Commit(ctx context.Context, transition agentcore.Transition) (agentcore.State, error) {
	return d.apply(ctx, transition, agentLoopCommit{})
}

func (d *PostgresAgentLoopDurability) Park(ctx context.Context, transition agentcore.ParkTransition) (agentcore.State, error) {
	return d.apply(ctx, transition.Transition, agentLoopPark{pause: transition.Pause})
}

func (d *PostgresAgentLoopDurability) Complete(ctx context.Context, transition agentcore.CompleteTransition) (agentcore.State, error) {
	return d.apply(ctx, transition.Transition, agentLoopComplete{finalMessageID: transition.FinalMessageID})
}

func (d *PostgresAgentLoopDurability) Fail(ctx context.Context, transition agentcore.TerminalTransition) (agentcore.State, error) {
	return d.apply(ctx, transition.Transition, agentLoopFail{failure: transition.Failure})
}

func (d *PostgresAgentLoopDurability) Cancel(ctx context.Context, transition agentcore.TerminalTransition) (agentcore.State, error) {
	return d.apply(ctx, transition.Transition, agentLoopCancel{failure: transition.Failure})
}

func (d *PostgresAgentLoopDurability) Load(ctx context.Context, sessionID, turnID string) (agentcore.State, error) {
	if d == nil || d.store == nil {
		return agentcore.State{}, fmt.Errorf("%w: postgres agent loop durability is not configured", ErrInvalid)
	}
	return d.store.LoadAgentLoopStateContext(ctx, sessionID, turnID)
}

func (s *PostgresStore) LoadAgentLoopStateContext(ctx context.Context, sessionID, turnID string) (agentcore.State, error) {
	if s == nil || s.db == nil {
		return agentcore.State{}, fmt.Errorf("%w: postgres store is required", ErrInvalid)
	}
	sessionID = strings.TrimSpace(sessionID)
	turnID = strings.TrimSpace(turnID)
	if sessionID == "" || turnID == "" {
		return agentcore.State{}, fmt.Errorf("%w: agent loop session_id and turn_id are required", ErrInvalid)
	}
	scope, ok := DatabaseAccessScopeFromContext(ctx)
	if !ok {
		return agentcore.State{}, fmt.Errorf("%w: agent loop database access scope is required", ErrForbidden)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return agentcore.State{}, err
	}
	defer tx.Rollback()
	if _, err := setDatabaseAccessScope(ctx, tx, scope.WorkspaceID); err != nil {
		return agentcore.State{}, err
	}
	var raw []byte
	var revision int64
	err = tx.QueryRowContext(ctx, `
		SELECT revision, state_json
		FROM agent_loop_states
		WHERE session_id = $1 AND turn_id = $2
	`, sessionID, turnID).Scan(&revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return agentcore.State{}, fmt.Errorf("%w: agent loop state %s/%s", ErrNotFound, sessionID, turnID)
	}
	if err != nil {
		return agentcore.State{}, err
	}
	state, err := decodeAgentLoopState(raw, revision, sessionID, turnID)
	if err != nil {
		return agentcore.State{}, err
	}
	if err := tx.Commit(); err != nil {
		return agentcore.State{}, err
	}
	return state, nil
}

type agentLoopAction interface {
	validate(agentcore.Transition) error
	apply(context.Context, *PostgresStore, *sql.Tx, Session, agentcore.State, time.Time) ([]Event, error)
}

type agentLoopCommit struct{}

func (agentLoopCommit) validate(transition agentcore.Transition) error {
	switch transition.Next.Phase {
	case agentcore.PhasePaused, agentcore.PhaseCompleted, agentcore.PhaseFailed, agentcore.PhaseCanceled:
		return fmt.Errorf("%w: phase %s requires its dedicated durability operation", ErrInvalid, transition.Next.Phase)
	default:
		return nil
	}
}
func (agentLoopCommit) apply(context.Context, *PostgresStore, *sql.Tx, Session, agentcore.State, time.Time) ([]Event, error) {
	return nil, nil
}

type agentLoopPark struct{ pause agentcore.PauseState }
type agentLoopComplete struct{ finalMessageID string }
type agentLoopFail struct{ failure agentcore.Failure }
type agentLoopCancel struct{ failure agentcore.Failure }

func (a agentLoopPark) validate(transition agentcore.Transition) error {
	if transition.Next.Phase != agentcore.PhasePaused || transition.Next.Pause == nil {
		return fmt.Errorf("%w: park transition requires paused state", ErrInvalid)
	}
	if err := a.pause.Validate(); err != nil {
		return fmt.Errorf("%w: invalid park transition: %v", ErrInvalid, err)
	}
	pauseJSON, _ := json.Marshal(a.pause)
	statePauseJSON, _ := json.Marshal(transition.Next.Pause)
	if string(pauseJSON) != string(statePauseJSON) {
		return fmt.Errorf("%w: park transition pause does not match state", ErrInvalid)
	}
	return nil
}

func (a agentLoopComplete) validate(transition agentcore.Transition) error {
	if transition.Next.Phase != agentcore.PhaseCompleted || strings.TrimSpace(a.finalMessageID) == "" {
		return fmt.Errorf("%w: complete transition is incomplete", ErrInvalid)
	}
	message, ok := agentLoopMessageByID(transition.Next.Messages, a.finalMessageID)
	if !ok || message.Role != model.RoleAssistant || message.Visibility != model.VisibilityPublic {
		return fmt.Errorf("%w: complete transition final message is not public", ErrInvalid)
	}
	return nil
}

func (a agentLoopFail) validate(transition agentcore.Transition) error {
	return validateAgentLoopTerminal(transition, agentcore.PhaseFailed, a.failure)
}

func (a agentLoopCancel) validate(transition agentcore.Transition) error {
	return validateAgentLoopTerminal(transition, agentcore.PhaseCanceled, a.failure)
}

func validateAgentLoopTerminal(transition agentcore.Transition, phase agentcore.Phase, failure agentcore.Failure) error {
	if transition.Next.Phase != phase || transition.Next.Failure == nil || *transition.Next.Failure != failure {
		return fmt.Errorf("%w: terminal transition does not match state", ErrInvalid)
	}
	return nil
}

func (d *PostgresAgentLoopDurability) apply(ctx context.Context, transition agentcore.Transition, action agentLoopAction) (agentcore.State, error) {
	if d == nil || d.store == nil || d.store.db == nil {
		return agentcore.State{}, fmt.Errorf("%w: postgres agent loop durability is not configured", ErrInvalid)
	}
	if d.fence.LeaseOwner == "" || d.fence.Attempt <= 0 {
		return agentcore.State{}, fmt.Errorf("%w: agent loop lease owner and attempt fence are required", ErrInvalid)
	}
	if err := validateAgentLoopTransitionInput(transition); err != nil {
		return agentcore.State{}, err
	}
	if err := action.validate(transition); err != nil {
		return agentcore.State{}, err
	}
	scope, ok := DatabaseAccessScopeFromContext(ctx)
	if !ok {
		return agentcore.State{}, fmt.Errorf("%w: agent loop database access scope is required", ErrForbidden)
	}

	tx, err := d.store.db.BeginTx(ctx, nil)
	if err != nil {
		return agentcore.State{}, err
	}
	defer tx.Rollback()
	if _, err := setDatabaseAccessScope(ctx, tx, scope.WorkspaceID); err != nil {
		return agentcore.State{}, err
	}
	session, turn, err := lockAgentLoopTurn(ctx, tx, transition.Next.SessionID, transition.Next.TurnID)
	if err != nil {
		return agentcore.State{}, err
	}
	if session.WorkspaceID != scope.WorkspaceID || (scope.OwnerID != "" && session.OwnerID != scope.OwnerID) {
		return agentcore.State{}, ErrForbidden
	}
	if session.Status == SessionStatusTerminated {
		return agentcore.State{}, ErrTerminated
	}
	_, cancelAction := action.(agentLoopCancel)
	interruptedCancel := cancelAction && turn.status == TurnStatusInterrupted && turn.attempt == d.fence.Attempt
	if !interruptedCancel {
		if turn.status != TurnStatusRunning {
			return agentcore.State{}, fmt.Errorf("%w: agent loop turn is %s", ErrLeaseLost, turn.status)
		}
		if !turn.leaseValid || turn.leaseOwner != d.fence.LeaseOwner || turn.attempt != d.fence.Attempt {
			return agentcore.State{}, fmt.Errorf("%w: expected owner=%s attempt=%d", ErrLeaseLost, d.fence.LeaseOwner, d.fence.Attempt)
		}
	}

	stored, exists, err := loadAgentLoopStateForUpdate(ctx, tx, transition.Next.SessionID, transition.Next.TurnID)
	if err != nil {
		return agentcore.State{}, err
	}
	if exists {
		if stored.Revision != transition.ExpectedRevision {
			return agentcore.State{}, fmt.Errorf("%w: expected=%d actual=%d", ErrRevisionConflict, transition.ExpectedRevision, stored.Revision)
		}
		if err := agentcore.ValidatePhaseTransition(stored.Phase, transition.Next.Phase); err != nil {
			return agentcore.State{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
	} else if transition.ExpectedRevision != 0 || transition.Next.Phase != agentcore.PhaseAwaitingModel {
		return agentcore.State{}, fmt.Errorf("%w: initial agent loop transition must create awaiting_model revision 1", ErrRevisionConflict)
	}

	next := transition.Next.Clone()
	next.Revision = transition.ExpectedRevision + 1
	if err := next.Validate(); err != nil {
		return agentcore.State{}, fmt.Errorf("%w: invalid agent loop state: %v", ErrInvalid, err)
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return agentcore.State{}, fmt.Errorf("encode agent loop state: %w", err)
	}
	if err := saveAgentLoopState(ctx, tx, session, next, raw, exists, transition.ExpectedRevision); err != nil {
		return agentcore.State{}, err
	}

	now := time.Now().UTC()
	events, err := appendAgentLoopRuntimeEvents(ctx, d.store, tx, next, transition.Events, now)
	if err != nil {
		return agentcore.State{}, err
	}
	var actionEvents []Event
	if !interruptedCancel {
		actionEvents, err = action.apply(ctx, d.store, tx, session, next, now)
		if err != nil {
			return agentcore.State{}, err
		}
	}
	events = append(events, actionEvents...)
	if err := tx.Commit(); err != nil {
		return agentcore.State{}, err
	}
	for _, event := range events {
		d.store.hub.publish(event)
	}
	return next.Clone(), nil
}

func validateAgentLoopTransitionInput(transition agentcore.Transition) error {
	if transition.ExpectedRevision < 0 || transition.Next.Revision != transition.ExpectedRevision {
		return fmt.Errorf("%w: transition revision is invalid", ErrInvalid)
	}
	if err := transition.Next.Validate(); err != nil {
		return fmt.Errorf("%w: invalid transition state: %v", ErrInvalid, err)
	}
	return nil
}

type agentLoopTurnFence struct {
	status     string
	leaseOwner string
	attempt    int
	leaseValid bool
}

func lockAgentLoopTurn(ctx context.Context, tx *sql.Tx, sessionID, turnID string) (Session, agentLoopTurnFence, error) {
	var session Session
	if err := tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, owner_id, status
		FROM sessions
		WHERE id = $1
		FOR UPDATE
	`, sessionID).Scan(&session.ID, &session.WorkspaceID, &session.OwnerID, &session.Status); errors.Is(err, sql.ErrNoRows) {
		return Session{}, agentLoopTurnFence{}, fmt.Errorf("%w: session %s", ErrNotFound, sessionID)
	} else if err != nil {
		return Session{}, agentLoopTurnFence{}, err
	}
	var turn agentLoopTurnFence
	var leaseOwner sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT status, lease_owner, attempt_count, COALESCE(lease_expires_at > CURRENT_TIMESTAMP, false)
		FROM session_turns
		WHERE session_id = $1 AND id = $2
		FOR UPDATE
	`, sessionID, turnID).Scan(&turn.status, &leaseOwner, &turn.attempt, &turn.leaseValid); errors.Is(err, sql.ErrNoRows) {
		return Session{}, agentLoopTurnFence{}, fmt.Errorf("%w: session turn %s/%s", ErrNotFound, sessionID, turnID)
	} else if err != nil {
		return Session{}, agentLoopTurnFence{}, err
	}
	turn.leaseOwner = leaseOwner.String
	return session, turn, nil
}

func loadAgentLoopStateForUpdate(ctx context.Context, tx *sql.Tx, sessionID, turnID string) (agentcore.State, bool, error) {
	var revision int64
	var raw []byte
	err := tx.QueryRowContext(ctx, `
		SELECT revision, state_json
		FROM agent_loop_states
		WHERE session_id = $1 AND turn_id = $2
		FOR UPDATE
	`, sessionID, turnID).Scan(&revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return agentcore.State{}, false, nil
	}
	if err != nil {
		return agentcore.State{}, false, err
	}
	state, err := decodeAgentLoopState(raw, revision, sessionID, turnID)
	return state, true, err
}

func decodeAgentLoopState(raw []byte, revision int64, sessionID, turnID string) (agentcore.State, error) {
	var state agentcore.State
	if err := json.Unmarshal(raw, &state); err != nil {
		return agentcore.State{}, fmt.Errorf("decode agent loop state: %w", err)
	}
	if state.Revision != revision || state.SessionID != sessionID || state.TurnID != turnID {
		return agentcore.State{}, fmt.Errorf("%w: agent loop state identity or revision mismatch", ErrInvalid)
	}
	if err := state.Validate(); err != nil {
		return agentcore.State{}, fmt.Errorf("%w: stored agent loop state is invalid: %v", ErrInvalid, err)
	}
	return state.Clone(), nil
}

func saveAgentLoopState(ctx context.Context, tx *sql.Tx, session Session, state agentcore.State, raw []byte, exists bool, expectedRevision int64) error {
	if !exists {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO agent_loop_states (
				workspace_id, owner_id, session_id, turn_id, revision, phase, state_json, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, now(), now())
		`, session.WorkspaceID, session.OwnerID, state.SessionID, state.TurnID, state.Revision, state.Phase, raw)
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE agent_loop_states
		SET revision = $3, phase = $4, state_json = $5, updated_at = now()
		WHERE session_id = $1 AND turn_id = $2 AND revision = $6
	`, state.SessionID, state.TurnID, state.Revision, state.Phase, raw, expectedRevision)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("%w: expected=%d", ErrRevisionConflict, expectedRevision)
	}
	return nil
}

func appendAgentLoopRuntimeEvents(ctx context.Context, store *PostgresStore, tx *sql.Tx, state agentcore.State, runtimeEvents []agentcore.RuntimeEvent, now time.Time) ([]Event, error) {
	events := make([]Event, 0, len(runtimeEvents))
	for _, runtimeEvent := range runtimeEvents {
		if runtimeEvent.Type == "" {
			return nil, fmt.Errorf("%w: agent loop runtime event type is required", ErrInvalid)
		}
		payload, err := json.Marshal(map[string]any{
			"turn_id":       state.TurnID,
			"loop_revision": state.Revision,
			"message":       runtimeEvent.Message,
			"data":          runtimeEvent.Payload,
		})
		if err != nil {
			return nil, fmt.Errorf("encode agent loop runtime event: %w", err)
		}
		event, err := store.appendEventTx(ctx, tx, state.SessionID, string(runtimeEvent.Type), payload, now)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}

func (a agentLoopPark) apply(ctx context.Context, _ *PostgresStore, tx *sql.Tx, _ Session, state agentcore.State, now time.Time) ([]Event, error) {
	callByID := make(map[string]model.ToolCall, len(state.PendingToolBatch.Calls))
	for _, planned := range state.PendingToolBatch.Calls {
		callByID[planned.Call.ID] = planned.Call
	}
	seenCalls := make(map[string]struct{}, len(a.pause.Interactions))
	waitingStatus := TurnStatusWaitingApproval
	for _, interaction := range a.pause.Interactions {
		recordCallID := interaction.CallID
		if strings.HasPrefix(interaction.ID, agentcore.ToolReconciliationRequestPurpose+":") {
			recordCallID = interaction.ID
		}
		if _, exists := seenCalls[recordCallID]; exists {
			return nil, fmt.Errorf("%w: postgres interventions allow one interaction per tool call", ErrInvalid)
		}
		seenCalls[recordCallID] = struct{}{}
		call, ok := callByID[interaction.CallID]
		if !ok {
			return nil, fmt.Errorf("%w: pause interaction references unknown call %s", ErrInvalid, interaction.CallID)
		}
		identifier, apiName := splitAgentLoopToolName(call.Name)
		kind, err := postgresInterventionKind(interaction.Kind)
		if err != nil {
			return nil, err
		}
		if kind == InterventionKindClarification || kind == InterventionKindUploadRequest {
			waitingStatus = TurnStatusWaitingHuman
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO session_interventions (
				session_id, turn_id, call_id, tool_identifier, api_name, arguments_json,
				intervention_mode, reason, status, requested_at, kind, request_json
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		`, state.SessionID, state.TurnID, recordCallID, identifier, apiName, nullableRaw(call.Arguments),
			agentLoopInterventionModeRequestApproval, a.pause.Reason, InterventionStatusPending, now, kind, nullableRaw(interaction.Request)); err != nil {
			return nil, err
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE session_turns
		SET status = $3,
			resume_intervention_call_id = NULL,
			lease_owner = NULL,
			lease_expires_at = NULL,
			last_heartbeat_at = NULL
		WHERE session_id = $1 AND id = $2 AND status = $4
	`, state.SessionID, state.TurnID, waitingStatus, TurnStatusRunning)
	if err != nil {
		return nil, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if rows != 1 {
		return nil, fmt.Errorf("%w: turn cannot be parked from its current status", ErrConflict)
	}
	return nil, nil
}

func (a agentLoopComplete) apply(ctx context.Context, store *PostgresStore, tx *sql.Tx, session Session, state agentcore.State, now time.Time) ([]Event, error) {
	message, _ := agentLoopMessageByID(state.Messages, a.finalMessageID)
	payload, err := json.Marshal(map[string]any{
		"protocol_version": AgentLoopMessageProtocolVersion,
		"content_format":   "blocks",
		"content":          message.Content,
		"message_id":       message.ID,
	})
	if err != nil {
		return nil, err
	}
	agentEvent, err := store.appendEventTx(ctx, tx, state.SessionID, EventAgentMessage, payloadWithTurnID(payload, state.TurnID), now)
	if err != nil {
		return nil, err
	}
	idleEvent, err := store.appendEventTx(ctx, tx, state.SessionID, EventSessionStatusIdle, statusPayload(SessionStatusIdle, state.TurnID), now)
	if err != nil {
		return nil, err
	}
	if err := completeTurnTx(ctx, tx, state.SessionID, state.TurnID, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET status = $2 WHERE id = $1`, session.ID, SessionStatusIdle); err != nil {
		return nil, err
	}
	return []Event{agentEvent, idleEvent}, nil
}

func (a agentLoopFail) apply(ctx context.Context, store *PostgresStore, tx *sql.Tx, session Session, state agentcore.State, now time.Time) ([]Event, error) {
	idleEvent, err := store.appendEventTx(ctx, tx, state.SessionID, EventSessionStatusIdle, failedTurnIdlePayload(state.TurnID, a.failure.Message), now)
	if err != nil {
		return nil, err
	}
	if err := failTurnTx(ctx, tx, state.SessionID, state.TurnID, a.failure.Message, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET status = $2 WHERE id = $1`, session.ID, SessionStatusIdle); err != nil {
		return nil, err
	}
	return []Event{idleEvent}, nil
}

func (a agentLoopCancel) apply(ctx context.Context, store *PostgresStore, tx *sql.Tx, session Session, state agentcore.State, now time.Time) ([]Event, error) {
	rejected, err := store.rejectPendingTurnInterventionsTx(ctx, tx, state.SessionID, state.TurnID, a.failure.Message, now)
	if err != nil {
		return nil, err
	}
	idleEvent, err := store.appendEventTx(ctx, tx, state.SessionID, EventSessionStatusIdle, failedTurnIdlePayload(state.TurnID, a.failure.Message), now)
	if err != nil {
		return nil, err
	}
	if err := interruptTurnTx(ctx, tx, state.SessionID, state.TurnID, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET status = $2 WHERE id = $1`, session.ID, SessionStatusIdle); err != nil {
		return nil, err
	}
	return append(rejected, idleEvent), nil
}

func agentLoopMessageByID(messages []model.Message, id string) (model.Message, bool) {
	for _, message := range messages {
		if message.ID == id {
			return model.CloneMessage(message), true
		}
	}
	return model.Message{}, false
}

func splitAgentLoopToolName(name string) (string, string) {
	name = strings.TrimSpace(name)
	index := strings.LastIndex(name, ".")
	if index <= 0 || index == len(name)-1 {
		return "default", name
	}
	return name[:index], name[index+1:]
}

func postgresInterventionKind(kind string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(kind)) {
	case "approval", InterventionKindToolApproval:
		return InterventionKindToolApproval, nil
	case InterventionKindClarification:
		return InterventionKindClarification, nil
	case InterventionKindPlanApproval:
		return InterventionKindPlanApproval, nil
	case InterventionKindUploadRequest:
		return InterventionKindUploadRequest, nil
	default:
		return "", fmt.Errorf("%w: unsupported agent loop interaction kind %q", ErrInvalid, kind)
	}
}
