package managedagents

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (s *PostgresStore) beginTaskGroupScopeTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, _, err := setContextDatabaseAccessScope(ctx, tx); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (s *PostgresStore) CreateSubagentTaskGroupContext(ctx context.Context, input CreateSubagentTaskGroupInput) (SubagentTaskGroup, error) {
	strategy := normalizeSubagentTaskGroupStrategy(input.Strategy)
	if strategy == "" {
		return SubagentTaskGroup{}, fmt.Errorf("%w: unsupported task group strategy %q", ErrInvalid, input.Strategy)
	}
	reducer := normalizeSubagentTaskGroupReducer(input.ResultReducer)
	if reducer == "" {
		return SubagentTaskGroup{}, fmt.Errorf("%w: unsupported task group reducer %q", ErrInvalid, input.ResultReducer)
	}
	if strings.TrimSpace(input.ParentSessionID) == "" {
		return SubagentTaskGroup{}, fmt.Errorf("%w: parent_session_id is required", ErrInvalid)
	}
	if input.PlannedCount <= 0 {
		return SubagentTaskGroup{}, fmt.Errorf("%w: planned_count must be positive", ErrInvalid)
	}
	if strategy == SubagentTaskGroupStrategyQuorum && (input.Quorum <= 0 || input.Quorum > input.PlannedCount) {
		return SubagentTaskGroup{}, fmt.Errorf("%w: quorum must be between 1 and planned_count", ErrInvalid)
	}
	tx, err := s.beginTaskGroupScopeTx(ctx)
	if err != nil {
		return SubagentTaskGroup{}, err
	}
	defer tx.Rollback()
	parent, err := getSessionTx(ctx, tx, strings.TrimSpace(input.ParentSessionID))
	if err != nil {
		return SubagentTaskGroup{}, err
	}
	input.WorkspaceID = parent.WorkspaceID
	input.OwnerID = parent.OwnerID
	id, err := nextSequenceID(ctx, tx, "sgrp", "tma_subagent_task_group_id_seq")
	if err != nil {
		return SubagentTaskGroup{}, err
	}
	group := SubagentTaskGroup{
		ID: id, WorkspaceID: parent.WorkspaceID, OwnerID: parent.OwnerID,
		ParentSessionID: parent.ID, ParentTurnID: strings.TrimSpace(input.ParentTurnID),
		ParentGroupID: strings.TrimSpace(input.ParentGroupID), ParentItemIndex: input.ParentItemIndex,
		Strategy: strategy, ResultReducer: reducer, Quorum: input.Quorum, FailFast: input.FailFast,
		PlannedCount: input.PlannedCount, CreatedAt: time.Now().UTC(),
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO subagent_task_groups (
			id, workspace_id, owner_id, parent_session_id, parent_turn_id, parent_group_id, parent_item_index, strategy, result_reducer, quorum, fail_fast, planned_count, created_at, cancel_reason
		) VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, $9, $10, $11, $12, $13, '')
	`, group.ID, group.WorkspaceID, group.OwnerID, group.ParentSessionID, group.ParentTurnID, group.ParentGroupID, group.ParentItemIndex, group.Strategy, group.ResultReducer, group.Quorum, group.FailFast, group.PlannedCount, group.CreatedAt); err != nil {
		return SubagentTaskGroup{}, err
	}
	if err := tx.Commit(); err != nil {
		return SubagentTaskGroup{}, err
	}
	return group, nil
}

func (s *PostgresStore) AppendSubagentTaskGroupItemContext(ctx context.Context, groupID string, input AppendSubagentTaskGroupItemInput) (SubagentTaskGroupItem, error) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" || input.ItemIndex < 0 {
		return SubagentTaskGroupItem{}, fmt.Errorf("%w: valid group_id and item_index are required", ErrInvalid)
	}
	if strings.TrimSpace(input.AgentID) == "" || strings.TrimSpace(input.EnvironmentID) == "" {
		return SubagentTaskGroupItem{}, fmt.Errorf("%w: agent_id and environment_id are required", ErrInvalid)
	}
	state := normalizeSubagentTaskGroupItemState(input.InitialState)
	if state == "" {
		return SubagentTaskGroupItem{}, fmt.Errorf("%w: unsupported initial_state %q", ErrInvalid, input.InitialState)
	}
	item := SubagentTaskGroupItem{
		GroupID: groupID, ItemIndex: input.ItemIndex, AgentID: strings.TrimSpace(input.AgentID),
		EnvironmentID: strings.TrimSpace(input.EnvironmentID), SessionID: strings.TrimSpace(input.SessionID),
		Title: strings.TrimSpace(input.Title), Message: strings.TrimSpace(input.Message), Priority: input.Priority,
		InitialState: state, ErrorType: strings.TrimSpace(input.ErrorType), ErrorMessage: strings.TrimSpace(input.ErrorMessage),
		ExpectedResultSchema: cloneRaw(input.ExpectedResultSchema), CreatedAt: time.Now().UTC(),
	}
	tx, err := s.beginTaskGroupScopeTx(ctx)
	if err != nil {
		return SubagentTaskGroupItem{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO subagent_task_group_items (
			group_id, item_index, agent_id, environment_id, session_id, title, message, priority, initial_state, error_type, error_message, expected_result_schema, retry_count, created_at
		) VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9, $10, $11, $12, 0, $13)
	`, item.GroupID, item.ItemIndex, item.AgentID, item.EnvironmentID, item.SessionID, item.Title, item.Message, item.Priority, item.InitialState, item.ErrorType, item.ErrorMessage, metadataJSON(item.ExpectedResultSchema), item.CreatedAt); err != nil {
		return SubagentTaskGroupItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return SubagentTaskGroupItem{}, err
	}
	return item, nil
}

