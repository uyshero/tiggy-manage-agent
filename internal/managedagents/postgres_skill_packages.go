package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/skillpackage"
	"tiggy-manage-agent/internal/skills"
)

const maxSkillPackageBackfillLimit = 100

func (s *PostgresStore) BackfillSkillPackages(ctx context.Context, input skills.PackageBackfillInput, createdBy string) (skills.PackageBackfillResult, error) {
	repository := s.skillPackageRepository()
	if repository == nil {
		return skills.PackageBackfillResult{}, objectstore.ErrNotConfigured
	}
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	if scope, ok := DatabaseAccessScopeFromContext(ctx); ok {
		if input.WorkspaceID != "" && input.WorkspaceID != scope.WorkspaceID {
			return skills.PackageBackfillResult{}, fmt.Errorf("%w: database workspace scope mismatch", ErrForbidden)
		}
		input.WorkspaceID = scope.WorkspaceID
	}
	if input.Limit <= 0 {
		input.Limit = 20
	}
	if input.Limit > maxSkillPackageBackfillLimit {
		return skills.PackageBackfillResult{}, fmt.Errorf("%w: skill package backfill limit must not exceed %d", ErrInvalid, maxSkillPackageBackfillLimit)
	}
	createdBy = defaultString(strings.TrimSpace(createdBy), "system")
	tx, err := s.beginSkillScopeTx(ctx, input.WorkspaceID)
	if err != nil {
		return skills.PackageBackfillResult{}, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT sv.id
		FROM skill_versions sv
		JOIN skills s ON s.id = sv.skill_id
		WHERE sv.package_format = 'legacy_db' AND ($1 = '' OR s.workspace_id = $1)
		ORDER BY sv.created_at, sv.id
		LIMIT $2
	`, input.WorkspaceID, input.Limit)
	if err != nil {
		return skills.PackageBackfillResult{}, err
	}
	versionIDs := make([]string, 0, input.Limit)
	for rows.Next() {
		var versionID string
		if err := rows.Scan(&versionID); err != nil {
			rows.Close()
			return skills.PackageBackfillResult{}, err
		}
		versionIDs = append(versionIDs, versionID)
	}
	if err := rows.Close(); err != nil {
		return skills.PackageBackfillResult{}, err
	}
	if err := rows.Err(); err != nil {
		return skills.PackageBackfillResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return skills.PackageBackfillResult{}, err
	}
	workCtx := ctx
	if _, ok := DatabaseAccessScopeFromContext(workCtx); !ok && input.WorkspaceID != "" {
		workCtx, err = ContextWithDatabaseAccessScope(workCtx, AccessScope{WorkspaceID: input.WorkspaceID})
		if err != nil {
			return skills.PackageBackfillResult{}, err
		}
	}
	result := skills.PackageBackfillResult{WorkspaceID: input.WorkspaceID, Scanned: len(versionIDs)}
	for _, versionID := range versionIDs {
		migrated, err := s.backfillSkillPackageVersion(workCtx, repository, versionID, createdBy)
		if err != nil {
			return result, err
		}
		if migrated {
			result.Migrated++
		}
	}
	return result, nil
}

func (s *PostgresStore) backfillSkillPackageVersion(ctx context.Context, repository *skillpackage.Repository, versionID string, createdBy string) (bool, error) {
	tx, err := s.beginSkillScopeTx(ctx, "")
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var skillID, workspaceID, identifier, contentText, packageFormat string
	var versionNumber int
	var assets []byte
	if err := tx.QueryRowContext(ctx, `
		SELECT sv.skill_id, sv.version, sv.content_text, sv.assets_json, sv.package_format,
			s.workspace_id, s.identifier
		FROM skill_versions sv
		JOIN skills s ON s.id = sv.skill_id
		WHERE sv.id = $1
		FOR UPDATE OF sv
	`, versionID).Scan(&skillID, &versionNumber, &contentText, &assets, &packageFormat, &workspaceID, &identifier); err == sql.ErrNoRows {
		return false, ErrNotFound
	} else if err != nil {
		return false, err
	}
	if packageFormat != skillpackage.FormatLegacyDB {
		return false, nil
	}
	if _, err := setDatabaseAccessScope(ctx, tx, workspaceID); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "tma-skill-asset-gc:"+workspaceID); err != nil {
		return false, err
	}
	bundle, err := skills.DecodeAssetBundle(json.RawMessage(assets))
	if err != nil {
		return false, fmt.Errorf("decode legacy skill package %s: %w", versionID, err)
	}
	objectCtx, err := ContextWithDatabaseAccessScope(ctx, AccessScope{WorkspaceID: workspaceID})
	if err != nil {
		return false, err
	}
	stored, err := repository.Store(ctx, skillpackage.BuildInput{
		WorkspaceID: workspaceID, Identifier: identifier, Version: versionNumber,
		SkillMarkdown: contentText, Assets: bundle,
		ResolveBinary: func(_ context.Context, objectRefID string) (skillpackage.BinaryObject, error) {
			objectRef, resolveErr := s.GetObjectRefContext(objectCtx, objectRefID)
			if resolveErr != nil {
				return skillpackage.BinaryObject{}, resolveErr
			}
			if objectRef.WorkspaceID != workspaceID {
				return skillpackage.BinaryObject{}, fmt.Errorf("%w: binary skill asset belongs to another workspace", ErrForbidden)
			}
			return skillpackage.BinaryObject{
				ObjectRefID: objectRef.ID, Bucket: objectRef.Bucket, Key: objectRef.ObjectKey, Version: objectRef.ObjectVersion,
			}, nil
		},
	})
	if err != nil {
		return false, err
	}
	committed := false
	defer func() {
		if !committed {
			repository.DeleteStored(context.Background(), stored)
		}
	}()
	var packageObjectRefID, skillMDObjectRefID string
	now := time.Now().UTC()
	for index := range stored.Files {
		storedObject := &stored.Files[index]
		if storedObject.File.ObjectRefID == "" {
			metadata, _ := json.Marshal(map[string]any{
				"kind": "skill_package_file", "skill_id": skillID, "skill_identifier": identifier,
				"skill_version": versionNumber, "package_path": storedObject.File.Path,
				"package_role": storedObject.File.Role, "backfilled": true,
			})
			objectRef, err := insertSkillPackageObjectRef(ctx, tx, CreateObjectRefInput{
				WorkspaceID: workspaceID, StorageProvider: repository.Provider(), Bucket: storedObject.Bucket,
				ObjectKey: storedObject.File.ObjectKey, ObjectVersion: storedObject.Version,
				ContentType: storedObject.File.ContentType, SizeBytes: storedObject.File.SizeBytes,
				ChecksumSHA256: storedObject.File.ChecksumSHA256, ETag: storedObject.ETag,
				Visibility: ObjectVisibilityWorkspace, Metadata: metadata, CreatedBy: createdBy,
			})
			if err != nil {
				return false, err
			}
			storedObject.File.ObjectRefID = objectRef.ID
		}
		switch storedObject.File.Role {
		case "archive":
			packageObjectRefID = storedObject.File.ObjectRefID
		case "skill_md":
			skillMDObjectRefID = storedObject.File.ObjectRefID
		}
	}
	manifest, err := skillpackage.EncodeManifest(stored)
	if err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE skill_versions
		SET package_format = $2, package_root = $3, package_checksum_sha256 = $4,
			package_object_ref_id = $5, skill_md_object_ref_id = $6, package_manifest_json = $7
		WHERE id = $1 AND package_format = 'legacy_db'
	`, versionID, skillpackage.FormatV1, stored.Root, stored.PackageChecksum,
		packageObjectRefID, skillMDObjectRefID, manifest)
	if err != nil {
		return false, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return false, nil
	}
	for _, storedObject := range stored.Files {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO skill_version_package_files (
				skill_version_id, path, role, object_ref_id, content_type, size_bytes,
				checksum_sha256, is_binary, executable, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`, versionID, storedObject.File.Path, storedObject.File.Role, storedObject.File.ObjectRefID,
			storedObject.File.ContentType, storedObject.File.SizeBytes, storedObject.File.ChecksumSHA256,
			storedObject.File.Binary, storedObject.File.Executable, now); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	committed = true
	return true, nil
}
