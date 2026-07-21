package httpapi

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/envvars"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/runner"
)

func TestEnvironmentVariableAPIStoresEncryptedValueAndNeverReturnsIt(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envvars.MasterKeyEnvironmentVariable, base64.StdEncoding.EncodeToString(key))
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, 0, nil), nil)

	put := httptest.NewRecorder()
	server.ServeHTTP(put, jsonRequest(t, http.MethodPut, "/v1/environment-variables/SERVICE_API_KEY", `{"value":"top-secret-value"}`))
	if put.Code != http.StatusOK {
		t.Fatalf("put variable: %d: %s", put.Code, put.Body.String())
	}
	if strings.Contains(put.Body.String(), "top-secret-value") {
		t.Fatal("put response exposed secret value")
	}
	if len(store.operatorAudits) != 1 || store.operatorAudits[0].Action != "environment_variable.put" || strings.Contains(string(store.operatorAudits[0].Details), "top-secret-value") {
		t.Fatalf("unexpected environment variable audit: %#v", store.operatorAudits)
	}
	record := store.environmentVariables["wksp_default"][environmentVariableStoreKey("", "SERVICE_API_KEY")]
	if len(record.Ciphertext) == 0 || strings.Contains(string(record.Ciphertext), "top-secret-value") {
		t.Fatal("store did not persist ciphertext")
	}

	get := httptest.NewRecorder()
	server.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/v1/environment-variables", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("list variables: %d: %s", get.Code, get.Body.String())
	}
	if strings.Contains(get.Body.String(), "top-secret-value") || !strings.Contains(get.Body.String(), "SERVICE_API_KEY") {
		t.Fatalf("unexpected list response: %s", get.Body.String())
	}

	service, err := envvars.NewServiceFromEnvironment(store)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := service.Resolve(t.Context(), "wksp_default")
	if err != nil || resolved["SERVICE_API_KEY"] != "top-secret-value" {
		t.Fatalf("unexpected resolved environment: %#v: %v", resolved, err)
	}
}

func TestEnvironmentVariablesSupportPersonalAndReadOnlyWorkspaceScopes(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envvars.MasterKeyEnvironmentVariable, base64.StdEncoding.EncodeToString(key))
	server, store := newUnifiedAuthTestServer(t, AuthConfig{
		Mode: AuthModeJWT, JWTSecret: testJWTSecret, JWTIssuer: "https://issuer.example", JWTAudience: "tma-api",
	})
	workspaceID := "wksp_environment_scope"
	adminToken := signedTestJWT(t, "environment-admin", workspaceID, "owner-admin", []string{RoleAdmin}, nil)
	memberToken := signedTestJWT(t, "environment-member", workspaceID, "owner-member", []string{RoleMember}, nil)

	adminPut := httptest.NewRecorder()
	server.ServeHTTP(adminPut, authenticatedJSONRequest(t, http.MethodPut, "/v1/environment-variables/ORG_API_KEY", `{"value":"workspace-secret"}`, adminToken))
	if adminPut.Code != http.StatusOK {
		t.Fatalf("admin put workspace variable: %d: %s", adminPut.Code, adminPut.Body.String())
	}
	var shared envvars.VariableMetadata
	decodeTestResponse(t, adminPut, &shared)
	if shared.Scope != envvars.ScopeWorkspace || !shared.Editable {
		t.Fatalf("unexpected admin workspace variable metadata: %+v", shared)
	}

	memberList := httptest.NewRecorder()
	server.ServeHTTP(memberList, authenticatedRequest(t, http.MethodGet, "/v1/environment-variables", memberToken))
	if memberList.Code != http.StatusOK {
		t.Fatalf("member list variables: %d: %s", memberList.Code, memberList.Body.String())
	}
	var listed struct {
		Variables []envvars.VariableMetadata `json:"variables"`
	}
	decodeTestResponse(t, memberList, &listed)
	if len(listed.Variables) != 1 || listed.Variables[0].Name != "ORG_API_KEY" ||
		listed.Variables[0].Scope != envvars.ScopeWorkspace || listed.Variables[0].Editable {
		t.Fatalf("member did not receive read-only workspace variable: %+v", listed.Variables)
	}

	memberDeleteShared := httptest.NewRecorder()
	server.ServeHTTP(memberDeleteShared, authenticatedRequest(t, http.MethodDelete, "/v1/environment-variables/ORG_API_KEY", memberToken))
	if memberDeleteShared.Code != http.StatusNotFound {
		t.Fatalf("member delete workspace variable returned %d: %s", memberDeleteShared.Code, memberDeleteShared.Body.String())
	}

	memberPut := httptest.NewRecorder()
	server.ServeHTTP(memberPut, authenticatedJSONRequest(t, http.MethodPut, "/v1/environment-variables/MY_API_KEY", `{"value":"personal-secret"}`, memberToken))
	if memberPut.Code != http.StatusOK {
		t.Fatalf("member put personal variable: %d: %s", memberPut.Code, memberPut.Body.String())
	}
	var personal envvars.VariableMetadata
	decodeTestResponse(t, memberPut, &personal)
	if personal.Scope != envvars.ScopePersonal || !personal.Editable {
		t.Fatalf("unexpected member personal variable metadata: %+v", personal)
	}

	memberCtx, err := managedagents.ContextWithDatabaseAccessScope(t.Context(), managedagents.AccessScope{
		WorkspaceID: workspaceID, OwnerID: "owner-member",
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := envvars.NewServiceFromEnvironment(store)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := service.Resolve(memberCtx, workspaceID)
	if err != nil || resolved["ORG_API_KEY"] != "workspace-secret" || resolved["MY_API_KEY"] != "personal-secret" {
		t.Fatalf("member runtime environment did not combine workspace and personal variables: values=%+v err=%v", resolved, err)
	}

	memberDeletePersonal := httptest.NewRecorder()
	server.ServeHTTP(memberDeletePersonal, authenticatedRequest(t, http.MethodDelete, "/v1/environment-variables/MY_API_KEY", memberToken))
	if memberDeletePersonal.Code != http.StatusNoContent {
		t.Fatalf("member delete personal variable: %d: %s", memberDeletePersonal.Code, memberDeletePersonal.Body.String())
	}

	adminList := httptest.NewRecorder()
	server.ServeHTTP(adminList, authenticatedRequest(t, http.MethodGet, "/v1/environment-variables", adminToken))
	decodeTestResponse(t, adminList, &listed)
	if len(listed.Variables) != 1 || listed.Variables[0].Name != "ORG_API_KEY" {
		t.Fatalf("admin variable list exposed another user's personal variables: %+v", listed.Variables)
	}
}

func TestEnvironmentVariableAPIRequiresMasterKey(t *testing.T) {
	t.Setenv(envvars.MasterKeyEnvironmentVariable, "")
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, 0, nil), nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/environment-variables", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected service unavailable, got %d: %s", response.Code, response.Body.String())
	}
}

func jsonRequest(t *testing.T, method string, path string, body string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return request
}
