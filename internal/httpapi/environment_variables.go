package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"tiggy-manage-agent/internal/envvars"
	"tiggy-manage-agent/internal/managedagents"
)

type putEnvironmentVariableRequest struct {
	Value string `json:"value"`
}

type environmentVariableTarget struct {
	WorkspaceID string
	OwnerID     string
	AppID       string
}

func (s *Server) environmentVariableService() (*envvars.Service, error) {
	store, ok := s.store.(envvars.Store)
	if !ok {
		return nil, errors.New("managed environment variable store is unavailable")
	}
	return envvars.NewServiceFromEnvironment(store)
}

func (s *Server) listEnvironmentVariables(w http.ResponseWriter, r *http.Request) {
	service, err := s.environmentVariableService()
	if err != nil {
		writeEnvironmentVariableError(w, err)
		return
	}
	target, scopedRequest, err := s.environmentVariableTarget(r)
	if err != nil {
		writeEnvironmentVariableError(w, err)
		return
	}
	var variables []envvars.VariableMetadata
	if target.AppID != "" {
		variables, err = service.ListOwned(scopedRequest.Context(), target.WorkspaceID, target.OwnerID)
	} else {
		variables, err = service.List(scopedRequest.Context(), target.WorkspaceID, target.OwnerID)
	}
	if err != nil {
		writeEnvironmentVariableError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"variables": nonNilSlice(variables)})
}

func (s *Server) putEnvironmentVariable(w http.ResponseWriter, r *http.Request) {
	service, err := s.environmentVariableService()
	if err != nil {
		writeEnvironmentVariableError(w, err)
		return
	}
	var request putEnvironmentVariableRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	target, scopedRequest, err := s.environmentVariableTarget(r)
	if err != nil {
		writeEnvironmentVariableError(w, err)
		return
	}
	variable, err := service.Put(
		scopedRequest.Context(), target.WorkspaceID, target.OwnerID,
		r.PathValue("name"), request.Value,
	)
	s.recordEnvironmentVariableAudit(r, "environment_variable.put", r.PathValue("name"), err)
	if err != nil {
		writeEnvironmentVariableError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, variable)
}

