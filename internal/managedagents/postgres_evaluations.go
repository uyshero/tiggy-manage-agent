package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *PostgresStore) CreateEvaluationRubricContext(ctx context.Context, input CreateEvaluationRubricInput) (EvaluationRubric, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.CreatedBy = strings.TrimSpace(input.CreatedBy)
	if input.WorkspaceID == "" || input.Name == "" || len(input.Name) > 160 {
		return EvaluationRubric{}, fmt.Errorf("%w: rubric workspace_id and name are required", ErrInvalid)
	}
	criteria, err := ValidateEvaluationCriteria(input.Criteria)
	if err != nil {
		return EvaluationRubric{}, err
	}
	criteriaJSON, err := json.Marshal(criteria)
	if err != nil {
		return EvaluationRubric{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EvaluationRubric{}, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return EvaluationRubric{}, err
	}
	if scoped && scope.WorkspaceID != input.WorkspaceID {
		return EvaluationRubric{}, fmt.Errorf("%w: rubric workspace scope mismatch", ErrForbidden)
	}
	id, err := nextSequenceID(ctx, tx, "erub", "tma_evaluation_rubric_id_seq")
	if err != nil {
		return EvaluationRubric{}, err
	}
	now := time.Now().UTC()
	rubric := EvaluationRubric{
		ID: id, WorkspaceID: input.WorkspaceID, Name: input.Name, Description: input.Description,
		Criteria: criteria, Revision: 1, CreatedBy: input.CreatedBy, UpdatedBy: input.CreatedBy,
		CreatedAt: now, UpdatedAt: now,
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO evaluation_rubrics (
			id, workspace_id, name, description, criteria_json, revision,
			created_by, updated_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, 1, $6, $6, $7, $7)
	`, rubric.ID, rubric.WorkspaceID, rubric.Name, rubric.Description, criteriaJSON, rubric.CreatedBy, now); err != nil {
		return EvaluationRubric{}, err
	}
	if err := tx.Commit(); err != nil {
		return EvaluationRubric{}, err
	}
	return rubric, nil
}

func (s *PostgresStore) GetEvaluationRubricContext(ctx context.Context, id string) (EvaluationRubric, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return EvaluationRubric{}, err
	}
	defer tx.Rollback()
	if _, _, err := setContextDatabaseAccessScope(ctx, tx); err != nil {
		return EvaluationRubric{}, err
	}
	rubric, err := scanEvaluationRubric(tx.QueryRowContext(ctx, evaluationRubricSelect+` WHERE id = $1`, strings.TrimSpace(id)))
	if err != nil {
		return EvaluationRubric{}, err
	}
	if err := tx.Commit(); err != nil {
		return EvaluationRubric{}, err
	}
	return rubric, nil
}

func (s *PostgresStore) ListEvaluationRubricsContext(ctx context.Context, workspaceID string) ([]EvaluationRubric, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, fmt.Errorf("%w: rubric workspace_id is required", ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return nil, err
	}
	if scoped && scope.WorkspaceID != workspaceID {
		return nil, ErrForbidden
	}
	rows, err := tx.QueryContext(ctx, evaluationRubricSelect+` WHERE workspace_id = $1 ORDER BY updated_at DESC, id DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rubrics := []EvaluationRubric{}
	for rows.Next() {
		rubric, err := scanEvaluationRubric(rows)
		if err != nil {
			return nil, err
		}
		rubrics = append(rubrics, rubric)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return rubrics, nil
}

func (s *PostgresStore) CreateRunEvaluationContext(ctx context.Context, input CreateRunEvaluationInput) (RunEvaluation, error) {
	input.LeftSessionID = strings.TrimSpace(input.LeftSessionID)
	input.LeftTurnID = strings.TrimSpace(input.LeftTurnID)
	input.RightSessionID = strings.TrimSpace(input.RightSessionID)
	input.RightTurnID = strings.TrimSpace(input.RightTurnID)
	input.RubricID = strings.TrimSpace(input.RubricID)
	input.CreatedBy = strings.TrimSpace(input.CreatedBy)
	input.Notes = strings.TrimSpace(input.Notes)
	input.EvaluationType = strings.TrimSpace(input.EvaluationType)
	input.JudgeProvider = strings.TrimSpace(input.JudgeProvider)
	input.JudgeModel = strings.TrimSpace(input.JudgeModel)
	input.JudgeReasoning = strings.TrimSpace(input.JudgeReasoning)
	if input.EvaluationType == "" {
		input.EvaluationType = EvaluationTypeManual
	}
	if input.EvaluationType != EvaluationTypeManual && input.EvaluationType != EvaluationTypeAuto {
		return RunEvaluation{}, fmt.Errorf("%w: unsupported evaluation type", ErrInvalid)
	}
	if input.EvaluationType == EvaluationTypeAuto && (input.JudgeProvider == "" || input.JudgeModel == "") {
		return RunEvaluation{}, fmt.Errorf("%w: automatic evaluation requires judge provider and model", ErrInvalid)
	}
	if input.LeftSessionID == "" || input.LeftTurnID == "" || input.RightSessionID == "" || input.RightTurnID == "" || input.RubricID == "" {
		return RunEvaluation{}, fmt.Errorf("%w: left run, right run, and rubric_id are required", ErrInvalid)
	}
	if input.LeftSessionID == input.RightSessionID && input.LeftTurnID == input.RightTurnID {
		return RunEvaluation{}, fmt.Errorf("%w: evaluation runs must be different", ErrInvalid)
	}
	if len(input.Notes) > 10000 {
		return RunEvaluation{}, fmt.Errorf("%w: evaluation notes must not exceed 10000 characters", ErrInvalid)
	}
	conclusion, err := ValidateEvaluationConclusion(input.Conclusion)
	if err != nil {
		return RunEvaluation{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RunEvaluation{}, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return RunEvaluation{}, err
	}
	leftSession, err := getSessionTx(ctx, tx, input.LeftSessionID)
	if err != nil {
		return RunEvaluation{}, err
	}
	rightSession, err := getSessionTx(ctx, tx, input.RightSessionID)
	if err != nil {
		return RunEvaluation{}, err
	}
	if err := authorizeSessionAccessScope(leftSession, scope, scoped); err != nil {
		return RunEvaluation{}, err
	}
	if err := authorizeSessionAccessScope(rightSession, scope, scoped); err != nil {
		return RunEvaluation{}, err
	}
	if leftSession.WorkspaceID != rightSession.WorkspaceID {
		return RunEvaluation{}, fmt.Errorf("%w: evaluation runs must belong to the same workspace", ErrInvalid)
	}
	if _, found, err := getSessionRunTx(ctx, tx, input.LeftSessionID, input.LeftTurnID); err != nil || !found {
		if err != nil {
			return RunEvaluation{}, err
		}
		return RunEvaluation{}, ErrNotFound
	}
	if _, found, err := getSessionRunTx(ctx, tx, input.RightSessionID, input.RightTurnID); err != nil || !found {
		if err != nil {
			return RunEvaluation{}, err
		}
		return RunEvaluation{}, ErrNotFound
	}
	rubric, err := scanEvaluationRubric(tx.QueryRowContext(ctx, evaluationRubricSelect+` WHERE id = $1`, input.RubricID))
	if err != nil {
		return RunEvaluation{}, err
	}
	if rubric.WorkspaceID != leftSession.WorkspaceID {
		return RunEvaluation{}, fmt.Errorf("%w: rubric belongs to another workspace", ErrInvalid)
	}
	scores, err := ValidateEvaluationScores(rubric.Criteria, input.Scores)
	if err != nil {
		return RunEvaluation{}, err
	}
	snapshot := EvaluationRubricSnapshot{
		RubricID: rubric.ID, Name: rubric.Name, Description: rubric.Description,
		Revision: rubric.Revision, Criteria: rubric.Criteria,
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return RunEvaluation{}, err
	}
	scoresJSON, err := json.Marshal(scores)
	if err != nil {
		return RunEvaluation{}, err
	}
	id, err := nextSequenceID(ctx, tx, "reval", "tma_run_evaluation_id_seq")
	if err != nil {
		return RunEvaluation{}, err
	}
	now := time.Now().UTC()
	evaluation := RunEvaluation{
		ID: id, WorkspaceID: leftSession.WorkspaceID,
		LeftSessionID: input.LeftSessionID, LeftTurnID: input.LeftTurnID,
		RightSessionID: input.RightSessionID, RightTurnID: input.RightTurnID,
		RubricID: rubric.ID, RubricSnapshot: snapshot, Scores: scores,
		Conclusion: conclusion, Notes: input.Notes, EvaluationType: input.EvaluationType,
		JudgeProvider: input.JudgeProvider, JudgeModel: input.JudgeModel, JudgeReasoning: input.JudgeReasoning,
		CreatedBy: input.CreatedBy, CreatedAt: now,
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO run_evaluations (
			id, workspace_id, left_session_id, left_turn_id, right_session_id, right_turn_id,
			rubric_id, rubric_snapshot_json, scores_json, conclusion, notes,
			evaluation_type, judge_provider, judge_model, judge_reasoning, created_by, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
	`, evaluation.ID, evaluation.WorkspaceID, evaluation.LeftSessionID, evaluation.LeftTurnID,
		evaluation.RightSessionID, evaluation.RightTurnID, evaluation.RubricID, snapshotJSON,
		scoresJSON, evaluation.Conclusion, evaluation.Notes, evaluation.EvaluationType,
		evaluation.JudgeProvider, evaluation.JudgeModel, evaluation.JudgeReasoning,
		evaluation.CreatedBy, evaluation.CreatedAt); err != nil {
		return RunEvaluation{}, err
	}
	if err := tx.Commit(); err != nil {
		return RunEvaluation{}, err
	}
	return evaluation, nil
}

func (s *PostgresStore) ListRunEvaluationsContext(ctx context.Context, input ListRunEvaluationsInput) ([]RunEvaluation, error) {
	input.LeftSessionID = strings.TrimSpace(input.LeftSessionID)
	input.LeftTurnID = strings.TrimSpace(input.LeftTurnID)
	input.RightSessionID = strings.TrimSpace(input.RightSessionID)
	input.RightTurnID = strings.TrimSpace(input.RightTurnID)
	if input.LeftSessionID == "" || input.LeftTurnID == "" || input.RightSessionID == "" || input.RightTurnID == "" {
		return nil, fmt.Errorf("%w: left and right run identities are required", ErrInvalid)
	}
	if input.Limit <= 0 || input.Limit > 200 {
		input.Limit = 50
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return nil, err
	}
	for _, sessionID := range []string{input.LeftSessionID, input.RightSessionID} {
		session, err := getSessionTx(ctx, tx, sessionID)
		if err != nil {
			return nil, err
		}
		if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
			return nil, err
		}
	}
	rows, err := tx.QueryContext(ctx, runEvaluationSelect+`
		WHERE left_session_id = $1 AND left_turn_id = $2
		  AND right_session_id = $3 AND right_turn_id = $4
		ORDER BY created_at DESC, id DESC
		LIMIT $5
	`, input.LeftSessionID, input.LeftTurnID, input.RightSessionID, input.RightTurnID, input.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	evaluations := []RunEvaluation{}
	for rows.Next() {
		evaluation, err := scanRunEvaluation(rows)
		if err != nil {
			return nil, err
		}
		evaluations = append(evaluations, evaluation)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return evaluations, nil
}

const evaluationRubricSelect = `
	SELECT id, workspace_id, name, description, criteria_json, revision,
		created_by, updated_by, created_at, updated_at
	FROM evaluation_rubrics
`

const runEvaluationSelect = `
	SELECT id, workspace_id, left_session_id, left_turn_id, right_session_id, right_turn_id,
		COALESCE(rubric_id, ''), rubric_snapshot_json, scores_json, conclusion, notes,
		evaluation_type, judge_provider, judge_model, judge_reasoning, created_by, created_at
	FROM run_evaluations
`

type evaluationScanner interface {
	Scan(dest ...any) error
}

func scanEvaluationRubric(scanner evaluationScanner) (EvaluationRubric, error) {
	var rubric EvaluationRubric
	var criteriaJSON []byte
	if err := scanner.Scan(
		&rubric.ID, &rubric.WorkspaceID, &rubric.Name, &rubric.Description, &criteriaJSON,
		&rubric.Revision, &rubric.CreatedBy, &rubric.UpdatedBy, &rubric.CreatedAt, &rubric.UpdatedAt,
	); err == sql.ErrNoRows {
		return EvaluationRubric{}, ErrNotFound
	} else if err != nil {
		return EvaluationRubric{}, err
	}
	if err := decodeEvaluationRubric(criteriaJSON, &rubric); err != nil {
		return EvaluationRubric{}, err
	}
	return rubric, nil
}

func scanRunEvaluation(scanner evaluationScanner) (RunEvaluation, error) {
	var evaluation RunEvaluation
	var snapshotJSON []byte
	var scoresJSON []byte
	if err := scanner.Scan(
		&evaluation.ID, &evaluation.WorkspaceID, &evaluation.LeftSessionID, &evaluation.LeftTurnID,
		&evaluation.RightSessionID, &evaluation.RightTurnID, &evaluation.RubricID,
		&snapshotJSON, &scoresJSON, &evaluation.Conclusion, &evaluation.Notes,
		&evaluation.EvaluationType, &evaluation.JudgeProvider, &evaluation.JudgeModel, &evaluation.JudgeReasoning,
		&evaluation.CreatedBy, &evaluation.CreatedAt,
	); err == sql.ErrNoRows {
		return RunEvaluation{}, ErrNotFound
	} else if err != nil {
		return RunEvaluation{}, err
	}
	if err := json.Unmarshal(snapshotJSON, &evaluation.RubricSnapshot); err != nil {
		return RunEvaluation{}, fmt.Errorf("decode run evaluation rubric snapshot: %w", err)
	}
	if err := json.Unmarshal(scoresJSON, &evaluation.Scores); err != nil {
		return RunEvaluation{}, fmt.Errorf("decode run evaluation scores: %w", err)
	}
	return evaluation, nil
}
