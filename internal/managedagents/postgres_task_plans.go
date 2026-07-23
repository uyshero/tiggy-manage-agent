package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *PostgresStore) CreateSessionTaskPlanContext(ctx context.Context, sessionID string, input CreateSessionTaskPlanInput) (SessionTaskPlanResult, error) {
	input, err := validateCreateTaskPlanInput(sessionID, input)
	if err != nil {
		return SessionTaskPlanResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SessionTaskPlanResult{}, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return SessionTaskPlanResult{}, err
	}
	session, err := getSessionForUpdateTx(ctx, tx, sessionID)
	if err != nil {
		return SessionTaskPlanResult{}, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return SessionTaskPlanResult{}, err
	}
	if session.Status == SessionStatusTerminated {
		return SessionTaskPlanResult{}, ErrTerminated
	}

	now := time.Now().UTC()
	events := make([]Event, 0, 2)
	var previousID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM session_task_plans WHERE session_id = $1 AND status = $2 FOR UPDATE`, sessionID, TaskPlanStatusActive).Scan(&previousID)
	if err != nil && err != sql.ErrNoRows {
		return SessionTaskPlanResult{}, err
	}
	if previousID != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE session_task_plans SET status = $2, updated_turn_id = $3, updated_at = $4, completed_at = $4 WHERE id = $1`, previousID, TaskPlanStatusSuperseded, input.TurnID, now); err != nil {
			return SessionTaskPlanResult{}, err
		}
		payload, err := json.Marshal(map[string]any{"turn_id": input.TurnID, "plan_id": previousID, "status": TaskPlanStatusSuperseded})
		if err != nil {
			return SessionTaskPlanResult{}, err
		}
		event, err := s.appendEventTx(ctx, tx, sessionID, EventRuntimeTaskPlanSuperseded, payload, now)
		if err != nil {
			return SessionTaskPlanResult{}, err
		}
		events = append(events, event)
	}

	planID, err := nextSequenceID(ctx, tx, "plan", "tma_task_plan_id_seq")
	if err != nil {
		return SessionTaskPlanResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO session_task_plans (
			id, workspace_id, owner_id, session_id, created_turn_id, updated_turn_id,
			title, goal, handling_mode, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $5, $6, $7, $8, $9, $10, $10)
	`, planID, session.WorkspaceID, session.OwnerID, sessionID, input.TurnID, input.Title, input.Goal, input.HandlingMode, TaskPlanStatusActive, now); err != nil {
		return SessionTaskPlanResult{}, err
	}
	for index, description := range input.Items {
		itemID, err := nextSequenceID(ctx, tx, "task", "tma_task_item_id_seq")
		if err != nil {
			return SessionTaskPlanResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO session_task_items (id, plan_id, item_index, description, status, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $6)
		`, itemID, planID, index, description, TaskItemStatusPending, now); err != nil {
			return SessionTaskPlanResult{}, err
		}
	}
	plan, err := getSessionTaskPlanTx(ctx, tx, sessionID, planID, false)
	if err != nil {
		return SessionTaskPlanResult{}, err
	}
	payload, err := taskPlanEventPayload(plan, input.TurnID, "")
	if err != nil {
		return SessionTaskPlanResult{}, err
	}
	event, err := s.appendEventTx(ctx, tx, sessionID, EventRuntimeTaskPlanCreated, payload, now)
	if err != nil {
		return SessionTaskPlanResult{}, err
	}
	events = append(events, event)
	if err := tx.Commit(); err != nil {
		return SessionTaskPlanResult{}, err
	}
	return SessionTaskPlanResult{Plan: plan, Events: events}, nil
}

