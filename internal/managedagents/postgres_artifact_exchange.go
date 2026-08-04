package managedagents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const artifactExchangeColumns = `
	id, workspace_id, app_id, owner_id, direction, status, session_id, object_ref_id,
	artifact_id, filename, description, artifact_type, environment_id, turn_id, tool_call_id,
	visibility, content_type, expected_size_bytes, max_size_bytes,
	expected_checksum_sha256, expires_at, claimed_at, completed_at, error_message,
	metadata_json, created_by, created_at, updated_at
`

func scanArtifactExchange(scanner rowScanner) (ArtifactExchange, error) {
	var item ArtifactExchange
	var appID, sessionID, objectRefID, artifactID, environmentID sql.NullString
	var expectedSize sql.NullInt64
	var claimedAt, completedAt sql.NullTime
	err := scanner.Scan(
		&item.ID, &item.WorkspaceID, &appID, &item.OwnerID, &item.Direction, &item.Status,
		&sessionID, &objectRefID, &artifactID, &item.Filename, &item.Description,
		&item.ArtifactType, &environmentID, &item.TurnID, &item.ToolCallID, &item.Visibility, &item.ContentType,
		&expectedSize, &item.MaxSizeBytes, &item.ExpectedChecksumSHA256, &item.ExpiresAt,
		&claimedAt, &completedAt, &item.ErrorMessage, &item.Metadata, &item.CreatedBy,
		&item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return ArtifactExchange{}, err
	}
	item.AppID = appID.String
	item.SessionID = sessionID.String
	item.ObjectRefID = objectRefID.String
	item.ArtifactID = artifactID.String
	item.EnvironmentID = environmentID.String
	if expectedSize.Valid {
		value := expectedSize.Int64
		item.ExpectedSizeBytes = &value
	}
	if claimedAt.Valid {
		item.ClaimedAt = &claimedAt.Time
	}
	if completedAt.Valid {
		item.CompletedAt = &completedAt.Time
	}
	return item, nil
}

func validateArtifactExchangeCreate(input CreateArtifactExchangeInput) error {
	if input.OwnerID == "" || input.Filename == "" || len(input.Filename) > 512 {
		return fmt.Errorf("%w: exchange owner_id and filename are required", ErrInvalid)
	}
	if len(input.TokenHash) != 32 || input.ExpiresAt.IsZero() {
		return fmt.Errorf("%w: exchange token hash and expires_at are required", ErrInvalid)
	}
	if input.MaxSizeBytes < 0 || input.ExpectedSizeBytes != nil && (*input.ExpectedSizeBytes < 0 || *input.ExpectedSizeBytes > input.MaxSizeBytes) {
		return fmt.Errorf("%w: invalid exchange size bounds", ErrInvalid)
	}
	checksum := strings.ToLower(strings.TrimSpace(input.ExpectedChecksumSHA256))
	if checksum != "" && !isLowerHexSHA256(checksum) {
		return fmt.Errorf("%w: expected checksum must be a SHA-256 hex digest", ErrInvalid)
	}
	if normalizeArtifactType(input.ArtifactType) == "" || normalizeObjectVisibility(input.Visibility) == "" {
		return fmt.Errorf("%w: unsupported artifact type or visibility", ErrInvalid)
	}
	switch input.Direction {
	case ArtifactExchangeDirectionImport:
		if input.SessionID == "" || input.ObjectRefID != "" || input.ArtifactID != "" {
			return fmt.Errorf("%w: import exchange requires only session_id", ErrInvalid)
		}
	case ArtifactExchangeDirectionExport:
		if input.ObjectRefID == "" {
			return fmt.Errorf("%w: export exchange requires object_ref_id", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: unsupported exchange direction %q", ErrInvalid, input.Direction)
	}
	return nil
}

func isLowerHexSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			if char < 'a' || char > 'f' {
				return false
			}
		}
	}
	return true
}

