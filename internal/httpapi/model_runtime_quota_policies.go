package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tiggy-manage-agent/internal/managedagents"
)

type putModelRuntimeQuotaPolicyRequest struct {
	Plan   string                                      `json:"plan"`
	Config managedagents.ModelRuntimeQuotaPolicyConfig `json:"config"`
}

func (s *Server) registerModelRuntimeQuotaPolicyRoutes() {
	s.mux.HandleFunc("GET /v2/quota-policies", s.withV2Request(s.requireWorkspaceQuotaOperator(s.listModelRuntimeQuotaPolicies)))
	s.mux.HandleFunc("PUT /v2/quota-policies/workspace", s.withV2Request(s.requireWorkspaceQuotaOperator(s.putWorkspaceModelRuntimeQuotaPolicy)))
	s.mux.HandleFunc("PUT /v2/quota-policies/applications/{app_id}", s.withV2Request(s.requireWorkspaceQuotaOperator(s.putApplicationModelRuntimeQuotaPolicy)))
	s.mux.HandleFunc("DELETE /v2/quota-policies/applications/{app_id}", s.withV2Request(s.requireWorkspaceQuotaOperator(s.deleteApplicationModelRuntimeQuotaPolicy)))
	s.mux.HandleFunc("GET /v2/quota-policies/effective", s.withV2Request(s.getEffectiveModelRuntimeQuotaPolicy))
}

func (s *Server) requireWorkspaceQuotaOperator(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := s.administrationPrincipal(r)
		if !ok && principal.Subject == "" {
			writeV2ManagedError(w, r, managedagents.ErrForbidden)
			return
		}
		allowed, err := s.principalIsWorkspaceQuotaOperator(r, principal)
		if err != nil {
			writeV2ManagedError(w, r, err)
			return
		}
		if !allowed {
			writeV2ManagedError(w, r, fmt.Errorf("%w: workspace operator or admin role required", managedagents.ErrForbidden))
			return
		}
		next(w, r)
	}
}

func (s *Server) principalIsWorkspaceQuotaOperator(r *http.Request, principal Principal) (bool, error) {
	if principal.Subject == "local-development" || principal.AuthType == "legacy-control" {
		return true, nil
	}
	if principal.ServiceIdentityID != "" {
		store, ok := s.store.(managedagents.ServiceIdentityStore)
		if !ok {
			return false, fmt.Errorf("%w: service identity store is unavailable", managedagents.ErrInvalid)
		}
		identity, err := store.GetServiceIdentity(r.Context(), principal.WorkspaceID, principal.ServiceIdentityID)
		if err != nil {
			return false, err
		}
		return identity.Kind == managedagents.ServiceIdentityKindService && principal.HasRole(RoleOperator) && principal.HasScope(managedagents.ServiceScopeQuotaWrite), nil
	}
	store, err := s.tenantAdministrationStore()
	if err != nil {
		return principal.HasRole(RoleOperator) || principal.HasRole(RoleAdmin), nil
	}
	membership, err := store.GetWorkspaceMembership(r.Context(), principal.WorkspaceID, principal.Subject)
	if err != nil {
		if errors.Is(err, managedagents.ErrNotFound) {
			return principal.HasRole(RoleOperator) || principal.HasRole(RoleAdmin), nil
		}
		return false, err
	}
	return membership.Status == "active" && (membership.Role == managedagents.WorkspaceRoleOperator || membership.Role == managedagents.WorkspaceRoleAdmin), nil
}

func (s *Server) modelRuntimeQuotaPolicyStore() (managedagents.ModelRuntimeQuotaPolicyStore, error) {
	store, ok := s.store.(managedagents.ModelRuntimeQuotaPolicyStore)
	if !ok {
		return nil, fmt.Errorf("%w: quota policy store is unavailable", managedagents.ErrInvalid)
	}
	return store, nil
}

func (s *Server) listModelRuntimeQuotaPolicies(w http.ResponseWriter, r *http.Request) {
	principal, _ := s.administrationPrincipal(r)
	store, err := s.modelRuntimeQuotaPolicyStore()
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	items, err := store.ListModelRuntimeQuotaPoliciesContext(r.Context(), principal.WorkspaceID, r.URL.Query().Get("include_archived") == "true")
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policies": items})
}

func (s *Server) putWorkspaceModelRuntimeQuotaPolicy(w http.ResponseWriter, r *http.Request) {
	s.putModelRuntimeQuotaPolicy(w, r, managedagents.ModelRuntimeQuotaScopeWorkspace, "")
}

func (s *Server) putApplicationModelRuntimeQuotaPolicy(w http.ResponseWriter, r *http.Request) {
	s.putModelRuntimeQuotaPolicy(w, r, managedagents.ModelRuntimeQuotaScopeApplication, strings.TrimSpace(r.PathValue("app_id")))
}

