package managedagents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *PostgresStore) CreateObjectRefContext(ctx context.Context, input CreateObjectRefInput) (ObjectRef, error) {
	scope, ok := DatabaseAccessScopeFromContext(ctx)
	if !ok {
		return s.CreateObjectRef(input)
	}
	if input.WorkspaceID != "" && input.WorkspaceID != scope.WorkspaceID {
		return ObjectRef{}, fmt.Errorf("%w: object ref workspace scope mismatch", ErrForbidden)
	}
	if input.Bucket == "" {
		return ObjectRef{}, fmt.Errorf("%w: object bucket is required", ErrInvalid)
	}
	if input.ObjectKey == "" {
		return ObjectRef{}, fmt.Errorf("%w: object_key is required", ErrInvalid)
	}
	if input.SizeBytes < 0 {
		return ObjectRef{}, fmt.Errorf("%w: object size_bytes must be non-negative", ErrInvalid)
	}
	visibility := normalizeObjectVisibility(input.Visibility)
	if visibility == "" {
		return ObjectRef{}, fmt.Errorf("%w: unsupported object visibility %q", ErrInvalid, input.Visibility)
	}

	tx, scope, err := s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
	if err != nil {
		return ObjectRef{}, err
	}
	defer tx.Rollback()
	id, err := nextSequenceID(ctx, tx, "obj", "tma_object_ref_id_seq")
	if err != nil {
		return ObjectRef{}, err
	}
	object := ObjectRef{
		ID:              id,
		WorkspaceID:     scope.WorkspaceID,
		StorageProvider: defaultString(input.StorageProvider, ObjectStorageProviderS3),
		Bucket:          input.Bucket,
		ObjectKey:       input.ObjectKey,
		ObjectVersion:   input.ObjectVersion,
		ContentType:     input.ContentType,
		SizeBytes:       input.SizeBytes,
		ChecksumSHA256:  input.ChecksumSHA256,
		ETag:            input.ETag,
		Visibility:      visibility,
		Metadata:        metadataJSON(input.Metadata),
		CreatedBy:       defaultString(input.CreatedBy, "system"),
		CreatedAt:       time.Now().UTC(),
	}
	created, err := scanObjectRef(tx.QueryRowContext(ctx, `
		INSERT INTO object_refs (
			id, workspace_id, storage_provider, bucket, object_key, object_version, content_type,
			size_bytes, checksum_sha256, etag, visibility, metadata_json, created_by, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id, workspace_id, storage_provider, bucket, object_key, object_version,
			content_type, size_bytes, checksum_sha256, etag, visibility, metadata_json, created_by, created_at
	`, object.ID, object.WorkspaceID, object.StorageProvider, object.Bucket, object.ObjectKey,
		object.ObjectVersion, object.ContentType, object.SizeBytes, object.ChecksumSHA256, object.ETag,
		object.Visibility, object.Metadata, object.CreatedBy, object.CreatedAt))
	if err != nil {
		return ObjectRef{}, err
	}
	if err := tx.Commit(); err != nil {
		return ObjectRef{}, err
	}
	return created, nil
}

