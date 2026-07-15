package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/skillretention"
)

type createSkillAssetRetentionPolicyRequest struct {
	ScopeType      string                `json:"scope_type"`
	OrganizationID string                `json:"organization_id"`
	WorkspaceID    string                `json:"workspace_id"`
	Config         skillretention.Policy `json:"config"`
}

type publishSkillAssetRetentionPolicyRequest struct {
	Config skillretention.Policy `json:"config"`
}

type skillAssetGCRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Limit       int    `json:"limit,omitempty"`
	Confirm     string `json:"confirm,omitempty"`
}

func (s *Server) skillAssetRetentionStore() (skillretention.Store, error) {
	store, ok := s.store.(skillretention.Store)
	if !ok || s.skillRetention == nil {
		return nil, fmt.Errorf("skill asset retention store is unavailable")
	}
	return store, nil
}

func (s *Server) getEffectiveSkillAssetRetentionPolicy(w http.ResponseWriter, r *http.Request) {
	if _, err := s.skillAssetRetentionStore(); err != nil {
		writeError(w, err)
		return
	}
	effective, err := s.skillRetention.EffectivePolicy(r.Context(), requestWorkspaceID(r, r.URL.Query().Get("workspace_id")))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, effective)
}

func (s *Server) createSkillAssetRetentionPolicy(w http.ResponseWriter, r *http.Request) {
	store, err := s.skillAssetRetentionStore()
	if err != nil {
		writeError(w, err)
		return
	}
	var request createSkillAssetRetentionPolicyRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	principal := controlPrincipalFromRequest(r)
	if _, ok := PrincipalFromRequest(r); ok {
		switch request.ScopeType {
		case skillretention.ScopeWorkspace:
			request.WorkspaceID = requestWorkspaceID(r, request.WorkspaceID)
			request.OrganizationID = ""
		case skillretention.ScopeOrganization:
			request.OrganizationID = requestOrganizationID(r, request.OrganizationID)
			request.WorkspaceID = ""
			if request.OrganizationID == "" {
				writeError(w, fmt.Errorf("%w: organization identity is required", managedagents.ErrForbidden))
				return
			}
		}
	}
	record, version, err := store.CreateSkillAssetRetentionPolicy(r.Context(), skillretention.CreatePolicyInput{
		ScopeType: request.ScopeType, OrganizationID: request.OrganizationID, WorkspaceID: request.WorkspaceID,
		Config: request.Config, CreatedBy: principal.ID,
	})
	s.recordSkillAssetRetentionAudit(r, "skills.asset_retention.policy_create", request.WorkspaceID, record.ID, map[string]any{
		"scope_type": request.ScopeType, "organization_id": request.OrganizationID,
		"policy_version": version.Version, "policy_revision": version.Checksum,
	}, err)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"policy": record, "version": version})
}

func (s *Server) listSkillAssetRetentionPolicies(w http.ResponseWriter, r *http.Request) {
	store, err := s.skillAssetRetentionStore()
	if err != nil {
		writeError(w, err)
		return
	}
	organizationID := ""
	workspaceID := requestWorkspaceID(r, r.URL.Query().Get("workspace_id"))
	if r.URL.Query().Get("organization_id") != "" {
		organizationID = requestOrganizationID(r, r.URL.Query().Get("organization_id"))
		workspaceID = ""
	}
	items, err := store.ListSkillAssetRetentionPolicies(r.Context(), skillretention.ListPoliciesInput{
		OrganizationID: organizationID, WorkspaceID: workspaceID,
		IncludeArchived: r.URL.Query().Get("include_archived") == "true",
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policies": nonNilSlice(items)})
}

func (s *Server) getSkillAssetRetentionPolicy(w http.ResponseWriter, r *http.Request) {
	store, err := s.skillAssetRetentionStore()
	if err != nil {
		writeError(w, err)
		return
	}
	record, err := store.GetSkillAssetRetentionPolicy(r.Context(), r.PathValue("policy_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	version, err := store.GetSkillAssetRetentionPolicyVersion(r.Context(), record.ID, record.CurrentVersion)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policy": record, "version": version})
}

func (s *Server) publishSkillAssetRetentionPolicyVersion(w http.ResponseWriter, r *http.Request) {
	store, err := s.skillAssetRetentionStore()
	if err != nil {
		writeError(w, err)
		return
	}
	var request publishSkillAssetRetentionPolicyRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	policyID := r.PathValue("policy_id")
	principal := controlPrincipalFromRequest(r)
	version, err := store.PublishSkillAssetRetentionPolicyVersion(r.Context(), policyID, request.Config, principal.ID)
	record, _ := store.GetSkillAssetRetentionPolicy(r.Context(), policyID)
	s.recordSkillAssetRetentionAudit(r, "skills.asset_retention.policy_publish", record.WorkspaceID, policyID, map[string]any{
		"scope_type": record.ScopeType, "organization_id": record.OrganizationID,
		"policy_version": version.Version, "policy_revision": version.Checksum,
	}, err)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, version)
}

func (s *Server) getSkillAssetRetentionPolicyVersion(w http.ResponseWriter, r *http.Request) {
	store, err := s.skillAssetRetentionStore()
	if err != nil {
		writeError(w, err)
		return
	}
	versionNumber, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || versionNumber <= 0 {
		writeError(w, fmt.Errorf("%w: retention policy version must be positive", managedagents.ErrInvalid))
		return
	}
	version, err := store.GetSkillAssetRetentionPolicyVersion(r.Context(), r.PathValue("policy_id"), versionNumber)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, version)
}

