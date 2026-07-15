package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/mcp"
	"tiggy-manage-agent/internal/mcpregistry"
)

func (s *testStore) CreateMCPRegistryServer(_ context.Context, input mcpregistry.CreateInput) (mcpregistry.Server, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	workspaceID := strings.TrimSpace(input.WorkspaceID)
	if workspaceID == "" {
		workspaceID = managedagents.DefaultWorkspaceID
	}
	for _, existing := range s.mcpRegistryServers {
		if existing.WorkspaceID == workspaceID && existing.Identifier == input.Identifier && existing.Status != mcpregistry.StatusArchived {
			return mcpregistry.Server{}, fmt.Errorf("%w: duplicate identifier", mcpregistry.ErrInvalid)
		}
	}
	s.nextMCPRegistryID++
	id := fmt.Sprintf("mcps_%06d", s.nextMCPRegistryID)
	now := time.Now().UTC()
	server := mcpregistry.Server{ID: id, WorkspaceID: workspaceID, Identifier: input.Identifier, Name: input.Name, Description: input.Description, Status: mcpregistry.StatusActive, CurrentVersion: 1, Config: cloneJSONRaw(input.Config), CreatedBy: input.CreatedBy, CreatedAt: now, UpdatedAt: now}
	version := mcpregistry.Version{ID: fmt.Sprintf("mcpsv_%06d", s.nextMCPRegistryID), ServerID: id, Version: 1, Config: cloneJSONRaw(input.Config), Checksum: mcpregistry.Checksum(input.Config), CreatedBy: input.CreatedBy, CreatedAt: now}
	s.mcpRegistryServers[id] = server
	s.mcpRegistryVersions[id] = []mcpregistry.Version{version}
	return server, nil
}

func (s *testStore) GetMCPRegistryServer(_ context.Context, id string) (mcpregistry.Server, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	server, ok := s.mcpRegistryServers[id]
	if !ok {
		return mcpregistry.Server{}, mcpregistry.ErrNotFound
	}
	server.Config = cloneJSONRaw(server.Config)
	server.UsageCount = s.countMCPRegistryBindingsLocked(id)
	return server, nil
}

func (s *testStore) ListMCPRegistryServers(_ context.Context, workspaceID string) ([]mcpregistry.Server, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []mcpregistry.Server{}
	for _, server := range s.mcpRegistryServers {
		if server.WorkspaceID == workspaceID && server.Status != mcpregistry.StatusArchived {
			server.Config = cloneJSONRaw(server.Config)
			server.UsageCount = s.countMCPRegistryBindingsLocked(server.ID)
			items = append(items, server)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

func (s *testStore) UpdateMCPRegistryServer(_ context.Context, input mcpregistry.UpdateInput) (mcpregistry.Server, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	server, ok := s.mcpRegistryServers[input.ServerID]
	if !ok {
		return mcpregistry.Server{}, mcpregistry.ErrNotFound
	}
	server.Name = input.Name
	server.Description = input.Description
	server.UpdatedAt = time.Now().UTC()
	if len(input.Config) > 0 {
		server.CurrentVersion++
		server.Config = cloneJSONRaw(input.Config)
		version := mcpregistry.Version{ID: fmt.Sprintf("mcpsv_%06d_%d", s.nextMCPRegistryID, server.CurrentVersion), ServerID: server.ID, Version: server.CurrentVersion, Config: cloneJSONRaw(input.Config), Checksum: mcpregistry.Checksum(input.Config), CreatedBy: input.UpdatedBy, CreatedAt: server.UpdatedAt}
		s.mcpRegistryVersions[server.ID] = append(s.mcpRegistryVersions[server.ID], version)
	}
	s.mcpRegistryServers[server.ID] = server
	server.UsageCount = s.countMCPRegistryBindingsLocked(server.ID)
	return server, nil
}

func (s *testStore) SetMCPRegistryServerStatus(_ context.Context, id string, status string, _ string) (mcpregistry.Server, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	server, ok := s.mcpRegistryServers[id]
	if !ok {
		return mcpregistry.Server{}, mcpregistry.ErrNotFound
	}
	server.Status = status
	server.UpdatedAt = time.Now().UTC()
	s.mcpRegistryServers[id] = server
	server.UsageCount = s.countMCPRegistryBindingsLocked(server.ID)
	return server, nil
}

func (s *testStore) GetMCPRegistryVersion(_ context.Context, id string, version int) (mcpregistry.Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.mcpRegistryVersions[id] {
		if item.Version == version {
			item.Config = cloneJSONRaw(item.Config)
			return item, nil
		}
	}
	return mcpregistry.Version{}, mcpregistry.ErrNotFound
}

func (s *testStore) ListMCPRegistryVersions(_ context.Context, id string) ([]mcpregistry.Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := append([]mcpregistry.Version(nil), s.mcpRegistryVersions[id]...)
	sort.Slice(items, func(i, j int) bool { return items[i].Version > items[j].Version })
	return items, nil
}

func (s *testStore) RestoreMCPRegistryVersion(_ context.Context, id string, sourceVersion int, restoredBy string) (mcpregistry.RestoreResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	server, ok := s.mcpRegistryServers[id]
	if !ok {
		return mcpregistry.RestoreResult{}, mcpregistry.ErrNotFound
	}
	if server.Status == mcpregistry.StatusArchived {
		return mcpregistry.RestoreResult{}, fmt.Errorf("%w: archived server cannot restore versions", mcpregistry.ErrInvalid)
	}
	if sourceVersion < 1 || sourceVersion >= server.CurrentVersion {
		return mcpregistry.RestoreResult{}, fmt.Errorf("%w: restore source must be older than current version %d", mcpregistry.ErrInvalid, server.CurrentVersion)
	}
	var source mcpregistry.Version
	found := false
	for _, item := range s.mcpRegistryVersions[id] {
		if item.Version == sourceVersion {
			source = item
			found = true
			break
		}
	}
	if !found {
		return mcpregistry.RestoreResult{}, mcpregistry.ErrNotFound
	}
	previousVersion := server.CurrentVersion
	server.CurrentVersion++
	server.Config = cloneJSONRaw(source.Config)
	server.UpdatedAt = time.Now().UTC()
	version := mcpregistry.Version{
		ID: fmt.Sprintf("mcpsv_%06d_%d", s.nextMCPRegistryID, server.CurrentVersion), ServerID: id,
		Version: server.CurrentVersion, Config: cloneJSONRaw(source.Config), Checksum: source.Checksum,
		CreatedBy: restoredBy, CreatedAt: server.UpdatedAt,
	}
	s.mcpRegistryVersions[id] = append(s.mcpRegistryVersions[id], version)
	s.mcpRegistryServers[id] = server
	server.UsageCount = s.countMCPRegistryBindingsLocked(id)
	return mcpregistry.RestoreResult{Server: server, SourceVersion: sourceVersion, PreviousVersion: previousVersion, NewVersion: server.CurrentVersion}, nil
}

func (s *testStore) CountMCPRegistryBindings(_ context.Context, id string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.countMCPRegistryBindingsLocked(id), nil
}

func (s *testStore) countMCPRegistryBindingsLocked(id string) int {
	count := 0
	for _, agent := range s.agents {
		var config struct {
			Bindings []mcp.ServerBinding `json:"bindings"`
		}
		if err := json.Unmarshal(agent.ConfigVersion.MCP, &config); err == nil {
			for _, binding := range config.Bindings {
				if binding.ServerID == id {
					count++
					break
				}
			}
		}
	}
	return count
}