func (s *PostgresStore) CreateArtifactExchangeContext(ctx context.Context, input CreateArtifactExchangeInput) (ArtifactExchange, error) {
	input.AppID = strings.TrimSpace(input.AppID)
	input.OwnerID = strings.TrimSpace(input.OwnerID)
	input.Filename = strings.TrimSpace(input.Filename)
	input.ExpectedChecksumSHA256 = strings.ToLower(strings.TrimSpace(input.ExpectedChecksumSHA256))
	if err := validateArtifactExchangeCreate(input); err != nil {
		return ArtifactExchange{}, err
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return ArtifactExchange{}, err
	}
	defer tx.Rollback()
	if input.AppID != "" {
		var appExists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM service_identities WHERE workspace_id = $1 AND id = $2 AND kind = 'application' AND status = 'active'
		)`, scope.WorkspaceID, input.AppID).Scan(&appExists); err != nil {
			return ArtifactExchange{}, err
		}
		if !appExists {
			return ArtifactExchange{}, fmt.Errorf("%w: active application identity not found", ErrInvalid)
		}
	}
	id, err := nextSequenceID(ctx, tx, "aex", "tma_artifact_exchange_id_seq")
	if err != nil {
		return ArtifactExchange{}, err
	}
	now := time.Now().UTC()
	item, err := scanArtifactExchange(tx.QueryRowContext(ctx, `
		INSERT INTO artifact_exchanges (
			id, workspace_id, app_id, owner_id, direction, session_id, object_ref_id, artifact_id,
			filename, description, artifact_type, environment_id, turn_id, tool_call_id, visibility,
			content_type, expected_size_bytes, max_size_bytes, expected_checksum_sha256,
			token_hash, expires_at, metadata_json, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $24)
		RETURNING `+artifactExchangeColumns,
		id, scope.WorkspaceID, nullableString(input.AppID), input.OwnerID, input.Direction,
		nullableString(input.SessionID), nullableString(input.ObjectRefID), nullableString(input.ArtifactID),
		input.Filename, input.Description, normalizeArtifactType(input.ArtifactType), nullableString(input.EnvironmentID),
		input.TurnID, input.ToolCallID, normalizeObjectVisibility(input.Visibility), input.ContentType,
		input.ExpectedSizeBytes, input.MaxSizeBytes, input.ExpectedChecksumSHA256, input.TokenHash,
		input.ExpiresAt.UTC(), metadataJSON(input.Metadata), defaultString(input.CreatedBy, input.OwnerID), now,
	))
	if err != nil {
		return ArtifactExchange{}, err
	}
	if err := tx.Commit(); err != nil {
		return ArtifactExchange{}, err
	}
	return item, nil
}

func (s *PostgresStore) GetArtifactExchangeContext(ctx context.Context, id string) (ArtifactExchange, error) {
	scope, ok := DatabaseAccessScopeFromContext(ctx)
	if !ok || strings.TrimSpace(id) == "" {
		return ArtifactExchange{}, fmt.Errorf("%w: database scope and exchange id are required", ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
	if err != nil {
		return ArtifactExchange{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		UPDATE artifact_exchanges SET status = 'expired', updated_at = $3
		WHERE id = $1 AND workspace_id = $2 AND status = 'pending' AND expires_at <= $3
	`, id, scope.WorkspaceID, now); err != nil {
		return ArtifactExchange{}, err
	}
	item, err := scanArtifactExchange(tx.QueryRowContext(ctx,
		`SELECT `+artifactExchangeColumns+` FROM artifact_exchanges WHERE id = $1 AND workspace_id = $2`, id, scope.WorkspaceID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return ArtifactExchange{}, ErrNotFound
	}
	if err != nil {
		return ArtifactExchange{}, err
	}
	if err := tx.Commit(); err != nil {
		return ArtifactExchange{}, err
	}
	return item, nil
}

