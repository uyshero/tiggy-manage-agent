package managedagents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const agentScheduleSelect = `
	SELECT id, workspace_id, owner_id, agent_id, environment_id,
	       session_mode, COALESCE(target_session_id, ''), approval_mode, name, prompt,
	       cron_expression, timezone, enabled, next_run_at, last_run_at,
	       COALESCE(last_session_id, ''), COALESCE(last_run_status, ''), last_error,
	       created_by, created_at, updated_at
	FROM agent_schedules`

func (s *PostgresStore) EnsureAgentScheduleEnvironment(ctx context.Context, workspaceID string) (Environment, error) {
	workspaceID = defaultString(strings.TrimSpace(workspaceID), DefaultWorkspaceID)
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return Environment{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "agent-schedule-environment:"+scope.WorkspaceID); err != nil {
		return Environment{}, err
	}
	var environment Environment
	var config []byte
	err = tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, name, config_json, archived_at, created_at
		FROM environments
		WHERE workspace_id=$1 AND archived_at IS NULL
		  AND config_json @> '{"managed_by":"agent_scheduler"}'::jsonb
		ORDER BY created_at, id
		LIMIT 1
	`, scope.WorkspaceID).Scan(&environment.ID, &environment.WorkspaceID, &environment.Name, &config, &environment.ArchivedAt, &environment.CreatedAt)
	if err == nil {
		environment.Config = cloneRaw(config)
		if err := tx.Commit(); err != nil {
			return Environment{}, err
		}
		return environment, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Environment{}, err
	}
	id, err := nextSequenceID(ctx, tx, "env", "tma_environment_id_seq")
	if err != nil {
		return Environment{}, err
	}
	now := time.Now().UTC()
	config = []byte(`{"managed_by":"agent_scheduler"}`)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO environments(id,workspace_id,name,config_json,created_at)
		VALUES($1,$2,'Scheduled tasks',$3,$4)
	`, id, scope.WorkspaceID, config, now); err != nil {
		return Environment{}, err
	}
	if err := tx.Commit(); err != nil {
		return Environment{}, err
	}
	return Environment{ID: id, WorkspaceID: scope.WorkspaceID, Name: "Scheduled tasks", Config: cloneRaw(config), CreatedAt: now}, nil
}

