package httpapi

import (
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
	variables, err := service.List(r.Context(), requestWorkspaceID(r, r.URL.Query().Get("workspace_id")), environmentVariableOwnerID(r))
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
	variable, err := service.Put(
		r.Context(), requestWorkspaceID(r, r.URL.Query().Get("workspace_id")), environmentVariableOwnerID(r),
		r.PathValue("name"), request.Value,
	)
	s.recordEnvironmentVariableAudit(r, "environment_variable.put", r.PathValue("name"), err)
	if err != nil {
		writeEnvironmentVariableError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, variable)
}

func environmentVariableOwnerID(r *http.Request) string {
	if principal, ok := PrincipalFromRequest(r); ok && !principal.HasRole(RoleOperator) {
		return principal.OwnerID
	}
	return ""
}

func (s *Server) deleteEnvironmentVariable(w http.ResponseWriter, r *http.Request) {
	service, err := s.environmentVariableService()
	if err != nil {
		writeEnvironmentVariableError(w, err)
		return
	}
	err = service.Delete(r.Context(), requestWorkspaceID(r, r.URL.Query().Get("workspace_id")), r.PathValue("name"))
	s.recordEnvironmentVariableAudit(r, "environment_variable.delete", r.PathValue("name"), err)
	if err != nil {
		writeEnvironmentVariableError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	details, _ := json.Marshal(map[string]any{"variable_name": strings.TrimSpace(name)})
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