func (s *PostgresStore) ClaimArtifactExchangeContext(ctx context.Context, input ClaimArtifactExchangeInput) (ArtifactExchange, error) {
	if input.ID == "" || input.WorkspaceID == "" || len(input.TokenHash) != 32 || input.ClaimedAt.IsZero() {
		return ArtifactExchange{}, fmt.Errorf("%w: complete exchange claim input is required", ErrInvalid)
	}
	if input.Direction != ArtifactExchangeDirectionImport && input.Direction != ArtifactExchangeDirectionExport {
		return ArtifactExchange{}, fmt.Errorf("%w: invalid exchange claim direction", ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return ArtifactExchange{}, err
	}
	defer tx.Rollback()
	claimedAt := input.ClaimedAt.UTC()
	if _, err := tx.ExecContext(ctx, `
		UPDATE artifact_exchanges SET status = 'expired', updated_at = $3
		WHERE id = $1 AND workspace_id = $2 AND status = 'pending' AND expires_at <= $3
	`, input.ID, scope.WorkspaceID, claimedAt); err != nil {
		return ArtifactExchange{}, err
	}
	item, err := scanArtifactExchange(tx.QueryRowContext(ctx, `
		UPDATE artifact_exchanges
		SET status = 'processing', claimed_at = $5, updated_at = $5
		WHERE id = $1 AND workspace_id = $2 AND direction = $3 AND token_hash = $4
			AND status = 'pending' AND expires_at > $5
		RETURNING `+artifactExchangeColumns,
		input.ID, scope.WorkspaceID, input.Direction, input.TokenHash, claimedAt,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return ArtifactExchange{}, ErrNotFound
	}
	if err != nil {
		return ArtifactExchange{}, err
	}
	if err := tx.Commit(); err != nil {
		return ArtifactExchange{}, err
	}
	return item, nil
}

func (s *PostgresStore) CompleteArtifactImportContext(ctx context.Context, input CompleteArtifactImportInput) (ArtifactExchange, ObjectRef, SessionArtifact, error) {
	if input.ID == "" || input.WorkspaceID == "" || input.CompletedAt.IsZero() {
		return ArtifactExchange{}, ObjectRef{}, SessionArtifact{}, fmt.Errorf("%w: complete import input is required", ErrInvalid)
	}
	visibility := normalizeObjectVisibility(input.ObjectRef.Visibility)
	artifactType := normalizeArtifactType(input.Artifact.ArtifactType)
	if input.ObjectRef.Bucket == "" || input.ObjectRef.ObjectKey == "" || input.ObjectRef.SizeBytes < 0 || visibility == "" || artifactType == "" {
		return ArtifactExchange{}, ObjectRef{}, SessionArtifact{}, fmt.Errorf("%w: invalid imported object or artifact", ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return ArtifactExchange{}, ObjectRef{}, SessionArtifact{}, err
	}
	defer tx.Rollback()
	exchange, err := scanArtifactExchange(tx.QueryRowContext(ctx, `
		SELECT `+artifactExchangeColumns+` FROM artifact_exchanges
		WHERE id = $1 AND workspace_id = $2 AND direction = 'import' AND status = 'processing'
		FOR UPDATE
	`, input.ID, scope.WorkspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return ArtifactExchange{}, ObjectRef{}, SessionArtifact{}, ErrConflict
	}
	if err != nil {
		return ArtifactExchange{}, ObjectRef{}, SessionArtifact{}, err
	}
	session, err := getSessionTx(ctx, tx, exchange.SessionID)
	if err != nil {
		return ArtifactExchange{}, ObjectRef{}, SessionArtifact{}, err
	}
	if session.WorkspaceID != scope.WorkspaceID {
		return ArtifactExchange{}, ObjectRef{}, SessionArtifact{}, ErrForbidden
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "tma-skill-asset-gc:"+scope.WorkspaceID); err != nil {
		return ArtifactExchange{}, ObjectRef{}, SessionArtifact{}, err
	}
	objectID, err := nextSequenceID(ctx, tx, "obj", "tma_object_ref_id_seq")
	if err != nil {
		return ArtifactExchange{}, ObjectRef{}, SessionArtifact{}, err
	}
	completedAt := input.CompletedAt.UTC()
	object, err := scanObjectRef(tx.QueryRowContext(ctx, `
		INSERT INTO object_refs (
			id, workspace_id, storage_provider, bucket, object_key, object_version, content_type,
			size_bytes, checksum_sha256, etag, visibility, metadata_json, created_by, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id, workspace_id, storage_provider, bucket, object_key, object_version,
			content_type, size_bytes, checksum_sha256, etag, visibility, metadata_json, created_by, created_at
	`, objectID, scope.WorkspaceID, defaultString(input.ObjectRef.StorageProvider, ObjectStorageProviderS3),
		input.ObjectRef.Bucket, input.ObjectRef.ObjectKey, input.ObjectRef.ObjectVersion,
		input.ObjectRef.ContentType, input.ObjectRef.SizeBytes, input.ObjectRef.ChecksumSHA256,
		input.ObjectRef.ETag, visibility, metadataJSON(input.ObjectRef.Metadata),
		defaultString(input.ObjectRef.CreatedBy, exchange.CreatedBy), completedAt,
	))
	if err != nil {
		return ArtifactExchange{}, ObjectRef{}, SessionArtifact{}, err
	}
	artifactID, err := nextSequenceID(ctx, tx, "art", "tma_session_artifact_id_seq")
	if err != nil {
		return ArtifactExchange{}, ObjectRef{}, SessionArtifact{}, err
	}
	name := exchange.Filename
	artifact, err := scanSessionArtifact(tx.QueryRowContext(ctx, `
		INSERT INTO session_artifacts (
			id, workspace_id, session_id, environment_id, object_ref_id, turn_id, tool_call_id,
			name, description, artifact_type, metadata_json, created_by, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id, workspace_id, session_id, environment_id, object_ref_id, turn_id,
			tool_call_id, name, description, artifact_type, metadata_json, created_by, created_at
	`, artifactID, scope.WorkspaceID, exchange.SessionID,
		nullableString(defaultString(exchange.EnvironmentID, session.EnvironmentID)), object.ID,
		exchange.TurnID, exchange.ToolCallID, name, exchange.Description,
		exchange.ArtifactType, metadataJSON(exchange.Metadata), exchange.CreatedBy, completedAt,
	))
	if err != nil {
		return ArtifactExchange{}, ObjectRef{}, SessionArtifact{}, err
	}
	if err := insertObjectRefLink(ctx, tx, scope.WorkspaceID, object.ID, objectRefLinkOwnerSessionArtifact, artifact.ID, artifact.ArtifactType); err != nil {
		return ArtifactExchange{}, ObjectRef{}, SessionArtifact{}, err
	}
	if err := enqueueArtifactCreatedDeliveriesTx(ctx, tx, session, artifact); err != nil {
		return ArtifactExchange{}, ObjectRef{}, SessionArtifact{}, err
	}
	exchange, err = scanArtifactExchange(tx.QueryRowContext(ctx, `
		UPDATE artifact_exchanges
		SET status = 'completed', object_ref_id = $3, artifact_id = $4,
			completed_at = $5, updated_at = $5, error_message = ''
		WHERE id = $1 AND workspace_id = $2 AND status = 'processing'
		RETURNING `+artifactExchangeColumns,
		input.ID, scope.WorkspaceID, object.ID, artifact.ID, completedAt,
	))
	if err != nil {
		return ArtifactExchange{}, ObjectRef{}, SessionArtifact{}, err
	}
	if err := tx.Commit(); err != nil {
		return ArtifactExchange{}, ObjectRef{}, SessionArtifact{}, err
	}
	return exchange, object, artifact, nil
}

func (s *PostgresStore) CompleteArtifactExportContext(ctx context.Context, id string, completedAt time.Time) (ArtifactExchange, error) {
	return s.finishArtifactExchange(ctx, id, completedAt, ArtifactExchangeStatusCompleted, "")
}

func (s *PostgresStore) FailArtifactExchangeContext(ctx context.Context, id string, failedAt time.Time, message string) (ArtifactExchange, error) {
	if len(message) > 1024 {
		message = message[:1024]
	}
	return s.finishArtifactExchange(ctx, id, failedAt, ArtifactExchangeStatusFailed, message)
}

func (s *PostgresStore) finishArtifactExchange(ctx context.Context, id string, at time.Time, status, message string) (ArtifactExchange, error) {
	scope, ok := DatabaseAccessScopeFromContext(ctx)
	if !ok || id == "" || at.IsZero() {
		return ArtifactExchange{}, fmt.Errorf("%w: database scope, exchange id, and completion time are required", ErrInvalid)
	}
	if status != ArtifactExchangeStatusCompleted && status != ArtifactExchangeStatusFailed {
		return ArtifactExchange{}, fmt.Errorf("%w: unsupported final exchange status", ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
	if err != nil {
		return ArtifactExchange{}, err
	}
	defer tx.Rollback()
	completedAt := any(nil)
	if status == ArtifactExchangeStatusCompleted {
		completedAt = at.UTC()
	}
	item, err := scanArtifactExchange(tx.QueryRowContext(ctx, `
		UPDATE artifact_exchanges
		SET status = $3, completed_at = $4, error_message = $5, updated_at = $6
		WHERE id = $1 AND workspace_id = $2 AND status = 'processing'
		RETURNING `+artifactExchangeColumns,
		id, scope.WorkspaceID, status, completedAt, message, at.UTC(),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return ArtifactExchange{}, ErrConflict
	}
	if err != nil {
		return ArtifactExchange{}, err
	}
	if err := tx.Commit(); err != nil {
		return ArtifactExchange{}, err
	}
	return item, nil
}
