package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func (s *PostgresStore) ListServiceIdentities(ctx context.Context, workspaceID string) ([]ServiceIdentity, error) {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT id, workspace_id, kind, name, description, role, to_json(scopes), status, created_by, created_at, updated_at
		FROM service_identities
		WHERE workspace_id = $1
		ORDER BY name, id
	`, scope.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ServiceIdentity{}
	for rows.Next() {
		item, err := scanServiceIdentity(rows)
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

func (s *PostgresStore) GetServiceIdentity(ctx context.Context, workspaceID, id string) (ServiceIdentity, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ServiceIdentity{}, fmt.Errorf("%w: service identity id is required", ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return ServiceIdentity{}, err
	}
	defer tx.Rollback()
	item, err := scanServiceIdentity(tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, kind, name, description, role, to_json(scopes), status, created_by, created_at, updated_at
		FROM service_identities WHERE workspace_id = $1 AND id = $2
	`, scope.WorkspaceID, id))
	if err != nil {
		return ServiceIdentity{}, err
	}
	if err := tx.Commit(); err != nil {
		return ServiceIdentity{}, err
	}
	return item, nil
}

func (s *PostgresStore) CreateServiceIdentity(ctx context.Context, input CreateServiceIdentityInput) (ServiceIdentity, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.CreatedBy = strings.TrimSpace(input.CreatedBy)
	if input.WorkspaceID == "" || input.CreatedBy == "" {
		return ServiceIdentity{}, fmt.Errorf("%w: service identity workspace and creator are required", ErrInvalid)
	}
	kind, name, description, role, scopes, err := NormalizeServiceIdentityInput(input.Kind, input.Name, input.Description, input.Role, input.Scopes)
	if err != nil {
		return ServiceIdentity{}, err
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return ServiceIdentity{}, err
	}
	defer tx.Rollback()
	id, err := nextSequenceID(ctx, tx, "svc", "tma_service_identity_id_seq")
	if err != nil {
		return ServiceIdentity{}, err
	}
	item, err := scanServiceIdentity(tx.QueryRowContext(ctx, `
		INSERT INTO service_identities (id, workspace_id, kind, name, description, role, scopes, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, workspace_id, kind, name, description, role, to_json(scopes), status, created_by, created_at, updated_at
	`, id, scope.WorkspaceID, kind, name, description, role, scopes, input.CreatedBy))
	if err != nil {
		if postgresUniqueViolation(err) {
			return ServiceIdentity{}, fmt.Errorf("%w: service identity name already exists", ErrConflict)
		}
		return ServiceIdentity{}, err
	}
	if err := tx.Commit(); err != nil {
		return ServiceIdentity{}, err
	}
	return item, nil
}

