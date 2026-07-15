package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"tiggy-manage-agent/internal/envvars"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/mcp"
	"tiggy-manage-agent/internal/mcpregistry"
	"tiggy-manage-agent/internal/runner"
)

type mcpRegistryCreateRequest struct {
	WorkspaceID string          `json:"workspace_id,omitempty"`
	Identifier  string          `json:"identifier"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Config      json.RawMessage `json:"config"`
}

type mcpRegistryUpdateRequest struct {
	Name        *string          `json:"name,omitempty"`
	Description *string          `json:"description,omitempty"`
	Config      *json.RawMessage `json:"config,omitempty"`
}

func (s *Server) mcpRegistryStore() (mcpregistry.Store, error) {
	store, ok := s.store.(mcpregistry.Store)
	if !ok {
		return nil, errors.New("mcp registry store is unavailable")
	}
	return store, nil
}

func (s *Server) listMCPRegistryServers(w http.ResponseWriter, r *http.Request) {
	store, err := s.mcpRegistryStore()
	if err != nil {
		writeMCPRegistryError(w, err)
		return
	}
	workspaceID := requestWorkspaceID(r, r.URL.Query().Get("workspace_id"))
	if workspaceID == "" {
		workspaceID = managedagents.DefaultWorkspaceID
	}
	servers, err := store.ListMCPRegistryServers(r.Context(), workspaceID)
	if err != nil {
		writeMCPRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": nonNilSlice(servers)})
}

func (s *Server) listMCPRegistryRuntimeStatus(w http.ResponseWriter, r *http.Request) {
	store, err := s.mcpRegistryStore()
	if err != nil {
		writeMCPRegistryError(w, err)
		return
	}
	workspaceID := requestWorkspaceID(r, r.URL.Query().Get("workspace_id"))
	if workspaceID == "" {
		workspaceID = managedagents.DefaultWorkspaceID
	}
	servers, err := store.ListMCPRegistryServers(r.Context(), workspaceID)
	if err != nil {
		writeMCPRegistryError(w, err)
		return
	}
	allowed := make(map[string]bool, len(servers))
	for _, server := range servers {
		allowed[server.ID] = true
	}
	states := []mcp.RegistryRuntimeState{}
	if provider, ok := s.runner.(runner.MCPRegistryRuntimeStatesProvider); ok {
		for _, state := range provider.MCPRegistryRuntimeStates(workspaceID) {
			if allowed[state.ServerID] {
				states = append(states, state)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"checked_at": time.Now().UTC(),
		"states":     states,
	})
}

func (s *Server) createMCPRegistryServer(w http.ResponseWriter, r *http.Request) {
	store, err := s.mcpRegistryStore()
	if err != nil {
		writeMCPRegistryError(w, err)
		return
	}
	var request mcpRegistryCreateRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	identifier := mcp.NormalizeName(request.Identifier, "mcp")
	config, err := mcpregistry.NormalizeServerConfig(identifier, request.Config)
	if err != nil {
		writeMCPRegistryError(w, err)
		return
	}
	principal := controlPrincipalFromRequest(r)
	workspaceID := requestWorkspaceID(r, request.WorkspaceID)
	if workspaceID == "" {
		workspaceID = managedagents.DefaultWorkspaceID
	}
	server, err := store.CreateMCPRegistryServer(r.Context(), mcpregistry.CreateInput{
		WorkspaceID: workspaceID, Identifier: identifier,
		Name: strings.TrimSpace(request.Name), Description: strings.TrimSpace(request.Description),
		Config: config, CreatedBy: principal.ID,
	})
	s.recordWorkspaceOperatorAction(r, server.WorkspaceID, "mcp_registry.create", "mcp_registry_server", server.ID, err, map[string]any{"identifier": identifier})
	if err != nil {
		writeMCPRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, server)
}

func (s *Server) getMCPRegistryServer(w http.ResponseWriter, r *http.Request) {
	server, err := s.getMCPRegistryServerForRequest(r, r.PathValue("server_id"))
	if err != nil {
		writeMCPRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, server)
}

func (s *Server) updateMCPRegistryServer(w http.ResponseWriter, r *http.Request) {
	store, err := s.mcpRegistryStore()
	if err != nil {
		writeMCPRegistryError(w, err)
		return
	}
	current, err := s.getMCPRegistryServerForRequest(r, r.PathValue("server_id"))
	if err != nil {
		writeMCPRegistryError(w, err)
		return
	}
	var request mcpRegistryUpdateRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	input := mcpregistry.UpdateInput{ServerID: current.ID, Name: current.Name, Description: current.Description, UpdatedBy: controlPrincipalFromRequest(r).ID}
	if request.Name != nil {
		input.Name = strings.TrimSpace(*request.Name)
	}
	if request.Description != nil {
		input.Description = strings.TrimSpace(*request.Description)
	}
	if request.Config != nil {
		input.Config, err = mcpregistry.NormalizeServerConfig(current.Identifier, *request.Config)
		if err != nil {
			writeMCPRegistryError(w, err)
			return
		}
	}
	updated, err := store.UpdateMCPRegistryServer(r.Context(), input)
	s.recordWorkspaceOperatorAction(r, current.WorkspaceID, "mcp_registry.update", "mcp_registry_server", current.ID, err, map[string]any{"new_version": updated.CurrentVersion})
	if err != nil {
		writeMCPRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) setMCPRegistryServerStatus(w http.ResponseWriter, r *http.Request, status string) {
	store, err := s.mcpRegistryStore()
	if err != nil {
		writeMCPRegistryError(w, err)
		return
	}
	current, err := s.getMCPRegistryServerForRequest(r, r.PathValue("server_id"))
	if err != nil {
		writeMCPRegistryError(w, err)
		return
	}
	if status == mcpregistry.StatusArchived && current.UsageCount > 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "mcp registry server is still bound to active agents"})
		return
	}
	updated, err := store.SetMCPRegistryServerStatus(r.Context(), current.ID, status, controlPrincipalFromRequest(r).ID)
	action := "mcp_registry." + status
	s.recordWorkspaceOperatorAction(r, current.WorkspaceID, action, "mcp_registry_server", current.ID, err, nil)
	if err != nil {
		writeMCPRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) enableMCPRegistryServer(w http.ResponseWriter, r *http.Request) {
	s.setMCPRegistryServerStatus(w, r, mcpregistry.StatusActive)
}

func (s *Server) disableMCPRegistryServer(w http.ResponseWriter, r *http.Request) {
	s.setMCPRegistryServerStatus(w, r, mcpregistry.StatusDisabled)
}

func (s *Server) deleteMCPRegistryServer(w http.ResponseWriter, r *http.Request) {
	s.setMCPRegistryServerStatus(w, r, mcpregistry.StatusArchived)
}

func (s *Server) listMCPRegistryVersions(w http.ResponseWriter, r *http.Request) {
	store, err := s.mcpRegistryStore()
	if err != nil {
		writeMCPRegistryError(w, err)
		return
	}
	server, err := s.getMCPRegistryServerForRequest(r, r.PathValue("server_id"))
	if err != nil {
		writeMCPRegistryError(w, err)
		return
	}
	versions, err := store.ListMCPRegistryVersions(r.Context(), server.ID)
	if err != nil {
		writeMCPRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": nonNilSlice(versions)})
}

func (s *Server) restoreMCPRegistryVersion(w http.ResponseWriter, r *http.Request) {
	store, err := s.mcpRegistryStore()
	if err != nil {
		writeMCPRegistryError(w, err)
		return
	}
	server, err := s.getMCPRegistryServerForRequest(r, r.PathValue("server_id"))
	if err != nil {
		writeMCPRegistryError(w, err)
		return
	}
	sourceVersion, err := parseMCPRegistryVersion(r)
	if err != nil {
		s.recordWorkspaceOperatorAction(r, server.WorkspaceID, "mcp_registry.version.restore", "mcp_registry_server", server.ID, err, map[string]any{"source_version": r.PathValue("version"), "previous_version": server.CurrentVersion})
		writeMCPRegistryError(w, err)
		return
	}
	result, err := store.RestoreMCPRegistryVersion(r.Context(), server.ID, sourceVersion, controlPrincipalFromRequest(r).ID)
	details := map[string]any{"source_version": sourceVersion, "previous_version": server.CurrentVersion}
	if err == nil {
		details["previous_version"] = result.PreviousVersion
		details["new_version"] = result.NewVersion
	}
	s.recordWorkspaceOperatorAction(r, server.WorkspaceID, "mcp_registry.version.restore", "mcp_registry_server", server.ID, err, details)
	if err != nil {
		writeMCPRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) testMCPRegistryServer(w http.ResponseWriter, r *http.Request) {
	server, err := s.getMCPRegistryServerForRequest(r, r.PathValue("server_id"))
	if err != nil {
		writeMCPRegistryError(w, err)
		return
	}
	if server.Status != mcpregistry.StatusActive {
		writeMCPRegistryError(w, mcpregistry.ErrDisabled)
		return
	}
	managedEnvironment, _, err := envvars.ResolveWorkspace(r.Context(), s.store, server.WorkspaceID)
	if err != nil {
		writeMCPRegistryError(w, err)
		return
	}
	lookup := func(key string) (string, bool) {
		if value, ok := managedEnvironment[key]; ok {
			return value, true
		}
		return os.LookupEnv(key)
	}
	config, _ := json.Marshal(map[string]any{"servers": []json.RawMessage{server.Config}})
	items := checkMCPHealthWithLookupEgressPolicy(r.Context(), config, server.Identifier, lookup, s.mcpHTTPEgressPolicy())
	s.recordWorkspaceOperatorAction(r, server.WorkspaceID, "mcp_registry.test", "mcp_registry_server", server.ID, nil, map[string]any{"status": items[0].Status})
	writeJSON(w, http.StatusOK, map[string]any{"server_id": server.ID, "version": server.CurrentVersion, "result": items[0]})
}

func (s *Server) getMCPRegistryServerForRequest(r *http.Request, id string) (mcpregistry.Server, error) {
	store, err := s.mcpRegistryStore()
	if err != nil {
		return mcpregistry.Server{}, err
	}
	server, err := store.GetMCPRegistryServer(r.Context(), id)
	if err != nil {
		return mcpregistry.Server{}, err
	}
	if principal, ok := PrincipalFromRequest(r); ok {
		if err := authorizeWorkspacePrincipal(principal, server.WorkspaceID); err != nil {
			return mcpregistry.Server{}, err
		}
	}
	return server, nil
}

func (s *Server) pinAgentMCPBindings(r *http.Request, workspaceID string, raw json.RawMessage) (json.RawMessage, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		workspaceID = managedagents.DefaultWorkspaceID
	}
	config, err := mcp.ParseConfig(raw)
	if err != nil || len(config.Bindings) == 0 {
		return raw, err
	}
	store, err := s.mcpRegistryStore()
	if err != nil {
		return nil, err
	}
	stored, _, err := mcpregistry.PinAndResolve(r.Context(), store, workspaceID, raw)
	return stored, err
}

func writeMCPRegistryError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "mcp registry operation failed"
	switch {
	case errors.Is(err, mcpregistry.ErrInvalid), errors.Is(err, managedagents.ErrInvalid):
		status, message = http.StatusBadRequest, err.Error()
	case errors.Is(err, mcpregistry.ErrNotFound), errors.Is(err, managedagents.ErrNotFound):
		status, message = http.StatusNotFound, "mcp registry server not found"
	case errors.Is(err, mcpregistry.ErrDisabled):
		status, message = http.StatusConflict, err.Error()
	case errors.Is(err, managedagents.ErrForbidden):
		status, message = http.StatusForbidden, "forbidden"
	case strings.Contains(err.Error(), "store is unavailable"):
		status, message = http.StatusServiceUnavailable, err.Error()
	}
	writeJSON(w, status, map[string]string{"error": message})
}

func parseMCPRegistryVersion(r *http.Request) (int, error) {
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || version < 1 {
		return 0, mcpregistry.ErrInvalid
	}
	return version, nil
}
