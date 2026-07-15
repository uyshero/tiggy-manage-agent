package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/skillmarketplace"
)

type createMarketplacePolicyRequest struct {
	ScopeType      string                  `json:"scope_type"`
	OrganizationID string                  `json:"organization_id,omitempty"`
	WorkspaceID    string                  `json:"workspace_id,omitempty"`
	Config         skillmarketplace.Policy `json:"config"`
}

type publishMarketplacePolicyVersionRequest struct {
	Config skillmarketplace.Policy `json:"config"`
}

func (s *Server) marketplacePolicyStore() (skillmarketplace.PolicyStore, error) {
	store, ok := s.store.(skillmarketplace.PolicyStore)
	if !ok {
		return nil, fmt.Errorf("marketplace policy store is unavailable")
	}
	return store, nil
}

func (s *Server) createMarketplacePolicy(w http.ResponseWriter, r *http.Request) {
	store, err := s.marketplacePolicyStore()
	if err != nil {
		writeError(w, err)
		return
	}
	var request createMarketplacePolicyRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	principal := controlPrincipalFromRequest(r)
	if _, ok := PrincipalFromRequest(r); ok {
		switch request.ScopeType {
		case skillmarketplace.PolicyScopeWorkspace:
			request.WorkspaceID = requestWorkspaceID(r, request.WorkspaceID)
			request.OrganizationID = ""
		case skillmarketplace.PolicyScopeOrganization:
			request.OrganizationID = requestOrganizationID(r, request.OrganizationID)
			request.WorkspaceID = ""
			if request.OrganizationID == "" {
				writeError(w, fmt.Errorf("%w: organization identity is required", managedagents.ErrForbidden))
				return
			}
		}
	}
	record, version, err := store.CreateMarketplacePolicy(r.Context(), skillmarketplace.CreatePolicyInput{
		ScopeType: request.ScopeType, OrganizationID: request.OrganizationID, WorkspaceID: request.WorkspaceID,
		Config: request.Config, CreatedBy: principal.ID,
	})
	s.recordMarketplacePolicyControlAudit(r, "skills.marketplace.policy.create", record, version, err)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"policy": record, "version": version})
}

func (s *Server) listMarketplacePolicies(w http.ResponseWriter, r *http.Request) {
	store, err := s.marketplacePolicyStore()
	if err != nil {
		writeError(w, err)
		return
	}
	includeArchived, err := optionalBool(r.URL.Query().Get("include_archived"))
	if err != nil {
		writeError(w, fmt.Errorf("%w: invalid include_archived: %v", managedagents.ErrInvalid, err))
		return
	}
	organizationID := ""
	workspaceID := requestWorkspaceID(r, r.URL.Query().Get("workspace_id"))
	if r.URL.Query().Get("organization_id") != "" {
		organizationID = requestOrganizationID(r, r.URL.Query().Get("organization_id"))
		workspaceID = ""
	}
	items, err := store.ListMarketplacePolicies(r.Context(), skillmarketplace.ListPoliciesInput{
		OrganizationID: organizationID, WorkspaceID: workspaceID,
		IncludeArchived: includeArchived != nil && *includeArchived,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policies": nonNilSlice(items)})
}

func (s *Server) getMarketplacePolicy(w http.ResponseWriter, r *http.Request) {
	store, err := s.marketplacePolicyStore()
	if err != nil {
		writeError(w, err)
		return
	}
	record, err := store.GetMarketplacePolicy(r.Context(), r.PathValue("policy_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	version, err := store.GetMarketplacePolicyVersion(r.Context(), record.ID, record.CurrentVersion)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policy": record, "version": version})
}

func (s *Server) publishMarketplacePolicyVersion(w http.ResponseWriter, r *http.Request) {
	store, err := s.marketplacePolicyStore()
	if err != nil {
		writeError(w, err)
		return
	}
	var request publishMarketplacePolicyVersionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	policyID := r.PathValue("policy_id")
	principal := controlPrincipalFromRequest(r)
	version, err := store.PublishMarketplacePolicyVersion(r.Context(), policyID, request.Config, principal.ID)
	record, recordErr := store.GetMarketplacePolicy(r.Context(), policyID)
	if err == nil && recordErr != nil {
		err = recordErr
	}
	s.recordMarketplacePolicyControlAudit(r, "skills.marketplace.policy.publish", record, version, err)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, version)
}

func (s *Server) getMarketplacePolicyVersion(w http.ResponseWriter, r *http.Request) {
	store, err := s.marketplacePolicyStore()
	if err != nil {
		writeError(w, err)
		return
	}
	versionNumber, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || versionNumber <= 0 {
		writeError(w, fmt.Errorf("%w: marketplace policy version must be positive", managedagents.ErrInvalid))
		return
	}
	version, err := store.GetMarketplacePolicyVersion(r.Context(), r.PathValue("policy_id"), versionNumber)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, version)
}

func (s *Server) archiveMarketplacePolicy(w http.ResponseWriter, r *http.Request) {
	store, err := s.marketplacePolicyStore()
	if err != nil {
		writeError(w, err)
		return
	}
	policyID := r.PathValue("policy_id")
	before, _ := store.GetMarketplacePolicy(r.Context(), policyID)
	record, err := store.ArchiveMarketplacePolicy(r.Context(), policyID)
	if record.ID == "" {
		record = before
	}
	s.recordMarketplacePolicyControlAudit(r, "skills.marketplace.policy.archive", record, skillmarketplace.PolicyVersion{}, err)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func (s *Server) recordMarketplacePolicyControlAudit(r *http.Request, action string, record skillmarketplace.PolicyRecord, version skillmarketplace.PolicyVersion, actionErr error) {
	store, ok := s.store.(managedagents.OperatorAuditStore)
	if !ok {
		return
	}
	principal := controlPrincipalFromRequest(r)
	details, _ := json.Marshal(map[string]any{
		"scope_type": record.ScopeType, "organization_id": record.OrganizationID,
		"workspace_id": record.WorkspaceID, "policy_version": version.Version,
		"policy_revision": version.Checksum,
	})
	outcome := "succeeded"
	errorMessage := ""
	if actionErr != nil {
		outcome = "failed"
		errorMessage = actionErr.Error()
	}
	if _, err := managedagents.RecordOperatorAuditWithContext(r.Context(), store, managedagents.RecordOperatorAuditInput{
		WorkspaceID: auditWorkspaceID(r, record.WorkspaceID), PrincipalID: principal.ID,
		OperatorLabel: principal.OperatorLabel, Role: principal.Role, Action: action,
		ResourceType: "skill_marketplace_policy", ResourceID: record.ID,
		Outcome: outcome, ErrorMessage: errorMessage, Details: details,
	}); err != nil {
		s.logger.Warn("marketplace policy audit write failed", "action", action, "policy_id", record.ID, "error", err)
	}
}
