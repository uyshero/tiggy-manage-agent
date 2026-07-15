package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"tiggy-manage-agent/internal/managedagents"
)

const (
	agentExportFormat        = "tma.agent"
	agentExportSchemaVersion = 1
)

type portableAgentConfig struct {
	Name        string          `json:"name"`
	LLMProvider string          `json:"llm_provider"`
	LLMModel    string          `json:"llm_model"`
	System      string          `json:"system"`
	Tools       json.RawMessage `json:"tools,omitempty"`
	MCP         json.RawMessage `json:"mcp,omitempty"`
	Skills      json.RawMessage `json:"skills,omitempty"`
}

type agentExportDocument struct {
	Format              string              `json:"format"`
	SchemaVersion       int                 `json:"schema_version"`
	ExportedAt          time.Time           `json:"exported_at"`
	SourceAgentID       string              `json:"source_agent_id,omitempty"`
	SourceConfigVersion int                 `json:"source_config_version,omitempty"`
	Agent               portableAgentConfig `json:"agent"`
	WorkspaceID         string              `json:"workspace_id,omitempty"`
}

type agentImportRequest struct {
	Format        string              `json:"format"`
	SchemaVersion int                 `json:"schema_version"`
	Agent         portableAgentConfig `json:"agent"`
	Name          string              `json:"name,omitempty"`
	WorkspaceID   string              `json:"workspace_id,omitempty"`
}

func (s *Server) exportAgent(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agent_id")
	agent, err := s.getAgentForRequest(r, agentID)
	s.recordOperatorAction(r, "", "agent.export", "agent", agentID, err, map[string]any{"config_version": agent.CurrentConfigVersion})
	if err != nil {
		writeError(w, err)
		return
	}

	document := agentExportDocument{
		Format:              agentExportFormat,
		SchemaVersion:       agentExportSchemaVersion,
		ExportedAt:          time.Now().UTC(),
		SourceAgentID:       agent.ID,
		SourceConfigVersion: agent.CurrentConfigVersion,
		WorkspaceID:         agent.WorkspaceID,
		Agent: portableAgentConfig{
			Name:        agent.Name,
			LLMProvider: agent.ConfigVersion.LLMProvider,
			LLMModel:    agent.ConfigVersion.LLMModel,
			System:      agent.ConfigVersion.System,
			Tools:       cloneJSONRaw(agent.ConfigVersion.Tools),
			MCP:         cloneJSONRaw(agent.ConfigVersion.MCP),
			Skills:      cloneJSONRaw(agent.ConfigVersion.Skills),
		},
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="agent-%s.json"`, agent.ID))
	writeJSON(w, http.StatusOK, document)
}

func (s *Server) importAgent(w http.ResponseWriter, r *http.Request) {
	var request agentImportRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if request.Format != agentExportFormat {
		err := fmt.Errorf("%w: import format must be %q", managedagents.ErrInvalid, agentExportFormat)
		s.recordOperatorAction(r, "", "agent.import", "agent", "", err, map[string]any{"format": request.Format, "schema_version": request.SchemaVersion})
		writeError(w, err)
		return
	}
	if request.SchemaVersion != agentExportSchemaVersion {
		err := fmt.Errorf("%w: unsupported agent schema_version %d", managedagents.ErrInvalid, request.SchemaVersion)
		s.recordOperatorAction(r, "", "agent.import", "agent", "", err, map[string]any{"format": request.Format, "schema_version": request.SchemaVersion})
		writeError(w, err)
		return
	}

	workspaceID := requestWorkspaceID(r, request.WorkspaceID)
	name := strings.TrimSpace(request.Name)
	if name == "" {
		name = strings.TrimSpace(request.Agent.Name)
	}
	input := managedagents.CreateAgentInput{
		WorkspaceID: workspaceID,
		Name:        name,
		LLMProvider: strings.TrimSpace(request.Agent.LLMProvider),
		LLMModel:    strings.TrimSpace(request.Agent.LLMModel),
		System:      request.Agent.System,
		Tools:       cloneJSONRaw(request.Agent.Tools),
		MCP:         cloneJSONRaw(request.Agent.MCP),
		Skills:      cloneJSONRaw(request.Agent.Skills),
	}
	if input.Skills != nil {
		normalized, err := s.validateAgentSkills(r.Context(), workspaceID, input.Skills)
		if err != nil {
			s.recordOperatorAction(r, "", "agent.import", "agent", "", err, map[string]any{"format": request.Format, "schema_version": request.SchemaVersion, "name": name})
			writeError(w, err)
			return
		}
		input.Skills = normalized
	}
	if input.MCP != nil {
		normalized, err := s.pinAgentMCPBindings(r, workspaceID, input.MCP)
		if err != nil {
			s.recordOperatorAction(r, "", "agent.import", "agent", "", err, map[string]any{"format": request.Format, "schema_version": request.SchemaVersion, "name": name})
			writeMCPRegistryError(w, err)
			return
		}
		input.MCP = normalized
	}
	created, err := managedagents.CreateAgentWithContext(r.Context(), s.store, input)
	details := map[string]any{
		"format":         request.Format,
		"schema_version": request.SchemaVersion,
		"name":           name,
		"workspace_id":   workspaceID,
	}
	resourceID := ""
	if err == nil {
		resourceID = created.ID
		details["created_agent_id"] = created.ID
	}
	s.recordOperatorAction(r, "", "agent.import", "agent", resourceID, err, details)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}