func (s *Server) putModelRuntimeQuotaPolicy(w http.ResponseWriter, r *http.Request, scope, appID string) {
	principal, _ := s.administrationPrincipal(r)
	var request putModelRuntimeQuotaPolicyRequest
	if err := decodeJSON(r, &request); err != nil {
		writeV2ManagedError(w, r, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err))
		return
	}
	expectedRevision, err := parseOptionalQuotaPolicyIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	store, err := s.modelRuntimeQuotaPolicyStore()
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	item, err := store.UpsertModelRuntimeQuotaPolicyContext(r.Context(), managedagents.UpsertModelRuntimeQuotaPolicyInput{
		WorkspaceID: principal.WorkspaceID, Scope: scope, AppID: appID, Plan: request.Plan,
		Config: request.Config, ExpectedRevision: expectedRevision, UpdatedBy: requestActorID(r, principal.Subject),
	})
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	s.auditModelRuntimeQuotaPolicyChange(r, principal, "quota_policy.upsert", item)
	w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(item.Revision, 10)))
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteApplicationModelRuntimeQuotaPolicy(w http.ResponseWriter, r *http.Request) {
	principal, _ := s.administrationPrincipal(r)
	expectedRevision, err := parseRequiredQuotaPolicyIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	store, err := s.modelRuntimeQuotaPolicyStore()
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	item, err := store.ArchiveModelRuntimeQuotaPolicyContext(r.Context(), managedagents.ArchiveModelRuntimeQuotaPolicyInput{
		WorkspaceID: principal.WorkspaceID, Scope: managedagents.ModelRuntimeQuotaScopeApplication,
		AppID: strings.TrimSpace(r.PathValue("app_id")), ExpectedRevision: expectedRevision,
		ArchivedBy: requestActorID(r, principal.Subject),
	})
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	s.auditModelRuntimeQuotaPolicyChange(r, principal, "quota_policy.archive", item)
	w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(item.Revision, 10)))
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) getEffectiveModelRuntimeQuotaPolicy(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.administrationPrincipal(r)
	if !ok && principal.Subject == "" {
		writeV2ManagedError(w, r, managedagents.ErrForbidden)
		return
	}
	appID := strings.TrimSpace(r.URL.Query().Get("app_id"))
	if principal.ServiceIdentityID != "" {
		if appID != "" && appID != principal.ServiceIdentityID {
			writeV2ManagedError(w, r, fmt.Errorf("%w: application credentials can only read their own quota status", managedagents.ErrForbidden))
			return
		}
		appID = principal.ServiceIdentityID
	} else {
		allowed, err := s.principalIsWorkspaceQuotaOperator(r, principal)
		if err != nil {
			writeV2ManagedError(w, r, err)
			return
		}
		if !allowed {
			writeV2ManagedError(w, r, fmt.Errorf("%w: workspace operator or admin role required", managedagents.ErrForbidden))
			return
		}
	}
	effective, err := s.effectiveModelRuntimeQuotaPolicy(r.Context(), principal.WorkspaceID, appID)
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	store, err := s.modelRuntimeQuotaPolicyStore()
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	usage, err := store.GetModelRuntimeQuotaUsageContext(r.Context(), principal.WorkspaceID, appID, periodStart, periodStart.AddDate(0, 1, 0))
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, managedagents.ModelRuntimeQuotaStatus{
		Policy: effective, Usage: usage, Alerts: managedagents.ModelRuntimeQuotaAlerts(effective, usage),
	})
}

func parseOptionalQuotaPolicyIfMatch(value string) (int64, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	return parseRequiredQuotaPolicyIfMatch(value)
}

func parseRequiredQuotaPolicyIfMatch(value string) (int64, error) {
	unquoted, err := strconv.Unquote(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%w: If-Match must be a quoted quota policy revision", managedagents.ErrInvalid)
	}
	revision, err := strconv.ParseInt(unquoted, 10, 64)
	if err != nil || revision <= 0 {
		return 0, fmt.Errorf("%w: If-Match must contain a positive quota policy revision", managedagents.ErrInvalid)
	}
	return revision, nil
}

func (s *Server) auditModelRuntimeQuotaPolicyChange(r *http.Request, principal Principal, action string, item managedagents.ModelRuntimeQuotaPolicy) {
	store, ok := s.store.(managedagents.OperatorAuditStore)
	if !ok {
		return
	}
	details, _ := json.Marshal(map[string]any{"scope": item.Scope, "app_id": item.AppID, "revision": item.Revision, "plan": item.Plan})
	if _, err := managedagents.RecordOperatorAuditWithContext(r.Context(), store, managedagents.RecordOperatorAuditInput{
		WorkspaceID: item.WorkspaceID, PrincipalID: requestActorID(r, principal.Subject), Role: quotaOperatorRole(principal),
		Action: action, ResourceType: "model_runtime_quota_policy", ResourceID: item.ID,
		Outcome: "succeeded", Details: details,
	}); err != nil && s.logger != nil {
		s.logger.Error("record quota policy operator audit", "action", action, "policy_id", item.ID, "error", err)
	}
}

func quotaOperatorRole(principal Principal) string {
	if principal.HasRole(RoleAdmin) {
		return RoleAdmin
	}
	return RoleOperator
}