func (s *PostgresStore) GetObjectRefContext(ctx context.Context, id string) (ObjectRef, error) {
	scope, ok := DatabaseAccessScopeFromContext(ctx)
	if !ok {
		return s.GetObjectRef(id)
	}
	if id == "" {
		return ObjectRef{}, fmt.Errorf("%w: object ref id is required", ErrInvalid)
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
	if err != nil {
		return ObjectRef{}, err
	}
	defer tx.Rollback()
	object, err := scanObjectRef(tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, storage_provider, bucket, object_key, object_version,
			content_type, size_bytes, checksum_sha256, etag, visibility, metadata_json, created_by, created_at
		FROM object_refs WHERE id = $1 AND workspace_id = $2
	`, id, scope.WorkspaceID))
	if err == sql.ErrNoRows {
		return ObjectRef{}, ErrForbidden
	}
	if err != nil {
		return ObjectRef{}, err
	}
	if err := tx.Commit(); err != nil {
		return ObjectRef{}, err
	}
	return object, nil
}

func (s *PostgresStore) CountSessionArtifactsByObjectRefContext(ctx context.Context, objectRefID string) (int, error) {
	scope, ok := DatabaseAccessScopeFromContext(ctx)
	if !ok {
		return s.CountSessionArtifactsByObjectRef(objectRefID)
	}
	if objectRefID == "" {
		return 0, fmt.Errorf("%w: object ref id is required", ErrInvalid)
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var objectVisible bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM object_refs WHERE id = $1 AND workspace_id = $2)
	`, objectRefID, scope.WorkspaceID).Scan(&objectVisible); err != nil {
		return 0, err
	}
	if !objectVisible {
		return 0, ErrForbidden
	}
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM session_artifacts WHERE object_ref_id = $1 AND workspace_id = $2
	`, objectRefID, scope.WorkspaceID).Scan(&count); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *PostgresStore) DeleteObjectRefContext(ctx context.Context, id string) error {
	scope, ok := DatabaseAccessScopeFromContext(ctx)
	if !ok {
		return s.DeleteObjectRef(id)
	}
	if id == "" {
		return fmt.Errorf("%w: object ref id is required", ErrInvalid)
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM object_refs WHERE id = $1 AND workspace_id = $2`, id, scope.WorkspaceID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrForbidden
	}
	return tx.Commit()
}