func (s *PostgresStore) UpdateSubagentTaskGroupItemContext(ctx context.Context, groupID string, itemIndex int, input UpdateSubagentTaskGroupItemInput) (SubagentTaskGroupItem, error) {
	state := normalizeSubagentTaskGroupItemState(input.InitialState)
	if strings.TrimSpace(groupID) == "" || itemIndex < 0 || state == "" {
		return SubagentTaskGroupItem{}, fmt.Errorf("%w: valid group_id, item_index, and initial_state are required", ErrInvalid)
	}
	tx, err := s.beginTaskGroupScopeTx(ctx)
	if err != nil {
		return SubagentTaskGroupItem{}, err
	}
	defer tx.Rollback()
	item, err := scanSubagentTaskGroupItem(tx.QueryRowContext(ctx, `
		UPDATE subagent_task_group_items
		SET session_id = NULLIF($3, ''), title = $4, message = $5, priority = $6, initial_state = $7,
			error_type = $8, error_message = $9, expected_result_schema = $10,
			retry_count = retry_count + CASE WHEN $11 THEN 1 ELSE 0 END, created_at = $12
		WHERE group_id = $1 AND item_index = $2
		RETURNING group_id, item_index, agent_id, environment_id, COALESCE(session_id, ''), title, message, priority, initial_state, error_type, error_message, expected_result_schema, retry_count, created_at
	`, strings.TrimSpace(groupID), itemIndex, strings.TrimSpace(input.SessionID), strings.TrimSpace(input.Title), strings.TrimSpace(input.Message), input.Priority, state, strings.TrimSpace(input.ErrorType), strings.TrimSpace(input.ErrorMessage), metadataJSON(input.ExpectedResultSchema), input.IncrementRetry, time.Now().UTC()))
	if err == sql.ErrNoRows {
		return SubagentTaskGroupItem{}, ErrNotFound
	}
	if err != nil {
		return SubagentTaskGroupItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return SubagentTaskGroupItem{}, err
	}
	return item, nil
}

func (s *PostgresStore) GetSubagentTaskGroupContext(ctx context.Context, id string) (SubagentTaskGroup, error) {
	tx, err := s.beginTaskGroupScopeTx(ctx)
	if err != nil {
		return SubagentTaskGroup{}, err
	}
	defer tx.Rollback()
	group, err := scanSubagentTaskGroup(tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, owner_id, parent_session_id, parent_turn_id, COALESCE(parent_group_id, ''), parent_item_index, strategy, result_reducer, quorum, fail_fast, planned_count, created_at, canceled_at, cancel_reason
		FROM subagent_task_groups WHERE id = $1
	`, strings.TrimSpace(id)))
	if err == sql.ErrNoRows {
		return SubagentTaskGroup{}, ErrNotFound
	}
	return group, err
}

func (s *PostgresStore) ListSubagentTaskGroupsByParentSessionContext(ctx context.Context, parentSessionID string) ([]SubagentTaskGroup, error) {
	return s.listSubagentTaskGroupsContext(ctx, `parent_session_id = $1`, []any{strings.TrimSpace(parentSessionID)}, `created_at DESC, id DESC`)
}

func (s *PostgresStore) ListChildSubagentTaskGroupsContext(ctx context.Context, parentGroupID string, parentItemIndex int) ([]SubagentTaskGroup, error) {
	return s.listSubagentTaskGroupsContext(ctx, `parent_group_id = $1 AND parent_item_index = $2`, []any{strings.TrimSpace(parentGroupID), parentItemIndex}, `created_at ASC, id ASC`)
}

func (s *PostgresStore) listSubagentTaskGroupsContext(ctx context.Context, where string, args []any, order string) ([]SubagentTaskGroup, error) {
	tx, err := s.beginTaskGroupScopeTx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT id, workspace_id, owner_id, parent_session_id, parent_turn_id, COALESCE(parent_group_id, ''), parent_item_index, strategy, result_reducer, quorum, fail_fast, planned_count, created_at, canceled_at, cancel_reason
		FROM subagent_task_groups WHERE `+where+` ORDER BY `+order, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := make([]SubagentTaskGroup, 0)
	for rows.Next() {
		group, err := scanSubagentTaskGroup(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func (s *PostgresStore) GetSubagentTaskGroupItemBySessionContext(ctx context.Context, sessionID string) (SubagentTaskGroupItem, error) {
	tx, err := s.beginTaskGroupScopeTx(ctx)
	if err != nil {
		return SubagentTaskGroupItem{}, err
	}
	defer tx.Rollback()
	item, err := scanSubagentTaskGroupItem(tx.QueryRowContext(ctx, `
		SELECT group_id, item_index, agent_id, environment_id, COALESCE(session_id, ''), title, message, priority, initial_state, error_type, error_message, expected_result_schema, retry_count, created_at
		FROM subagent_task_group_items WHERE session_id = $1 ORDER BY created_at DESC LIMIT 1
	`, strings.TrimSpace(sessionID)))
	if err == sql.ErrNoRows {
		return SubagentTaskGroupItem{}, ErrNotFound
	}
	return item, err
}

func (s *PostgresStore) ListSubagentTaskGroupItemsContext(ctx context.Context, groupID string) ([]SubagentTaskGroupItem, error) {
	tx, err := s.beginTaskGroupScopeTx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT group_id, item_index, agent_id, environment_id, COALESCE(session_id, ''), title, message, priority, initial_state, error_type, error_message, expected_result_schema, retry_count, created_at
		FROM subagent_task_group_items WHERE group_id = $1 ORDER BY item_index ASC
	`, strings.TrimSpace(groupID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]SubagentTaskGroupItem, 0)
	for rows.Next() {
		item, err := scanSubagentTaskGroupItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
