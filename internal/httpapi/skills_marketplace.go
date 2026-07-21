package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/skillmarketplace"
	skillspkg "tiggy-manage-agent/internal/skills"
	"tiggy-manage-agent/internal/tools"
)

type skillsMarketplacePreviewRequest struct {
	SessionID  string                  `json:"session_id"`
	Identifier string                  `json:"identifier,omitempty"`
	Source     skillmarketplace.Source `json:"source"`
}

type skillsMarketplaceInstallRequest struct {
	SessionID       string                  `json:"session_id"`
	Identifier      string                  `json:"identifier"`
	Source          skillmarketplace.Source `json:"source"`
	PolicyID        string                  `json:"policy_id,omitempty"`
	PolicyVersion   int                     `json:"policy_version,omitempty"`
	PolicyRevision  string                  `json:"policy_revision,omitempty"`
	UpgradeExisting bool                    `json:"upgrade_existing,omitempty"`
}

type enableInstalledSkillRequest struct {
	SessionID string          `json:"session_id"`
	Version   int             `json:"version,omitempty"`
	Mode      string          `json:"mode,omitempty"`
	Priority  int             `json:"priority,omitempty"`
	Inputs    json.RawMessage `json:"inputs,omitempty"`
}

type disableInstalledSkillRequest struct {
	SessionID string `json:"session_id"`
}

func (s *Server) marketplaceSkillsService() (tools.SkillsToolService, error) {
	if s == nil || s.skillsToolService == nil {
		return nil, fmt.Errorf("skills marketplace service is unavailable")
	}
	return s.skillsToolService, nil
}

