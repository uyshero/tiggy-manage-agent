package tma

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestTypedAdministrationServices(t *testing.T) {
	expected := map[string]string{
		"GET /v2/auth/config":                                      `{"mode":"oidc","oidc":{"issuer":"https://identity.example","audience":"tma-api","client_id":"tma-cli","scopes":["openid","profile","email"],"device_authorization":true}}`,
		"GET /v2/auth/me":                                          `{"authenticated":true,"principal":{"subject":"user_1","workspace_id":"wksp/1","owner_id":"user_1","roles":["operator"],"auth_type":"jwt"}}`,
		"GET /v2/mcp-servers?workspace_id=wksp%2F1":                `{"servers":[]}`,
		"GET /v2/mcp-servers/runtime-status?workspace_id=wksp%2F1": `{"checked_at":"2026-07-15T00:00:00Z","states":[]}`,
		"POST /v2/mcp-servers":                                     mcpServerFixture("active"),
		"GET /v2/mcp-servers/mcps%2F1":                             mcpServerFixture("active"),
		"PATCH /v2/mcp-servers/mcps%2F1":                           mcpServerFixture("active"),
		"POST /v2/mcp-servers/mcps%2F1/enable":                     mcpServerFixture("active"),
		"POST /v2/mcp-servers/mcps%2F1/disable":                    mcpServerFixture("disabled"),
		"DELETE /v2/mcp-servers/mcps%2F1":                          mcpServerFixture("archived"),
		"POST /v2/mcp-servers/mcps%2F1/test":                       `{"server_id":"mcps/1","version":2,"result":{"identifier":"git","kind":"mcp","status":"online"}}`,
		"GET /v2/mcp-servers/mcps%2F1/versions":                    `{"versions":[{"id":"mcpsv_1","server_id":"mcps/1","version":1,"config":{"identifier":"git"},"checksum_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","created_at":"2026-07-15T00:00:00Z"}]}`,
		"POST /v2/mcp-servers/mcps%2F1/versions/1/restore":         `{"server":` + mcpServerFixture("active") + `,"source_version":1,"previous_version":2,"new_version":3}`,
		"GET /v2/operator-audit?action=mcp_registry.update&limit=25&principal_id=user%2F1&session_id=sesn%2F1&workspace_id=wksp%2F1": `{"audit_records":[]}`,
		"GET /v2/sessions/sesn%2F1/operator-audit":                               `{"audit_records":[]}`,
		"GET /v2/observability/security-audit/integrity-keys":                    `{"active_key_id":"key_1","historical_unidentified_blocking":0,"keys":[]}`,
		"POST /v2/observability/security-audit/replay?limit=50":                  `{"replayed":3}`,
		"GET /v2/environment-variables?workspace_id=wksp%2F1":                    `{"variables":[]}`,
		"PUT /v2/environment-variables/SERVICE_API_KEY?workspace_id=wksp%2F1":    `{"name":"SERVICE_API_KEY","configured":true,"created_at":"2026-07-15T00:00:00Z","updated_at":"2026-07-15T00:00:00Z"}`,
		"DELETE /v2/environment-variables/SERVICE_API_KEY?workspace_id=wksp%2F1": "",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.EscapedPath()
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		body, ok := expected[key]
		if !ok {
			t.Fatalf("unexpected administration request %s", key)
		}
		delete(expected, key)
		if key == "DELETE /v2/environment-variables/SERVICE_API_KEY?workspace_id=wksp%2F1" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(key, "POST /v2/mcp-servers") && key == "POST /v2/mcp-servers" {
			w.WriteHeader(http.StatusCreated)
		}
		fmt.Fprint(w, body)
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if configuration, err := client.Auth.Configuration(ctx); err != nil || configuration.Mode != "oidc" || configuration.OIDC == nil || configuration.OIDC.ClientID != "tma-cli" {
		t.Fatalf("auth configuration=%+v err=%v", configuration, err)
	}
	if state, err := client.Auth.Me(ctx); err != nil || !state.Authenticated || state.Principal == nil || state.Principal.Subject != "user_1" {
		t.Fatalf("auth state=%+v err=%v", state, err)
	}
	mcpQuery := MCPServerQuery{WorkspaceID: "wksp/1"}
	if _, err = client.MCP.List(ctx, mcpQuery); err != nil {
		t.Fatal(err)
	}
	if _, err = client.MCP.RuntimeStatus(ctx, mcpQuery); err != nil {
		t.Fatal(err)
	}
	if _, err = client.MCP.Create(ctx, CreateMCPServerRequest{Identifier: "git", Name: "Git", Config: MCPServerConfig{Identifier: "git", Command: "git-mcp"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.MCP.Get(ctx, "mcps/1"); err != nil {
		t.Fatal(err)
	}
	name := "Git MCP"
	if _, err = client.MCP.Update(ctx, "mcps/1", UpdateMCPServerRequest{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.MCP.SetEnabled(ctx, "mcps/1", true); err != nil {
		t.Fatal(err)
	}
	if _, err = client.MCP.SetEnabled(ctx, "mcps/1", false); err != nil {
		t.Fatal(err)
	}
	if _, err = client.MCP.Archive(ctx, "mcps/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.MCP.Test(ctx, "mcps/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.MCP.Versions(ctx, "mcps/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.MCP.RestoreVersion(ctx, "mcps/1", 1); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Audit.List(ctx, OperatorAuditQuery{WorkspaceID: "wksp/1", SessionID: "sesn/1", PrincipalID: "user/1", Action: "mcp_registry.update", Limit: 25}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Audit.ListSession(ctx, "sesn/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Audit.IntegrityKeys(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Audit.ReplayDeadLetters(ctx, 50); err != nil {
		t.Fatal(err)
	}
	environmentQuery := EnvironmentVariableQuery{WorkspaceID: "wksp/1"}
	if _, err = client.EnvironmentVariables.List(ctx, environmentQuery); err != nil {
		t.Fatal(err)
	}
	if _, err = client.EnvironmentVariables.Put(ctx, "SERVICE_API_KEY", environmentQuery, PutEnvironmentVariableRequest{Value: "secret"}); err != nil {
		t.Fatal(err)
	}
	if err = client.EnvironmentVariables.Delete(ctx, "SERVICE_API_KEY", environmentQuery); err != nil {
		t.Fatal(err)
	}
	if len(expected) != 0 {
		t.Fatalf("administration operations not called: %#v", expected)
	}
}

func TestAdministrationClientFieldsAreTyped(t *testing.T) {
	clientType := reflect.TypeOf(Client{})
	for fieldName, typeName := range map[string]string{
		"Auth": "AuthService", "MCP": "MCPService", "Audit": "AuditService", "EnvironmentVariables": "EnvironmentVariablesService",
	} {
		field, ok := clientType.FieldByName(fieldName)
		if !ok || field.Type.Kind() != reflect.Pointer || field.Type.Elem().Name() != typeName {
			t.Fatalf("Client.%s must be *%s, got %v", fieldName, typeName, field.Type)
		}
	}
}

func TestMCPConfigValueJSONUnion(t *testing.T) {
	config := MCPServerConfig{
		Identifier: "git",
		Env: map[string]MCPConfigValue{
			"EMPTY": MCPConfigLiteral(""),
			"TOKEN": MCPConfigEnvRef("GIT_TOKEN"),
		},
		Headers: map[string]MCPConfigValue{"Authorization": MCPConfigSecretRef("env:GIT_AUTH")},
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"EMPTY":""`, `"env_ref":"GIT_TOKEN"`, `"secret_ref":"env:GIT_AUTH"`} {
		if !bytes.Contains(encoded, []byte(expected)) {
			t.Fatalf("encoded MCP config is missing %s: %s", expected, encoded)
		}
	}
	var decoded MCPServerConfig
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Env["EMPTY"].Literal == nil || *decoded.Env["EMPTY"].Literal != "" || decoded.Env["TOKEN"].EnvRef != "GIT_TOKEN" || decoded.Headers["Authorization"].SecretRef != "env:GIT_AUTH" {
		t.Fatalf("unexpected decoded MCP config: %+v", decoded)
	}
	if _, err := json.Marshal(MCPConfigValue{}); err == nil {
		t.Fatal("empty MCP config value must not marshal")
	}
}

func mcpServerFixture(status string) string {
	return `{"id":"mcps/1","workspace_id":"wksp/1","identifier":"git","name":"Git","status":"` + status + `","current_version":2,"config":{"identifier":"git","command":"git-mcp"},"usage_count":0,"created_at":"2026-07-15T00:00:00Z","updated_at":"2026-07-15T00:00:00Z"}`
}
