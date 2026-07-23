package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *PostgresStore) CreateEvaluationDatasetContext(ctx context.Context, input CreateEvaluationDatasetInput) (EvaluationDataset, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.CreatedBy = strings.TrimSpace(input.CreatedBy)
	if input.WorkspaceID == "" || input.Name == "" || len([]rune(input.Name)) > 160 {
		return EvaluationDataset{}, fmt.Errorf("%w: dataset workspace_id and name are required", ErrInvalid)
	}
	if len([]rune(input.Description)) > 2000 {
		return EvaluationDataset{}, fmt.Errorf("%w: dataset description must not exceed 2000 characters", ErrInvalid)
	}
	items, err := ValidateEvaluationDatasetItems(input.Items)
	if err != nil {
		return EvaluationDataset{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EvaluationDataset{}, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return EvaluationDataset{}, err
	}
	if scoped && scope.WorkspaceID != input.WorkspaceID {
		return EvaluationDataset{}, ErrForbidden
	}
	id, err := nextSequenceID(ctx, tx, "edset", "tma_evaluation_dataset_id_seq")
	if err != nil {
		return EvaluationDataset{}, err
	}
	now := time.Now().UTC()
	dataset := EvaluationDataset{
		ID: id, WorkspaceID: input.WorkspaceID, Name: input.Name, Description: input.Description,
		Items: []EvaluationDatasetItem{}, CreatedBy: input.CreatedBy, CreatedAt: now, UpdatedAt: now,
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO evaluation_datasets (id, workspace_id, name, description, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
	`, dataset.ID, dataset.WorkspaceID, dataset.Name, dataset.Description, dataset.CreatedBy, now); err != nil {
		return EvaluationDataset{}, err
	}
	for index, inputItem := range items {
		itemID, err := nextSequenceID(ctx, tx, "editem", "tma_evaluation_dataset_item_id_seq")
		if err != nil {
			return EvaluationDataset{}, err
		}
		tagsJSON, err := json.Marshal(inputItem.Tags)
		if err != nil {
			return EvaluationDataset{}, err
		}
		item := EvaluationDatasetItem{
			ID: itemID, DatasetID: dataset.ID, ItemIndex: index,
			Prompt: inputItem.Prompt, ExpectedOutput: inputItem.ExpectedOutput,
			Tags: append([]string(nil), inputItem.Tags...), CreatedAt: now,
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO evaluation_dataset_items (
				id, dataset_id, workspace_id, item_index, prompt, expected_output, tags_json, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, item.ID, dataset.ID, dataset.WorkspaceID, item.ItemIndex, item.Prompt, item.ExpectedOutput, tagsJSON, now); err != nil {
			return EvaluationDataset{}, err
		}
		dataset.Items = append(dataset.Items, item)
	}
	if err := tx.Commit(); err != nil {
		return EvaluationDataset{}, err
	}
	return dataset, nil
}

func (s *PostgresStore) GetEvaluationDatasetContext(ctx context.Context, id string) (EvaluationDataset, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return EvaluationDataset{}, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return EvaluationDataset{}, err
	}
	dataset, err := getEvaluationDatasetTx(ctx, tx, strings.TrimSpace(id))
	if err != nil {
		return EvaluationDataset{}, err
	}
	if scoped && dataset.WorkspaceID != scope.WorkspaceID {
		return EvaluationDataset{}, ErrForbidden
	}
	if err := tx.Commit(); err != nil {
		return EvaluationDataset{}, err
	}
	return dataset, nil
}

func (s *PostgresStore) ListEvaluationDatasetsContext(ctx context.Context, workspaceID string) ([]EvaluationDataset, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, fmt.Errorf("%w: dataset workspace_id is required", ErrInvalid)
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
	rows, err := tx.QueryContext(ctx, evaluationDatasetSelect+` WHERE workspace_id = $1 ORDER BY updated_at DESC, id DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	datasets := []EvaluationDataset{}
	for rows.Next() {
		dataset, err := scanEvaluationDataset(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		datasets = append(datasets, dataset)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range datasets {
		items, err := listEvaluationDatasetItemsTx(ctx, tx, datasets[index].ID)
		if err != nil {
			return nil, err
		}
		datasets[index].Items = items
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return datasets, nil
}

func (s *PostgresStore) CreateEvaluationExperimentContext(ctx context.Context, input CreateEvaluationExperimentInput) (EvaluationExperiment, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Name = strings.TrimSpace(input.Name)
	input.DatasetID = strings.TrimSpace(input.DatasetID)
	input.RubricID = strings.TrimSpace(input.RubricID)
	input.LeftTemplateSessionID = strings.TrimSpace(input.LeftTemplateSessionID)
	input.RightTemplateSessionID = strings.TrimSpace(input.RightTemplateSessionID)
	input.CreatedBy = strings.TrimSpace(input.CreatedBy)
	if input.WorkspaceID == "" || input.Name == "" || len([]rune(input.Name)) > 160 || input.DatasetID == "" || input.RubricID == "" || input.LeftTemplateSessionID == "" || input.RightTemplateSessionID == "" {
		return EvaluationExperiment{}, fmt.Errorf("%w: experiment name, dataset, rubric, and template sessions are required", ErrInvalid)
	}
	if input.LeftTemplateSessionID == input.RightTemplateSessionID {
		return EvaluationExperiment{}, fmt.Errorf("%w: experiment template sessions must be different", ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EvaluationExperiment{}, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return EvaluationExperiment{}, err
	}
	if scoped && scope.WorkspaceID != input.WorkspaceID {
		return EvaluationExperiment{}, ErrForbidden
	}
	dataset, err := getEvaluationDatasetTx(ctx, tx, input.DatasetID)
	if err != nil {
		return EvaluationExperiment{}, err
	}
	rubric, err := scanEvaluationRubric(tx.QueryRowContext(ctx, evaluationRubricSelect+` WHERE id = $1`, input.RubricID))
	if err != nil {
		return EvaluationExperiment{}, err
	}
	leftSession, err := getSessionTx(ctx, tx, input.LeftTemplateSessionID)
	if err != nil {
		return EvaluationExperiment{}, err
	}
	rightSession, err := getSessionTx(ctx, tx, input.RightTemplateSessionID)
	if err != nil {
		return EvaluationExperiment{}, err
	}
	if dataset.WorkspaceID != input.WorkspaceID || rubric.WorkspaceID != input.WorkspaceID || leftSession.WorkspaceID != input.WorkspaceID || rightSession.WorkspaceID != input.WorkspaceID {
		return EvaluationExperiment{}, fmt.Errorf("%w: experiment resources must belong to the same workspace", ErrInvalid)
	}
	id, err := nextSequenceID(ctx, tx, "eexp", "tma_evaluation_experiment_id_seq")
	if err != nil {
		return EvaluationExperiment{}, err
	}
	now := time.Now().UTC()
	experiment := EvaluationExperiment{
		ID: id, WorkspaceID: input.WorkspaceID, Name: input.Name,
		DatasetID: dataset.ID, RubricID: rubric.ID,
		LeftTemplateSessionID: leftSession.ID, RightTemplateSessionID: rightSession.ID,
		Status: EvaluationExperimentStatusRunning, Items: []EvaluationExperimentItem{},
		CreatedBy: input.CreatedBy, CreatedAt: now, UpdatedAt: now,
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO evaluation_experiments (
			id, workspace_id, name, dataset_id, rubric_id,
			left_template_session_id, right_template_session_id, status,
			created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
	`, experiment.ID, experiment.WorkspaceID, experiment.Name, experiment.DatasetID, experiment.RubricID,
		experiment.LeftTemplateSessionID, experiment.RightTemplateSessionID, experiment.Status,
		experiment.CreatedBy, now); err != nil {
		return EvaluationExperiment{}, err
	}
	for _, datasetItem := range dataset.Items {
		itemID, err := nextSequenceID(ctx, tx, "eexpi", "tma_evaluation_experiment_item_id_seq")
		if err != nil {
			return EvaluationExperiment{}, err
		}
		tagsJSON, err := json.Marshal(datasetItem.Tags)
		if err != nil {
			return EvaluationExperiment{}, err
		}
		item := EvaluationExperimentItem{
			ID: itemID, ExperimentID: experiment.ID, DatasetItemID: datasetItem.ID,
			ItemIndex: datasetItem.ItemIndex, Prompt: datasetItem.Prompt,
			ExpectedOutput: datasetItem.ExpectedOutput, Tags: append([]string(nil), datasetItem.Tags...),
			Status: EvaluationExperimentItemStatusQueued, CreatedAt: now, UpdatedAt: now,
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO evaluation_experiment_items (
				id, experiment_id, workspace_id, dataset_item_id, item_index,
				prompt, expected_output, tags_json, status, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
		`, item.ID, experiment.ID, experiment.WorkspaceID, item.DatasetItemID, item.ItemIndex,
			item.Prompt, item.ExpectedOutput, tagsJSON, item.Status, now); err != nil {
			return EvaluationExperiment{}, err
		}
		experiment.Items = append(experiment.Items, item)
	}
	experiment.Summary = SummarizeEvaluationExperiment(experiment.Items)
	if err := tx.Commit(); err != nil {
		return EvaluationExperiment{}, err
	}
	return experiment, nil
}

func (s *PostgresStore) GetEvaluationExperimentContext(ctx context.Context, id string) (EvaluationExperiment, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return EvaluationExperiment{}, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return EvaluationExperiment{}, err
	}
	experiment, err := getEvaluationExperimentTx(ctx, tx, strings.TrimSpace(id))
	if err != nil {
		return EvaluationExperiment{}, err
	}
	if scoped && experiment.WorkspaceID != scope.WorkspaceID {
		return EvaluationExperiment{}, ErrForbidden
	}
	if err := tx.Commit(); err != nil {
		return EvaluationExperiment{}, err
	}
	return experiment, nil
}

func (s *PostgresStore) ListEvaluationExperimentsContext(ctx context.Context, workspaceID string, limit int) ([]EvaluationExperiment, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, fmt.Errorf("%w: experiment workspace_id is required", ErrInvalid)
	}
	if limit <= 0 || limit > 100 {
		limit = 20
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
	rows, err := tx.QueryContext(ctx, evaluationExperimentSelect+` WHERE workspace_id = $1 ORDER BY updated_at DESC, id DESC LIMIT $2`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	experiments := []EvaluationExperiment{}
	for rows.Next() {
		experiment, err := scanEvaluationExperiment(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		experiments = append(experiments, experiment)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range experiments {
		items, err := listEvaluationExperimentItemsTx(ctx, tx, experiments[index].ID)
		if err != nil {
			return nil, err
		}
		experiments[index].Items = items
		experiments[index].Summary = SummarizeEvaluationExperiment(items)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return experiments, nil
}

func (s *PostgresStore) UpdateEvaluationExperimentItemContext(ctx context.Context, input UpdateEvaluationExperimentItemInput) (EvaluationExperiment, error) {
	input.ExperimentID = strings.TrimSpace(input.ExperimentID)
	input.ItemID = strings.TrimSpace(input.ItemID)
	input.LeftSessionID = strings.TrimSpace(input.LeftSessionID)
	input.LeftTurnID = strings.TrimSpace(input.LeftTurnID)
	input.RightSessionID = strings.TrimSpace(input.RightSessionID)
	input.RightTurnID = strings.TrimSpace(input.RightTurnID)
	input.EvaluationID = strings.TrimSpace(input.EvaluationID)
	input.Status = strings.TrimSpace(input.Status)
	input.Conclusion = strings.TrimSpace(input.Conclusion)
	input.ErrorMessage = strings.TrimSpace(input.ErrorMessage)
	if input.ExperimentID == "" || input.ItemID == "" {
		return EvaluationExperiment{}, fmt.Errorf("%w: experiment and item ids are required", ErrInvalid)
	}
	switch input.Status {
	case EvaluationExperimentItemStatusQueued, EvaluationExperimentItemStatusRunning, EvaluationExperimentItemStatusCompleted, EvaluationExperimentItemStatusFailed:
	default:
		return EvaluationExperiment{}, fmt.Errorf("%w: unsupported experiment item status", ErrInvalid)
	}
	if input.Conclusion != "" {
		if _, err := ValidateEvaluationConclusion(input.Conclusion); err != nil {
			return EvaluationExperiment{}, err
		}
	}
	if len([]rune(input.ErrorMessage)) > 10000 {
		return EvaluationExperiment{}, fmt.Errorf("%w: experiment item error must not exceed 10000 characters", ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EvaluationExperiment{}, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return EvaluationExperiment{}, err
	}
	experiment, err := scanEvaluationExperiment(tx.QueryRowContext(ctx, evaluationExperimentSelect+` WHERE id = $1`, input.ExperimentID))
	if err != nil {
		return EvaluationExperiment{}, err
	}
	if scoped && experiment.WorkspaceID != scope.WorkspaceID {
		return EvaluationExperiment{}, ErrForbidden
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE evaluation_experiment_items
		SET left_session_id = CASE WHEN $3 = '' THEN left_session_id ELSE $3 END,
			left_turn_id = CASE WHEN $4 = '' THEN left_turn_id ELSE $4 END,
			right_session_id = CASE WHEN $5 = '' THEN right_session_id ELSE $5 END,
			right_turn_id = CASE WHEN $6 = '' THEN right_turn_id ELSE $6 END,
			evaluation_id = CASE WHEN $7 = '' THEN evaluation_id ELSE $7 END,
			status = $8, conclusion = $9, left_average = $10, right_average = $11,
			error_message = $12, updated_at = now()
		WHERE experiment_id = $1 AND id = $2
	`, input.ExperimentID, input.ItemID, input.LeftSessionID, input.LeftTurnID,
		input.RightSessionID, input.RightTurnID, input.EvaluationID, input.Status,
		input.Conclusion, input.LeftAverage, input.RightAverage, input.ErrorMessage)
	if err != nil {
		return EvaluationExperiment{}, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return EvaluationExperiment{}, err
	}
	if updated == 0 {
		return EvaluationExperiment{}, ErrNotFound
	}
	items, err := listEvaluationExperimentItemsTx(ctx, tx, input.ExperimentID)
	if err != nil {
		return EvaluationExperiment{}, err
	}
	summary := SummarizeEvaluationExperiment(items)
	status := EvaluationExperimentStatusRunning
	var completedAt *time.Time
	if summary.Total > 0 && summary.Completed == summary.Total {
		status = EvaluationExperimentStatusCompleted
		now := time.Now().UTC()
		completedAt = &now
	} else if summary.Total > 0 && summary.Queued == 0 && summary.Running == 0 && summary.Failed > 0 {
		status = EvaluationExperimentStatusFailed
		now := time.Now().UTC()
		completedAt = &now
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE evaluation_experiments
		SET status = $2, updated_at = now(), completed_at = $3
		WHERE id = $1
	`, input.ExperimentID, status, completedAt); err != nil {
		return EvaluationExperiment{}, err
	}
	experiment, err = getEvaluationExperimentTx(ctx, tx, input.ExperimentID)
	if err != nil {
		return EvaluationExperiment{}, err
	}
	if err := tx.Commit(); err != nil {
		return EvaluationExperiment{}, err
	}
	return experiment, nil
}

const evaluationDatasetSelect = `
	SELECT id, workspace_id, name, description, created_by, created_at, updated_at
	FROM evaluation_datasets
`

const evaluationExperimentSelect = `
	SELECT id, workspace_id, name, COALESCE(dataset_id, ''), COALESCE(rubric_id, ''),
		COALESCE(left_template_session_id, ''), COALESCE(right_template_session_id, ''),
		status, created_by, created_at, updated_at, completed_at
	FROM evaluation_experiments
`

func getEvaluationDatasetTx(ctx context.Context, tx *sql.Tx, id string) (EvaluationDataset, error) {
	dataset, err := scanEvaluationDataset(tx.QueryRowContext(ctx, evaluationDatasetSelect+` WHERE id = $1`, id))
	if err != nil {
		return EvaluationDataset{}, err
	}
	items, err := listEvaluationDatasetItemsTx(ctx, tx, dataset.ID)
	if err != nil {
		return EvaluationDataset{}, err
	}
	dataset.Items = items
	return dataset, nil
}

func scanEvaluationDataset(scanner evaluationScanner) (EvaluationDataset, error) {
	var dataset EvaluationDataset
	if err := scanner.Scan(&dataset.ID, &dataset.WorkspaceID, &dataset.Name, &dataset.Description,
		&dataset.CreatedBy, &dataset.CreatedAt, &dataset.UpdatedAt); err == sql.ErrNoRows {
		return EvaluationDataset{}, ErrNotFound
	} else if err != nil {
		return EvaluationDataset{}, err
	}
	dataset.Items = []EvaluationDatasetItem{}
	return dataset, nil
}

func listEvaluationDatasetItemsTx(ctx context.Context, tx *sql.Tx, datasetID string) ([]EvaluationDatasetItem, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, dataset_id, item_index, prompt, expected_output, tags_json, created_at
		FROM evaluation_dataset_items WHERE dataset_id = $1 ORDER BY item_index
	`, datasetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []EvaluationDatasetItem{}
	for rows.Next() {
		var item EvaluationDatasetItem
		var tagsJSON []byte
		if err := rows.Scan(&item.ID, &item.DatasetID, &item.ItemIndex, &item.Prompt,
			&item.ExpectedOutput, &tagsJSON, &item.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(tagsJSON, &item.Tags); err != nil {
			return nil, fmt.Errorf("decode evaluation dataset item tags: %w", err)
		}
		if item.Tags == nil {
			item.Tags = []string{}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func getEvaluationExperimentTx(ctx context.Context, tx *sql.Tx, id string) (EvaluationExperiment, error) {
	experiment, err := scanEvaluationExperiment(tx.QueryRowContext(ctx, evaluationExperimentSelect+` WHERE id = $1`, id))
	if err != nil {
		return EvaluationExperiment{}, err
	}
	items, err := listEvaluationExperimentItemsTx(ctx, tx, experiment.ID)
	if err != nil {
		return EvaluationExperiment{}, err
	}
	experiment.Items = items
	experiment.Summary = SummarizeEvaluationExperiment(items)
	return experiment, nil
}

func scanEvaluationExperiment(scanner evaluationScanner) (EvaluationExperiment, error) {
	var experiment EvaluationExperiment
	var completedAt sql.NullTime
	if err := scanner.Scan(&experiment.ID, &experiment.WorkspaceID, &experiment.Name,
		&experiment.DatasetID, &experiment.RubricID,
		&experiment.LeftTemplateSessionID, &experiment.RightTemplateSessionID,
		&experiment.Status, &experiment.CreatedBy, &experiment.CreatedAt, &experiment.UpdatedAt,
		&completedAt); err == sql.ErrNoRows {
		return EvaluationExperiment{}, ErrNotFound
	} else if err != nil {
		return EvaluationExperiment{}, err
	}
	if completedAt.Valid {
		experiment.CompletedAt = &completedAt.Time
	}
	experiment.Items = []EvaluationExperimentItem{}
	return experiment, nil
}

func listEvaluationExperimentItemsTx(ctx context.Context, tx *sql.Tx, experimentID string) ([]EvaluationExperimentItem, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, experiment_id, COALESCE(dataset_item_id, ''), item_index,
			prompt, expected_output, tags_json,
			COALESCE(left_session_id, ''), left_turn_id,
			COALESCE(right_session_id, ''), right_turn_id,
			COALESCE(evaluation_id, ''), status, conclusion,
			left_average, right_average, error_message, created_at, updated_at
		FROM evaluation_experiment_items
		WHERE experiment_id = $1 ORDER BY item_index
	`, experimentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []EvaluationExperimentItem{}
	for rows.Next() {
		var item EvaluationExperimentItem
		var tagsJSON []byte
		if err := rows.Scan(&item.ID, &item.ExperimentID, &item.DatasetItemID, &item.ItemIndex,
			&item.Prompt, &item.ExpectedOutput, &tagsJSON,
			&item.LeftSessionID, &item.LeftTurnID, &item.RightSessionID, &item.RightTurnID,
			&item.EvaluationID, &item.Status, &item.Conclusion,
			&item.LeftAverage, &item.RightAverage, &item.ErrorMessage,
			&item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(tagsJSON, &item.Tags); err != nil {
			return nil, fmt.Errorf("decode evaluation experiment item tags: %w", err)
		}
		if item.Tags == nil {
			item.Tags = []string{}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
