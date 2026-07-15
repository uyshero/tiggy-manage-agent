package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tiggy-manage-agent/internal/skillmarketplace"
	"tiggy-manage-agent/internal/skills"
)

const marketplaceEntrySelect = `
	SELECT e.id, e.workspace_id, e.skill_id, e.skill_version,
		s.identifier, s.title, s.description, s.status, v.checksum_sha256, v.package_format,
		e.summary, e.category, e.tags_json, e.status,
		e.submitted_by, e.submitted_at, e.published_by, e.published_at,
		e.withdrawn_by, e.withdrawn_at, e.review_note, e.withdrawal_reason,
		e.created_by, e.created_at, e.updated_by, e.updated_at
	FROM skill_marketplace_entries e
	JOIN skills s ON s.id = e.skill_id
	JOIN skill_versions v ON v.skill_id = e.skill_id AND v.version = e.skill_version
`

func (s *PostgresStore) CreateMarketplaceEntry(ctx context.Context, input skillmarketplace.CreateMarketplaceEntryInput) (skillmarketplace.MarketplaceEntry, error) {
	input.WorkspaceID = defaultString(strings.TrimSpace(input.WorkspaceID), DefaultWorkspaceID)
	if scope, ok := DatabaseAccessScopeFromContext(ctx); ok {
		input.WorkspaceID = scope.WorkspaceID
	}
	input.SkillID = strings.TrimSpace(input.SkillID)
	input.CreatedBy = defaultString(strings.TrimSpace(input.CreatedBy), "system")
	if input.SkillID == "" || input.SkillVersion <= 0 {
		return skillmarketplace.MarketplaceEntry{}, fmt.Errorf("%w: skill_id and positive skill_version are required", ErrInvalid)
	}
	summary, category, tags, err := skillmarketplace.NormalizeMarketplaceEntryMetadata(input.Summary, input.Category, input.Tags)
	if err != nil {
		return skillmarketplace.MarketplaceEntry{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	encodedTags, err := json.Marshal(tags)
	if err != nil {
		return skillmarketplace.MarketplaceEntry{}, err
	}
	tx, err := s.beginSkillScopeTx(ctx, input.WorkspaceID)
	if err != nil {
		return skillmarketplace.MarketplaceEntry{}, err
	}
	defer tx.Rollback()
	if err := ensureMarketplaceEntryTarget(ctx, tx, input.WorkspaceID, input.SkillID, input.SkillVersion); err != nil {
		return skillmarketplace.MarketplaceEntry{}, err
	}
	id, err := nextSequenceID(ctx, tx, "sment", "tma_skill_marketplace_entry_id_seq")
	if err != nil {
		return skillmarketplace.MarketplaceEntry{}, err
	}
	now := time.Now().UTC()
	var insertedID string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO skill_marketplace_entries (
			id, workspace_id, skill_id, skill_version, summary, category, tags_json,
			status, created_by, created_at, updated_by, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 'draft', $8, $9, $8, $9)
		ON CONFLICT (workspace_id, skill_id, skill_version) DO NOTHING
		RETURNING id
	`, id, input.WorkspaceID, input.SkillID, input.SkillVersion, summary, category, encodedTags, input.CreatedBy, now).Scan(&insertedID)
	if err == sql.ErrNoRows {
		return skillmarketplace.MarketplaceEntry{}, fmt.Errorf("%w: marketplace entry already exists for this skill version", ErrConflict)
	}
	if err != nil {
		return skillmarketplace.MarketplaceEntry{}, err
	}
	entry, err := scanMarketplaceEntry(tx.QueryRowContext(ctx, marketplaceEntrySelect+`
		WHERE e.id = $1 AND e.workspace_id = $2
	`, insertedID, input.WorkspaceID))
	if err != nil {
		return skillmarketplace.MarketplaceEntry{}, err
	}
	if err := tx.Commit(); err != nil {
		return skillmarketplace.MarketplaceEntry{}, err
	}
	return entry, nil
}

func (s *PostgresStore) GetMarketplaceEntry(ctx context.Context, workspaceID string, entryID string) (skillmarketplace.MarketplaceEntry, error) {
	workspaceID = defaultString(strings.TrimSpace(workspaceID), DefaultWorkspaceID)
	entryID = strings.TrimSpace(entryID)
	if entryID == "" {
		return skillmarketplace.MarketplaceEntry{}, fmt.Errorf("%w: marketplace entry id is required", ErrInvalid)
	}
	tx, err := s.beginSkillScopeTx(ctx, workspaceID)
	if err != nil {
		return skillmarketplace.MarketplaceEntry{}, err
	}
	defer tx.Rollback()
	return scanMarketplaceEntry(tx.QueryRowContext(ctx, marketplaceEntrySelect+`
		WHERE e.id = $1 AND e.workspace_id = $2
	`, entryID, workspaceID))
}

func (s *PostgresStore) ListMarketplaceEntries(ctx context.Context, input skillmarketplace.ListMarketplaceEntriesInput) ([]skillmarketplace.MarketplaceEntry, error) {
	input.WorkspaceID = defaultString(strings.TrimSpace(input.WorkspaceID), DefaultWorkspaceID)
	input.Status = strings.TrimSpace(input.Status)
	if input.Status != "" && !skillmarketplace.ValidMarketplaceEntryStatus(input.Status) {
		return nil, fmt.Errorf("%w: unsupported marketplace entry status", ErrInvalid)
	}
	tx, err := s.beginSkillScopeTx(ctx, input.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, marketplaceEntrySelect+`
		WHERE e.workspace_id = $1
			AND ($2 = '' OR e.status = $2)
			AND ($3 OR e.status <> 'withdrawn')
		ORDER BY e.updated_at DESC, e.id DESC
	`, input.WorkspaceID, input.Status, input.IncludeWithdrawn)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []skillmarketplace.MarketplaceEntry{}
	for rows.Next() {
		item, err := scanMarketplaceEntry(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) BrowsePublishedMarketplaceEntries(ctx context.Context, input skillmarketplace.BrowseMarketplaceEntriesInput) ([]skillmarketplace.MarketplaceEntry, error) {
	input.WorkspaceID = defaultString(strings.TrimSpace(input.WorkspaceID), DefaultWorkspaceID)
	input.Query = strings.ToLower(strings.TrimSpace(input.Query))
	input.Category = strings.ToLower(strings.TrimSpace(input.Category))
	_, _, tags, err := skillmarketplace.NormalizeMarketplaceEntryMetadata("", "", input.Tags)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	encodedTags, err := json.Marshal(tags)
	if err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		return nil, fmt.Errorf("%w: internal marketplace limit cannot exceed 50", ErrInvalid)
	}
	tx, err := s.beginSkillScopeTx(ctx, input.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, marketplaceEntrySelect+`
		WHERE e.status = 'published' AND s.status = 'active'
			AND tma_workspaces_share_organization(e.workspace_id, $1)
			AND ($2 = '' OR LOWER(s.identifier || ' ' || s.title || ' ' || s.description || ' ' || e.summary || ' ' || e.category) LIKE '%' || $2 || '%')
			AND ($3 = '' OR LOWER(e.category) = $3)
			AND (
				$4::jsonb = '[]'::jsonb
				OR EXISTS (
					SELECT 1
					FROM jsonb_array_elements_text(e.tags_json) entry_tag
					JOIN jsonb_array_elements_text($4::jsonb) requested_tag
						ON LOWER(entry_tag.value) = LOWER(requested_tag.value)
				)
			)
		ORDER BY e.published_at DESC, e.id DESC
		LIMIT $5
	`, input.WorkspaceID, input.Query, input.Category, string(encodedTags), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []skillmarketplace.MarketplaceEntry{}
	for rows.Next() {
		item, err := scanMarketplaceEntry(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) GetPublishedMarketplaceEntry(ctx context.Context, consumerWorkspaceID string, entryID string) (skillmarketplace.MarketplaceEntry, error) {
	consumerWorkspaceID = defaultString(strings.TrimSpace(consumerWorkspaceID), DefaultWorkspaceID)
	entryID = strings.TrimSpace(entryID)
	if entryID == "" {
		return skillmarketplace.MarketplaceEntry{}, fmt.Errorf("%w: marketplace entry id is required", ErrInvalid)
	}
	tx, err := s.beginSkillScopeTx(ctx, consumerWorkspaceID)
	if err != nil {
		return skillmarketplace.MarketplaceEntry{}, err
	}
	defer tx.Rollback()
	return scanMarketplaceEntry(tx.QueryRowContext(ctx, marketplaceEntrySelect+`
		WHERE e.id = $2 AND e.status = 'published' AND s.status = 'active'
			AND tma_workspaces_share_organization(e.workspace_id, $1)
	`, consumerWorkspaceID, entryID))
}

func (s *PostgresStore) UpdateMarketplaceEntry(ctx context.Context, input skillmarketplace.UpdateMarketplaceEntryInput) (skillmarketplace.MarketplaceEntry, error) {
	input.WorkspaceID = defaultString(strings.TrimSpace(input.WorkspaceID), DefaultWorkspaceID)
	input.EntryID = strings.TrimSpace(input.EntryID)
	input.UpdatedBy = defaultString(strings.TrimSpace(input.UpdatedBy), "system")
	if input.EntryID == "" {
		return skillmarketplace.MarketplaceEntry{}, fmt.Errorf("%w: marketplace entry id is required", ErrInvalid)
	}
	summary, category, tags, err := skillmarketplace.NormalizeMarketplaceEntryMetadata(input.Summary, input.Category, input.Tags)
	if err != nil {
		return skillmarketplace.MarketplaceEntry{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	encodedTags, err := json.Marshal(tags)
	if err != nil {
		return skillmarketplace.MarketplaceEntry{}, err
	}
	tx, err := s.beginSkillScopeTx(ctx, input.WorkspaceID)
	if err != nil {
		return skillmarketplace.MarketplaceEntry{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE skill_marketplace_entries
		SET summary = $3, category = $4, tags_json = $5, updated_by = $6, updated_at = $7
		WHERE id = $1 AND workspace_id = $2 AND status = 'draft'
	`, input.EntryID, input.WorkspaceID, summary, category, encodedTags, input.UpdatedBy, time.Now().UTC())
	if err != nil {
		return skillmarketplace.MarketplaceEntry{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		current, getErr := scanMarketplaceEntry(tx.QueryRowContext(ctx, marketplaceEntrySelect+`
			WHERE e.id = $1 AND e.workspace_id = $2
		`, input.EntryID, input.WorkspaceID))
		if getErr != nil {
			return skillmarketplace.MarketplaceEntry{}, getErr
		}
		return current, fmt.Errorf("%w: only draft marketplace entries can be edited", ErrConflict)
	}
	entry, err := scanMarketplaceEntry(tx.QueryRowContext(ctx, marketplaceEntrySelect+`
		WHERE e.id = $1 AND e.workspace_id = $2
	`, input.EntryID, input.WorkspaceID))
	if err != nil {
		return skillmarketplace.MarketplaceEntry{}, err
	}
	if err := tx.Commit(); err != nil {
		return skillmarketplace.MarketplaceEntry{}, err
	}
	return entry, nil
}

func (s *PostgresStore) TransitionMarketplaceEntry(ctx context.Context, input skillmarketplace.TransitionMarketplaceEntryInput) (skillmarketplace.MarketplaceEntry, error) {
	input.WorkspaceID = defaultString(strings.TrimSpace(input.WorkspaceID), DefaultWorkspaceID)
	input.EntryID = strings.TrimSpace(input.EntryID)
	input.TargetStatus = strings.TrimSpace(input.TargetStatus)
	input.Actor = defaultString(strings.TrimSpace(input.Actor), "system")
	input.Note = strings.TrimSpace(input.Note)
	if input.EntryID == "" {
		return skillmarketplace.MarketplaceEntry{}, fmt.Errorf("%w: marketplace entry id is required", ErrInvalid)
	}
	if len(input.Note) > 2000 {
		return skillmarketplace.MarketplaceEntry{}, fmt.Errorf("%w: transition note must not exceed 2000 characters", ErrInvalid)
	}
	expectedStatus, ok := skillmarketplace.MarketplaceEntryTransitionSource(input.TargetStatus)
	if !ok {
		return skillmarketplace.MarketplaceEntry{}, fmt.Errorf("%w: unsupported marketplace entry transition", ErrInvalid)
	}
	tx, err := s.beginSkillScopeTx(ctx, input.WorkspaceID)
	if err != nil {
		return skillmarketplace.MarketplaceEntry{}, err
	}
	defer tx.Rollback()
	var currentStatus, skillID string
	var skillVersion int
	if err := tx.QueryRowContext(ctx, `
		SELECT status, skill_id, skill_version
		FROM skill_marketplace_entries
		WHERE id = $1 AND workspace_id = $2
		FOR UPDATE
	`, input.EntryID, input.WorkspaceID).Scan(&currentStatus, &skillID, &skillVersion); err == sql.ErrNoRows {
		return skillmarketplace.MarketplaceEntry{}, ErrNotFound
	} else if err != nil {
		return skillmarketplace.MarketplaceEntry{}, err
	}
	if currentStatus == input.TargetStatus {
		if err := tx.Commit(); err != nil {
			return skillmarketplace.MarketplaceEntry{}, err
		}
		return s.GetMarketplaceEntry(ctx, input.WorkspaceID, input.EntryID)
	}
	if currentStatus != expectedStatus {
		return skillmarketplace.MarketplaceEntry{}, fmt.Errorf("%w: marketplace entry must transition from %s to %s", ErrConflict, expectedStatus, input.TargetStatus)
	}
	if input.TargetStatus != skillmarketplace.MarketplaceEntryStatusWithdrawn {
		var skillStatus string
		if err := tx.QueryRowContext(ctx, `
			SELECT s.status
			FROM skills s
			JOIN skill_versions v ON v.skill_id = s.id AND v.version = $3
			WHERE s.id = $1 AND s.workspace_id = $2
			FOR UPDATE OF s
		`, skillID, input.WorkspaceID, skillVersion).Scan(&skillStatus); err == sql.ErrNoRows {
			return skillmarketplace.MarketplaceEntry{}, ErrNotFound
		} else if err != nil {
			return skillmarketplace.MarketplaceEntry{}, err
		}
		if skillStatus != skills.StatusActive {
			return skillmarketplace.MarketplaceEntry{}, fmt.Errorf("%w: archived skill cannot be submitted or published", ErrConflict)
		}
	}
	if input.TargetStatus == skillmarketplace.MarketplaceEntryStatusPublished {
		var publishedExists bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM skill_marketplace_entries
				WHERE workspace_id = $1 AND skill_id = $2 AND status = 'published' AND id <> $3
			)
		`, input.WorkspaceID, skillID, input.EntryID).Scan(&publishedExists); err != nil {
			return skillmarketplace.MarketplaceEntry{}, err
		}
		if publishedExists {
			return skillmarketplace.MarketplaceEntry{}, fmt.Errorf("%w: another version of this skill is already published", ErrConflict)
		}
	}
	now := time.Now().UTC()
	query := `UPDATE skill_marketplace_entries SET status = $3, updated_by = $4, updated_at = $5`
	args := []any{input.EntryID, input.WorkspaceID, input.TargetStatus, input.Actor, now}
	switch input.TargetStatus {
	case skillmarketplace.MarketplaceEntryStatusPendingReview:
		query += `, submitted_by = $4, submitted_at = $5`
	case skillmarketplace.MarketplaceEntryStatusPublished:
		query += `, published_by = $4, published_at = $5, review_note = $6`
		args = append(args, input.Note)
	case skillmarketplace.MarketplaceEntryStatusWithdrawn:
		query += `, withdrawn_by = $4, withdrawn_at = $5, withdrawal_reason = $6`
		args = append(args, input.Note)
	}
	query += ` WHERE id = $1 AND workspace_id = $2`
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return skillmarketplace.MarketplaceEntry{}, err
	}
	if err := tx.Commit(); err != nil {
		return skillmarketplace.MarketplaceEntry{}, err
	}
	return s.GetMarketplaceEntry(ctx, input.WorkspaceID, input.EntryID)
}

func ensureMarketplaceEntryTarget(ctx context.Context, q queryer, workspaceID string, skillID string, version int) error {
	var status string
	if err := q.QueryRowContext(ctx, `
		SELECT s.status
		FROM skills s
		JOIN skill_versions v ON v.skill_id = s.id AND v.version = $3
		WHERE s.id = $1 AND s.workspace_id = $2
	`, skillID, workspaceID, version).Scan(&status); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if status != skills.StatusActive {
		return fmt.Errorf("%w: archived skill cannot enter the marketplace", ErrConflict)
	}
	return nil
}

func scanMarketplaceEntry(scanner rowScanner) (skillmarketplace.MarketplaceEntry, error) {
	var item skillmarketplace.MarketplaceEntry
	var tags []byte
	var submittedAt, publishedAt, withdrawnAt sql.NullTime
	if err := scanner.Scan(
		&item.ID, &item.WorkspaceID, &item.SkillID, &item.SkillVersion,
		&item.SkillIdentifier, &item.SkillTitle, &item.SkillDescription, &item.SkillStatus,
		&item.VersionChecksum, &item.PackageFormat, &item.Summary, &item.Category, &tags, &item.Status,
		&item.SubmittedBy, &submittedAt, &item.PublishedBy, &publishedAt,
		&item.WithdrawnBy, &withdrawnAt, &item.ReviewNote, &item.WithdrawalReason,
		&item.CreatedBy, &item.CreatedAt, &item.UpdatedBy, &item.UpdatedAt,
	); err == sql.ErrNoRows {
		return skillmarketplace.MarketplaceEntry{}, ErrNotFound
	} else if err != nil {
		return skillmarketplace.MarketplaceEntry{}, err
	}
	if err := json.Unmarshal(tags, &item.Tags); err != nil {
		return skillmarketplace.MarketplaceEntry{}, fmt.Errorf("decode marketplace entry tags: %w", err)
	}
	if item.Tags == nil {
		item.Tags = []string{}
	}
	if submittedAt.Valid {
		item.SubmittedAt = &submittedAt.Time
	}
	if publishedAt.Valid {
		item.PublishedAt = &publishedAt.Time
	}
	if withdrawnAt.Valid {
		item.WithdrawnAt = &withdrawnAt.Time
	}
	return item, nil
}
