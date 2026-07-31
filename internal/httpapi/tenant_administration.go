package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"tiggy-manage-agent/internal/managedagents"
)

type administrationContextResponse struct {
	Authenticated  bool      `json:"authenticated"`
	Principal      Principal `json:"principal"`
	WorkspaceAdmin bool      `json:"workspace_admin"`
	PlatformAdmin  bool      `json:"platform_admin"`
}

func (s *Server) registerTenantAdministrationRoutes() {
	s.mux.HandleFunc("GET /v2/administration/context", s.withV2Request(s.getAdministrationContext))
	// Deprecated compatibility alias for clients released before the API was decoupled from the Console UI.
	s.mux.HandleFunc("GET /v2/console/context", s.withV2Request(s.getAdministrationContext))
	s.mux.HandleFunc("GET /v2/workspace/members", s.withV2Request(s.requireWorkspaceAdmin(s.listWorkspaceMembers)))
	s.mux.HandleFunc("PUT /v2/workspace/members/{subject}", s.withV2Request(s.requireWorkspaceAdmin(s.upsertWorkspaceMember)))
	s.mux.HandleFunc("DELETE /v2/workspace/members/{subject}", s.withV2Request(s.requireWorkspaceAdmin(s.deleteWorkspaceMember)))
	s.mux.HandleFunc("GET /v2/platform/workspaces", s.withV2Request(s.requirePlatformAdmin(s.listPlatformWorkspaces)))
	s.mux.HandleFunc("POST /v2/platform/workspaces", s.withV2Request(s.requirePlatformAdmin(s.createPlatformWorkspace)))
	s.mux.HandleFunc("GET /v2/platform/workspaces/{workspace_id}/members", s.withV2Request(s.requirePlatformAdmin(s.listPlatformWorkspaceMembers)))
	s.mux.HandleFunc("PUT /v2/platform/workspaces/{workspace_id}/members/{subject}", s.withV2Request(s.requirePlatformAdmin(s.upsertPlatformWorkspaceMember)))
	s.mux.HandleFunc("DELETE /v2/platform/workspaces/{workspace_id}/members/{subject}", s.withV2Request(s.requirePlatformAdmin(s.deletePlatformWorkspaceMember)))
	s.mux.HandleFunc("GET /v2/platform/admins", s.withV2Request(s.requirePlatformAdmin(s.listPlatformAdmins)))
	s.mux.HandleFunc("PUT /v2/platform/admins/{subject}", s.withV2Request(s.requirePlatformAdmin(s.upsertPlatformAdmin)))
	s.mux.HandleFunc("DELETE /v2/platform/admins/{subject}", s.withV2Request(s.requirePlatformAdmin(s.deletePlatformAdmin)))
}

func (s *Server) tenantAdministrationStore() (managedagents.TenantAdministrationStore, error) {
	store, ok := s.store.(managedagents.TenantAdministrationStore)
	if !ok {
		return nil, fmt.Errorf("%w: tenant administration store is unavailable", managedagents.ErrInvalid)
	}
	return store, nil
}

func (s *Server) administrationPrincipal(r *http.Request) (Principal, bool) {
	if principal, ok := PrincipalFromRequest(r); ok {
		return principal, true
	}
	if s != nil && s.authenticator != nil && s.authenticator.config.Mode == AuthModeDisabled {
		return Principal{
			Subject: "local-development", WorkspaceID: managedagents.DefaultWorkspaceID,
			OwnerID: "local-development", Roles: []string{RoleAdmin}, AuthType: AuthModeDisabled,
		}, false
	}
	return Principal{}, false
}

func (s *Server) principalIsPlatformAdmin(r *http.Request, principal Principal) (bool, error) {
	if principal.Subject == "local-development" || principal.AuthType == "legacy-control" {
		return true, nil
	}
	store, err := s.tenantAdministrationStore()
	if err != nil {
		return false, err
	}
	return store.IsPlatformAdmin(r.Context(), principal.Subject)
}