func (s *Server) discoverSkillsMarketplace(w http.ResponseWriter, r *http.Request) {
	service, err := s.marketplaceSkillsService()
	if err != nil {
		writeError(w, err)
		return
	}
	if err := s.authorizeSessionID(r, r.URL.Query().Get("session_id")); err != nil {
		writeError(w, err)
		return
	}
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit <= 0 || limit > 20 {
			writeError(w, fmt.Errorf("%w: marketplace discovery limit must be between 1 and 20", managedagents.ErrInvalid))
			return
		}
	}
	response, err := service.Discover(r.Context(), tools.SkillsDiscoverRequest{
		SessionID: r.URL.Query().Get("session_id"), Provider: skillmarketplace.GitHubProvider, Query: r.URL.Query().Get("query"),
		Repository: r.URL.Query().Get("repository"), Limit: limit,
	})
	if err != nil {
		s.writeSkillsMarketplaceReadError(w, "discovery", err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) previewSkillsMarketplace(w http.ResponseWriter, r *http.Request) {
	service, err := s.marketplaceSkillsService()
	if err != nil {
		writeError(w, err)
		return
	}
	var request skillsMarketplacePreviewRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.authorizeSessionID(r, request.SessionID); err != nil {
		writeError(w, err)
		return
	}
	response, err := service.Preview(r.Context(), tools.SkillsPreviewRequest{
		SessionID: request.SessionID, Identifier: request.Identifier, Source: request.Source,
	})
	if err != nil {
		s.writeSkillsMarketplaceReadError(w, "preview", err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) writeSkillsMarketplaceReadError(w http.ResponseWriter, operation string, err error) {
	if errors.Is(err, managedagents.ErrInvalid) || errors.Is(err, managedagents.ErrForbidden) ||
		errors.Is(err, managedagents.ErrConflict) || errors.Is(err, managedagents.ErrNotFound) {
		writeError(w, err)
		return
	}
	if s != nil && s.logger != nil {
		s.logger.Warn("skills marketplace read failed", "operation", operation, "error", err)
	}
	writeJSON(w, http.StatusBadGateway, map[string]string{
		"error": "Marketplace " + operation + " failed; verify the repository, ref, and SKILL.md path.",
	})
}

func (s *Server) installSkillsMarketplace(w http.ResponseWriter, r *http.Request) {
	service, err := s.marketplaceSkillsService()
	if err != nil {
		writeError(w, err)
		return
	}
	var request skillsMarketplaceInstallRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.authorizeSessionID(r, request.SessionID); err != nil {
		writeError(w, err)
		return
	}
	response, err := service.Install(r.Context(), tools.SkillsInstallRequest{
		SessionID: request.SessionID, Identifier: request.Identifier, Source: &request.Source,
		PolicyID: request.PolicyID, PolicyVersion: request.PolicyVersion, PolicyRevision: request.PolicyRevision,
		UpgradeExisting: request.UpgradeExisting,
	})
	s.recordSkillsControlAudit(r, request.SessionID, "skills.marketplace.install", request.Identifier, map[string]any{
		"source": request.Source, "upgraded": response.Upgraded,
	}, err)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, response)
}

func (s *Server) enableInstalledSkill(w http.ResponseWriter, r *http.Request) {
	service, err := s.marketplaceSkillsService()
	if err != nil {
		writeError(w, err)
		return
	}
	var request enableInstalledSkillRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.authorizeSessionID(r, request.SessionID); err != nil {
		writeError(w, err)
		return
	}
	registry, ok := s.store.(skillspkg.Registry)
	if !ok {
		writeError(w, fmt.Errorf("skills registry is unavailable"))
		return
	}
	skill, err := registry.GetSkill(r.Context(), r.PathValue("skill_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	response, err := service.Enable(r.Context(), tools.SkillsEnableRequest{
		SessionID: request.SessionID, Identifier: skill.Identifier, Version: request.Version,
		Mode: request.Mode, Priority: request.Priority, Inputs: request.Inputs,
	})
	s.recordSkillsControlAudit(r, request.SessionID, "skills.enable", skill.ID, map[string]any{
		"identifier": skill.Identifier, "version": response.Binding.Version, "agent_id": response.AgentID,
		"changed": response.Changed,
	}, err)
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusOK
	if response.Changed {
		status = http.StatusCreated
	}
	writeJSON(w, status, response)
}

func (s *Server) disableInstalledSkill(w http.ResponseWriter, r *http.Request) {
	service, err := s.marketplaceSkillsService()
	if err != nil {
		writeError(w, err)
		return
	}
	var request disableInstalledSkillRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.authorizeSessionID(r, request.SessionID); err != nil {
		writeError(w, err)
		return
	}
	registry, ok := s.store.(skillspkg.Registry)
	if !ok {
		writeError(w, fmt.Errorf("skills registry is unavailable"))
		return
	}
	skill, err := registry.GetSkill(r.Context(), r.PathValue("skill_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	response, err := service.Disable(r.Context(), tools.SkillsDisableRequest{
		SessionID: request.SessionID, Identifier: skill.Identifier,
	})
	s.recordSkillsControlAudit(r, request.SessionID, "skills.disable", skill.ID, map[string]any{
		"identifier": skill.Identifier, "version": response.Binding.Version, "agent_id": response.AgentID,
		"removed": response.Removed,
	}, err)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, response)
}

func (s *Server) recordSkillsControlAudit(r *http.Request, sessionID string, action string, resourceID string, details map[string]any, actionErr error) {
	store, ok := s.store.(managedagents.OperatorAuditStore)
	if !ok {
		return
	}
	workspaceID := ""
	if session, err := s.getSessionForRequest(r, strings.TrimSpace(sessionID)); err == nil {
		workspaceID = session.WorkspaceID
	}
	detailsJSON, _ := json.Marshal(details)
	outcome := "succeeded"
	errorMessage := ""
	if actionErr != nil {
		outcome = "failed"
		errorMessage = actionErr.Error()
	}
	principal := controlPrincipalFromRequest(r)
	if _, err := managedagents.RecordOperatorAuditWithContext(r.Context(), store, managedagents.RecordOperatorAuditInput{
		WorkspaceID: auditWorkspaceID(r, workspaceID), SessionID: strings.TrimSpace(sessionID), PrincipalID: principal.ID,
		OperatorLabel: principal.OperatorLabel, Role: principal.Role, Action: action,
		ResourceType: "skill", ResourceID: strings.TrimSpace(resourceID), Outcome: outcome,
		ErrorMessage: errorMessage, Details: detailsJSON,
	}); err != nil && s.logger != nil {
		s.logger.Warn("skills control audit write failed", "action", action, "resource_id", resourceID, "error", err)
	}
}
