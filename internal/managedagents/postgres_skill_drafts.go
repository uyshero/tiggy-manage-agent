package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tiggy-manage-agent/internal/skills"
)

func (s *PostgresStore) GetSkillDraft(ctx context.Context, skillID string) (skills.Draft, error) {
	tx, err := s.beginSkillScopeTx(ctx, "")
	if err != nil {
		return skills.Draft{}, err
	}
	defer tx.Rollback()
	return scanSkillDraft(tx.QueryRowContext(ctx, `SELECT skill_id, revision, content_format, manifest_json, content_text, assets_json, updated_by, updated_at FROM skill_drafts WHERE skill_id = $1`, strings.TrimSpace(skillID)))
}

func (s *PostgresStore) PutSkillDraft(ctx context.Context, input skills.PutDraftInput) (skills.Draft, error) {
	input.SkillID = strings.TrimSpace(input.SkillID)
	input.ContentFormat = defaultString(strings.TrimSpace(input.ContentFormat), "hybrid")
	input.UpdatedBy = defaultString(strings.TrimSpace(input.UpdatedBy), "system")
	if input.SkillID == "" {
		return skills.Draft{}, fmt.Errorf("%w: skill id is required", ErrInvalid)
	}
	if err := skills.ValidateManifest(input.Manifest); err != nil {
		return skills.Draft{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if _, err := skills.DecodeAssetBundle(input.Assets); err != nil {
		return skills.Draft{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	manifest := input.Manifest
	if len(manifest) == 0 || string(manifest) == "null" {
		manifest = json.RawMessage(`{}`)
	}
	assets := input.Assets
	if len(assets) == 0 || string(assets) == "null" {
		assets = json.RawMessage(`[]`)
	}
	tx, err := s.beginSkillScopeTx(ctx, "")
	if err != nil {
		return skills.Draft{}, err
	}
	defer tx.Rollback()
	var ownerType string
	if err := tx.QueryRowContext(ctx, `SELECT owner_type FROM skills WHERE id = $1`, input.SkillID).Scan(&ownerType); err == sql.ErrNoRows {
		return skills.Draft{}, ErrNotFound
	} else if err != nil {
		return skills.Draft{}, err
	}
	if ownerType != skills.OwnerTypeUser {
		return skills.Draft{}, fmt.Errorf("%w: drafts are only supported for personal Skills", ErrForbidden)
	}
	now := time.Now().UTC()
	draft, err := scanSkillDraft(tx.QueryRowContext(ctx, `
		INSERT INTO skill_drafts (skill_id, revision, content_format, manifest_json, content_text, assets_json, updated_by, updated_at)
		VALUES ($1, 1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (skill_id) DO UPDATE SET revision = skill_drafts.revision + 1, content_format = EXCLUDED.content_format,
			manifest_json = EXCLUDED.manifest_json, content_text = EXCLUDED.content_text, assets_json = EXCLUDED.assets_json,
			updated_by = EXCLUDED.updated_by, updated_at = EXCLUDED.updated_at
		WHERE $8 = 0 OR skill_drafts.revision = $8
		RETURNING skill_id, revision, content_format, manifest_json, content_text, assets_json, updated_by, updated_at
	`, input.SkillID, input.ContentFormat, manifest, input.ContentText, assets, input.UpdatedBy, now, input.ExpectedRevision))
	if err == ErrNotFound {
		return skills.Draft{}, ErrRevisionConflict
	}
	if err != nil {
		return skills.Draft{}, err
	}
	if err := tx.Commit(); err != nil {
		return skills.Draft{}, err
	}
	return draft, nil
}

func (s *PostgresStore) PublishSkillDraft(ctx context.Context, skillID string, expectedRevision int64, createdBy string) (skills.Version, error) {
	draft, err := s.GetSkillDraft(ctx, skillID)
	if err != nil {
		return skills.Version{}, err
	}
	if expectedRevision > 0 && draft.Revision != expectedRevision {
		return skills.Version{}, ErrRevisionConflict
	}
	version, err := s.CreateSkillVersion(ctx, skills.CreateVersionInput{SkillID: skillID, ContentFormat: draft.ContentFormat, Manifest: draft.Manifest, ContentText: draft.ContentText, Assets: draft.Assets, CreatedBy: createdBy})
	if err != nil {
		return skills.Version{}, err
	}
	tx, err := s.beginSkillScopeTx(ctx, "")
	if err != nil {
		return skills.Version{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM skill_drafts WHERE skill_id = $1 AND revision = $2`, skillID, draft.Revision); err != nil {
		return skills.Version{}, err
	}
	if err := tx.Commit(); err != nil {
		return skills.Version{}, err
	}
	return version, nil
}

func (s *PostgresStore) ForkSkill(ctx context.Context, sourceSkillID string, sourceVersion int, input skills.CreateSkillInput) (skills.Skill, error) {
	source, err := s.GetSkill(ctx, strings.TrimSpace(sourceSkillID))
	if err != nil {
		return skills.Skill{}, err
	}
	version, err := s.GetSkillVersion(ctx, source.ID, sourceVersion)
	if err != nil {
		return skills.Skill{}, err
	}
	input.WorkspaceID = source.WorkspaceID
	input.OwnerType = skills.OwnerTypeUser
	input.Visibility = skills.VisibilityPrivate
	input.ForkedFromSkillID = source.ID
	input.ForkedFromVersion = version.Version
	forked, err := s.CreateSkill(ctx, input)
	if err != nil {
		return skills.Skill{}, err
	}
	_, err = s.PutSkillDraft(ctx, skills.PutDraftInput{SkillID: forked.ID, ContentFormat: version.ContentFormat, Manifest: version.Manifest, ContentText: version.ContentText, Assets: version.Assets, UpdatedBy: input.CreatedBy})
	if err != nil {
		return skills.Skill{}, err
	}
	return forked, nil
}

type skillDraftScanner interface{ Scan(dest ...any) error }

func scanSkillDraft(scanner skillDraftScanner) (skills.Draft, error) {
	var draft skills.Draft
	var manifest, assets []byte
	err := scanner.Scan(&draft.SkillID, &draft.Revision, &draft.ContentFormat, &manifest, &draft.ContentText, &assets, &draft.UpdatedBy, &draft.UpdatedAt)
	if err == sql.ErrNoRows {
		return skills.Draft{}, ErrNotFound
	}
	if err != nil {
		return skills.Draft{}, err
	}
	draft.Manifest = cloneSkillRaw(manifest)
	draft.Assets = cloneSkillRaw(assets)
	return draft, nil
}