func (s *Server) principalIsWorkspaceAdmin(r *http.Request, principal Principal) (bool, error) {
	if principal.Subject == "local-development" || principal.AuthType == "legacy-control" {
		return true, nil
	}
	store, err := s.tenantAdministrationStore()
	if err != nil {
		return false, err
	}
	membership, err := store.GetWorkspaceMembership(r.Context(), principal.WorkspaceID, principal.Subject)
	if errors.Is(err, managedagents.ErrNotFound) {
		return principal.HasRole(RoleAdmin), nil
	}
	if err != nil {
		return false, err
	}
	return membership.Status == "active" && membership.Role == managedagents.WorkspaceRoleAdmin, nil
}

func (s *Server) requireWorkspaceAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := s.administrationPrincipal(r)
		if !ok && principal.Subject == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		allowed, err := s.principalIsWorkspaceAdmin(r, principal)
		if err != nil {
			writeError(w, err)
			return
		}
		if !allowed {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "workspace admin role required"})
			return
		}
		next(w, r)
	}
}

func (s *Server) requirePlatformAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := s.administrationPrincipal(r)
		if !ok && principal.Subject == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		allowed, err := s.principalIsPlatformAdmin(r, principal)
		if err != nil {
			s.auditAuthorizationDecision(r, principal, authorizationOutcomeForError(err), "platform_admin_resolution_failed", managedagents.PlatformRoleAdmin, err)
			writeError(w, err)
			return
		}
		if !allowed {
			s.auditAuthorizationDecision(r, principal, "denied", "platform_admin_required", managedagents.PlatformRoleAdmin, nil)
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "platform administrator required"})
			return
		}
		s.auditAuthorizationDecision(r, principal, "allowed", "platform_admin", managedagents.PlatformRoleAdmin, nil)
		next(w, r)
	}
}

func (s *Server) getAdministrationContext(w http.ResponseWriter, r *http.Request) {
	principal, authenticated := s.administrationPrincipal(r)
	if principal.Subject == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	platformAdmin, err := s.principalIsPlatformAdmin(r, principal)
	if err != nil {
		writeError(w, err)
		return
	}
	workspaceAdmin, err := s.principalIsWorkspaceAdmin(r, principal)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, administrationContextResponse{
		Authenticated: authenticated, Principal: principal,
		WorkspaceAdmin: workspaceAdmin, PlatformAdmin: platformAdmin,
	})
}

