package managedagents

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/skillpackage"
	"tiggy-manage-agent/internal/skills"
)

func (s *PostgresStore) ConfigureSkillPackageStorage(client objectstore.Client, bucket string) error {
	repository, err := skillpackage.NewRepository(client, bucket)
	if err != nil {
		return err
	}
	s.skillPackageMu.Lock()
	s.skillPackages = repository
	s.skillPackageMu.Unlock()
	return nil
}

func (s *PostgresStore) skillPackageRepository() *skillpackage.Repository {
	s.skillPackageMu.RLock()
	defer s.skillPackageMu.RUnlock()
	return s.skillPackages
}

func (s *PostgresStore) beginSkillScopeTx(ctx context.Context, workspaceID string) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(workspaceID) != "" {
		if _, err := setDatabaseAccessScope(ctx, tx, workspaceID); err != nil {
			tx.Rollback()
			return nil, err
		}
	} else if _, _, err := setContextDatabaseAccessScope(ctx, tx); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (s *PostgresStore) CreateSkill(ctx context.Context, input skills.CreateSkillInput) (skills.Skill, error) {
	input.WorkspaceID = defaultString(strings.TrimSpace(input.WorkspaceID), DefaultWorkspaceID)
	if scope, ok := DatabaseAccessScopeFromContext(ctx); ok {
		input.WorkspaceID = scope.WorkspaceID
	}
	input.Identifier = strings.TrimSpace(input.Identifier)
	input.Title = strings.TrimSpace(input.Title)
	input.OwnerType = defaultString(strings.TrimSpace(input.OwnerType), skills.OwnerTypeWorkspace)
	input.SourceType = defaultString(strings.TrimSpace(input.SourceType), defaultSkillSourceType(input.OwnerType))
	input.SourceLocator = strings.TrimSpace(input.SourceLocator)
	input.SourcePath = strings.TrimSpace(input.SourcePath)
	input.CreatedBy = defaultString(strings.TrimSpace(input.CreatedBy), "system")
	if err := skills.ValidateIdentifier(input.Identifier); err != nil {
		return skills.Skill{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if input.Title == "" {
		return skills.Skill{}, fmt.Errorf("%w: skill title is required", ErrInvalid)
	}
	if !validSkillOwnerType(input.OwnerType) {
		return skills.Skill{}, fmt.Errorf("%w: unsupported skill owner_type", ErrInvalid)
	}
	if input.OwnerType == skills.OwnerTypePlugin && strings.TrimSpace(input.SourcePluginID) == "" {
		return skills.Skill{}, fmt.Errorf("%w: plugin skill requires source_plugin_id", ErrInvalid)
	}
	if !validSkillSourceType(input.SourceType) {
		return skills.Skill{}, fmt.Errorf("%w: unsupported skill source_type", ErrInvalid)
	}
	if input.SourceType == skills.SourceTypeGitHub && (input.SourceLocator == "" || input.SourcePath == "") {
		return skills.Skill{}, fmt.Errorf("%w: github skill requires source_locator and source_path", ErrInvalid)
	}
	tx, err := s.beginSkillScopeTx(ctx, input.WorkspaceID)
	if err != nil {
		return skills.Skill{}, err
	}
	defer tx.Rollback()
	id, err := nextSequenceID(ctx, tx, "skl", "tma_skill_id_seq")
	if err != nil {
		return skills.Skill{}, err
	}
	now := time.Now().UTC()
	var skill skills.Skill
	err = tx.QueryRowContext(ctx, `
		INSERT INTO skills (
			id, workspace_id, identifier, title, description, owner_type, source_plugin_id,
			source_type, source_locator, source_path, status, created_by, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'active', $11, $12)
		ON CONFLICT (workspace_id, identifier) DO NOTHING
		RETURNING id, workspace_id, identifier, title, description, owner_type, COALESCE(source_plugin_id, ''),
			source_type, source_locator, source_path, status, created_by, created_at
	`, id, input.WorkspaceID, input.Identifier, input.Title, input.Description, input.OwnerType, nullableString(input.SourcePluginID),
		input.SourceType, input.SourceLocator, input.SourcePath, input.CreatedBy, now).Scan(
		&skill.ID, &skill.WorkspaceID, &skill.Identifier, &skill.Title, &skill.Description, &skill.OwnerType,
		&skill.SourcePluginID, &skill.SourceType, &skill.SourceLocator, &skill.SourcePath,
		&skill.Status, &skill.CreatedBy, &skill.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return skills.Skill{}, fmt.Errorf("%w: skill identifier %q already exists", ErrConflict, input.Identifier)
	}
	if err != nil {
		return skills.Skill{}, err
	}
	if err := tx.Commit(); err != nil {
		return skills.Skill{}, err
	}
	return skill, nil
}

func (s *PostgresStore) GetSkill(ctx context.Context, id string) (skills.Skill, error) {
	if strings.TrimSpace(id) == "" {
		return skills.Skill{}, fmt.Errorf("%w: skill id is required", ErrInvalid)
	}
	tx, err := s.beginSkillScopeTx(ctx, "")
	if err != nil {
		return skills.Skill{}, err
	}
	defer tx.Rollback()
	return scanSkill(tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, identifier, title, description, owner_type, COALESCE(source_plugin_id, ''),
			source_type, source_locator, source_path, status, created_by, created_at, archived_at
		FROM skills WHERE id = $1
	`, id))
}

func (s *PostgresStore) GetSkillByIdentifier(ctx context.Context, workspaceID string, identifier string) (skills.Skill, error) {
	workspaceID = defaultString(strings.TrimSpace(workspaceID), DefaultWorkspaceID)
	if err := skills.ValidateIdentifier(identifier); err != nil {
		return skills.Skill{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	tx, err := s.beginSkillScopeTx(ctx, workspaceID)
	if err != nil {
		return skills.Skill{}, err
	}
	defer tx.Rollback()
	return scanSkill(tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, identifier, title, description, owner_type, COALESCE(source_plugin_id, ''),
			source_type, source_locator, source_path, status, created_by, created_at, archived_at
		FROM skills WHERE workspace_id = $1 AND identifier = $2
	`, workspaceID, identifier))
}

func (s *PostgresStore) ListSkills(ctx context.Context, input skills.ListSkillsInput) ([]skills.Skill, error) {
	workspaceID := defaultString(strings.TrimSpace(input.WorkspaceID), DefaultWorkspaceID)
	tx, err := s.beginSkillScopeTx(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT id, workspace_id, identifier, title, description, owner_type, COALESCE(source_plugin_id, ''),
			source_type, source_locator, source_path, status, created_by, created_at, archived_at
		FROM skills
		WHERE workspace_id = $1 AND ($2 OR status = 'active')
		ORDER BY identifier
	`, workspaceID, input.IncludeArchived)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]skills.Skill, 0)
	for rows.Next() {
		skill, scanErr := scanSkill(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, skill)
	}
	return result, rows.Err()
}

func (s *PostgresStore) ArchiveSkill(ctx context.Context, id string) (skills.Skill, error) {
	if strings.TrimSpace(id) == "" {
		return skills.Skill{}, fmt.Errorf("%w: skill id is required", ErrInvalid)
	}
	tx, err := s.beginSkillScopeTx(ctx, "")
	if err != nil {
		return skills.Skill{}, err
	}
	defer tx.Rollback()

	current, err := scanSkill(tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, identifier, title, description, owner_type, COALESCE(source_plugin_id, ''),
			source_type, source_locator, source_path, status, created_by, created_at, archived_at
		FROM skills WHERE id = $1
		FOR UPDATE
	`, id))
	if err != nil {
		return skills.Skill{}, err
	}
	if current.Status == skills.StatusArchived {
		return current, nil
	}
	if _, err := setDatabaseAccessScope(ctx, tx, current.WorkspaceID); err != nil {
		return skills.Skill{}, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT a.id, av.skills_json
		FROM agents a
		JOIN agent_config_versions av ON av.agent_id = a.id AND av.version = a.current_config_version
		WHERE a.workspace_id = $1 AND a.archived_at IS NULL
	`, current.WorkspaceID)
	if err != nil {
		return skills.Skill{}, err
	}
	for rows.Next() {
		var agentID string
		var raw []byte
		if err := rows.Scan(&agentID, &raw); err != nil {
			rows.Close()
			return skills.Skill{}, err
		}
		config, ok := skills.NormalizeConfig(raw)
		if !ok {
			rows.Close()
			return skills.Skill{}, fmt.Errorf("%w: cannot archive skill %q while Agent %s has an unreadable current skills config", ErrConflict, current.Identifier, agentID)
		}
		for _, binding := range config.Enabled {
			if binding.Skill == current.Identifier {
				rows.Close()
				return skills.Skill{}, fmt.Errorf("%w: cannot archive skill %q while Agent %s currently enables it; disable it first", ErrConflict, current.Identifier, agentID)
			}
		}
	}
	if err := rows.Close(); err != nil {
		return skills.Skill{}, err
	}
	if err := rows.Err(); err != nil {
		return skills.Skill{}, err
	}
	var publishedInMarketplace bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM skill_marketplace_entries
			WHERE skill_id = $1 AND status = 'published'
		)
	`, current.ID).Scan(&publishedInMarketplace); err != nil {
		return skills.Skill{}, err
	}
	if publishedInMarketplace {
		return skills.Skill{}, fmt.Errorf("%w: cannot archive skill %q while an internal marketplace entry is published; withdraw it first", ErrConflict, current.Identifier)
	}

	archived, err := scanSkill(tx.QueryRowContext(ctx, `
		UPDATE skills SET status = 'archived', archived_at = COALESCE(archived_at, $2)
		WHERE id = $1
		RETURNING id, workspace_id, identifier, title, description, owner_type, COALESCE(source_plugin_id, ''),
			source_type, source_locator, source_path, status, created_by, created_at, archived_at
	`, id, time.Now().UTC()))
	if err != nil {
		return skills.Skill{}, err
	}
	if err := tx.Commit(); err != nil {
		return skills.Skill{}, err
	}
	return archived, nil
}