func (s *Server) deleteEnvironmentVariable(w http.ResponseWriter, r *http.Request) {
	service, err := s.environmentVariableService()
	if err != nil {
		writeEnvironmentVariableError(w, err)
		return
	}
	target, scopedRequest, err := s.environmentVariableTarget(r)
	if err != nil {
		writeEnvironmentVariableError(w, err)
		return
	}
	err = service.Delete(scopedRequest.Context(), target.WorkspaceID, r.PathValue("name"))
	s.recordEnvironmentVariableAudit(r, "environment_variable.delete", r.PathValue("name"), err)
	if err != nil {
		writeEnvironmentVariableError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) environmentVariableTarget(r *http.Request) (environmentVariableTarget, *http.Request, error) {
	workspaceID := requestWorkspaceID(r, r.URL.Query().Get("workspace_id"))
	requestedAppID := strings.TrimSpace(r.URL.Query().Get("app_id"))
	target := environmentVariableTarget{WorkspaceID: workspaceID}
	principal, authenticated := PrincipalFromRequest(r)
	if authenticated && principal.ServiceIdentityID != "" {
		if requestedAppID != "" && requestedAppID != principal.ServiceIdentityID {
			return target, r, managedagents.ErrForbidden
		}
		target.AppID = principal.ServiceIdentityID
	} else if requestedAppID != "" {
		if !authenticated || !principal.HasRole(RoleOperator) {
			return target, r, managedagents.ErrForbidden
		}
		target.AppID = requestedAppID
	} else if authenticated && !principal.HasRole(RoleOperator) {
		target.OwnerID = principal.OwnerID
	}
	if target.AppID != "" {
		store, err := s.serviceIdentityStore()
		if err != nil {
			return target, r, err
		}
		identity, err := store.GetServiceIdentity(r.Context(), workspaceID, target.AppID)
		if err != nil {
			return target, r, err
		}
		if identity.Kind != managedagents.ServiceIdentityKindApplication || identity.Status != managedagents.ServiceIdentityStatusActive {
			return target, r, managedagents.ErrForbidden
		}
		target.OwnerID = envvars.ApplicationOwnerID(target.AppID)
	}
	if !authenticated {
		return target, r, nil
	}
	ctx, err := managedagents.ContextWithDatabaseAccessScope(r.Context(), managedagents.AccessScope{
		WorkspaceID: workspaceID,
		OwnerID:     target.OwnerID,
	})
	if err != nil {
		return target, r, err
	}
	return target, r.WithContext(ctx), nil
}

func (s *Server) resolveManagedEnvironmentForApp(ctx context.Context, workspaceID string, appID string) (map[string]string, error) {
	values, _, err := envvars.ResolveWorkspace(ctx, s.store, workspaceID)
	if err != nil || strings.TrimSpace(appID) == "" {
		return values, err
	}
	ownerID := envvars.ApplicationOwnerID(appID)
	appContext, err := managedagents.ContextWithDatabaseAccessScope(ctx, managedagents.AccessScope{WorkspaceID: workspaceID, OwnerID: ownerID})
	if err != nil {
		return nil, err
	}
	appValues, _, err := envvars.ResolveOwned(appContext, s.store, workspaceID, ownerID)
	if err != nil {
		return nil, err
	}
	if values == nil {
		values = make(map[string]string, len(appValues))
	}
	for name, value := range appValues {
		values[name] = value
	}
	return values, nil
}

func (s *Server) recordEnvironmentVariableAudit(r *http.Request, action string, name string, actionErr error) {
	store, ok := s.store.(managedagents.OperatorAuditStore)
	if !ok {
		return
	}
	workspaceID := requestWorkspaceID(r, r.URL.Query().Get("workspace_id"))
	principal := controlPrincipalFromRequest(r)
	outcome := "succeeded"
	errorMessage := ""
	if actionErr != nil {
		outcome = "failed"
		errorMessage = actionErr.Error()
	}
	appID := strings.TrimSpace(r.URL.Query().Get("app_id"))
	if principal, ok := PrincipalFromRequest(r); ok && appID == "" {
		appID = principal.ServiceIdentityID
	}
	details, _ := json.Marshal(map[string]any{
		"variable_name": strings.TrimSpace(name),
		"app_id":        appID,
	})
	if _, err := managedagents.RecordOperatorAuditWithContext(r.Context(), store, managedagents.RecordOperatorAuditInput{
		WorkspaceID: auditWorkspaceID(r, workspaceID), PrincipalID: principal.ID,
		OperatorLabel: principal.OperatorLabel, Role: principal.Role, Action: action,
		ResourceType: "environment_variable", ResourceID: strings.TrimSpace(name), Outcome: outcome,
		ErrorMessage: errorMessage, Details: details,
	}); err != nil && s.logger != nil {
		s.logger.Warn("environment variable audit write failed", "action", action, "variable_name", name, "error", err)
	}
}

func writeEnvironmentVariableError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "managed environment variable operation failed"
	switch {
	case errors.Is(err, envvars.ErrInvalid):
		status = http.StatusBadRequest
		message = err.Error()
	case errors.Is(err, envvars.ErrNotConfigured), strings.Contains(err.Error(), "store is unavailable"):
		status = http.StatusServiceUnavailable
		message = err.Error()
	case errors.Is(err, managedagents.ErrForbidden):
		status = http.StatusForbidden
		message = "environment variable is read-only"
	case errors.Is(err, managedagents.ErrNotFound), strings.Contains(err.Error(), "not found"):
		status = http.StatusNotFound
		message = "managed environment variable not found"
	}
	writeJSON(w, status, map[string]string{"error": message})
}
