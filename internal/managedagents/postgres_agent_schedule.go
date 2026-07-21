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
	SELECT id, workspace_id, owner_id, agent_id, environment_id, name, prompt,
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
	if input.AgentID == "" || input.EnvironmentID == "" || input.Name == "" || input.Prompt == "" {
		return AgentSchedule{}, fmt.Errorf("%w: agent_id, environment_id, name, and prompt are required", ErrInvalid)
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
	id, err := nextSequenceID(ctx, tx, "asch", "tma_agent_schedule_id_seq")
	if err != nil {
		return AgentSchedule{}, err
	}
	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_schedules (
			id, workspace_id, owner_id, agent_id, environment_id, name, prompt,
			cron_expression, timezone, enabled, next_run_at, created_by, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13)
	`, id, scope.WorkspaceID, input.OwnerID, input.AgentID, input.EnvironmentID, input.Name, input.Prompt,
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
	result, err := tx.ExecContext(ctx, `
		UPDATE agent_schedules SET name=$2, prompt=$3, cron_expression=$4, timezone=$5,
		       enabled=$6, next_run_at=$7, updated_at=now()
		WHERE id=$1
	`, id, name, prompt, expression, timezone, enabled, nextRunAt)
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
		UPDATE agent_schedule_runs SET status=$3,session_id=NULLIF($4,''),error_message=$5,updated_at=now()
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
		&item.Name, &item.Prompt, &item.CronExpression, &item.Timezone, &item.Enabled,
		&item.NextRunAt, &item.LastRunAt, &item.LastSessionID, &item.LastRunStatus, &item.LastError,
		&item.CreatedBy, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}