func (s *PostgresStore) UpdateServiceIdentity(ctx context.Context, input UpdateServiceIdentityInput) (ServiceIdentity, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ID = strings.TrimSpace(input.ID)
	if input.WorkspaceID == "" || input.ID == "" {
		return ServiceIdentity{}, fmt.Errorf("%w: service identity workspace and id are required", ErrInvalid)
	}
	_, name, description, role, scopes, err := NormalizeServiceIdentityInput(ServiceIdentityKindApplication, input.Name, input.Description, input.Role, input.Scopes)
	if err != nil {
		return ServiceIdentity{}, err
	}
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	if input.Status != ServiceIdentityStatusActive && input.Status != ServiceIdentityStatusDisabled {
		return ServiceIdentity{}, fmt.Errorf("%w: service identity status must be active or disabled", ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return ServiceIdentity{}, err
	}
	defer tx.Rollback()
	item, err := scanServiceIdentity(tx.QueryRowContext(ctx, `
		UPDATE service_identities
		SET name = $3, description = $4, role = $5, scopes = $6, status = $7, updated_at = now()
		WHERE workspace_id = $1 AND id = $2
		RETURNING id, workspace_id, kind, name, description, role, to_json(scopes), status, created_by, created_at, updated_at
	`, scope.WorkspaceID, input.ID, name, description, role, scopes, input.Status))
	if err != nil {
		if postgresUniqueViolation(err) {
			return ServiceIdentity{}, fmt.Errorf("%w: service identity name already exists", ErrConflict)
		}
		return ServiceIdentity{}, err
	}
	if err := tx.Commit(); err != nil {
		return ServiceIdentity{}, err
	}
	return item, nil
}

func (s *PostgresStore) ListServiceCredentials(ctx context.Context, workspaceID, identityID string) ([]ServiceCredential, error) {
	identityID = strings.TrimSpace(identityID)
	if identityID == "" {
		return nil, fmt.Errorf("%w: service identity id is required", ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT id, workspace_id, service_identity_id, name, token_prefix, status,
			expires_at, last_used_at, created_by, created_at, revoked_at
		FROM service_identity_credentials
		WHERE workspace_id = $1 AND service_identity_id = $2
		ORDER BY created_at DESC, id DESC
	`, scope.WorkspaceID, identityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ServiceCredential{}
	for rows.Next() {
		item, err := scanServiceCredential(rows)
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

func (s *PostgresStore) CreateServiceCredential(ctx context.Context, input CreateServiceCredentialInput) (ServiceCredential, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ServiceIdentityID = strings.TrimSpace(input.ServiceIdentityID)
	input.Name = strings.TrimSpace(input.Name)
	input.Locator = strings.TrimSpace(input.Locator)
	input.TokenPrefix = strings.TrimSpace(input.TokenPrefix)
	input.CreatedBy = strings.TrimSpace(input.CreatedBy)
	if input.WorkspaceID == "" || input.ServiceIdentityID == "" || input.Name == "" || len(input.Name) > 120 ||
		len(input.Locator) < 16 || len(input.Locator) > 64 || input.TokenPrefix == "" || len(input.SecretHash) != 32 || input.CreatedBy == "" {
		return ServiceCredential{}, fmt.Errorf("%w: service credential fields are invalid", ErrInvalid)
	}
	if input.ExpiresAt != nil {
		expiresAt := input.ExpiresAt.UTC()
		if !expiresAt.After(time.Now().UTC()) {
			return ServiceCredential{}, fmt.Errorf("%w: service credential expiry must be in the future", ErrInvalid)
		}
		input.ExpiresAt = &expiresAt
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return ServiceCredential{}, err
	}
	defer tx.Rollback()
	id, err := nextSequenceID(ctx, tx, "cred", "tma_service_credential_id_seq")
	if err != nil {
		return ServiceCredential{}, err
	}
	item, err := scanServiceCredential(tx.QueryRowContext(ctx, `
		INSERT INTO service_identity_credentials (
			id, workspace_id, service_identity_id, name, locator, token_prefix, secret_hash, expires_at, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, workspace_id, service_identity_id, name, token_prefix, status,
			expires_at, last_used_at, created_by, created_at, revoked_at
	`, id, scope.WorkspaceID, input.ServiceIdentityID, input.Name, input.Locator, input.TokenPrefix, input.SecretHash, input.ExpiresAt, input.CreatedBy))
	if err != nil {
		if postgresUniqueViolation(err) {
			return ServiceCredential{}, fmt.Errorf("%w: service credential locator already exists", ErrConflict)
		}
		return ServiceCredential{}, err
	}
	if err := tx.Commit(); err != nil {
		return ServiceCredential{}, err
	}
	return item, nil
}

func (s *PostgresStore) RevokeServiceCredential(ctx context.Context, workspaceID, identityID, credentialID string) error {
	identityID = strings.TrimSpace(identityID)
	credentialID = strings.TrimSpace(credentialID)
	if identityID == "" || credentialID == "" {
		return fmt.Errorf("%w: service identity and credential ids are required", ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE service_identity_credentials
		SET status = 'revoked', revoked_at = COALESCE(revoked_at, now())
		WHERE workspace_id = $1 AND service_identity_id = $2 AND id = $3
	`, scope.WorkspaceID, identityID, credentialID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *PostgresStore) AuthenticateServiceCredential(ctx context.Context, locator string, secretHash []byte) (AuthenticatedServiceIdentity, error) {
	locator = strings.TrimSpace(locator)
	if len(locator) < 16 || len(locator) > 64 || len(secretHash) != 32 {
		return AuthenticatedServiceIdentity{}, ErrNotFound
	}
	var authenticated AuthenticatedServiceIdentity
	err := s.db.QueryRowContext(ctx, `
		SELECT identity_id, workspace_id, kind, name, description, role, to_json(scopes), identity_status,
			created_by, identity_created_at, identity_updated_at, credential_id
		FROM tma_authenticate_service_credential($1, $2)
	`, locator, secretHash).Scan(
		&authenticated.Identity.ID, &authenticated.Identity.WorkspaceID, &authenticated.Identity.Kind,
		&authenticated.Identity.Name, &authenticated.Identity.Description, &authenticated.Identity.Role,
		&serviceIdentityScopesScanner{destination: &authenticated.Identity.Scopes}, &authenticated.Identity.Status, &authenticated.Identity.CreatedBy,
		&authenticated.Identity.CreatedAt, &authenticated.Identity.UpdatedAt, &authenticated.CredentialID,
	)
	if err == sql.ErrNoRows {
		return AuthenticatedServiceIdentity{}, ErrNotFound
	}
	return authenticated, err
}

type serviceIdentityScanner interface {
	Scan(...any) error
}

func scanServiceIdentity(scanner serviceIdentityScanner) (ServiceIdentity, error) {
	var item ServiceIdentity
	if err := scanner.Scan(&item.ID, &item.WorkspaceID, &item.Kind, &item.Name, &item.Description, &item.Role,
		&serviceIdentityScopesScanner{destination: &item.Scopes}, &item.Status, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return ServiceIdentity{}, ErrNotFound
		}
		return ServiceIdentity{}, err
	}
	return item, nil
}

type serviceIdentityScopesScanner struct {
	destination *[]string
}

func (s *serviceIdentityScopesScanner) Scan(source any) error {
	if s == nil || s.destination == nil {
		return errors.New("service identity scopes destination is unavailable")
	}
	var encoded []byte
	switch value := source.(type) {
	case []byte:
		encoded = value
	case string:
		encoded = []byte(value)
	default:
		return fmt.Errorf("unsupported service identity scopes value %T", source)
	}
	return json.Unmarshal(encoded, s.destination)
}

func scanServiceCredential(scanner serviceIdentityScanner) (ServiceCredential, error) {
	var item ServiceCredential
	if err := scanner.Scan(&item.ID, &item.WorkspaceID, &item.ServiceIdentityID, &item.Name, &item.TokenPrefix,
		&item.Status, &item.ExpiresAt, &item.LastUsedAt, &item.CreatedBy, &item.CreatedAt, &item.RevokedAt); err != nil {
		if err == sql.ErrNoRows {
			return ServiceCredential{}, ErrNotFound
		}
		return ServiceCredential{}, err
	}
	return item, nil
}

func postgresUniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return err != nil && errors.As(err, &postgresError) && postgresError.Code == "23505"
}

var _ ServiceIdentityStore = (*PostgresStore)(nil)
