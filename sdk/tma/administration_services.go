package tma

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

type AuthService struct{ client *Client }

func (s *AuthService) Configuration(ctx context.Context) (AuthClientConfiguration, error) {
	var configuration AuthClientConfiguration
	err := s.client.DoJSON(ctx, http.MethodGet, "/v2/auth/config", nil, &configuration)
	return configuration, err
}

func (s *AuthService) Me(ctx context.Context) (AuthState, error) {
	var state AuthState
	err := s.client.DoJSON(ctx, http.MethodGet, "/v2/auth/me", nil, &state)
	return state, err
}

type MCPService struct{ client *Client }

func (s *MCPService) List(ctx context.Context, query MCPServerQuery) ([]MCPServer, error) {
	var response struct {
		Servers []MCPServer `json:"servers"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, mcpServersPath(query), nil, &response)
	return response.Servers, err
}

func (s *MCPService) RuntimeStatus(ctx context.Context, query MCPServerQuery) (MCPRuntimeStatus, error) {
	var status MCPRuntimeStatus
	path := "/v2/mcp-servers/runtime-status"
	if query.WorkspaceID != "" {
		path += "?workspace_id=" + url.QueryEscape(query.WorkspaceID)
	}
	err := s.client.DoJSON(ctx, http.MethodGet, path, nil, &status)
	return status, err
}

func (s *MCPService) Create(ctx context.Context, request CreateMCPServerRequest) (MCPServer, error) {
	var server MCPServer
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/mcp-servers", request, &server)
	return server, err
}

func (s *MCPService) Get(ctx context.Context, serverID string) (MCPServer, error) {
	var server MCPServer
	err := s.client.DoJSON(ctx, http.MethodGet, mcpServerPath(serverID), nil, &server)
	return server, err
}

func (s *MCPService) Update(ctx context.Context, serverID string, request UpdateMCPServerRequest) (MCPServer, error) {
	var server MCPServer
	err := s.client.DoJSON(ctx, http.MethodPatch, mcpServerPath(serverID), request, &server)
	return server, err
}

func (s *MCPService) SetEnabled(ctx context.Context, serverID string, enabled bool) (MCPServer, error) {
	action := "disable"
	if enabled {
		action = "enable"
	}
	var server MCPServer
	err := s.client.DoJSON(ctx, http.MethodPost, mcpServerPath(serverID)+"/"+action, nil, &server)
	return server, err
}

func (s *MCPService) Archive(ctx context.Context, serverID string) (MCPServer, error) {
	var server MCPServer
	err := s.client.DoJSON(ctx, http.MethodDelete, mcpServerPath(serverID), nil, &server)
	return server, err
}

func (s *MCPService) Test(ctx context.Context, serverID string) (MCPServerTestResult, error) {
	var result MCPServerTestResult
	err := s.client.DoJSON(ctx, http.MethodPost, mcpServerPath(serverID)+"/test", nil, &result)
	return result, err
}

func (s *MCPService) Versions(ctx context.Context, serverID string) ([]MCPServerVersion, error) {
	var response struct {
		Versions []MCPServerVersion `json:"versions"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, mcpServerPath(serverID)+"/versions", nil, &response)
	return response.Versions, err
}

func (s *MCPService) RestoreVersion(ctx context.Context, serverID string, version int32) (MCPRestoreResult, error) {
	var result MCPRestoreResult
	path := mcpServerPath(serverID) + "/versions/" + strconv.FormatInt(int64(version), 10) + "/restore"
	err := s.client.DoJSON(ctx, http.MethodPost, path, nil, &result)
	return result, err
}

func mcpServersPath(query MCPServerQuery) string {
	values := url.Values{}
	if query.WorkspaceID != "" {
		values.Set("workspace_id", query.WorkspaceID)
	}
	if len(values) == 0 {
		return "/v2/mcp-servers"
	}
	return "/v2/mcp-servers?" + values.Encode()
}

func mcpServerPath(serverID string) string {
	return "/v2/mcp-servers/" + url.PathEscape(serverID)
}

type AuditService struct{ client *Client }

func (s *AuditService) List(ctx context.Context, query OperatorAuditQuery) ([]OperatorAuditRecord, error) {
	values := operatorAuditValues(query)
	path := "/v2/operator-audit"
	if len(values) > 0 {
		path += "?" + values.Encode()
	}
	return s.list(ctx, path)
}

