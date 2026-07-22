package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const maxAchievementLibraryTags = 20

type AchievementLibraryItem struct {
	ID               string    `json:"id"`
	WorkspaceID      string    `json:"workspace_id"`
	ObjectRefID      string    `json:"object_ref_id"`
	SourceSessionID  string    `json:"source_session_id,omitempty"`
	SourceArtifactID string    `json:"source_artifact_id,omitempty"`
	Name             string    `json:"name"`
	Description      string    `json:"description,omitempty"`
	Directory        string    `json:"directory,omitempty"`
	Tags             []string  `json:"tags"`
	CreatedBy        string    `json:"created_by"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type CreateAchievementLibraryItemInput struct {
	WorkspaceID      string   `json:"workspace_id,omitempty"`
	ObjectRefID      string   `json:"object_ref_id"`
	SourceSessionID  string   `json:"source_session_id,omitempty"`
	SourceArtifactID string   `json:"source_artifact_id,omitempty"`
	Name             string   `json:"name"`
	Description      string   `json:"description,omitempty"`
	Directory        string   `json:"directory,omitempty"`
	Tags             []string `json:"tags,omitempty"`
	CreatedBy        string   `json:"created_by,omitempty"`
}

type UpdateAchievementLibraryItemInput struct {
	WorkspaceID string   `json:"workspace_id,omitempty"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Directory   string   `json:"directory,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	UpdatedBy   string   `json:"updated_by,omitempty"`
}

type AchievementLibraryStore interface {
	CreateAchievementLibraryItemContext(context.Context, CreateAchievementLibraryItemInput) (AchievementLibraryItem, error)
	GetAchievementLibraryItemContext(context.Context, string, string) (AchievementLibraryItem, error)
	ListAchievementLibraryItemsContext(context.Context, string) ([]AchievementLibraryItem, error)
	UpdateAchievementLibraryItemContext(context.Context, string, UpdateAchievementLibraryItemInput) (AchievementLibraryItem, error)
	DeleteAchievementLibraryItemContext(context.Context, string, string) error
}

func normalizeAchievementDirectory(value string) (string, error) {
	value = strings.Trim(strings.TrimSpace(value), "/")
	parts := strings.Split(value, "/")
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return "", fmt.Errorf("%w: achievement directory cannot contain ..", ErrInvalid)
		}
		clean = append(clean, part)
	}
	value = strings.Join(clean, "/")
	if len(value) > 512 {
		return "", fmt.Errorf("%w: achievement directory exceeds 512 characters", ErrInvalid)
	}
	return value, nil
}

func normalizeAchievementTags(values []string) ([]string, error) {
	tags := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len(value) > 64 {
			return nil, fmt.Errorf("%w: achievement tag exceeds 64 characters", ErrInvalid)
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		tags = append(tags, value)
		if len(tags) > maxAchievementLibraryTags {
			return nil, fmt.Errorf("%w: achievement supports at most %d tags", ErrInvalid, maxAchievementLibraryTags)
		}
	}
	return tags, nil
}

func validateAchievementLibraryFields(name, description, directory string, tags []string) (string, string, string, []string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 512 {
		return "", "", "", nil, fmt.Errorf("%w: achievement name must contain 1 to 512 characters", ErrInvalid)
	}
	if len(description) > 2000 {
		return "", "", "", nil, fmt.Errorf("%w: achievement description exceeds 2000 characters", ErrInvalid)
	}
	directory, err := normalizeAchievementDirectory(directory)
	if err != nil {
		return "", "", "", nil, err
	}
	tags, err = normalizeAchievementTags(tags)
	if err != nil {
		return "", "", "", nil, err
	}
	return name, strings.TrimSpace(description), directory, tags, nil
}

func scanAchievementLibraryItem(scanner interface{ Scan(...any) error }) (AchievementLibraryItem, error) {
	var item AchievementLibraryItem
	var tagsJSON []byte
	err := scanner.Scan(
		&item.ID, &item.WorkspaceID, &item.ObjectRefID, &item.SourceSessionID, &item.SourceArtifactID,
		&item.Name, &item.Description, &item.Directory, &tagsJSON, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return AchievementLibraryItem{}, err
	}
	if err := json.Unmarshal(tagsJSON, &item.Tags); err != nil {
		return AchievementLibraryItem{}, fmt.Errorf("decode achievement tags: %w", err)
	}
	if item.Tags == nil {
		item.Tags = []string{}
	}
	return item, nil
}

const achievementLibraryColumns = `
	id, workspace_id, object_ref_id, source_session_id, source_artifact_id,
	name, description, directory, tags_json, created_by, created_at, updated_at`

func (s *PostgresStore) CreateAchievementLibraryItemContext(ctx context.Context, input CreateAchievementLibraryItemInput) (AchievementLibraryItem, error) {
	name, description, directory, tags, err := validateAchievementLibraryFields(input.Name, input.Description, input.Directory, input.Tags)
	if err != nil {
		return AchievementLibraryItem{}, err
	}
	if strings.TrimSpace(input.ObjectRefID) == "" {
		return AchievementLibraryItem{}, fmt.Errorf("%w: achievement object_ref_id is required", ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return AchievementLibraryItem{}, err
	}
	defer tx.Rollback()
	var objectExists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM object_refs WHERE id = $1 AND workspace_id = $2)`, input.ObjectRefID, scope.WorkspaceID).Scan(&objectExists); err != nil {
		return AchievementLibraryItem{}, err
	}
	if !objectExists {
		return AchievementLibraryItem{}, ErrForbidden
	}
	id, err := nextSequenceID(ctx, tx, "ach", "tma_achievement_library_item_id_seq")
	if err != nil {
		return AchievementLibraryItem{}, err
	}
	tagsJSON, _ := json.Marshal(tags)
	now := time.Now().UTC()
	item, err := scanAchievementLibraryItem(tx.QueryRowContext(ctx, `
		INSERT INTO achievement_library_items (`+achievementLibraryColumns+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11)
		RETURNING `+achievementLibraryColumns,
		id, scope.WorkspaceID, input.ObjectRefID, strings.TrimSpace(input.SourceSessionID), strings.TrimSpace(input.SourceArtifactID),
		name, description, directory, tagsJSON, defaultString(strings.TrimSpace(input.CreatedBy), "system"), now))
	if err != nil {
		return AchievementLibraryItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return AchievementLibraryItem{}, err
	}
	return item, nil
}

func (s *PostgresStore) GetAchievementLibraryItemContext(ctx context.Context, workspaceID, id string) (AchievementLibraryItem, error) {
	if strings.TrimSpace(id) == "" {
		return AchievementLibraryItem{}, fmt.Errorf("%w: achievement id is required", ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return AchievementLibraryItem{}, err
	}
	defer tx.Rollback()
	item, err := scanAchievementLibraryItem(tx.QueryRowContext(ctx, `SELECT `+achievementLibraryColumns+` FROM achievement_library_items WHERE id = $1 AND workspace_id = $2`, id, scope.WorkspaceID))
	if err == sql.ErrNoRows {
		return AchievementLibraryItem{}, ErrNotFound
	}
	if err != nil {
		return AchievementLibraryItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return AchievementLibraryItem{}, err
	}
	return item, nil
}

func (s *PostgresStore) ListAchievementLibraryItemsContext(ctx context.Context, workspaceID string) ([]AchievementLibraryItem, error) {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT `+achievementLibraryColumns+` FROM achievement_library_items WHERE workspace_id = $1 ORDER BY updated_at DESC, id DESC`, scope.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AchievementLibraryItem{}
	for rows.Next() {
		item, err := scanAchievementLibraryItem(rows)
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

func (s *PostgresStore) UpdateAchievementLibraryItemContext(ctx context.Context, id string, input UpdateAchievementLibraryItemInput) (AchievementLibraryItem, error) {
	name, description, directory, tags, err := validateAchievementLibraryFields(input.Name, input.Description, input.Directory, input.Tags)
	if err != nil {
		return AchievementLibraryItem{}, err
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return AchievementLibraryItem{}, err
	}
	defer tx.Rollback()
	tagsJSON, _ := json.Marshal(tags)
	item, err := scanAchievementLibraryItem(tx.QueryRowContext(ctx, `
		UPDATE achievement_library_items SET name=$3, description=$4, directory=$5, tags_json=$6, updated_at=$7
		WHERE id=$1 AND workspace_id=$2 RETURNING `+achievementLibraryColumns,
		id, scope.WorkspaceID, name, description, directory, tagsJSON, time.Now().UTC()))
	if err == sql.ErrNoRows {
		return AchievementLibraryItem{}, ErrNotFound
	}
	if err != nil {
		return AchievementLibraryItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return AchievementLibraryItem{}, err
	}
	return item, nil
}

func (s *PostgresStore) DeleteAchievementLibraryItemContext(ctx context.Context, workspaceID, id string) error {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM achievement_library_items WHERE id=$1 AND workspace_id=$2`, id, scope.WorkspaceID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}