func (s *PostgresStore) GetCurrentSessionTaskPlanContext(ctx context.Context, sessionID string) (SessionTaskPlan, error) {
	if strings.TrimSpace(sessionID) == "" {
		return SessionTaskPlan{}, fmt.Errorf("%w: task plan session_id is required", ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SessionTaskPlan{}, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return SessionTaskPlan{}, err
	}
	session, err := getSessionTx(ctx, tx, sessionID)
	if err != nil {
		return SessionTaskPlan{}, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return SessionTaskPlan{}, err
	}
	plan, err := getSessionTaskPlanTx(ctx, tx, sessionID, "", false)
	if err != nil {
		return SessionTaskPlan{}, err
	}
	if err := tx.Commit(); err != nil {
		return SessionTaskPlan{}, err
	}
	return plan, nil
}

func (s *PostgresStore) ListSessionTaskPlansContext(ctx context.Context, sessionID string) ([]SessionTaskPlan, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("%w: task plan session_id is required", ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return nil, err
	}
	session, err := getSessionTx(ctx, tx, sessionID)
	if err != nil {
		return nil, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, workspace_id, owner_id, session_id, created_turn_id, updated_turn_id,
			title, goal, handling_mode, status, created_at, updated_at, completed_at
		FROM session_task_plans
		WHERE session_id = $1
		ORDER BY created_at DESC, id DESC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	plans := make([]SessionTaskPlan, 0)
	for rows.Next() {
		plan, err := scanSessionTaskPlan(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		plans = append(plans, plan)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range plans {
		if err := loadSessionTaskItemsTx(ctx, tx, &plans[index]); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return plans, nil
}

func (s *PostgresStore) UpdateSessionTaskItemsContext(ctx context.Context, sessionID string, input UpdateSessionTaskItemsInput) (SessionTaskPlanResult, error) {
	input, err := validateUpdateTaskItemsInput(sessionID, input)
	if err != nil {
		return SessionTaskPlanResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SessionTaskPlanResult{}, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return SessionTaskPlanResult{}, err
	}
	session, err := getSessionForUpdateTx(ctx, tx, sessionID)
	if err != nil {
		return SessionTaskPlanResult{}, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return SessionTaskPlanResult{}, err
	}
	plan, err := getSessionTaskPlanTx(ctx, tx, sessionID, input.PlanID, true)
	if err != nil {
		return SessionTaskPlanResult{}, err
	}
	if plan.Status != TaskPlanStatusActive {
		return SessionTaskPlanResult{}, fmt.Errorf("%w: task plan %s is %s", ErrConflict, plan.ID, plan.Status)
	}
	byID := make(map[string]SessionTaskItem, len(plan.Items))
	for _, item := range plan.Items {
		byID[item.ID] = item
	}
	seen := map[string]bool{}
	for _, update := range input.Items {
		if seen[update.ItemID] {
			return SessionTaskPlanResult{}, fmt.Errorf("%w: duplicate task item %s", ErrInvalid, update.ItemID)
		}
		seen[update.ItemID] = true
		item, ok := byID[update.ItemID]
		if !ok {
			return SessionTaskPlanResult{}, fmt.Errorf("%w: task item %s does not belong to plan %s", ErrInvalid, update.ItemID, plan.ID)
		}
		previousStatus := item.Status
		item.Status = update.Status
		if update.Evidence != "" {
			item.Evidence = update.Evidence
		}
		if len(update.EvidenceRefs) > 0 {
			refs, err := resolveTaskEvidenceRefsTx(ctx, tx, sessionID, input.TurnID, plan.CreatedAt, update.EvidenceRefs)
			if err != nil {
				return SessionTaskPlanResult{}, fmt.Errorf("%w: task item %s evidence: %v", ErrInvalid, item.ID, err)
			}
			item.EvidenceRefs = refs
		}
		if item.Status != TaskItemStatusCompleted {
			item.EvidenceRefs = []TaskEvidenceRef{}
		}
		if item.Status == TaskItemStatusCompleted && previousStatus == TaskItemStatusCompleted && len(update.EvidenceRefs) == 0 {
			return SessionTaskPlanResult{}, fmt.Errorf("%w: completed task item %s updates require fresh evidence_refs", ErrInvalid, item.ID)
		}
		if item.Status == TaskItemStatusCompleted && (strings.TrimSpace(item.Evidence) == "" || len(item.EvidenceRefs) == 0) {
			return SessionTaskPlanResult{}, fmt.Errorf("%w: completed task item %s requires evidence text and a verified tool result reference", ErrInvalid, item.ID)
		}
		byID[item.ID] = item
	}
	inProgress := 0
	for _, item := range byID {
		if item.Status == TaskItemStatusInProgress {
			inProgress++
		}
	}
	if inProgress > 1 {
		return SessionTaskPlanResult{}, fmt.Errorf("%w: task plan may have at most one in_progress item", ErrInvalid)
	}

	now := time.Now().UTC()
	for _, update := range input.Items {
		item := byID[update.ItemID]
		if item.EvidenceRefs == nil {
			item.EvidenceRefs = []TaskEvidenceRef{}
		}
		evidenceRefs, err := json.Marshal(item.EvidenceRefs)
		if err != nil {
			return SessionTaskPlanResult{}, fmt.Errorf("encode task item evidence refs: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE session_task_items
			SET status = $2, evidence = $3, evidence_refs = $4, updated_at = $5,
				completed_at = CASE WHEN $2 = 'completed' THEN COALESCE(completed_at, $5) ELSE NULL END
			WHERE id = $1 AND plan_id = $6
		`, item.ID, item.Status, item.Evidence, evidenceRefs, now, plan.ID); err != nil {
			return SessionTaskPlanResult{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE session_task_plans SET updated_turn_id = $2, updated_at = $3 WHERE id = $1`, plan.ID, input.TurnID, now); err != nil {
		return SessionTaskPlanResult{}, err
	}
	plan, err = getSessionTaskPlanTx(ctx, tx, sessionID, plan.ID, false)
	if err != nil {
		return SessionTaskPlanResult{}, err
	}
	payload, err := taskPlanEventPayload(plan, input.TurnID, "")
	if err != nil {
		return SessionTaskPlanResult{}, err
	}
	event, err := s.appendEventTx(ctx, tx, sessionID, EventRuntimeTaskItemsUpdated, payload, now)
	if err != nil {
		return SessionTaskPlanResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SessionTaskPlanResult{}, err
	}
	return SessionTaskPlanResult{Plan: plan, Events: []Event{event}}, nil
}

func (s *PostgresStore) CompleteSessionTaskPlanContext(ctx context.Context, sessionID string, input FinishSessionTaskPlanInput) (SessionTaskPlanResult, error) {
	return s.finishSessionTaskPlanContext(ctx, sessionID, input, TaskPlanStatusCompleted, EventRuntimeTaskPlanCompleted)
}

func (s *PostgresStore) CancelSessionTaskPlanContext(ctx context.Context, sessionID string, input FinishSessionTaskPlanInput) (SessionTaskPlanResult, error) {
	return s.finishSessionTaskPlanContext(ctx, sessionID, input, TaskPlanStatusCanceled, EventRuntimeTaskPlanCanceled)
}

func (s *PostgresStore) finishSessionTaskPlanContext(ctx context.Context, sessionID string, input FinishSessionTaskPlanInput, status string, eventType string) (SessionTaskPlanResult, error) {
	if strings.TrimSpace(sessionID) == "" {
		return SessionTaskPlanResult{}, fmt.Errorf("%w: task plan session_id is required", ErrInvalid)
	}
	input.PlanID = strings.TrimSpace(input.PlanID)
	input.TurnID = strings.TrimSpace(input.TurnID)
	input.Reason = strings.TrimSpace(input.Reason)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SessionTaskPlanResult{}, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return SessionTaskPlanResult{}, err
	}
	session, err := getSessionForUpdateTx(ctx, tx, sessionID)
	if err != nil {
		return SessionTaskPlanResult{}, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return SessionTaskPlanResult{}, err
	}
	plan, err := getSessionTaskPlanTx(ctx, tx, sessionID, input.PlanID, true)
	if err != nil {
		return SessionTaskPlanResult{}, err
	}
	if plan.Status != TaskPlanStatusActive {
		return SessionTaskPlanResult{}, fmt.Errorf("%w: task plan %s is %s", ErrConflict, plan.ID, plan.Status)
	}
	if status == TaskPlanStatusCompleted {
		for _, item := range plan.Items {
			if item.Status != TaskItemStatusCompleted || strings.TrimSpace(item.Evidence) == "" || len(item.EvidenceRefs) == 0 {
				return SessionTaskPlanResult{}, fmt.Errorf("%w: all task items require completed status, evidence text, and a verified tool result reference", ErrInvalid)
			}
		}
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		UPDATE session_task_plans
		SET status = $2, updated_turn_id = $3, updated_at = $4, completed_at = $4
		WHERE id = $1
	`, plan.ID, status, input.TurnID, now); err != nil {
		return SessionTaskPlanResult{}, err
	}
	plan, err = getSessionTaskPlanTx(ctx, tx, sessionID, plan.ID, false)
	if err != nil {
		return SessionTaskPlanResult{}, err
	}
	payload, err := taskPlanEventPayload(plan, input.TurnID, input.Reason)
	if err != nil {
		return SessionTaskPlanResult{}, err
	}
	event, err := s.appendEventTx(ctx, tx, sessionID, eventType, payload, now)
	if err != nil {
		return SessionTaskPlanResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SessionTaskPlanResult{}, err
	}
	return SessionTaskPlanResult{Plan: plan, Events: []Event{event}}, nil
}

func validateCreateTaskPlanInput(sessionID string, input CreateSessionTaskPlanInput) (CreateSessionTaskPlanInput, error) {
	if strings.TrimSpace(sessionID) == "" {
		return input, fmt.Errorf("%w: task plan session_id is required", ErrInvalid)
	}
	input.TurnID = strings.TrimSpace(input.TurnID)
	input.Title = strings.TrimSpace(input.Title)
	input.Goal = strings.TrimSpace(input.Goal)
	if input.Goal == "" {
		return input, fmt.Errorf("%w: task plan goal is required", ErrInvalid)
	}
	if len(input.Items) < 3 || len(input.Items) > 10 {
		return input, fmt.Errorf("%w: task plan requires 3-10 items", ErrInvalid)
	}
	seen := map[string]bool{}
	for index := range input.Items {
		input.Items[index] = strings.TrimSpace(input.Items[index])
		if input.Items[index] == "" || seen[input.Items[index]] {
			return input, fmt.Errorf("%w: task plan items must be unique and non-empty", ErrInvalid)
		}
		seen[input.Items[index]] = true
	}
	input.HandlingMode = normalizeTaskPlanMode(input.HandlingMode, len(input.Items))
	if input.HandlingMode == "" {
		return input, fmt.Errorf("%w: unsupported task plan handling_mode", ErrInvalid)
	}
	return input, nil
}

func validateUpdateTaskItemsInput(sessionID string, input UpdateSessionTaskItemsInput) (UpdateSessionTaskItemsInput, error) {
	if strings.TrimSpace(sessionID) == "" {
		return input, fmt.Errorf("%w: task plan session_id is required", ErrInvalid)
	}
	input.TurnID = strings.TrimSpace(input.TurnID)
	input.PlanID = strings.TrimSpace(input.PlanID)
	if len(input.Items) == 0 || len(input.Items) > 10 {
		return input, fmt.Errorf("%w: task item update requires 1-10 items", ErrInvalid)
	}
	for index := range input.Items {
		input.Items[index].ItemID = strings.TrimSpace(input.Items[index].ItemID)
		input.Items[index].Status = normalizeTaskItemStatus(input.Items[index].Status)
		input.Items[index].Evidence = strings.TrimSpace(input.Items[index].Evidence)
		if input.Items[index].ItemID == "" || input.Items[index].Status == "" {
			return input, fmt.Errorf("%w: task item update requires item_id and valid status", ErrInvalid)
		}
		if len(input.Items[index].EvidenceRefs) > 10 {
			return input, fmt.Errorf("%w: task item evidence_refs supports at most 10 tool results", ErrInvalid)
		}
		seenRefs := map[string]bool{}
		for refIndex := range input.Items[index].EvidenceRefs {
			ref := &input.Items[index].EvidenceRefs[refIndex]
			ref.ToolCallID = strings.TrimSpace(ref.ToolCallID)
			if ref.ToolCallID == "" || seenRefs[ref.ToolCallID] {
				return input, fmt.Errorf("%w: task item evidence_refs require unique non-empty tool_call_id values", ErrInvalid)
			}
			seenRefs[ref.ToolCallID] = true
		}
	}
	return input, nil
}

func normalizeTaskPlanMode(value string, itemCount int) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		if itemCount >= 5 {
			return TaskPlanModePlanned
		}
		return TaskPlanModeTracked
	case TaskPlanModeTracked, TaskPlanModePlanned:
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func normalizeTaskItemStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case TaskItemStatusPending, TaskItemStatusInProgress, TaskItemStatusCompleted, TaskItemStatusBlocked:
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func getSessionTaskPlanTx(ctx context.Context, tx *sql.Tx, sessionID string, planID string, forUpdate bool) (SessionTaskPlan, error) {
	query := `
		SELECT id, workspace_id, owner_id, session_id, created_turn_id, updated_turn_id,
			title, goal, handling_mode, status, created_at, updated_at, completed_at
		FROM session_task_plans
		WHERE session_id = $1 AND (($2 = '' AND status = 'active') OR id = $2)
		ORDER BY created_at DESC
		LIMIT 1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	plan, err := scanSessionTaskPlan(tx.QueryRowContext(ctx, query, sessionID, strings.TrimSpace(planID)))
	if err == sql.ErrNoRows {
		return SessionTaskPlan{}, ErrNotFound
	}
	if err != nil {
		return SessionTaskPlan{}, err
	}
	if err := loadSessionTaskItemsTx(ctx, tx, &plan); err != nil {
		return SessionTaskPlan{}, err
	}
	return plan, nil
}

type taskPlanScanner interface {
	Scan(dest ...any) error
}

func scanSessionTaskPlan(scanner taskPlanScanner) (SessionTaskPlan, error) {
	var plan SessionTaskPlan
	var completedAt sql.NullTime
	if err := scanner.Scan(
		&plan.ID, &plan.WorkspaceID, &plan.OwnerID, &plan.SessionID, &plan.CreatedTurnID, &plan.UpdatedTurnID,
		&plan.Title, &plan.Goal, &plan.HandlingMode, &plan.Status, &plan.CreatedAt, &plan.UpdatedAt, &completedAt,
	); err != nil {
		return SessionTaskPlan{}, err
	}
	if completedAt.Valid {
		plan.CompletedAt = &completedAt.Time
	}
	return plan, nil
}

func loadSessionTaskItemsTx(ctx context.Context, tx *sql.Tx, plan *SessionTaskPlan) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, plan_id, item_index, description, status, evidence, evidence_refs, created_at, updated_at, completed_at
		FROM session_task_items WHERE plan_id = $1 ORDER BY item_index ASC
	`, plan.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	plan.Items = make([]SessionTaskItem, 0)
	for rows.Next() {
		var item SessionTaskItem
		var itemCompletedAt sql.NullTime
		var evidenceRefs json.RawMessage
		if err := rows.Scan(&item.ID, &item.PlanID, &item.Index, &item.Description, &item.Status, &item.Evidence, &evidenceRefs, &item.CreatedAt, &item.UpdatedAt, &itemCompletedAt); err != nil {
			return err
		}
		if len(evidenceRefs) > 0 && string(evidenceRefs) != "null" {
			if err := json.Unmarshal(evidenceRefs, &item.EvidenceRefs); err != nil {
				return fmt.Errorf("decode task item %s evidence refs: %w", item.ID, err)
			}
		}
		if item.EvidenceRefs == nil {
			item.EvidenceRefs = []TaskEvidenceRef{}
		}
		if itemCompletedAt.Valid {
			item.CompletedAt = &itemCompletedAt.Time
		}
		plan.Items = append(plan.Items, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return nil
}

func resolveTaskEvidenceRefsTx(ctx context.Context, tx *sql.Tx, sessionID string, turnID string, planCreatedAt time.Time, inputs []TaskEvidenceRefInput) ([]TaskEvidenceRef, error) {
	if strings.TrimSpace(turnID) == "" {
		return nil, fmt.Errorf("turn_id is required")
	}
	refs := make([]TaskEvidenceRef, 0, len(inputs))
	for _, input := range inputs {
		var identifier string
		var apiName string
		var artifactsJSON json.RawMessage
		err := tx.QueryRowContext(ctx, `
			SELECT payload_json->'data'->>'identifier', payload_json->'data'->>'api_name',
				COALESCE(payload_json->'data'->'artifacts', '[]'::jsonb)
			FROM session_events
			WHERE session_id = $1 AND turn_id = $2 AND type = $3
				AND payload_json->'data'->>'id' = $4
				AND payload_json->'data'->>'success' = 'true'
				AND created_at >= $5
			ORDER BY seq DESC
			LIMIT 1
		`, sessionID, turnID, EventRuntimeToolResult, input.ToolCallID, planCreatedAt).Scan(&identifier, &apiName, &artifactsJSON)
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("successful tool result %q was not found in turn %s after plan creation", input.ToolCallID, turnID)
		}
		if err != nil {
			return nil, err
		}
		if identifier == "task" {
			return nil, fmt.Errorf("task tool result %q cannot be used as execution evidence", input.ToolCallID)
		}
		var artifacts []struct {
			ArtifactID string `json:"artifact_id"`
		}
		if len(artifactsJSON) > 0 {
			if err := json.Unmarshal(artifactsJSON, &artifacts); err != nil {
				return nil, fmt.Errorf("decode tool result %q artifacts: %w", input.ToolCallID, err)
			}
		}
		artifactIDs := make([]string, 0, len(artifacts))
		for _, artifact := range artifacts {
			if id := strings.TrimSpace(artifact.ArtifactID); id != "" {
				artifactIDs = append(artifactIDs, id)
			}
		}
		refs = append(refs, TaskEvidenceRef{
			Kind: TaskEvidenceKindToolResult, TurnID: turnID, ToolCallID: input.ToolCallID,
			Tool: strings.Trim(strings.TrimSpace(identifier)+"_"+strings.TrimSpace(apiName), "_"), ArtifactIDs: artifactIDs,
		})
	}
	return refs, nil
}

func taskPlanEventPayload(plan SessionTaskPlan, turnID string, reason string) (json.RawMessage, error) {
	payload := map[string]any{"turn_id": strings.TrimSpace(turnID), "plan": plan}
	if strings.TrimSpace(reason) != "" {
		payload["reason"] = strings.TrimSpace(reason)
	}
	return json.Marshal(payload)
}
