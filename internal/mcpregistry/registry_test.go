package mcpregistry

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type registryTestStore struct {
	server  Server
	version Version
}

func (s registryTestStore) CreateMCPRegistryServer(context.Context, CreateInput) (Server, error) {
	return Server{}, errors.New("not implemented")
}
func (s registryTestStore) GetMCPRegistryServer(_ context.Context, id string) (Server, error) {
	if id != s.server.ID {
		return Server{}, ErrNotFound
	}
	return s.server, nil
}
func (s registryTestStore) ListMCPRegistryServers(context.Context, string) ([]Server, error) {
	return nil, errors.New("not implemented")
}
func (s registryTestStore) UpdateMCPRegistryServer(context.Context, UpdateInput) (Server, error) {
	return Server{}, errors.New("not implemented")
}
func (s registryTestStore) SetMCPRegistryServerStatus(context.Context, string, string, string) (Server, error) {
	return Server{}, errors.New("not implemented")
}
func (s registryTestStore) GetMCPRegistryVersion(_ context.Context, id string, version int) (Version, error) {
	if id != s.version.ServerID || version != s.version.Version {
		return Version{}, ErrNotFound
	}
	return s.version, nil
}
func (s registryTestStore) ListMCPRegistryVersions(context.Context, string) ([]Version, error) {
	return nil, errors.New("not implemented")
}
func (s registryTestStore) RestoreMCPRegistryVersion(context.Context, string, int, string) (RestoreResult, error) {
	return RestoreResult{}, errors.New("not implemented")
}
func (s registryTestStore) CountMCPRegistryBindings(context.Context, string) (int, error) {
	return 0, nil
}

func TestPinAndResolveRegistryBinding(t *testing.T) {
	config, err := NormalizeServerConfig("remote-search", json.RawMessage(`{"transport":"streamable_http","url":"https://mcp.example.test/mcp"}`))
	if err != nil {
		t.Fatalf("normalize registry config: %v", err)
	}
	store := registryTestStore{
		server:  Server{ID: "mcps_000001", WorkspaceID: "wksp_default", Status: StatusActive, CurrentVersion: 4},
		version: Version{ServerID: "mcps_000001", Version: 4, Config: config},
	}
	stored, resolved, err := PinAndResolve(t.Context(), store, "wksp_default", json.RawMessage(`{"bindings":[{"server_id":"mcps_000001","version":0,"identifier":"search"}]}`))
	if err != nil {
		t.Fatalf("pin and resolve: %v", err)
	}
	if !strings.Contains(string(stored), `"version":4`) || !strings.Contains(string(stored), `"server_id":"mcps_000001"`) {
		t.Fatalf("binding was not pinned: %s", stored)
	}
	if !strings.Contains(string(resolved), `"identifier":"search"`) || !strings.Contains(string(resolved), `https://mcp.example.test/mcp`) {
		t.Fatalf("unexpected resolved config: %s", resolved)
	}
	if !strings.Contains(string(resolved), `"_registry":{"server_id":"mcps_000001","version":4}`) {
		t.Fatalf("resolved config must retain runtime guard partition metadata: %s", resolved)
	}
}

func TestNormalizeServerConfigRemovesRuntimeRegistryMetadata(t *testing.T) {
	config, err := NormalizeServerConfig("remote", json.RawMessage(`{"transport":"streamable_http","url":"https://mcp.example.test/mcp","_registry":{"server_id":"spoofed","version":99}}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(config), "_registry") || strings.Contains(string(config), "spoofed") {
		t.Fatalf("registry metadata must be assigned only while resolving a binding: %s", config)
	}
}

func TestPinAndResolveRejectsDisabledServer(t *testing.T) {
	store := registryTestStore{server: Server{ID: "mcps_000001", WorkspaceID: "wksp_default", Status: StatusDisabled, CurrentVersion: 1}}
	_, _, err := PinAndResolve(t.Context(), store, "wksp_default", json.RawMessage(`{"bindings":[{"server_id":"mcps_000001"}]}`))
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("expected disabled error, got %v", err)
	}
}

func TestNormalizeServerConfigRejectsLiteralSensitiveHeader(t *testing.T) {
	_, err := NormalizeServerConfig("remote", json.RawMessage(`{"transport":"streamable_http","url":"https://mcp.example.test/mcp","headers":{"Authorization":"Bearer secret"}}`))
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "must use env_ref") {
		t.Fatalf("expected sensitive header reference error, got %v", err)
	}
}
