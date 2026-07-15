package mcpregistry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"tiggy-manage-agent/internal/mcp"
)

const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
	StatusArchived = "archived"
)

var (
	ErrNotFound = errors.New("mcp registry server not found")
	ErrInvalid  = errors.New("invalid mcp registry server")
	ErrDisabled = errors.New("mcp registry server is disabled")
)

type Server struct {
	ID             string          `json:"id"`
	WorkspaceID    string          `json:"workspace_id"`
	Identifier     string          `json:"identifier"`
	Name           string          `json:"name"`
	Description    string          `json:"description,omitempty"`
	Status         string          `json:"status"`
	CurrentVersion int             `json:"current_version"`
	Config         json.RawMessage `json:"config"`
	UsageCount     int             `json:"usage_count"`
	CreatedBy      string          `json:"created_by,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type Version struct {
	ID        string          `json:"id"`
	ServerID  string          `json:"server_id"`
	Version   int             `json:"version"`
	Config    json.RawMessage `json:"config"`
	Checksum  string          `json:"checksum_sha256"`
	CreatedBy string          `json:"created_by,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type CreateInput struct {
	WorkspaceID string
	Identifier  string
	Name        string
	Description string
	Config      json.RawMessage
	CreatedBy   string
}

type UpdateInput struct {
	ServerID    string
	Name        string
	Description string
	Config      json.RawMessage
	UpdatedBy   string
}

type RestoreResult struct {
	Server          Server `json:"server"`
	SourceVersion   int    `json:"source_version"`
	PreviousVersion int    `json:"previous_version"`
	NewVersion      int    `json:"new_version"`
}

type Store interface {
	CreateMCPRegistryServer(context.Context, CreateInput) (Server, error)
	GetMCPRegistryServer(context.Context, string) (Server, error)
	ListMCPRegistryServers(context.Context, string) ([]Server, error)
	UpdateMCPRegistryServer(context.Context, UpdateInput) (Server, error)
	SetMCPRegistryServerStatus(context.Context, string, string, string) (Server, error)
	GetMCPRegistryVersion(context.Context, string, int) (Version, error)
	ListMCPRegistryVersions(context.Context, string) ([]Version, error)
	RestoreMCPRegistryVersion(context.Context, string, int, string) (RestoreResult, error)
	CountMCPRegistryBindings(context.Context, string) (int, error)
}

func NormalizeServerConfig(identifier string, raw json.RawMessage) (json.RawMessage, error) {
	identifier = mcp.NormalizeName(identifier, "mcp")
	if len(raw) == 0 || string(raw) == "null" {
		return nil, fmt.Errorf("%w: config is required", ErrInvalid)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, fmt.Errorf("%w: config must be an object", ErrInvalid)
	}
	object["identifier"] = identifier
	envelope, _ := json.Marshal(map[string]any{"servers": []any{object}})
	config, err := mcp.ParseConfig(envelope)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if len(config.Servers) != 1 {
		return nil, fmt.Errorf("%w: config must contain one server", ErrInvalid)
	}
	config.Servers[0].Registry = nil
	for key, value := range config.Servers[0].Headers {
		name := strings.ToLower(strings.TrimSpace(key))
		sensitive := name == "authorization" || name == "proxy-authorization" || name == "cookie" || strings.Contains(name, "token") || strings.Contains(name, "secret") || strings.Contains(name, "password") || strings.Contains(name, "api-key")
		if sensitive && value.Literal {
			return nil, fmt.Errorf("%w: sensitive header %q must use env_ref or secret_ref", ErrInvalid, key)
		}
	}
	return json.Marshal(config.Servers[0])
}

func Checksum(raw json.RawMessage) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func PinAndResolve(ctx context.Context, store Store, workspaceID string, raw json.RawMessage) (json.RawMessage, json.RawMessage, error) {
	config, err := mcp.ParseConfig(raw)
	if err != nil {
		return nil, nil, err
	}
	if len(config.Bindings) == 0 {
		canonical, canonicalErr := mcp.CanonicalJSON(raw)
		return canonical, canonical, canonicalErr
	}
	if store == nil {
		return nil, nil, errors.New("mcp registry store is unavailable")
	}
	resolved := append([]mcp.ServerConfig(nil), config.Servers...)
	seen := map[string]bool{}
	for _, server := range resolved {
		seen[server.Identifier] = true
	}
	for index := range config.Bindings {
		binding := &config.Bindings[index]
		server, getErr := store.GetMCPRegistryServer(ctx, binding.ServerID)
		if getErr != nil {
			return nil, nil, getErr
		}
		if strings.TrimSpace(server.WorkspaceID) != strings.TrimSpace(workspaceID) {
			return nil, nil, ErrNotFound
		}
		if server.Status != StatusActive {
			return nil, nil, fmt.Errorf("%w: %s", ErrDisabled, server.ID)
		}
		if binding.Version == 0 {
			binding.Version = server.CurrentVersion
		}
		version, versionErr := store.GetMCPRegistryVersion(ctx, server.ID, binding.Version)
		if versionErr != nil {
			return nil, nil, versionErr
		}
		var selected mcp.ServerConfig
		if err := json.Unmarshal(version.Config, &selected); err != nil {
			return nil, nil, fmt.Errorf("decode mcp registry server %s version %d: %w", server.ID, binding.Version, err)
		}
		if binding.Identifier != "" {
			selected.Identifier = binding.Identifier
		}
		selected.Registry = &mcp.RegistrySource{ServerID: server.ID, Version: binding.Version}
		if seen[selected.Identifier] {
			return nil, nil, fmt.Errorf("duplicate mcp server identifier %q", selected.Identifier)
		}
		seen[selected.Identifier] = true
		resolved = append(resolved, selected)
	}
	stored, err := json.Marshal(config)
	if err != nil {
		return nil, nil, err
	}
	resolvedRaw, err := json.Marshal(mcp.Config{Servers: resolved})
	if err != nil {
		return nil, nil, err
	}
	return stored, resolvedRaw, nil
}