func (s *PostgresStore) CreateSessionArtifactContext(ctx context.Context, input CreateSessionArtifactInput) (SessionArtifact, error) {
	scope, ok := DatabaseAccessScopeFromContext(ctx)
	if !ok {
		return s.CreateSessionArtifact(input)
	}
	if input.SessionID == "" {
		return SessionArtifact{}, fmt.Errorf("%w: artifact session_id is required", ErrInvalid)
	}
	if input.ObjectRefID == "" {
		return SessionArtifact{}, fmt.Errorf("%w: artifact object_ref_id is required", ErrInvalid)
	}
	if input.WorkspaceID != "" && input.WorkspaceID != scope.WorkspaceID {
		return SessionArtifact{}, fmt.Errorf("%w: artifact workspace scope mismatch", ErrForbidden)
	}
	artifactType := normalizeArtifactType(input.ArtifactType)
	if artifactType == "" {
		return SessionArtifact{}, fmt.Errorf("%w: unsupported artifact_type %q", ErrInvalid, input.ArtifactType)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
	if err != nil {
		return SessionArtifact{}, err
	}
	defer tx.Rollback()
	session, err := getSessionTx(ctx, tx, input.SessionID)
	if errors.Is(err, ErrNotFound) {
		return SessionArtifact{}, ErrForbidden
	}
	if err != nil {
		return SessionArtifact{}, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "tma-skill-asset-gc:"+scope.WorkspaceID); err != nil {
		return SessionArtifact{}, err
	}
	object, err := scanObjectRef(tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, storage_provider, bucket, object_key, object_version,
			content_type, size_bytes, checksum_sha256, etag, visibility, metadata_json, created_by, created_at
		FROM object_refs WHERE id = $1 AND workspace_id = $2
	`, input.ObjectRefID, scope.WorkspaceID))
	if err == sql.ErrNoRows {
		return SessionArtifact{}, ErrForbidden
	}
	if err != nil {
		return SessionArtifact{}, err
	}
	if session.WorkspaceID != object.WorkspaceID {
		return SessionArtifact{}, fmt.Errorf("%w: artifact workspace mismatch", ErrForbidden)
	}
	id, err := nextSequenceID(ctx, tx, "art", "tma_session_artifact_id_seq")
	if err != nil {
		return SessionArtifact{}, err
	}
	name := input.Name
	if name == "" {
		name = object.ObjectKey
	}
	artifact := SessionArtifact{
		ID:            id,
		WorkspaceID:   scope.WorkspaceID,
		SessionID:     input.SessionID,
		EnvironmentID: defaultString(input.EnvironmentID, session.EnvironmentID),
		ObjectRefID:   input.ObjectRefID,
		TurnID:        input.TurnID,
		ToolCallID:    input.ToolCallID,
		Name:          name,
		Description:   input.Description,
		ArtifactType:  artifactType,
		Metadata:      metadataJSON(input.Metadata),
		CreatedBy:     defaultString(input.CreatedBy, "system"),
		CreatedAt:     time.Now().UTC(),
	}
	created, err := scanSessionArtifact(tx.QueryRowContext(ctx, `
		INSERT INTO session_artifacts (
			id, workspace_id, session_id, environment_id, object_ref_id, turn_id, tool_call_id,
			name, description, artifact_type, metadata_json, created_by, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id, workspace_id, session_id, environment_id, object_ref_id, turn_id,
			tool_call_id, name, description, artifact_type, metadata_json, created_by, created_at
	`, artifact.ID, artifact.WorkspaceID, artifact.SessionID, nullableString(artifact.EnvironmentID),
		artifact.ObjectRefID, artifact.TurnID, artifact.ToolCallID, artifact.Name, artifact.Description,
		artifact.ArtifactType, artifact.Metadata, artifact.CreatedBy, artifact.CreatedAt))
	if err != nil {
		return SessionArtifact{}, err
	}
	if err := tx.Commit(); err != nil {
		return SessionArtifact{}, err
	}
	return created, nil
}

func (s *PostgresStore) GetSessionArtifactContext(ctx context.Context, sessionID string, artifactID string) (SessionArtifact, error) {
	scope, ok := DatabaseAccessScopeFromContext(ctx)
	if !ok {
		return s.GetSessionArtifact(sessionID, artifactID)
	}
	if sessionID == "" {
		return SessionArtifact{}, fmt.Errorf("%w: artifact session_id is required", ErrInvalid)
	}
	if artifactID == "" {
		return SessionArtifact{}, fmt.Errorf("%w: artifact id is required", ErrInvalid)
	}
	if _, err := s.GetSessionScoped(sessionID, scope); err != nil {
		return SessionArtifact{}, err
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
	if err != nil {
		return SessionArtifact{}, err
	}
	defer tx.Rollback()
	artifact, err := scanSessionArtifact(tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, session_id, environment_id, object_ref_id, turn_id,
			tool_call_id, name, description, artifact_type, metadata_json, created_by, created_at
		FROM session_artifacts WHERE session_id = $1 AND id = $2 AND workspace_id = $3
	`, sessionID, artifactID, scope.WorkspaceID))
	if err == sql.ErrNoRows {
		return SessionArtifact{}, ErrForbidden
	}
	if err != nil {
		return SessionArtifact{}, err
	}
	if err := tx.Commit(); err != nil {
		return SessionArtifact{}, err
	}
	return artifact, nil
}

func (s *PostgresStore) DeleteSessionArtifactContext(ctx context.Context, sessionID string, artifactID string) error {
	scope, ok := DatabaseAccessScopeFromContext(ctx)
	if !ok {
		return s.DeleteSessionArtifact(sessionID, artifactID)
	}
	if sessionID == "" {
		return fmt.Errorf("%w: artifact session_id is required", ErrInvalid)
	}
	if artifactID == "" {
		return fmt.Errorf("%w: artifact id is required", ErrInvalid)
	}
	if _, err := s.GetSessionScoped(sessionID, scope); err != nil {
		return err
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		DELETE FROM session_artifacts WHERE session_id = $1 AND id = $2 AND workspace_id = $3
	`, sessionID, artifactID, scope.WorkspaceID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrForbidden
	}
	return tx.Commit()
}

func (s *PostgresStore) ListSessionArtifactsContext(ctx context.Context, sessionID string) ([]SessionArtifact, error) {
	scope, ok := DatabaseAccessScopeFromContext(ctx)
	if !ok {
		return s.ListSessionArtifacts(sessionID)
	}
	if sessionID == "" {
		return nil, fmt.Errorf("%w: artifact session_id is required", ErrInvalid)
	}
	if _, err := s.GetSessionScoped(sessionID, scope); err != nil {
		return nil, err
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT id, workspace_id, session_id, environment_id, object_ref_id, turn_id,
			tool_call_id, name, description, artifact_type, metadata_json, created_by, created_at
		FROM session_artifacts WHERE session_id = $1 AND workspace_id = $2
		ORDER BY created_at ASC, id ASC
	`, sessionID, scope.WorkspaceID)
	if err != nil {
		return nil, err
	}
	artifacts := []SessionArtifact{}
	for rows.Next() {
		artifact, err := scanSessionArtifact(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return artifacts, nil
}
