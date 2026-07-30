package managedagents

import (
	"context"
	"fmt"
	"strings"

	"tiggy-manage-agent/internal/objectreconcile"
)

const objectReconciliationReferenceColumns = `
	id, workspace_id, storage_provider, bucket, object_key, object_version,
	content_type, size_bytes, checksum_sha256, etag
`

func (s *PostgresStore) ListObjectReconciliationReferences(ctx context.Context, input objectreconcile.ListReferencesInput) (objectreconcile.ReferencePage, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.StorageProvider = strings.TrimSpace(input.StorageProvider)
	input.Bucket = strings.TrimSpace(input.Bucket)
	input.Prefix = strings.TrimSpace(input.Prefix)
	if input.WorkspaceID == "" || input.StorageProvider == "" || input.Bucket == "" || input.Prefix == "" || input.Limit < 1 || input.Limit > 500 {
		return objectreconcile.ReferencePage{}, fmt.Errorf("%w: invalid reconciliation reference list", objectreconcile.ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return objectreconcile.ReferencePage{}, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT `+objectReconciliationReferenceColumns+`
		FROM object_refs
		WHERE workspace_id = $1 AND storage_provider = $2 AND bucket = $3
			AND LEFT(object_key, LENGTH($4)) = $4
		ORDER BY object_key, object_version, id
		LIMIT $5
	`, scope.WorkspaceID, input.StorageProvider, input.Bucket, input.Prefix, input.Limit+1)
	if err != nil {
		return objectreconcile.ReferencePage{}, err
	}
	references, err := scanObjectReconciliationReferences(rows)
	if err != nil {
		return objectreconcile.ReferencePage{}, err
	}
	page := objectreconcile.ReferencePage{References: references}
	if len(page.References) > input.Limit {
		page.References = page.References[:input.Limit]
		page.Truncated = true
	}
	if err := tx.Commit(); err != nil {
		return objectreconcile.ReferencePage{}, err
	}
	return page, nil
}

func (s *PostgresStore) LookupObjectReconciliationReferences(ctx context.Context, input objectreconcile.LookupReferencesInput) ([]objectreconcile.Reference, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.StorageProvider = strings.TrimSpace(input.StorageProvider)
	input.Bucket = strings.TrimSpace(input.Bucket)
	if input.WorkspaceID == "" || input.StorageProvider == "" || input.Bucket == "" || len(input.ObjectKeys) < 1 || len(input.ObjectKeys) > 500 {
		return nil, fmt.Errorf("%w: invalid reconciliation reference lookup", objectreconcile.ErrInvalid)
	}
	arguments := []any{input.WorkspaceID, input.StorageProvider, input.Bucket}
	placeholders := make([]string, 0, len(input.ObjectKeys))
	for _, key := range input.ObjectKeys {
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("%w: object key is required", objectreconcile.ErrInvalid)
		}
		arguments = append(arguments, key)
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(arguments)))
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	arguments[0] = scope.WorkspaceID
	rows, err := tx.QueryContext(ctx, `
		SELECT `+objectReconciliationReferenceColumns+`
		FROM object_refs
		WHERE workspace_id = $1 AND storage_provider = $2 AND bucket = $3
			AND object_key IN (`+strings.Join(placeholders, ", ")+`)
		ORDER BY object_key, object_version, id
	`, arguments...)
	if err != nil {
		return nil, err
	}
	references, err := scanObjectReconciliationReferences(rows)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return references, nil
}

func scanObjectReconciliationReferences(rows interface {
	Next() bool
	Scan(...any) error
	Close() error
	Err() error
}) ([]objectreconcile.Reference, error) {
	defer rows.Close()
	references := []objectreconcile.Reference{}
	for rows.Next() {
		var reference objectreconcile.Reference
		if err := rows.Scan(
			&reference.ID, &reference.WorkspaceID, &reference.StorageProvider, &reference.Bucket,
			&reference.ObjectKey, &reference.ObjectVersion, &reference.ContentType, &reference.SizeBytes,
			&reference.ChecksumSHA256, &reference.ETag,
		); err != nil {
			return nil, err
		}
		references = append(references, reference)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return references, nil
}
