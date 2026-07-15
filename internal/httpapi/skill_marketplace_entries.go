package httpapi

import (
	"context"
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

type createMarketplaceEntryRequest struct {
	WorkspaceID  string   `json:"workspace_id,omitempty"`
	SkillID      string   `json:"skill_id"`
	SkillVersion int      `json:"skill_version"`
	Summary      string   `json:"summary,omitempty"`
	Category     string   `json:"category,omitempty"`
	Tags         []string `json:"tags,omitempty"`
}

type updateMarketplaceEntryRequest struct {
	WorkspaceID string   `json:"workspace_id,omitempty"`
	Summary     string   `json:"summary,omitempty"`
	Category    string   `json:"category,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

type transitionMarketplaceEntryRequest struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	Note        string `json:"note,omitempty"`
}

type internalMarketplaceCandidate struct {
	skillmarketplace.MarketplaceEntry
	Provider            string                       `json:"provider"`
	SuggestedIdentifier string                       `json:"suggested_identifier"`
	InstallState        string                       `json:"install_state"`
	Existing            *tools.SkillsPreviewExisting `json:"existing,omitempty"`
}

func (s *Server) browseInternalMarketplace(w http.ResponseWriter, r *http.Request) {
	store, err := s.marketplaceCatalogStore()
	if err != nil {
		writeError(w, err)
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if err := s.authorizeSessionID(r, sessionID); err != nil {
		writeError(w, err)
		return
	}
	session, err := s.getSessionForRequest(r, sessionID)
	if err != nil {
		writeError(w, err)
		return
	}
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if limit, err = strconv.Atoi(raw); err != nil || limit <= 0 || limit > 50 {
			writeError(w, fmt.Errorf("%w: internal marketplace limit must be between 1 and 50", managedagents.ErrInvalid))
			return
		}
	}
	items, err := store.BrowsePublishedMarketplaceEntries(r.Context(), skillmarketplace.BrowseMarketplaceEntriesInput{
		WorkspaceID: session.WorkspaceID, Query: r.URL.Query().Get("query"), Category: r.URL.Query().Get("category"),
		Tags: splitMarketplaceEntryQueryValues(r.URL.Query()["tag"]), Limit: limit,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	registry, err := s.skillRegistry()
	if err != nil {
		writeError(w, err)
		return
	}
	candidates := make([]internalMarketplaceCandidate, 0, len(items))
	for _, item := range items {
		candidate := internalMarketplaceCandidate{
			MarketplaceEntry: item, Provider: skillmarketplace.CatalogProvider, SuggestedIdentifier: item.SkillIdentifier,
		}
		candidate.InstallState, candidate.Existing, err = internalMarketplaceCandidateInstallState(r.Context(), registry, session.WorkspaceID, item)
		if err != nil {
			writeError(w, err)
			return
		}
		candidates = append(candidates, candidate)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"provider": skillmarketplace.CatalogProvider, "items": candidates, "count": len(candidates),
	})
}

func internalMarketplaceCandidateInstallState(ctx context.Context, registry skillspkg.Registry, workspaceID string, entry skillmarketplace.MarketplaceEntry) (string, *tools.SkillsPreviewExisting, error) {
	skill, err := registry.GetSkillByIdentifier(ctx, workspaceID, entry.SkillIdentifier)
	if errors.Is(err, managedagents.ErrNotFound) {
		return "new_install", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	existing := &tools.SkillsPreviewExisting{
		SkillID: skill.ID, Status: skill.Status, SourceType: skill.SourceType,
		SourceLocator: skill.SourceLocator, SourcePath: skill.SourcePath,
	}
	if skill.Status != skillspkg.StatusActive {
		return "blocked", existing, nil
	}
	publisherViewingOwnEntry := skill.ID == entry.SkillID
	source := skillmarketplace.Source{
		Provider: skillmarketplace.CatalogProvider, CatalogEntryID: entry.ID,
		CatalogSkillID: entry.SkillID, Path: "SKILL.md",
	}
	if !publisherViewingOwnEntry && !installedSkillMatchesPackageSource(skill, source) {
		return "blocked", existing, nil
	}
	versions, err := registry.ListSkillVersions(ctx, skill.ID)
	if err != nil {
		return "", nil, err
	}
	if len(versions) == 0 {
		return "blocked", existing, nil
	}
	current := versions[0]
	existing.Version = current.Version
	existing.SourceRef = current.SourceRef
	existing.SourceRevision = current.SourceRevision
	if publisherViewingOwnEntry || current.SourceRef == entry.ID {
		return "unchanged", existing, nil
	}
	return "upgrade", existing, nil
}

func (s *Server) previewInternalMarketplace(w http.ResponseWriter, r *http.Request) {
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
	if !strings.EqualFold(strings.TrimSpace(request.Source.Provider), skillmarketplace.CatalogProvider) {
		writeError(w, fmt.Errorf("%w: internal marketplace preview requires catalog source", managedagents.ErrInvalid))
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
		s.writeSkillsMarketplaceReadError(w, "internal preview", err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) installInternalMarketplace(w http.ResponseWriter, r *http.Request) {
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
	if !strings.EqualFold(strings.TrimSpace(request.Source.Provider), skillmarketplace.CatalogProvider) {
		writeError(w, fmt.Errorf("%w: internal marketplace install requires catalog source", managedagents.ErrInvalid))
		return
	}
	if err := s.authorizeSessionID(r, request.SessionID); err != nil {
		writeError(w, err)
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), controlPrincipalContextKey{}, s.controlPrincipal(r)))
	response, err := service.Install(r.Context(), tools.SkillsInstallRequest{
		SessionID: request.SessionID, Identifier: request.Identifier, Source: &request.Source,
		PolicyID: request.PolicyID, PolicyVersion: request.PolicyVersion, PolicyRevision: request.PolicyRevision,
		UpgradeExisting: request.UpgradeExisting,
	})
	s.recordSkillsControlAudit(r, request.SessionID, "skills.marketplace.internal.install", request.Source.CatalogEntryID, map[string]any{
		"source": request.Source, "identifier": request.Identifier, "upgraded": response.Upgraded,
	}, err)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, response)
}

func splitMarketplaceEntryQueryValues(values []string) []string {
	result := []string{}
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			if item = strings.TrimSpace(item); item != "" {
				result = append(result, item)
			}
		}
	}
	return result
}

func (s *Server) marketplaceCatalogStore() (skillmarketplace.MarketplaceCatalogStore, error) {
	store, ok := s.store.(skillmarketplace.MarketplaceCatalogStore)
	if !ok {
		return nil, fmt.Errorf("marketplace catalog store is unavailable")
	}
	return store, nil
}

func (s *Server) createMarketplaceEntry(w http.ResponseWriter, r *http.Request) {
	store, err := s.marketplaceCatalogStore()
	if err != nil {
		writeError(w, err)
		return
	}
	var request createMarketplaceEntryRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	workspaceID := requestWorkspaceID(r, request.WorkspaceID)
	principal := controlPrincipalFromRequest(r)
	entry, err := store.CreateMarketplaceEntry(r.Context(), skillmarketplace.CreateMarketplaceEntryInput{
		WorkspaceID: workspaceID, SkillID: request.SkillID, SkillVersion: request.SkillVersion,
		Summary: request.Summary, Category: request.Category, Tags: request.Tags, CreatedBy: principal.ID,
	})
	s.recordMarketplaceEntryControlAudit(r, "skills.marketplace.entry.create", entry, "", entry.Status, err)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, entry)
}

func (s *Server) listMarketplaceEntries(w http.ResponseWriter, r *http.Request) {
	store, err := s.marketplaceCatalogStore()
	if err != nil {
		writeError(w, err)
		return
	}
	includeWithdrawn, err := optionalBool(r.URL.Query().Get("include_withdrawn"))
	if err != nil {
		writeError(w, fmt.Errorf("%w: invalid include_withdrawn: %v", managedagents.ErrInvalid, err))
		return
	}
	items, err := store.ListMarketplaceEntries(r.Context(), skillmarketplace.ListMarketplaceEntriesInput{
		WorkspaceID:      requestWorkspaceID(r, r.URL.Query().Get("workspace_id")),
		Status:           strings.TrimSpace(r.URL.Query().Get("status")),
		IncludeWithdrawn: includeWithdrawn != nil && *includeWithdrawn,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": nonNilSlice(items)})
}

func (s *Server) getMarketplaceEntry(w http.ResponseWriter, r *http.Request) {
	store, err := s.marketplaceCatalogStore()
	if err != nil {
		writeError(w, err)
		return
	}
	entry, err := store.GetMarketplaceEntry(r.Context(), requestWorkspaceID(r, r.URL.Query().Get("workspace_id")), r.PathValue("entry_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

func (s *Server) updateMarketplaceEntry(w http.ResponseWriter, r *http.Request) {
	store, err := s.marketplaceCatalogStore()
	if err != nil {
		writeError(w, err)
		return
	}
	var request updateMarketplaceEntryRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	workspaceID := requestWorkspaceID(r, request.WorkspaceID)
	entryID := r.PathValue("entry_id")
	before, _ := store.GetMarketplaceEntry(r.Context(), workspaceID, entryID)
	entry, err := store.UpdateMarketplaceEntry(r.Context(), skillmarketplace.UpdateMarketplaceEntryInput{
		WorkspaceID: workspaceID, EntryID: entryID, Summary: request.Summary,
		Category: request.Category, Tags: request.Tags, UpdatedBy: controlPrincipalFromRequest(r).ID,
	})
	if entry.ID == "" {
		entry = before
	}
	s.recordMarketplaceEntryControlAudit(r, "skills.marketplace.entry.update", entry, before.Status, entry.Status, err)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

func (s *Server) submitMarketplaceEntry(w http.ResponseWriter, r *http.Request) {
	s.transitionMarketplaceEntry(w, r, skillmarketplace.MarketplaceEntryStatusPendingReview, "skills.marketplace.entry.submit")
}

func (s *Server) publishMarketplaceEntry(w http.ResponseWriter, r *http.Request) {
	s.transitionMarketplaceEntry(w, r, skillmarketplace.MarketplaceEntryStatusPublished, "skills.marketplace.entry.publish")
}

func (s *Server) withdrawMarketplaceEntry(w http.ResponseWriter, r *http.Request) {
	s.transitionMarketplaceEntry(w, r, skillmarketplace.MarketplaceEntryStatusWithdrawn, "skills.marketplace.entry.withdraw")
}

func (s *Server) transitionMarketplaceEntry(w http.ResponseWriter, r *http.Request, targetStatus string, action string) {
	store, err := s.marketplaceCatalogStore()
	if err != nil {
		writeError(w, err)
		return
	}
	var request transitionMarketplaceEntryRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	workspaceID := requestWorkspaceID(r, request.WorkspaceID)
	entryID := r.PathValue("entry_id")
	before, _ := store.GetMarketplaceEntry(r.Context(), workspaceID, entryID)
	entry, err := store.TransitionMarketplaceEntry(r.Context(), skillmarketplace.TransitionMarketplaceEntryInput{
		WorkspaceID: workspaceID, EntryID: entryID, TargetStatus: targetStatus,
		Actor: controlPrincipalFromRequest(r).ID, Note: request.Note,
	})
	if entry.ID == "" {
		entry = before
	}
	s.recordMarketplaceEntryControlAudit(r, action, entry, before.Status, targetStatus, err)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

func (s *Server) recordMarketplaceEntryControlAudit(r *http.Request, action string, entry skillmarketplace.MarketplaceEntry, fromStatus string, toStatus string, actionErr error) {
	store, ok := s.store.(managedagents.OperatorAuditStore)
	if !ok {
		return
	}
	principal := controlPrincipalFromRequest(r)
	details, _ := json.Marshal(map[string]any{
		"skill_id": entry.SkillID, "skill_identifier": entry.SkillIdentifier,
		"skill_version": entry.SkillVersion, "from_status": fromStatus, "to_status": toStatus,
	})
	outcome := "succeeded"
	errorMessage := ""
	if actionErr != nil {
		outcome = "failed"
		errorMessage = actionErr.Error()
	}
	if _, err := managedagents.RecordOperatorAuditWithContext(r.Context(), store, managedagents.RecordOperatorAuditInput{
		WorkspaceID: auditWorkspaceID(r, entry.WorkspaceID), PrincipalID: principal.ID,
		OperatorLabel: principal.OperatorLabel, Role: principal.Role, Action: action,
		ResourceType: "skill_marketplace_entry", ResourceID: entry.ID,
		Outcome: outcome, ErrorMessage: errorMessage, Details: details,
	}); err != nil {
		s.logger.Warn("marketplace entry audit write failed", "action", action, "entry_id", entry.ID, "error", err)
	}
}