func (s *Server) listWorkspaceMembers(w http.ResponseWriter, r *http.Request) {
	principal, _ := s.administrationPrincipal(r)
	store, err := s.tenantAdministrationStore()
	if err != nil {
		writeError(w, err)
		return
	}
	items, err := store.ListWorkspaceMemberships(r.Context(), principal.WorkspaceID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": items})
}

func (s *Server) upsertWorkspaceMember(w http.ResponseWriter, r *http.Request) {
	principal, _ := s.administrationPrincipal(r)
	var request struct {
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
		Role        string `json:"role"`
		Status      string `json:"status"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	store, err := s.tenantAdministrationStore()
	if err != nil {
		writeError(w, err)
		return
	}
	item, err := store.UpsertWorkspaceMembership(r.Context(), managedagents.UpsertWorkspaceMembershipInput{
		WorkspaceID: principal.WorkspaceID, Subject: r.PathValue("subject"), DisplayName: request.DisplayName,
		Email: request.Email, Role: request.Role, Status: request.Status,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteWorkspaceMember(w http.ResponseWriter, r *http.Request) {
	principal, _ := s.administrationPrincipal(r)
	subject := strings.TrimSpace(r.PathValue("subject"))
	if subject == principal.Subject {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace administrator cannot remove itself"})
		return
	}
	store, err := s.tenantAdministrationStore()
	if err != nil {
		writeError(w, err)
		return
	}
	if err := store.DeleteWorkspaceMembership(r.Context(), principal.WorkspaceID, subject); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listPlatformWorkspaces(w http.ResponseWriter, r *http.Request) {
	principal, _ := s.administrationPrincipal(r)
	store, err := s.tenantAdministrationStore()
	if err != nil {
		writeError(w, err)
		return
	}
	items, err := store.ListTenantWorkspaces(r.Context(), principal.Subject)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspaces": items})
}

func (s *Server) createPlatformWorkspace(w http.ResponseWriter, r *http.Request) {
	principal, _ := s.administrationPrincipal(r)
	var request struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	store, err := s.tenantAdministrationStore()
	if err != nil {
		writeError(w, err)
		return
	}
	item, err := store.CreateTenantWorkspace(r.Context(), principal.Subject, request.Name)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func platformWorkspaceContext(r *http.Request) (context.Context, string, error) {
	workspaceID := strings.TrimSpace(r.PathValue("workspace_id"))
	if workspaceID == "" {
		return nil, "", fmt.Errorf("%w: workspace id is required", managedagents.ErrInvalid)
	}
	ctx, err := managedagents.ContextWithDatabaseAccessScope(r.Context(), managedagents.AccessScope{WorkspaceID: workspaceID})
	return ctx, workspaceID, err
}

func (s *Server) listPlatformWorkspaceMembers(w http.ResponseWriter, r *http.Request) {
	ctx, workspaceID, err := platformWorkspaceContext(r)
	if err != nil {
		writeError(w, err)
		return
	}
	store, err := s.tenantAdministrationStore()
	if err != nil {
		writeError(w, err)
		return
	}
	items, err := store.ListWorkspaceMemberships(ctx, workspaceID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": items})
}

func (s *Server) upsertPlatformWorkspaceMember(w http.ResponseWriter, r *http.Request) {
	ctx, workspaceID, err := platformWorkspaceContext(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var request struct {
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
		Role        string `json:"role"`
		Status      string `json:"status"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	store, err := s.tenantAdministrationStore()
	if err != nil {
		writeError(w, err)
		return
	}
	item, err := store.UpsertWorkspaceMembership(ctx, managedagents.UpsertWorkspaceMembershipInput{
		WorkspaceID: workspaceID, Subject: r.PathValue("subject"), DisplayName: request.DisplayName,
		Email: request.Email, Role: request.Role, Status: request.Status,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deletePlatformWorkspaceMember(w http.ResponseWriter, r *http.Request) {
	ctx, workspaceID, err := platformWorkspaceContext(r)
	if err != nil {
		writeError(w, err)
		return
	}
	store, err := s.tenantAdministrationStore()
	if err != nil {
		writeError(w, err)
		return
	}
	if err := store.DeleteWorkspaceMembership(ctx, workspaceID, r.PathValue("subject")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listPlatformAdmins(w http.ResponseWriter, r *http.Request) {
	principal, _ := s.administrationPrincipal(r)
	store, err := s.tenantAdministrationStore()
	if err != nil {
		writeError(w, err)
		return
	}
	items, err := store.ListPlatformAdmins(r.Context(), principal.Subject)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"admins": items})
}

func (s *Server) upsertPlatformAdmin(w http.ResponseWriter, r *http.Request) {
	principal, _ := s.administrationPrincipal(r)
	var request struct {
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	store, err := s.tenantAdministrationStore()
	if err != nil {
		writeError(w, err)
		return
	}
	item, err := store.UpsertPlatformAdmin(r.Context(), principal.Subject, managedagents.PlatformRoleAssignment{
		Subject: r.PathValue("subject"), DisplayName: request.DisplayName, Email: request.Email,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deletePlatformAdmin(w http.ResponseWriter, r *http.Request) {
	principal, _ := s.administrationPrincipal(r)
	store, err := s.tenantAdministrationStore()
	if err != nil {
		writeError(w, err)
		return
	}
	if err := store.DeletePlatformAdmin(r.Context(), principal.Subject, r.PathValue("subject")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
