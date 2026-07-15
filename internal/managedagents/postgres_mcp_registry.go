package managedagents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"tiggy-manage-agent/internal/mcpregistry"
)

func (s *PostgresStore) CreateMCPRegistryServer(ctx context.Context, input mcpregistry.CreateInput) (mcpregistry.Server, error) {
	if strings.TrimSpace(input.WorkspaceID) == "" || strings.TrimSpace(input.Identifier) == "" || strings.TrimSpace(input.Name) == "" || len(input.Config) == 0 {
		return mcpregistry.Server{}, fmt.Errorf("%w: workspace_id, identifier, name and config are required", mcpregistry.ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return mcpregistry.Server{}, err
	}
	defer tx.Rollback()
	id, err := nextSequenceID(ctx, tx, "mcps", "tma_mcp_registry_server_id_seq")
	if err != nil {
		return mcpregistry.Server{}, err
	}
	versionID, err := nextSequenceID(ctx, tx, "mcpsv", "tma_mcp_registry_version_id_seq")
	if err != nil {
		return mcpregistry.Server{}, err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO mcp_registry_servers
			(id, workspace_id, identifier, name, description, status, current_version, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'active', 1, $6, $7, $7)
	`, id, scope.WorkspaceID, input.Identifier, input.Name, input.Description, input.CreatedBy, now); err != nil {
		return mcpregistry.Server{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO mcp_registry_server_versions
			(id, server_id, version, config_json, checksum_sha256, created_by, created_at)
		VALUES ($1, $2, 1, $3, $4, $5, $6)
	`, versionID, id, nullableRaw(input.Config), mcpregistry.Checksum(input.Config), input.CreatedBy, now); err != nil {
		return mcpregistry.Server{}, err
	}
	if err := tx.Commit(); err != nil {
		return mcpregistry.Server{}, err
	}
	return s.GetMCPRegistryServer(ctx, id)
}

func (s *PostgresStore) GetMCPRegistryServer(ctx context.Context, id string) (mcpregistry.Server, error) {
	workspaceID, err := s.mcpRegistryWorkspaceID(ctx, id)
	if err != nil {
		return mcpregistry.Server{}, err
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return mcpregistry.Server{}, err
	}
	defer tx.Rollback()
	var server mcpregistry.Server
	var config []byte
	err = tx.QueryRowContext(ctx, `
		SELECT s.id, s.workspace_id, s.identifier, s.name, s.description, s.status,
			s.current_version, v.config_json, s.created_by, s.created_at, s.updated_at,
			(SELECT COUNT(DISTINCT a.id)
			 FROM agents a
			 JOIN agent_config_versions av ON av.agent_id = a.id AND av.version = a.current_config_version
			 WHERE a.workspace_id = s.workspace_id
			   AND av.mcp_json->'bindings' @> jsonb_build_array(jsonb_build_object('server_id', s.id)))
		FROM mcp_registry_servers s
		JOIN mcp_registry_server_versions v ON v.server_id = s.id AND v.version = s.current_version
		WHERE s.id = $1
	`, id).Scan(&server.ID, &server.WorkspaceID, &server.Identifier, &server.Name, &server.Description, &server.Status,
		&server.CurrentVersion, &config, &server.CreatedBy, &server.CreatedAt, &server.UpdatedAt, &server.UsageCount)
	if errors.Is(err, sql.ErrNoRows) {
		return mcpregistry.Server{}, mcpregistry.ErrNotFound
	}
	if err != nil {
		return mcpregistry.Server{}, err
	}
	if err := tx.Commit(); err != nil {
		return mcpregistry.Server{}, err
	}
	server.Config = cloneRaw(config)
	return server, nil
}

func (s *PostgresStore) ListMCPRegistryServers(ctx context.Context, workspaceID string) ([]mcpregistry.Server, error) {
	tx, _, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT s.id, s.workspace_id, s.identifier, s.name, s.description, s.status,
			s.current_version, v.config_json, s.created_by, s.created_at, s.updated_at,
			(SELECT COUNT(DISTINCT a.id)
			 FROM agents a
			 JOIN agent_config_versions av ON av.agent_id = a.id AND av.version = a.current_config_version
			 WHERE a.workspace_id = s.workspace_id
			   AND av.mcp_json->'bindings' @> jsonb_build_array(jsonb_build_object('server_id', s.id)))
		FROM mcp_registry_servers s
		JOIN mcp_registry_server_versions v ON v.server_id = s.id AND v.version = s.current_version
		WHERE s.workspace_id = $1 AND s.status <> 'archived'
		ORDER BY s.name, s.id
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	servers := []mcpregistry.Server{}
	for rows.Next() {
		var server mcpregistry.Server
		var config []byte
		if err := rows.Scan(&server.ID, &server.WorkspaceID, &server.Identifier, &server.Name, &server.Description, &server.Status,
			&server.CurrentVersion, &config, &server.CreatedBy, &server.CreatedAt, &server.UpdatedAt, &server.UsageCount); err != nil {
			rows.Close()
			return nil, err
		}
		server.Config = cloneRaw(config)
		servers = append(servers, server)
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
	return servers, nil
}

func (s *PostgresStore) UpdateMCPRegistryServer(ctx context.Context, input mcpregistry.UpdateInput) (mcpregistry.Server, error) {
	current, err := s.GetMCPRegistryServer(ctx, input.ServerID)
	if err != nil {
		return mcpregistry.Server{}, err
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = current.Name
	}
	description := input.Description
	if input.Description == "" {
		description = current.Description
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, current.WorkspaceID)
	if err != nil {
		return mcpregistry.Server{}, err
	}
	defer tx.Rollback()
	nextVersion := current.CurrentVersion
	if len(input.Config) > 0 {
		nextVersion++
		versionID, nextErr := nextSequenceID(ctx, tx, "mcpsv", "tma_mcp_registry_version_id_seq")
		if nextErr != nil {
			return mcpregistry.Server{}, nextErr
		}
		if _, nextErr = tx.ExecContext(ctx, `
			INSERT INTO mcp_registry_server_versions
				(id, server_id, version, config_json, checksum_sha256, created_by, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, now())
		`, versionID, current.ID, nextVersion, nullableRaw(input.Config), mcpregistry.Checksum(input.Config), input.UpdatedBy); nextErr != nil {
			return mcpregistry.Server{}, nextErr
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE mcp_registry_servers
		SET name = $2, description = $3, current_version = $4, updated_at = now()
		WHERE id = $1
	`, current.ID, name, description, nextVersion); err != nil {
		return mcpregistry.Server{}, err
	}
	if err := tx.Commit(); err != nil {
		return mcpregistry.Server{}, err
	}
	return s.GetMCPRegistryServer(ctx, current.ID)
}

func (s *PostgresStore) SetMCPRegistryServerStatus(ctx context.Context, id string, status string, _ string) (mcpregistry.Server, error) {
	if status != mcpregistry.StatusActive && status != mcpregistry.StatusDisabled && status != mcpregistry.StatusArchived {
		return mcpregistry.Server{}, fmt.Errorf("%w: unsupported status", mcpregistry.ErrInvalid)
	}
	workspaceID, err := s.mcpRegistryWorkspaceID(ctx, id)
	if err != nil {
		return mcpregistry.Server{}, err
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return mcpregistry.Server{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE mcp_registry_servers SET status = $2, updated_at = now() WHERE id = $1`, id, status)
	if err != nil {
		return mcpregistry.Server{}, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return mcpregistry.Server{}, mcpregistry.ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return mcpregistry.Server{}, err
	}
	return s.GetMCPRegistryServer(ctx, id)
}

func (s *PostgresStore) GetMCPRegistryVersion(ctx context.Context, serverID string, version int) (mcpregistry.Version, error) {
	workspaceID, err := s.mcpRegistryWorkspaceID(ctx, serverID)
	if err != nil {
		return mcpregistry.Version{}, err
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return mcpregistry.Version{}, err
	}
	defer tx.Rollback()
	var item mcpregistry.Version
	var config []byte
	err = tx.QueryRowContext(ctx, `
		SELECT id, server_id, version, config_json, checksum_sha256, created_by, created_at
		FROM mcp_registry_server_versions WHERE server_id = $1 AND version = $2
	`, serverID, version).Scan(&item.ID, &item.ServerID, &item.Version, &config, &item.Checksum, &item.CreatedBy, &item.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return mcpregistry.Version{}, mcpregistry.ErrNotFound
	}
	if err != nil {
		return mcpregistry.Version{}, err
	}
	if err := tx.Commit(); err != nil {
		return mcpregistry.Version{}, err
	}
	item.Config = cloneRaw(config)
	return item, nil
}

func (s *PostgresStore) ListMCPRegistryVersions(ctx context.Context, serverID string) ([]mcpregistry.Version, error) {
	workspaceID, err := s.mcpRegistryWorkspaceID(ctx, serverID)
	if err != nil {
		return nil, err
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT id, server_id, version, config_json, checksum_sha256, created_by, created_at
		FROM mcp_registry_server_versions WHERE server_id = $1 ORDER BY version DESC
	`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []mcpregistry.Version{}
	for rows.Next() {
		var item mcpregistry.Version
		var config []byte
		if err := rows.Scan(&item.ID, &item.ServerID, &item.Version, &config, &item.Checksum, &item.CreatedBy, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.Config = cloneRaw(config)
		items = append(items, item)
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
	return items, nil
}

func (s *PostgresStore) RestoreMCPRegistryVersion(ctx context.Context, serverID string, sourceVersion int, restoredBy string) (mcpregistry.RestoreResult, error) {
	workspaceID, err := s.mcpRegistryWorkspaceID(ctx, serverID)
	if err != nil {
		return mcpregistry.RestoreResult{}, err
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return mcpregistry.RestoreResult{}, err
	}
	defer tx.Rollback()

	var previousVersion int
	var status string
	if err := tx.QueryRowContext(ctx, `
		SELECT current_version, status
		FROM mcp_registry_servers
		WHERE id = $1
		FOR UPDATE
	`, serverID).Scan(&previousVersion, &status); errors.Is(err, sql.ErrNoRows) {
		return mcpregistry.RestoreResult{}, mcpregistry.ErrNotFound
	} else if err != nil {
		return mcpregistry.RestoreResult{}, err
	}
	if status == mcpregistry.StatusArchived {
		return mcpregistry.RestoreResult{}, fmt.Errorf("%w: archived server cannot restore versions", mcpregistry.ErrInvalid)
	}
	if sourceVersion < 1 || sourceVersion >= previousVersion {
		return mcpregistry.RestoreResult{}, fmt.Errorf("%w: restore source must be older than current version %d", mcpregistry.ErrInvalid, previousVersion)
	}

	var sourceConfig []byte
	var sourceChecksum string
	if err := tx.QueryRowContext(ctx, `
		SELECT config_json, checksum_sha256
		FROM mcp_registry_server_versions
		WHERE server_id = $1 AND version = $2
	`, serverID, sourceVersion).Scan(&sourceConfig, &sourceChecksum); errors.Is(err, sql.ErrNoRows) {
		return mcpregistry.RestoreResult{}, mcpregistry.ErrNotFound
	} else if err != nil {
		return mcpregistry.RestoreResult{}, err
	}

	newVersion := previousVersion + 1
	versionID, err := nextSequenceID(ctx, tx, "mcpsv", "tma_mcp_registry_version_id_seq")
	if err != nil {
		return mcpregistry.RestoreResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO mcp_registry_server_versions
			(id, server_id, version, config_json, checksum_sha256, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
	`, versionID, serverID, newVersion, nullableRaw(sourceConfig), sourceChecksum, restoredBy); err != nil {
		return mcpregistry.RestoreResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE mcp_registry_servers
		SET current_version = $2, updated_at = now()
		WHERE id = $1
	`, serverID, newVersion); err != nil {
		return mcpregistry.RestoreResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return mcpregistry.RestoreResult{}, err
	}

	server, err := s.GetMCPRegistryServer(ctx, serverID)
	if err != nil {
		return mcpregistry.RestoreResult{}, err
	}
	return mcpregistry.RestoreResult{
		Server:          server,
		SourceVersion:   sourceVersion,
		PreviousVersion: previousVersion,
		NewVersion:      newVersion,
	}, nil
}

func (s *PostgresStore) CountMCPRegistryBindings(ctx context.Context, serverID string) (int, error) {
	workspaceID, err := s.mcpRegistryWorkspaceID(ctx, serverID)
	if err != nil {
		return 0, err
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var count int
	err = tx.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT a.id)
		FROM agents a
		JOIN agent_config_versions av ON av.agent_id = a.id AND av.version = a.current_config_version
		WHERE a.workspace_id = $2
		  AND av.mcp_json->'bindings' @> jsonb_build_array(jsonb_build_object('server_id', $1::text))
	`, serverID, workspaceID).Scan(&count)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *PostgresStore) mcpRegistryWorkspaceID(ctx context.Context, serverID string) (string, error) {
	if scope, ok := DatabaseAccessScopeFromContext(ctx); ok {
		return scope.WorkspaceID, nil
	}
	var workspaceID string
	if err := s.db.QueryRowContext(ctx, `SELECT workspace_id FROM mcp_registry_servers WHERE id = $1`, serverID).Scan(&workspaceID); errors.Is(err, sql.ErrNoRows) {
		return "", mcpregistry.ErrNotFound
	} else if err != nil {
		return "", err
	}
	return workspaceID, nil
}