func (s *PostgresStore) CreateAgentSchedule(ctx context.Context, input CreateAgentScheduleInput) (AgentSchedule, error) {
	input.WorkspaceID = defaultString(strings.TrimSpace(input.WorkspaceID), DefaultWorkspaceID)
	input.OwnerID = strings.TrimSpace(input.OwnerID)
	input.AgentID = strings.TrimSpace(input.AgentID)
	input.EnvironmentID = strings.TrimSpace(input.EnvironmentID)
	input.Name = strings.TrimSpace(input.Name)
	input.Prompt = strings.TrimSpace(input.Prompt)
	sessionMode, targetSessionID, approvalMode, err := NormalizeAgentScheduleModes(input.SessionMode, input.TargetSessionID, input.ApprovalMode)
	if err != nil {
		return AgentSchedule{}, err
	}
	input.SessionMode = sessionMode
	input.TargetSessionID = targetSessionID
	input.ApprovalMode = approvalMode
	if input.AgentID == "" || input.Name == "" || input.Prompt == "" {
		return AgentSchedule{}, fmt.Errorf("%w: agent_id, name, and prompt are required", ErrInvalid)
	}
	if input.SessionMode == AgentScheduleSessionNew && input.EnvironmentID == "" {
		return AgentSchedule{}, fmt.Errorf("%w: environment_id is required for new_session mode", ErrInvalid)
	}
	expression, timezone, next, err := NormalizeAgentSchedule(input.CronExpression, input.Timezone, time.Now().UTC())
	if err != nil {
		return AgentSchedule{}, err
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	var nextRunAt any = next
	if !enabled {
		nextRunAt = nil
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return AgentSchedule{}, err
	}
	defer tx.Rollback()
	if scope.OwnerID != "" {
		input.OwnerID = scope.OwnerID
	}
	if input.OwnerID == "" {
		input.OwnerID = input.CreatedBy
	}
	if input.SessionMode == AgentScheduleSessionExisting {
		var session Session
		if err := tx.QueryRowContext(ctx, `
			SELECT id, workspace_id, owner_id, agent_id, environment_id, status, archived_at
			FROM sessions WHERE id=$1
		`, input.TargetSessionID).Scan(&session.ID, &session.WorkspaceID, &session.OwnerID, &session.AgentID, &session.EnvironmentID, &session.Status, &session.ArchivedAt); errors.Is(err, sql.ErrNoRows) {
			return AgentSchedule{}, fmt.Errorf("%w: target session %s", ErrNotFound, input.TargetSessionID)
		} else if err != nil {
			return AgentSchedule{}, err
		}
		if session.WorkspaceID != scope.WorkspaceID || (scope.OwnerID != "" && session.OwnerID != scope.OwnerID) {
			return AgentSchedule{}, ErrForbidden
		}
		if session.AgentID != input.AgentID {
			return AgentSchedule{}, fmt.Errorf("%w: target session must use the scheduled agent", ErrInvalid)
		}
		if session.ArchivedAt != nil || session.Status == SessionStatusTerminated {
			return AgentSchedule{}, fmt.Errorf("%w: target session is not active", ErrInvalid)
		}
		input.EnvironmentID = session.EnvironmentID
	} else {
		var valid bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM agents a
				JOIN environments e ON e.workspace_id = a.workspace_id
				WHERE a.id = $1 AND e.id = $2 AND a.workspace_id = $3 AND a.archived_at IS NULL AND e.archived_at IS NULL
			)
		`, input.AgentID, input.EnvironmentID, scope.WorkspaceID).Scan(&valid); err != nil {
			return AgentSchedule{}, err
		}
		if !valid {
			return AgentSchedule{}, fmt.Errorf("%w: agent or environment is not available", ErrInvalid)
		}
	}
	id, err := nextSequenceID(ctx, tx, "asch", "tma_agent_schedule_id_seq")
	if err != nil {
		return AgentSchedule{}, err
	}
	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_schedules (
			id, workspace_id, owner_id, agent_id, environment_id, session_mode, target_session_id, approval_mode,
			name, prompt, cron_expression, timezone, enabled, next_run_at, created_by, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,$10,$11,$12,$13,$14,$15,$16,$16)
	`, id, scope.WorkspaceID, input.OwnerID, input.AgentID, input.EnvironmentID,
		input.SessionMode, input.TargetSessionID, input.ApprovalMode, input.Name, input.Prompt,
		expression, timezone, enabled, nextRunAt, input.CreatedBy, now)
	if err != nil {
		return AgentSchedule{}, err
	}
	created, err := scanAgentSchedule(tx.QueryRowContext(ctx, agentScheduleSelect+` WHERE id = $1`, id))
	if err != nil {
		return AgentSchedule{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentSchedule{}, err
	}
	return created, nil
}

func (s *PostgresStore) GetAgentSchedule(ctx context.Context, id string) (AgentSchedule, error) {
	scope, ok := DatabaseAccessScopeFromContext(ctx)
	if !ok {
		return AgentSchedule{}, fmt.Errorf("%w: schedule access scope is required", ErrInvalid)
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
	if err != nil {
		return AgentSchedule{}, err
	}
	defer tx.Rollback()
	item, err := scanAgentSchedule(tx.QueryRowContext(ctx, agentScheduleSelect+` WHERE id = $1`, strings.TrimSpace(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return AgentSchedule{}, ErrNotFound
	}
	if err != nil {
		return AgentSchedule{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentSchedule{}, err
	}
	return item, nil
}

func (s *PostgresStore) ListAgentSchedules(ctx context.Context, agentID string) ([]AgentSchedule, error) {
	scope, ok := DatabaseAccessScopeFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("%w: schedule access scope is required", ErrInvalid)
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, agentScheduleSelect+` WHERE agent_id = $1 ORDER BY created_at DESC, id DESC`, strings.TrimSpace(agentID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AgentSchedule, 0)
	for rows.Next() {
		item, err := scanAgentSchedule(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *PostgresStore) UpdateAgentSchedule(ctx context.Context, id string, input UpdateAgentScheduleInput) (AgentSchedule, error) {
	current, err := s.GetAgentSchedule(ctx, id)
	if err != nil {
		return AgentSchedule{}, err
	}
	name, prompt := current.Name, current.Prompt
	expression, timezone := current.CronExpression, current.Timezone
	enabled := current.Enabled
	sessionMode, targetSessionID, approvalMode := current.SessionMode, current.TargetSessionID, current.ApprovalMode
	if input.Name != nil {
		name = strings.TrimSpace(*input.Name)
	}
	if input.Prompt != nil {
		prompt = strings.TrimSpace(*input.Prompt)
	}
	if input.CronExpression != nil {
		expression = strings.TrimSpace(*input.CronExpression)
	}
	if input.Timezone != nil {
		timezone = strings.TrimSpace(*input.Timezone)
	}
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	if input.SessionMode != nil {
		sessionMode = strings.TrimSpace(*input.SessionMode)
	}
	if input.TargetSessionID != nil {
		targetSessionID = strings.TrimSpace(*input.TargetSessionID)
	}
	if input.ApprovalMode != nil {
		approvalMode = strings.TrimSpace(*input.ApprovalMode)
	}
	sessionMode, targetSessionID, approvalMode, err = NormalizeAgentScheduleModes(sessionMode, targetSessionID, approvalMode)
	if err != nil {
		return AgentSchedule{}, err
	}
	if name == "" || prompt == "" {
		return AgentSchedule{}, fmt.Errorf("%w: name and prompt are required", ErrInvalid)
	}
	expression, timezone, next, err := NormalizeAgentSchedule(expression, timezone, time.Now().UTC())
	if err != nil {
		return AgentSchedule{}, err
	}
	var nextRunAt any = next
	if !enabled {
		nextRunAt = nil
	}
	scope, _ := DatabaseAccessScopeFromContext(ctx)
	tx, _, err := s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
	if err != nil {
		return AgentSchedule{}, err
	}
	defer tx.Rollback()
	environmentID := current.EnvironmentID
	if sessionMode == AgentScheduleSessionExisting {
		var target Session
		if err := tx.QueryRowContext(ctx, `SELECT id,workspace_id,owner_id,agent_id,environment_id,status,archived_at FROM sessions WHERE id=$1`, targetSessionID).
			Scan(&target.ID, &target.WorkspaceID, &target.OwnerID, &target.AgentID, &target.EnvironmentID, &target.Status, &target.ArchivedAt); errors.Is(err, sql.ErrNoRows) {
			return AgentSchedule{}, fmt.Errorf("%w: target session %s", ErrNotFound, targetSessionID)
		} else if err != nil {
			return AgentSchedule{}, err
		}
		if target.WorkspaceID != current.WorkspaceID || (scope.OwnerID != "" && target.OwnerID != scope.OwnerID) {
			return AgentSchedule{}, ErrForbidden
		}
		if target.AgentID != current.AgentID || target.ArchivedAt != nil || target.Status == SessionStatusTerminated {
			return AgentSchedule{}, fmt.Errorf("%w: target session is not active for this agent", ErrInvalid)
		}
		environmentID = target.EnvironmentID
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE agent_schedules SET name=$2, prompt=$3, cron_expression=$4, timezone=$5,
		       enabled=$6, next_run_at=$7, session_mode=$8, target_session_id=NULLIF($9,''),
		       approval_mode=$10, environment_id=$11, updated_at=now()
		WHERE id=$1
	`, id, name, prompt, expression, timezone, enabled, nextRunAt, sessionMode, targetSessionID, approvalMode, environmentID)
	if err != nil {
		return AgentSchedule{}, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return AgentSchedule{}, ErrNotFound
	}
	updated, err := scanAgentSchedule(tx.QueryRowContext(ctx, agentScheduleSelect+` WHERE id=$1`, id))
	if err != nil {
		return AgentSchedule{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentSchedule{}, err
	}
	return updated, nil
}

func (s *PostgresStore) DeleteAgentSchedule(ctx context.Context, id string) error {
	scope, ok := DatabaseAccessScopeFromContext(ctx)
	if !ok {
		return fmt.Errorf("%w: schedule access scope is required", ErrInvalid)
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM agent_schedules WHERE id=$1`, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *PostgresStore) ClaimDueAgentSchedules(ctx context.Context, now time.Time, limit int) ([]AgentScheduleInvocation, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `SELECT workspace_id FROM tma_list_workspace_ids()`)
	if err != nil {
		return nil, err
	}
	workspaceIDs := make([]string, 0)
	for rows.Next() {
		var workspaceID string
		if err := rows.Scan(&workspaceID); err != nil {
			rows.Close()
			return nil, err
		}
		workspaceIDs = append(workspaceIDs, workspaceID)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	claimed := make([]AgentScheduleInvocation, 0, limit)
	for _, workspaceID := range workspaceIDs {
		if len(claimed) >= limit {
			break
		}
		tx, _, err := s.beginDatabaseAccessScope(ctx, workspaceID)
		if err != nil {
			return nil, err
		}
		dueRows, err := tx.QueryContext(ctx, `
			SELECT id FROM agent_schedules
			WHERE enabled AND next_run_at <= $1
			ORDER BY next_run_at, id
			LIMIT $2
		`, now.UTC(), limit-len(claimed))
		if err != nil {
			tx.Rollback()
			return nil, err
		}
		ids := make([]string, 0)
		for dueRows.Next() {
			var id string
			if err := dueRows.Scan(&id); err != nil {
				dueRows.Close()
				tx.Rollback()
				return nil, err
			}
			ids = append(ids, id)
		}
		if err := dueRows.Close(); err != nil {
			tx.Rollback()
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		for _, id := range ids {
			invocation, ok, err := s.claimAgentSchedule(ctx, id, workspaceID, now.UTC())
			if err != nil {
				return nil, err
			}
			if ok {
				claimed = append(claimed, invocation)
			}
		}
	}
	return claimed, nil
}

func (s *PostgresStore) claimAgentSchedule(ctx context.Context, id, workspaceID string, now time.Time) (AgentScheduleInvocation, bool, error) {
	tx, _, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return AgentScheduleInvocation{}, false, err
	}
	defer tx.Rollback()
	item, err := scanAgentSchedule(tx.QueryRowContext(ctx, agentScheduleSelect+` WHERE id=$1 AND enabled AND next_run_at <= $2 FOR UPDATE`, id, now))
	if errors.Is(err, sql.ErrNoRows) {
		return AgentScheduleInvocation{}, false, nil
	}
	if err != nil {
		return AgentScheduleInvocation{}, false, err
	}
	scheduledFor := *item.NextRunAt
	_, _, next, err := NormalizeAgentSchedule(item.CronExpression, item.Timezone, now)
	if err != nil {
		return AgentScheduleInvocation{}, false, err
	}
	runID, err := nextSequenceID(ctx, tx, "asrun", "tma_agent_schedule_run_id_seq")
	if err != nil {
		return AgentScheduleInvocation{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE agent_schedules SET next_run_at=$2, last_run_at=$3, last_run_status=$4, last_error='', updated_at=now() WHERE id=$1;
	`, id, next, scheduledFor, AgentScheduleRunPending); err != nil {
		return AgentScheduleInvocation{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_schedule_runs(id,schedule_id,workspace_id,scheduled_for,status) VALUES($1,$2,$3,$4,$5)
	`, runID, id, workspaceID, scheduledFor, AgentScheduleRunPending); err != nil {
		return AgentScheduleInvocation{}, false, err
	}
	item.NextRunAt = &next
	item.LastRunAt = &scheduledFor
	item.LastRunStatus = AgentScheduleRunPending
	if err := tx.Commit(); err != nil {
		return AgentScheduleInvocation{}, false, err
	}
	return AgentScheduleInvocation{RunID: runID, ScheduledFor: scheduledFor, Schedule: item}, true, nil
}

func (s *PostgresStore) ClaimRunnableAgentScheduleRuns(ctx context.Context, now time.Time, limit int, leaseDuration time.Duration) ([]AgentScheduleInvocation, error) {
	if limit <= 0 {
		limit = 20
	}
	if leaseDuration <= 0 {
		leaseDuration = time.Minute
	}
	rows, err := s.db.QueryContext(ctx, `SELECT workspace_id FROM tma_list_workspace_ids()`)
	if err != nil {
		return nil, err
	}
	workspaceIDs := make([]string, 0)
	for rows.Next() {
		var workspaceID string
		if err := rows.Scan(&workspaceID); err != nil {
			rows.Close()
			return nil, err
		}
		workspaceIDs = append(workspaceIDs, workspaceID)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	claimed := make([]AgentScheduleInvocation, 0, limit)
	for _, workspaceID := range workspaceIDs {
		if len(claimed) >= limit {
			break
		}
		tx, _, err := s.beginDatabaseAccessScope(ctx, workspaceID)
		if err != nil {
			return nil, err
		}
		candidateLimit := (limit - len(claimed)) * 10
		if candidateLimit < 20 {
			candidateLimit = 20
		}
		if candidateLimit > 200 {
			candidateLimit = 200
		}
		candidateRows, err := tx.QueryContext(ctx, `
			SELECT r.id
			FROM agent_schedule_runs r
			WHERE r.status IN ('pending','waiting_session')
			   OR (r.status='dispatching' AND r.lease_expires_at <= $1)
			ORDER BY r.scheduled_for, r.id
			LIMIT $2
		`, now.UTC(), candidateLimit)
		if err != nil {
			tx.Rollback()
			return nil, err
		}
		ids := make([]string, 0)
		for candidateRows.Next() {
			var id string
			if err := candidateRows.Scan(&id); err != nil {
				candidateRows.Close()
				tx.Rollback()
				return nil, err
			}
			ids = append(ids, id)
		}
		if err := candidateRows.Close(); err != nil {
			tx.Rollback()
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		for _, id := range ids {
			if len(claimed) >= limit {
				break
			}
			invocation, ok, err := s.claimAgentScheduleRunInWorkspace(ctx, workspaceID, id, now.UTC(), leaseDuration)
			if err != nil {
				return nil, err
			}
			if ok {
				claimed = append(claimed, invocation)
			}
		}
	}
	return claimed, nil
}

func (s *PostgresStore) ClaimAgentScheduleRun(ctx context.Context, runID string, now time.Time, leaseDuration time.Duration) (AgentScheduleInvocation, bool, error) {
	scope, ok := DatabaseAccessScopeFromContext(ctx)
	if !ok {
		return AgentScheduleInvocation{}, false, fmt.Errorf("%w: schedule access scope is required", ErrInvalid)
	}
	return s.claimAgentScheduleRunInWorkspace(ctx, scope.WorkspaceID, strings.TrimSpace(runID), now.UTC(), leaseDuration)
}

func (s *PostgresStore) claimAgentScheduleRunInWorkspace(ctx context.Context, workspaceID, runID string, now time.Time, leaseDuration time.Duration) (AgentScheduleInvocation, bool, error) {
	if leaseDuration <= 0 {
		leaseDuration = time.Minute
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return AgentScheduleInvocation{}, false, err
	}
	defer tx.Rollback()
	var scheduleID string
	var scheduledFor time.Time
	err = tx.QueryRowContext(ctx, `
		UPDATE agent_schedule_runs r
		SET status='dispatching', lease_expires_at=$3, error_message='', updated_at=$2
		FROM agent_schedules s
		WHERE r.id=$1 AND r.schedule_id=s.id
		  AND (r.status IN ('pending','waiting_session') OR (r.status='dispatching' AND r.lease_expires_at <= $2))
		  AND (
		    (
		      s.session_mode='new_session'
		      AND NOT EXISTS (
		        SELECT 1 FROM agent_schedule_runs earlier
		        WHERE earlier.schedule_id=r.schedule_id
		          AND (earlier.scheduled_for, earlier.id) < (r.scheduled_for, r.id)
		          AND earlier.status IN ('pending','waiting_session','dispatching')
		      )
		      AND NOT EXISTS (
		        SELECT 1 FROM agent_schedule_runs previous
		        JOIN sessions previous_session ON previous_session.id=previous.session_id
		        WHERE previous.schedule_id=r.schedule_id
		          AND (previous.scheduled_for, previous.id) < (r.scheduled_for, r.id)
		          AND previous.status='dispatched'
		          AND previous_session.status NOT IN ('idle','failed','terminated')
		      )
		    )
		    OR
		    (
		      s.session_mode='existing_session'
		      AND EXISTS (
		        SELECT 1 FROM sessions target
		        WHERE target.id=s.target_session_id AND target.status='idle' AND target.archived_at IS NULL
		      )
		      AND NOT EXISTS (
		        SELECT 1
		        FROM agent_schedule_runs earlier
		        JOIN agent_schedules earlier_schedule ON earlier_schedule.id=earlier.schedule_id
		        WHERE earlier_schedule.target_session_id=s.target_session_id
		          AND (earlier.scheduled_for, earlier.id) < (r.scheduled_for, r.id)
		          AND earlier.status IN ('pending','waiting_session','dispatching')
		      )
		    )
		  )
		RETURNING r.schedule_id, r.scheduled_for
	`, runID, now, now.Add(leaseDuration)).Scan(&scheduleID, &scheduledFor)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentScheduleInvocation{}, false, nil
	}
	if err != nil {
		return AgentScheduleInvocation{}, false, err
	}
	schedule, err := scanAgentSchedule(tx.QueryRowContext(ctx, agentScheduleSelect+` WHERE id=$1`, scheduleID))
	if err != nil {
		return AgentScheduleInvocation{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_schedules SET last_run_status=$2,last_error='',updated_at=$3 WHERE id=$1`, scheduleID, AgentScheduleRunDispatching, now); err != nil {
		return AgentScheduleInvocation{}, false, err
	}
	schedule.LastRunStatus = AgentScheduleRunDispatching
	if err := tx.Commit(); err != nil {
		return AgentScheduleInvocation{}, false, err
	}
	return AgentScheduleInvocation{RunID: runID, ScheduledFor: scheduledFor, Schedule: schedule}, true, nil
}

func (s *PostgresStore) StartAgentScheduleNow(ctx context.Context, id string, now time.Time) (AgentScheduleInvocation, error) {
	current, err := s.GetAgentSchedule(ctx, id)
	if err != nil {
		return AgentScheduleInvocation{}, err
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, current.WorkspaceID)
	if err != nil {
		return AgentScheduleInvocation{}, err
	}
	defer tx.Rollback()
	item, err := scanAgentSchedule(tx.QueryRowContext(ctx, agentScheduleSelect+` WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return AgentScheduleInvocation{}, err
	}
	runID, err := nextSequenceID(ctx, tx, "asrun", "tma_agent_schedule_run_id_seq")
	if err != nil {
		return AgentScheduleInvocation{}, err
	}
	now = now.UTC().Truncate(time.Microsecond)
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_schedule_runs(id,schedule_id,workspace_id,scheduled_for,status) VALUES($1,$2,$3,$4,$5)`, runID, id, item.WorkspaceID, now, AgentScheduleRunPending); err != nil {
		return AgentScheduleInvocation{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_schedules SET last_run_at=$2,last_run_status=$3,last_error='',updated_at=now() WHERE id=$1`, id, now, AgentScheduleRunPending); err != nil {
		return AgentScheduleInvocation{}, err
	}
	item.LastRunAt = &now
	item.LastRunStatus = AgentScheduleRunPending
	if err := tx.Commit(); err != nil {
		return AgentScheduleInvocation{}, err
	}
	return AgentScheduleInvocation{RunID: runID, ScheduledFor: now, Schedule: item}, nil
}

func (s *PostgresStore) DeferAgentScheduleRun(ctx context.Context, runID string, scheduleID string) error {
	scope, ok := DatabaseAccessScopeFromContext(ctx)
	if !ok {
		return fmt.Errorf("%w: schedule access scope is required", ErrInvalid)
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE agent_schedule_runs SET status=$3,lease_expires_at=NULL,updated_at=now()
		WHERE id=$1 AND schedule_id=$2 AND status IN ('pending','waiting_session','dispatching')
	`, runID, scheduleID, AgentScheduleRunWaitingSession)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_schedules SET last_run_status=$2,updated_at=now() WHERE id=$1`, scheduleID, AgentScheduleRunWaitingSession); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) ReconcileInvalidAgentScheduleRuns(ctx context.Context, now time.Time) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace_id FROM tma_list_workspace_ids()`)
	if err != nil {
		return 0, err
	}
	workspaceIDs := make([]string, 0)
	for rows.Next() {
		var workspaceID string
		if err := rows.Scan(&workspaceID); err != nil {
			rows.Close()
			return 0, err
		}
		workspaceIDs = append(workspaceIDs, workspaceID)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	total := 0
	for _, workspaceID := range workspaceIDs {
		tx, _, err := s.beginDatabaseAccessScope(ctx, workspaceID)
		if err != nil {
			return total, err
		}
		invalidRows, err := tx.QueryContext(ctx, `
			SELECT s.id
			FROM agent_schedules s
			LEFT JOIN sessions target ON target.id=s.target_session_id
			WHERE s.session_mode='existing_session'
			  AND (target.id IS NULL OR target.archived_at IS NOT NULL OR target.status='terminated')
			  AND (s.enabled OR EXISTS (
			    SELECT 1 FROM agent_schedule_runs queued
			    WHERE queued.schedule_id=s.id AND queued.status IN ('pending','waiting_session','dispatching')
			  ))
			FOR UPDATE OF s
		`)
		if err != nil {
			tx.Rollback()
			return total, err
		}
		ids := make([]string, 0)
		for invalidRows.Next() {
			var id string
			if err := invalidRows.Scan(&id); err != nil {
				invalidRows.Close()
				tx.Rollback()
				return total, err
			}
			ids = append(ids, id)
		}
		if err := invalidRows.Close(); err != nil {
			tx.Rollback()
			return total, err
		}
		for _, id := range ids {
			reason := "bound session is unavailable; schedule paused"
			if _, err := tx.ExecContext(ctx, `UPDATE agent_schedules SET enabled=false,next_run_at=NULL,last_run_status=$2,last_error=$3,updated_at=$4 WHERE id=$1`, id, AgentScheduleRunFailed, reason, now); err != nil {
				tx.Rollback()
				return total, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE agent_schedule_runs SET status=$2,lease_expires_at=NULL,error_message=$3,updated_at=$4 WHERE schedule_id=$1 AND status IN ('pending','waiting_session','dispatching')`, id, AgentScheduleRunFailed, reason, now); err != nil {
				tx.Rollback()
				return total, err
			}
		}
		if err := tx.Commit(); err != nil {
			return total, err
		}
		total += len(ids)
	}
	return total, nil
}

func (s *PostgresStore) CompleteAgentScheduleRun(ctx context.Context, input CompleteAgentScheduleRunInput) error {
	if input.Status != AgentScheduleRunDispatched && input.Status != AgentScheduleRunFailed {
		return fmt.Errorf("%w: invalid schedule run status", ErrInvalid)
	}
	scope, ok := DatabaseAccessScopeFromContext(ctx)
	if !ok {
		return fmt.Errorf("%w: schedule access scope is required", ErrInvalid)
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE agent_schedule_runs SET status=$3,session_id=NULLIF($4,''),lease_expires_at=NULL,error_message=$5,updated_at=now()
		WHERE id=$1 AND schedule_id=$2
	`, input.RunID, input.ScheduleID, input.Status, input.SessionID, input.Error)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE agent_schedules SET last_session_id=NULLIF($2,''),last_run_status=$3,last_error=$4,updated_at=now() WHERE id=$1
	`, input.ScheduleID, input.SessionID, input.Status, input.Error); err != nil {
		return err
	}
	return tx.Commit()
}

type agentScheduleScanner interface{ Scan(...any) error }

func scanAgentSchedule(scanner agentScheduleScanner) (AgentSchedule, error) {
	var item AgentSchedule
	err := scanner.Scan(&item.ID, &item.WorkspaceID, &item.OwnerID, &item.AgentID, &item.EnvironmentID,
		&item.SessionMode, &item.TargetSessionID, &item.ApprovalMode,
		&item.Name, &item.Prompt, &item.CronExpression, &item.Timezone, &item.Enabled,
		&item.NextRunAt, &item.LastRunAt, &item.LastSessionID, &item.LastRunStatus, &item.LastError,
		&item.CreatedBy, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}