func (s *PostgresStore) CreateSkillVersion(ctx context.Context, input skills.CreateVersionInput) (skills.Version, error) {
	input.SkillID = strings.TrimSpace(input.SkillID)
	input.ContentFormat = defaultString(strings.TrimSpace(input.ContentFormat), "hybrid")
	input.CreatedBy = defaultString(strings.TrimSpace(input.CreatedBy), "system")
	if input.SkillID == "" {
		return skills.Version{}, fmt.Errorf("%w: skill id is required", ErrInvalid)
	}
	if input.ContentFormat != "markdown" && input.ContentFormat != "json" && input.ContentFormat != "hybrid" {
		return skills.Version{}, fmt.Errorf("%w: unsupported skill content_format", ErrInvalid)
	}
	if err := skills.ValidateManifest(input.Manifest); err != nil {
		return skills.Version{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if len(input.Assets) > 0 && string(input.Assets) != "null" && !json.Valid(input.Assets) {
		return skills.Version{}, fmt.Errorf("%w: invalid skill assets", ErrInvalid)
	}
	manifest := input.Manifest
	if len(manifest) == 0 || string(manifest) == "null" {
		manifest = json.RawMessage(`{}`)
	}
	assets := input.Assets
	if len(assets) == 0 || string(assets) == "null" {
		assets = json.RawMessage(`[]`)
	}
	assetBundle, err := skills.DecodeAssetBundle(assets)
	if err != nil {
		return skills.Version{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(string(manifest)+"\x00"+input.ContentText+"\x00"+string(assets))))
	tx, err := s.beginSkillScopeTx(ctx, "")
	if err != nil {
		return skills.Version{}, err
	}
	defer tx.Rollback()
	var status string
	var workspaceID, identifier string
	if err := tx.QueryRowContext(ctx, `SELECT status, workspace_id, identifier FROM skills WHERE id = $1 FOR UPDATE`, input.SkillID).Scan(&status, &workspaceID, &identifier); err == sql.ErrNoRows {
		return skills.Version{}, ErrNotFound
	} else if err != nil {
		return skills.Version{}, err
	}
	if status != skills.StatusActive {
		return skills.Version{}, fmt.Errorf("%w: archived skill cannot publish versions", ErrConflict)
	}
	if _, err := setDatabaseAccessScope(ctx, tx, workspaceID); err != nil {
		return skills.Version{}, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "tma-skill-asset-gc:"+workspaceID); err != nil {
		return skills.Version{}, err
	}
	var versionNumber int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM skill_versions WHERE skill_id = $1`, input.SkillID).Scan(&versionNumber); err != nil {
		return skills.Version{}, err
	}
	id, err := nextSequenceID(ctx, tx, "sklv", "tma_skill_version_id_seq")
	if err != nil {
		return skills.Version{}, err
	}
	now := time.Now().UTC()
	packageFormat := skillpackage.FormatLegacyDB
	packageRoot := ""
	packageChecksum := ""
	packageObjectRefID := ""
	skillMDObjectRefID := ""
	packageManifest := json.RawMessage(`{}`)
	var storedPackage *skillpackage.StoredPackage
	objectCtx, err := ContextWithDatabaseAccessScope(ctx, AccessScope{WorkspaceID: workspaceID})
	if err != nil {
		return skills.Version{}, err
	}
	if repository := s.skillPackageRepository(); repository != nil {
		stored, storeErr := repository.Store(ctx, skillpackage.BuildInput{
			WorkspaceID: workspaceID, Identifier: identifier, Version: versionNumber,
			SkillMarkdown: input.ContentText, Assets: assetBundle,
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
		if storeErr != nil {
			return skills.Version{}, storeErr
		}
		storedPackage = &stored
		defer func() {
			if storedPackage != nil {
				repository.DeleteStored(context.Background(), *storedPackage)
			}
		}()
		packageFormat = skillpackage.FormatV1
		packageRoot = stored.Root
		packageChecksum = stored.PackageChecksum
		for index := range stored.Files {
			storedObject := &stored.Files[index]
			if storedObject.File.ObjectRefID == "" {
				metadata, _ := json.Marshal(map[string]any{
					"kind": "skill_package_file", "skill_id": input.SkillID, "skill_identifier": identifier,
					"skill_version": versionNumber, "package_path": storedObject.File.Path, "package_role": storedObject.File.Role,
				})
				objectRef, refErr := insertSkillPackageObjectRef(ctx, tx, CreateObjectRefInput{
					WorkspaceID: workspaceID, StorageProvider: repository.Provider(), Bucket: storedObject.Bucket,
					ObjectKey: storedObject.File.ObjectKey, ObjectVersion: storedObject.Version,
					ContentType: storedObject.File.ContentType, SizeBytes: storedObject.File.SizeBytes,
					ChecksumSHA256: storedObject.File.ChecksumSHA256, ETag: storedObject.ETag,
					Visibility: ObjectVisibilityWorkspace, Metadata: metadata, CreatedBy: input.CreatedBy,
				})
				if refErr != nil {
					return skills.Version{}, refErr
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
		packageManifest, err = skillpackage.EncodeManifest(stored)
		if err != nil {
			return skills.Version{}, err
		}
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO skill_versions (
			id, skill_id, version, content_format, manifest_json, content_text, assets_json,
			checksum_sha256, source_ref, source_revision, source_url,
			package_format, package_root, package_checksum_sha256, package_object_ref_id,
			skill_md_object_ref_id, package_manifest_json, created_by, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
			NULLIF($15, ''), NULLIF($16, ''), $17, $18, $19)
	`, id, input.SkillID, versionNumber, input.ContentFormat, manifest, input.ContentText, assets, checksum,
		strings.TrimSpace(input.SourceRef), strings.TrimSpace(input.SourceRevision), strings.TrimSpace(input.SourceURL),
		packageFormat, packageRoot, packageChecksum, packageObjectRefID, skillMDObjectRefID, packageManifest, input.CreatedBy, now)
	if err != nil {
		return skills.Version{}, err
	}
	if storedPackage != nil {
		for _, storedObject := range storedPackage.Files {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO skill_version_package_files (
					skill_version_id, path, role, object_ref_id, content_type, size_bytes,
					checksum_sha256, is_binary, executable, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			`, id, storedObject.File.Path, storedObject.File.Role, storedObject.File.ObjectRefID,
				storedObject.File.ContentType, storedObject.File.SizeBytes, storedObject.File.ChecksumSHA256,
				storedObject.File.Binary, storedObject.File.Executable, now); err != nil {
				return skills.Version{}, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return skills.Version{}, err
	}
	storedPackage = nil
	return skills.Version{
		ID: id, SkillID: input.SkillID, Version: versionNumber, ContentFormat: input.ContentFormat,
		Manifest: cloneSkillRaw(manifest), ContentText: input.ContentText, Assets: cloneSkillRaw(assets), Checksum: checksum,
		SourceRef: strings.TrimSpace(input.SourceRef), SourceRevision: strings.TrimSpace(input.SourceRevision),
		SourceURL: strings.TrimSpace(input.SourceURL), PackageFormat: packageFormat, PackageRoot: packageRoot,
		PackageChecksum: packageChecksum, PackageObjectRefID: packageObjectRefID,
		SkillMDObjectRefID: skillMDObjectRefID, PackageManifest: cloneSkillRaw(packageManifest),
		CreatedBy: input.CreatedBy, CreatedAt: now,
	}, nil
}

func (s *PostgresStore) GetSkillVersion(ctx context.Context, skillID string, version int) (skills.Version, error) {
	if strings.TrimSpace(skillID) == "" || version <= 0 {
		return skills.Version{}, fmt.Errorf("%w: skill id and positive version are required", ErrInvalid)
	}
	tx, err := s.beginSkillScopeTx(ctx, "")
	if err != nil {
		return skills.Version{}, err
	}
	defer tx.Rollback()
	result, err := scanSkillVersion(tx.QueryRowContext(ctx, `
		SELECT id, skill_id, version, content_format, manifest_json, content_text, assets_json, checksum_sha256,
			source_ref, source_revision, source_url, package_format, package_root, package_checksum_sha256,
			COALESCE(package_object_ref_id, ''), COALESCE(skill_md_object_ref_id, ''), package_manifest_json,
			created_by, created_at
		FROM skill_versions WHERE skill_id = $1 AND version = $2
	`, skillID, version))
	if err != nil {
		return skills.Version{}, err
	}
	return s.hydrateSkillVersion(ctx, result), nil
}

func (s *PostgresStore) ListSkillVersions(ctx context.Context, skillID string) ([]skills.Version, error) {
	if strings.TrimSpace(skillID) == "" {
		return nil, fmt.Errorf("%w: skill id is required", ErrInvalid)
	}
	tx, err := s.beginSkillScopeTx(ctx, "")
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM skills WHERE id = $1)`, skillID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, skill_id, version, content_format, manifest_json, content_text, assets_json, checksum_sha256,
			source_ref, source_revision, source_url, package_format, package_root, package_checksum_sha256,
			COALESCE(package_object_ref_id, ''), COALESCE(skill_md_object_ref_id, ''), package_manifest_json,
			created_by, created_at
		FROM skill_versions WHERE skill_id = $1 ORDER BY version DESC
	`, skillID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]skills.Version, 0)
	for rows.Next() {
		version, scanErr := scanSkillVersion(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, s.hydrateSkillVersion(ctx, version))
	}
	return result, rows.Err()
}

func (s *PostgresStore) RecordSkillUsages(ctx context.Context, usages []skills.Usage) error {
	if len(usages) == 0 {
		return nil
	}
	tx, err := s.beginSkillScopeTx(ctx, "")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, usage := range usages {
		session, sessionErr := getSessionTx(ctx, tx, strings.TrimSpace(usage.SessionID))
		if sessionErr != nil {
			return sessionErr
		}
		var skillWorkspaceID, skillIdentifier string
		if skillErr := tx.QueryRowContext(ctx, `
			SELECT workspace_id, identifier FROM skills WHERE id = $1
		`, strings.TrimSpace(usage.SkillID)).Scan(&skillWorkspaceID, &skillIdentifier); skillErr == sql.ErrNoRows {
			return ErrNotFound
		} else if skillErr != nil {
			return skillErr
		}
		if skillWorkspaceID != session.WorkspaceID {
			return fmt.Errorf("%w: skill belongs to another workspace", ErrForbidden)
		}
		var versionExists bool
		if versionErr := tx.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM skill_versions WHERE skill_id = $1 AND version = $2
			)
		`, strings.TrimSpace(usage.SkillID), usage.SkillVersion).Scan(&versionExists); versionErr != nil {
			return versionErr
		}
		if !versionExists {
			return ErrNotFound
		}
		usage.WorkspaceID = session.WorkspaceID
		usage.AgentID = session.AgentID
		usage.AgentConfigVersion = session.AgentConfigVersion
		usage.SkillIdentifier = skillIdentifier
		id, idErr := nextSequenceID(ctx, tx, "sklu", "tma_skill_usage_id_seq")
		if idErr != nil {
			return idErr
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO session_turn_skill_usages (
				id, workspace_id, session_id, turn_id, agent_id, agent_config_version, skill_id, skill_identifier,
				skill_version, requested_mode, rendered_mode, priority, estimated_tokens, status, failure_reason
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
			ON CONFLICT (session_id, turn_id, skill_id) DO UPDATE SET
				rendered_mode = EXCLUDED.rendered_mode,
				estimated_tokens = EXCLUDED.estimated_tokens,
				status = EXCLUDED.status,
				failure_reason = EXCLUDED.failure_reason
		`, id, usage.WorkspaceID, usage.SessionID, usage.TurnID, usage.AgentID, usage.AgentConfigVersion,
			usage.SkillID, usage.SkillIdentifier, usage.SkillVersion, usage.RequestedMode, usage.RenderedMode,
			usage.Priority, usage.EstimatedTokens, usage.Status, usage.FailureReason)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PostgresStore) ListSkillUsages(ctx context.Context, sessionID string, turnID string) ([]skills.Usage, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("%w: session id is required", ErrInvalid)
	}
	tx, err := s.beginSkillScopeTx(ctx, "")
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT workspace_id, session_id, turn_id, agent_id, agent_config_version, skill_id, skill_identifier,
			skill_version, requested_mode, rendered_mode, priority, estimated_tokens, status, failure_reason, created_at
		FROM session_turn_skill_usages
		WHERE session_id = $1 AND ($2 = '' OR turn_id = $2)
		ORDER BY created_at, skill_identifier
	`, sessionID, strings.TrimSpace(turnID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]skills.Usage, 0)
	for rows.Next() {
		var usage skills.Usage
		if err := rows.Scan(
			&usage.WorkspaceID, &usage.SessionID, &usage.TurnID, &usage.AgentID, &usage.AgentConfigVersion,
			&usage.SkillID, &usage.SkillIdentifier, &usage.SkillVersion, &usage.RequestedMode, &usage.RenderedMode,
			&usage.Priority, &usage.EstimatedTokens, &usage.Status, &usage.FailureReason, &usage.CreatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, usage)
	}
	return result, rows.Err()
}

func (s *PostgresStore) normalizeAgentSkills(ctx context.Context, workspaceID string, raw json.RawMessage) (json.RawMessage, error) {
	if raw == nil {
		return nil, nil
	}
	result, err := skills.ResolveRegistry(ctx, s, defaultString(strings.TrimSpace(workspaceID), DefaultWorkspaceID), raw, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	for _, resolved := range result.Skills {
		if resolved.Skill.Status != skills.StatusActive {
			return nil, fmt.Errorf("%w: skill %q is archived", ErrInvalid, resolved.Skill.Identifier)
		}
	}
	normalized, err := json.Marshal(result.Config)
	if err != nil {
		return nil, fmt.Errorf("%w: encode skills config: %v", ErrInvalid, err)
	}
	return normalized, nil
}

type skillScanner interface {
	Scan(dest ...any) error
}

func scanSkill(scanner skillScanner) (skills.Skill, error) {
	var skill skills.Skill
	var archivedAt sql.NullTime
	err := scanner.Scan(&skill.ID, &skill.WorkspaceID, &skill.Identifier, &skill.Title, &skill.Description, &skill.OwnerType,
		&skill.SourcePluginID, &skill.SourceType, &skill.SourceLocator, &skill.SourcePath,
		&skill.Status, &skill.CreatedBy, &skill.CreatedAt, &archivedAt)
	if err == sql.ErrNoRows {
		return skills.Skill{}, ErrNotFound
	}
	if err != nil {
		return skills.Skill{}, err
	}
	if archivedAt.Valid {
		skill.ArchivedAt = &archivedAt.Time
	}
	return skill, nil
}

func scanSkillVersion(scanner skillScanner) (skills.Version, error) {
	var version skills.Version
	var manifest, assets, packageManifest []byte
	err := scanner.Scan(&version.ID, &version.SkillID, &version.Version, &version.ContentFormat, &manifest,
		&version.ContentText, &assets, &version.Checksum, &version.SourceRef, &version.SourceRevision,
		&version.SourceURL, &version.PackageFormat, &version.PackageRoot, &version.PackageChecksum,
		&version.PackageObjectRefID, &version.SkillMDObjectRefID, &packageManifest,
		&version.CreatedBy, &version.CreatedAt)
	if err == sql.ErrNoRows {
		return skills.Version{}, ErrNotFound
	}
	if err != nil {
		return skills.Version{}, err
	}
	version.Manifest = cloneSkillRaw(manifest)
	version.Assets = cloneSkillRaw(assets)
	version.PackageManifest = cloneSkillRaw(packageManifest)
	return version, nil
}

func (s *PostgresStore) hydrateSkillVersion(ctx context.Context, version skills.Version) skills.Version {
	repository := s.skillPackageRepository()
	if repository == nil || version.PackageFormat != skillpackage.FormatV1 || version.SkillMDObjectRefID == "" {
		return version
	}
	objectRef, err := s.GetObjectRefContext(ctx, version.SkillMDObjectRefID)
	if err != nil {
		return version
	}
	content, err := repository.Read(ctx, skillpackage.BinaryObject{
		ObjectRefID: objectRef.ID, Bucket: objectRef.Bucket, Key: objectRef.ObjectKey, Version: objectRef.ObjectVersion,
	})
	if err == nil {
		version.ContentText = string(content)
	}
	return version
}

func insertSkillPackageObjectRef(ctx context.Context, tx *sql.Tx, input CreateObjectRefInput) (ObjectRef, error) {
	id, err := nextSequenceID(ctx, tx, "obj", "tma_object_ref_id_seq")
	if err != nil {
		return ObjectRef{}, err
	}
	object := ObjectRef{
		ID: id, WorkspaceID: defaultString(input.WorkspaceID, DefaultWorkspaceID),
		StorageProvider: defaultString(input.StorageProvider, ObjectStorageProviderS3),
		Bucket:          input.Bucket, ObjectKey: input.ObjectKey, ObjectVersion: input.ObjectVersion,
		ContentType: input.ContentType, SizeBytes: input.SizeBytes, ChecksumSHA256: input.ChecksumSHA256,
		ETag: input.ETag, Visibility: ObjectVisibilityWorkspace, Metadata: metadataJSON(input.Metadata),
		CreatedBy: defaultString(input.CreatedBy, "system"), CreatedAt: time.Now().UTC(),
	}
	return scanObjectRef(tx.QueryRowContext(ctx, `
		INSERT INTO object_refs (
			id, workspace_id, storage_provider, bucket, object_key, object_version, content_type,
			size_bytes, checksum_sha256, etag, visibility, metadata_json, created_by, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id, workspace_id, storage_provider, bucket, object_key, object_version,
			content_type, size_bytes, checksum_sha256, etag, visibility, metadata_json, created_by, created_at
	`, object.ID, object.WorkspaceID, object.StorageProvider, object.Bucket, object.ObjectKey,
		object.ObjectVersion, object.ContentType, object.SizeBytes, object.ChecksumSHA256, object.ETag,
		object.Visibility, object.Metadata, object.CreatedBy, object.CreatedAt))
}

func validSkillOwnerType(value string) bool {
	return value == skills.OwnerTypeBuiltin || value == skills.OwnerTypeWorkspace || value == skills.OwnerTypePlugin
}

func validSkillSourceType(value string) bool {
	return value == skills.SourceTypeInline || value == skills.SourceTypeGitHub ||
		value == skills.SourceTypeArtifact || value == skills.SourceTypeCatalog ||
		value == skills.SourceTypePlugin || value == skills.SourceTypeBuiltin
}

func defaultSkillSourceType(ownerType string) string {
	switch ownerType {
	case skills.OwnerTypePlugin:
		return skills.SourceTypePlugin
	case skills.OwnerTypeBuiltin:
		return skills.SourceTypeBuiltin
	default:
		return skills.SourceTypeInline
	}
}

func cloneSkillRaw(value []byte) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}