func (s *AuditService) ListSession(ctx context.Context, sessionID string) ([]OperatorAuditRecord, error) {
	return s.list(ctx, sessionPath(sessionID)+"/operator-audit")
}

func (s *AuditService) ListToolPermissions(ctx context.Context, sessionID string, query ToolPermissionAuditQuery) (ToolPermissionAuditPage, error) {
	values := url.Values{}
	if query.Decision != "" {
		values.Set("decision", query.Decision)
	}
	if query.Tool != "" {
		values.Set("tool", query.Tool)
	}
	if query.Limit > 0 {
		values.Set("limit", strconv.FormatInt(int64(query.Limit), 10))
	}
	if query.Cursor != "" {
		values.Set("cursor", query.Cursor)
	}
	path := sessionPath(sessionID) + "/tool-permission-audit"
	if len(values) > 0 {
		path += "?" + values.Encode()
	}
	var response ToolPermissionAuditPage
	err := s.client.DoJSON(ctx, http.MethodGet, path, nil, &response)
	return response, err
}

func (s *AuditService) IntegrityKeys(ctx context.Context) (SecurityAuditIntegrityKeyStatus, error) {
	var status SecurityAuditIntegrityKeyStatus
	err := s.client.DoJSON(ctx, http.MethodGet, "/v2/observability/security-audit/integrity-keys", nil, &status)
	return status, err
}

func (s *AuditService) ReplayDeadLetters(ctx context.Context, limit int32) (SecurityAuditReplayResult, error) {
	path := "/v2/observability/security-audit/replay"
	if limit > 0 {
		path += "?limit=" + strconv.FormatInt(int64(limit), 10)
	}
	var result SecurityAuditReplayResult
	err := s.client.DoJSON(ctx, http.MethodPost, path, nil, &result)
	return result, err
}

func (s *AuditService) list(ctx context.Context, path string) ([]OperatorAuditRecord, error) {
	var response struct {
		Records []OperatorAuditRecord `json:"audit_records"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, path, nil, &response)
	return response.Records, err
}

func operatorAuditValues(query OperatorAuditQuery) url.Values {
	values := url.Values{}
	if query.WorkspaceID != "" {
		values.Set("workspace_id", query.WorkspaceID)
	}
	if query.SessionID != "" {
		values.Set("session_id", query.SessionID)
	}
	if query.PrincipalID != "" {
		values.Set("principal_id", query.PrincipalID)
	}
	if query.Action != "" {
		values.Set("action", query.Action)
	}
	if query.Limit > 0 {
		values.Set("limit", strconv.FormatInt(int64(query.Limit), 10))
	}
	return values
}

type EnvironmentVariablesService struct{ client *Client }

func (s *EnvironmentVariablesService) List(ctx context.Context, query EnvironmentVariableQuery) ([]EnvironmentVariable, error) {
	var response struct {
		Variables []EnvironmentVariable `json:"variables"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, environmentVariablesPath(query), nil, &response)
	return response.Variables, err
}

func (s *EnvironmentVariablesService) Put(ctx context.Context, name string, query EnvironmentVariableQuery, request PutEnvironmentVariableRequest) (EnvironmentVariable, error) {
	var variable EnvironmentVariable
	path := environmentVariablePath(name, query)
	err := s.client.DoJSON(ctx, http.MethodPut, path, request, &variable)
	return variable, err
}

func (s *EnvironmentVariablesService) Delete(ctx context.Context, name string, query EnvironmentVariableQuery) error {
	return s.client.DoJSON(ctx, http.MethodDelete, environmentVariablePath(name, query), nil, nil)
}

func environmentVariablesPath(query EnvironmentVariableQuery) string {
	path := "/v2/environment-variables"
	if query.WorkspaceID != "" {
		path += "?workspace_id=" + url.QueryEscape(query.WorkspaceID)
	}
	return path
}

func environmentVariablePath(name string, query EnvironmentVariableQuery) string {
	path := "/v2/environment-variables/" + url.PathEscape(name)
	if query.WorkspaceID != "" {
		path += "?workspace_id=" + url.QueryEscape(query.WorkspaceID)
	}
	return path
}