func (s *Server) archiveSkillAssetRetentionPolicy(w http.ResponseWriter, r *http.Request) {
	store, err := s.skillAssetRetentionStore()
	if err != nil {
		writeError(w, err)
		return
	}
	policyID := r.PathValue("policy_id")
	before, _ := store.GetSkillAssetRetentionPolicy(r.Context(), policyID)
	record, err := store.ArchiveSkillAssetRetentionPolicy(r.Context(), policyID)
	if record.ID == "" {
		record = before
	}
	s.recordSkillAssetRetentionAudit(r, "skills.asset_retention.policy_archive", record.WorkspaceID, policyID, map[string]any{
		"scope_type": record.ScopeType, "organization_id": record.OrganizationID,
	}, err)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func (s *Server) previewSkillAssetGC(w http.ResponseWriter, r *http.Request) {
	if _, err := s.skillAssetRetentionStore(); err != nil {
		writeError(w, err)
		return
	}
	var request skillAssetGCRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	request.WorkspaceID = requestWorkspaceID(r, request.WorkspaceID)
	preview, err := s.skillRetention.Preview(r.Context(), request.WorkspaceID, request.Limit)
	s.recordSkillAssetRetentionAudit(r, "skills.asset_gc.preview", request.WorkspaceID, "", map[string]any{
		"candidate_count": preview.CandidateCount, "candidate_bytes": preview.CandidateBytes,
		"policy_source": preview.Effective.Source, "policy_revision": preview.Effective.Revision,
	}, err)
	if err != nil {
		writeError(w, err)
		return
	}
	preview.Candidates = nonNilSlice(preview.Candidates)
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) runSkillAssetGC(w http.ResponseWriter, r *http.Request) {
	if _, err := s.skillAssetRetentionStore(); err != nil {
		writeError(w, err)
		return
	}
	var request skillAssetGCRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	request.WorkspaceID = requestWorkspaceID(r, request.WorkspaceID)
	if request.Confirm != "DELETE" {
		writeError(w, fmt.Errorf("%w: confirm must equal DELETE", skillretention.ErrInvalid))
		return
	}
	principal := controlPrincipalFromRequest(r)
	result, err := s.skillRetention.Run(r.Context(), skillretention.RunRequest{
		WorkspaceID: request.WorkspaceID, Limit: request.Limit, RequestedBy: principal.ID,
	})
	s.recordSkillAssetRetentionAudit(r, "skills.asset_gc.run", request.WorkspaceID, result.Run.ID, map[string]any{
		"status": result.Run.Status, "candidate_count": result.Run.CandidateCount,
		"deleted_count": result.Run.DeletedCount, "skipped_count": result.Run.SkippedCount,
		"failed_count": result.Run.FailedCount, "bytes_deleted": result.Run.BytesDeleted,
	}, err)
	if err != nil {
		writeError(w, err)
		return
	}
	result.Items = nonNilSlice(result.Items)
	for _, item := range result.Items {
		if item.Status != skillretention.ItemStatusDeleted {
			continue
		}
		s.recordSkillAssetRetentionAudit(r, "skills.asset_gc.delete", request.WorkspaceID, item.Candidate.ObjectRefID, map[string]any{
			"run_id": result.Run.ID, "skill_id": item.Candidate.SkillID,
			"skill_version_id": item.Candidate.SkillVersionID, "asset_path": item.Candidate.AssetPath,
			"size_bytes": item.Candidate.SizeBytes, "checksum_sha256": item.Candidate.ChecksumSHA256,
			"reason": item.Reason, "object_was_missing": item.ObjectWasMissing,
		}, nil)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) listSkillAssetGCRuns(w http.ResponseWriter, r *http.Request) {
	store, err := s.skillAssetRetentionStore()
	if err != nil {
		writeError(w, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := store.ListSkillAssetGCRuns(r.Context(), skillretention.ListRunsInput{
		WorkspaceID: requestWorkspaceID(r, r.URL.Query().Get("workspace_id")), Limit: limit,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": nonNilSlice(runs)})
}

func (s *Server) getSkillAssetGCRun(w http.ResponseWriter, r *http.Request) {
	store, err := s.skillAssetRetentionStore()
	if err != nil {
		writeError(w, err)
		return
	}
	run, items, err := store.GetSkillAssetGCRun(r.Context(), r.PathValue("run_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if principal, ok := PrincipalFromRequest(r); ok {
		if err := authorizeWorkspacePrincipal(principal, run.WorkspaceID); err != nil {
			writeError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, skillretention.RunResult{Run: run, Items: nonNilSlice(items)})
}

func (s *Server) listSkillAssetGCTombstones(w http.ResponseWriter, r *http.Request) {
	store, err := s.skillAssetRetentionStore()
	if err != nil {
		writeError(w, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := store.ListSkillAssetGCTombstones(r.Context(), skillretention.ListTombstonesInput{
		WorkspaceID: requestWorkspaceID(r, r.URL.Query().Get("workspace_id")), Limit: limit,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tombstones": nonNilSlice(items)})
}

func (s *Server) recordSkillAssetRetentionAudit(r *http.Request, action string, workspaceID string, resourceID string, details map[string]any, actionErr error) {
	store, ok := s.store.(managedagents.OperatorAuditStore)
	if !ok {
		return
	}
	principal := controlPrincipalFromRequest(r)
	detailsJSON, _ := json.Marshal(details)
	outcome := "succeeded"
	errorMessage := ""
	if actionErr != nil {
		outcome = "failed"
		errorMessage = actionErr.Error()
	}
	if _, err := managedagents.RecordOperatorAuditWithContext(r.Context(), store, managedagents.RecordOperatorAuditInput{
		WorkspaceID: auditWorkspaceID(r, workspaceID), PrincipalID: principal.ID,
		OperatorLabel: principal.OperatorLabel, Role: principal.Role, Action: action,
		ResourceType: "skill_asset", ResourceID: strings.TrimSpace(resourceID), Outcome: outcome,
		ErrorMessage: errorMessage, Details: detailsJSON,
	}); err != nil && s.logger != nil {
		s.logger.Warn("skill asset retention audit write failed", "action", action, "resource_id", resourceID, "error", err)
	}
}

func skillAssetRetentionPolicyFromEnv() skillretention.Policy {
	policy := skillretention.DefaultPolicy()
	if value := strings.TrimSpace(os.Getenv("TMA_SKILLS_ASSET_RETENTION_ENABLED")); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			policy.Enabled = parsed
		}
	}
	if value, err := strconv.Atoi(strings.TrimSpace(os.Getenv("TMA_SKILLS_ASSET_RETENTION_DAYS"))); err == nil && value > 0 {
		policy.RetentionDays = value
	}
	if value, err := strconv.Atoi(strings.TrimSpace(os.Getenv("TMA_SKILLS_ASSET_GC_DELETE_LIMIT"))); err == nil && value > 0 {
		policy.DeleteLimit = value
	}
	return policy
}
