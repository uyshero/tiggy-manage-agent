package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestParseConfigEnvRefs(t *testing.T) {
	config, err := ParseConfig(json.RawMessage(`{
		"mcpServers": {
			"fetch": {
				"command": "uvx",
				"args": ["mcp-server-fetch"],
				"env": {
					"FETCH_USER_AGENT": "tiggy-manage-agent",
					"API_TOKEN": {"env_ref": "TMA_MCP_FETCH_TOKEN"},
					"LEGACY_TOKEN": {"secret_ref": "env:TMA_MCP_LEGACY_TOKEN"}
				}
			}
		}
	}`))
	if err != nil {
		t.Fatalf("parse config with env refs: %v", err)
	}
	if len(config.Servers) != 1 {
		t.Fatalf("expected one server, got %#v", config.Servers)
	}
	if config.Servers[0].StdioFraming != StdioFramingContentLength {
		t.Fatalf("omitted framing must preserve legacy MCP configs: %#v", config.Servers[0])
	}
	env := config.Servers[0].Env
	if !env["FETCH_USER_AGENT"].Literal || env["FETCH_USER_AGENT"].Value != "tiggy-manage-agent" {
		t.Fatalf("unexpected literal env value: %#v", env["FETCH_USER_AGENT"])
	}
	if env["API_TOKEN"].EnvRef != "TMA_MCP_FETCH_TOKEN" {
		t.Fatalf("unexpected env_ref: %#v", env["API_TOKEN"])
	}
	if env["LEGACY_TOKEN"].SecretRef != "env:TMA_MCP_LEGACY_TOKEN" {
		t.Fatalf("unexpected secret_ref: %#v", env["LEGACY_TOKEN"])
	}
}

func TestParseConfigValidatesStdioFraming(t *testing.T) {
	config, err := ParseConfig(json.RawMessage(`{"servers":[{"identifier":"legacy","command":"fixture","stdio_framing":"CONTENT_LENGTH"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if config.Servers[0].StdioFraming != StdioFramingContentLength {
		t.Fatalf("unexpected legacy framing: %#v", config.Servers[0])
	}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"servers":[{"identifier":"bad","command":"fixture","stdio_framing":"unknown"}]}`),
		json.RawMessage(`{"servers":[{"identifier":"remote","transport":"streamable_http","url":"https://mcp.example.test","stdio_framing":"json_lines"}]}`),
	} {
		if _, err := ParseConfig(raw); err == nil || !strings.Contains(err.Error(), "stdio_framing") {
			t.Fatalf("expected stdio framing validation error for %s, got %v", raw, err)
		}
	}
}

func TestParseConfigStreamableHTTP(t *testing.T) {
	config, err := ParseConfig(json.RawMessage(`{
		"mcpServers": {
			"remote": {
				"transport": "streamable_http",
				"url": "https://mcp.example.test/mcp",
				"listen": true,
				"roots": [
					{"uri": "file:///workspace/project", "name": "Project"},
					{"path": "/tmp/mcp-root", "name": "Temp Root"}
				],
				"sampling": {
					"enabled": true
				},
					"elicitation": {
						"enabled": true
					},
					"logging": {
						"level": "WARNING"
					},
				"expose": {
					"resources": true,
					"prompts": true
				},
				"headers": {
					"Authorization": {"env_ref": "TMA_MCP_REMOTE_AUTH"}
				}
			}
		}
	}`))
	if err != nil {
		t.Fatalf("parse streamable_http config: %v", err)
	}
	if len(config.Servers) != 1 {
		t.Fatalf("expected one server, got %#v", config.Servers)
	}
	server := config.Servers[0]
	if server.Transport != TransportStreamableHTTP || server.URL != "https://mcp.example.test/mcp" || !server.Listen {
		t.Fatalf("unexpected streamable_http server: %#v", server)
	}
	if server.Headers["Authorization"].EnvRef != "TMA_MCP_REMOTE_AUTH" {
		t.Fatalf("unexpected header env_ref: %#v", server.Headers)
	}
	if len(server.Roots) != 2 || server.Roots[0].URI != "file:///workspace/project" || server.Roots[0].Name != "Project" {
		t.Fatalf("unexpected streamable_http roots: %#v", server.Roots)
	}
	if !strings.HasPrefix(server.Roots[1].URI, "file:///") || server.Roots[1].Name != "Temp Root" {
		t.Fatalf("unexpected path-derived root: %#v", server.Roots[1])
	}
	if server.Sampling == nil || !server.Sampling.Enabled {
		t.Fatalf("unexpected sampling config: %#v", server.Sampling)
	}
	if server.Elicitation == nil || !server.Elicitation.Enabled {
		t.Fatalf("unexpected elicitation config: %#v", server.Elicitation)
	}
	if server.Logging == nil || server.Logging.Level != "warning" {
		t.Fatalf("unexpected logging config: %#v", server.Logging)
	}
	if !server.Expose.Resources || !server.Expose.Prompts {
		t.Fatalf("unexpected expose config: %#v", server.Expose)
	}
}

func TestParseConfigPreservesVersionedBindings(t *testing.T) {
	raw := json.RawMessage(`{"bindings":[{"server_id":"mcps_000001","version":3,"identifier":"Team Search"}],"servers":[{"identifier":"local","command":"fixture"}]}`)
	config, err := ParseConfig(raw)
	if err != nil {
		t.Fatalf("parse config bindings: %v", err)
	}
	if len(config.Bindings) != 1 || config.Bindings[0].ServerID != "mcps_000001" || config.Bindings[0].Version != 3 || config.Bindings[0].Identifier != "team_search" {
		t.Fatalf("unexpected bindings: %#v", config.Bindings)
	}
	canonical, err := CanonicalJSON(raw)
	if err != nil {
		t.Fatalf("canonicalize bindings: %v", err)
	}
	if !strings.Contains(string(canonical), `"server_id":"mcps_000001"`) || !strings.Contains(string(canonical), `"identifier":"team_search"`) {
		t.Fatalf("canonical bindings missing: %s", canonical)
	}
}

func TestParseConfigAcceptsEmptyEnvelopeCollections(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"servers":[]}`),
		json.RawMessage(`{"mcpServers":{}}`),
		json.RawMessage(`{"bindings":[]}`),
	} {
		config, err := ParseConfig(raw)
		if err != nil {
			t.Fatalf("parse empty MCP envelope %s: %v", raw, err)
		}
		if len(config.Servers) != 0 || len(config.Bindings) != 0 {
			t.Fatalf("expected empty MCP config for %s, got %+v", raw, config)
		}
	}
}

func TestParseConfigRejectsUnsupportedLoggingLevel(t *testing.T) {
	_, err := ParseConfig(json.RawMessage(`{
		"servers": [{
			"identifier": "local",
			"command": "mcp-server",
			"logging": {"level": "verbose"}
		}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "logging level") {
		t.Fatalf("expected unsupported logging level error, got %v", err)
	}
}

func TestParseConfigStreamableHTTPOAuth(t *testing.T) {
	config, err := ParseConfig(json.RawMessage(`{
		"servers": [{
			"identifier": "remote",
			"transport": "streamable_http",
			"url": "https://mcp.example.test/mcp",
			"oauth": {
				"grant_type": "client_credentials",
				"token_url": "https://auth.example.test/oauth/token",
				"client_id": {"env_ref": "TMA_MCP_CLIENT_ID"},
				"client_secret": {"secret_ref": "env:TMA_MCP_CLIENT_SECRET"},
				"scopes": ["mcp.read", "mcp.write", "mcp.read"],
				"audience": "https://mcp.example.test",
				"resource": "https://mcp.example.test/mcp",
				"token_endpoint_auth_method": "client_secret_basic"
			}
		}]
	}`))
	if err != nil {
		t.Fatalf("parse streamable_http oauth config: %v", err)
	}
	server := config.Servers[0]
	if server.OAuth == nil {
		t.Fatalf("expected oauth config")
	}
	if server.OAuth.GrantType != "client_credentials" || server.OAuth.TokenURL != "https://auth.example.test/oauth/token" {
		t.Fatalf("unexpected oauth config: %#v", server.OAuth)
	}
	if server.OAuth.ClientID.EnvRef != "TMA_MCP_CLIENT_ID" || server.OAuth.ClientSecret.SecretRef != "env:TMA_MCP_CLIENT_SECRET" {
		t.Fatalf("unexpected oauth secret refs: %#v", server.OAuth)
	}
	if strings.Join(server.OAuth.Scopes, ",") != "mcp.read,mcp.write" {
		t.Fatalf("unexpected oauth scopes: %#v", server.OAuth.Scopes)
	}
	credentials, err := ResolveOAuthClientCredentialsWithLookup(server, func(key string) (string, bool) {
		values := map[string]string{
			"TMA_MCP_CLIENT_ID":     "client-id",
			"TMA_MCP_CLIENT_SECRET": "client-secret",
		}
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("resolve oauth credentials: %v", err)
	}
	if credentials.ClientID != "client-id" || credentials.ClientSecret != "client-secret" || credentials.TokenEndpointAuthMethod != "client_secret_basic" {
		t.Fatalf("unexpected resolved oauth credentials: %#v", credentials)
	}
}

func TestParseConfigStreamableHTTPRefreshTokenOAuth(t *testing.T) {
	config, err := ParseConfig(json.RawMessage(`{
		"servers": [{
			"identifier": "remote",
			"transport": "streamable_http",
			"url": "https://mcp.example.test/mcp",
			"oauth": {
				"grant_type": "refresh_token",
				"token_url": "https://auth.example.test/oauth/token",
				"client_id": {"env_ref": "TMA_MCP_CLIENT_ID"},
				"client_secret": {"secret_ref": "env:TMA_MCP_CLIENT_SECRET"},
				"refresh_token": {"env_ref": "TMA_MCP_REFRESH_TOKEN"},
				"scopes": ["mcp.read"],
				"token_endpoint_auth_method": "client_secret_post"
			}
		}]
	}`))
	if err != nil {
		t.Fatalf("parse refresh_token oauth config: %v", err)
	}
	server := config.Servers[0]
	if server.OAuth == nil || server.OAuth.GrantType != "refresh_token" {
		t.Fatalf("unexpected refresh_token oauth config: %#v", server.OAuth)
	}
	if server.OAuth.RefreshToken == nil || server.OAuth.RefreshToken.EnvRef != "TMA_MCP_REFRESH_TOKEN" {
		t.Fatalf("unexpected refresh token ref: %#v", server.OAuth)
	}
	credentials, err := ResolveOAuthClientCredentialsWithLookup(server, func(key string) (string, bool) {
		values := map[string]string{
			"TMA_MCP_CLIENT_ID":     "client-id",
			"TMA_MCP_CLIENT_SECRET": "client-secret",
			"TMA_MCP_REFRESH_TOKEN": "refresh-token",
		}
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("resolve refresh_token oauth credentials: %v", err)
	}
	if credentials.GrantType != "refresh_token" || credentials.RefreshToken != "refresh-token" {
		t.Fatalf("unexpected resolved refresh_token oauth credentials: %#v", credentials)
	}
}

func TestParseConfigStreamableHTTPRequiresURL(t *testing.T) {
	_, err := ParseConfig(json.RawMessage(`{
		"servers": [{
			"identifier": "remote",
			"transport": "streamable_http"
		}]
	}`))
	if err == nil {
		t.Fatalf("expected missing streamable_http url error")
	}
	if !strings.Contains(err.Error(), "url is required") {
		t.Fatalf("unexpected streamable_http url error: %v", err)
	}
}

func TestParseConfigStreamableHTTPRejectsInvalidURL(t *testing.T) {
	_, err := ParseConfig(json.RawMessage(`{
		"servers": [{
			"identifier": "remote",
			"transport": "streamable_http",
			"url": "ftp://mcp.example.test/mcp"
		}]
	}`))
	if err == nil {
		t.Fatalf("expected invalid streamable_http url error")
	}
	if !strings.Contains(err.Error(), "scheme must be http or https") {
		t.Fatalf("unexpected invalid streamable_http url error: %v", err)
	}
}

func TestParseConfigRejectsOAuthWithAuthorizationHeader(t *testing.T) {
	_, err := ParseConfig(json.RawMessage(`{
		"servers": [{
			"identifier": "remote",
			"transport": "streamable_http",
			"url": "https://mcp.example.test/mcp",
			"headers": {
				"Authorization": {"env_ref": "TMA_MCP_AUTH"}
			},
			"oauth": {
				"token_url": "https://auth.example.test/oauth/token",
				"client_id": {"env_ref": "TMA_MCP_CLIENT_ID"},
				"client_secret": {"env_ref": "TMA_MCP_CLIENT_SECRET"}
			}
		}]
	}`))
	if err == nil {
		t.Fatalf("expected oauth Authorization conflict error")
	}
	if !strings.Contains(err.Error(), "cannot set both oauth and Authorization header") {
		t.Fatalf("unexpected oauth Authorization conflict error: %v", err)
	}
}

func TestParseConfigRejectsOAuthOnStdio(t *testing.T) {
	_, err := ParseConfig(json.RawMessage(`{
		"servers": [{
			"identifier": "local",
			"command": "mcp-server",
			"oauth": {
				"token_url": "https://auth.example.test/oauth/token",
				"client_id": {"env_ref": "TMA_MCP_CLIENT_ID"},
				"client_secret": {"env_ref": "TMA_MCP_CLIENT_SECRET"}
			}
		}]
	}`))
	if err == nil {
		t.Fatalf("expected stdio oauth error")
	}
	if !strings.Contains(err.Error(), "oauth is only supported for streamable_http") {
		t.Fatalf("unexpected stdio oauth error: %v", err)
	}
}

func TestParseConfigRejectsUnsupportedOAuthOptions(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "literal_secret",
			raw: `{
				"token_url": "https://auth.example.test/oauth/token",
				"client_id": {"env_ref": "TMA_MCP_CLIENT_ID"},
				"client_secret": "literal-secret"
			}`,
			want: `client_secret must use env_ref or secret_ref`,
		},
		{
			name: "grant",
			raw: `{
				"grant_type": "authorization_code",
				"token_url": "https://auth.example.test/oauth/token",
				"client_id": {"env_ref": "TMA_MCP_CLIENT_ID"},
				"client_secret": {"env_ref": "TMA_MCP_CLIENT_SECRET"}
			}`,
			want: `grant_type "authorization_code" is not supported`,
		},
		{
			name: "auth_method",
			raw: `{
				"token_url": "https://auth.example.test/oauth/token",
				"client_id": {"env_ref": "TMA_MCP_CLIENT_ID"},
				"client_secret": {"env_ref": "TMA_MCP_CLIENT_SECRET"},
				"token_endpoint_auth_method": "private_key_jwt"
			}`,
			want: `token_endpoint_auth_method "private_key_jwt" is not supported`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseConfig(json.RawMessage(`{
				"servers": [{
					"identifier": "remote",
					"transport": "streamable_http",
					"url": "https://mcp.example.test/mcp",
					"oauth": ` + tt.raw + `
				}]
			}`))
			if err == nil {
				t.Fatalf("expected unsupported oauth option error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("unexpected unsupported oauth option error: %v", err)
			}
		})
	}
}

func TestCanonicalJSONKeepsEnvRefsNotSecretValues(t *testing.T) {
	normalized, err := CanonicalJSON(json.RawMessage(`{
		"servers": [{
			"identifier": "fetch",
			"command": "uvx",
			"env": {
				"API_TOKEN": {"env_ref": "TMA_MCP_FETCH_TOKEN"}
			}
		}]
	}`))
	if err != nil {
		t.Fatalf("canonicalize config with env ref: %v", err)
	}
	raw := string(normalized)
	if !strings.Contains(raw, `"env_ref":"TMA_MCP_FETCH_TOKEN"`) {
		t.Fatalf("expected env_ref in canonical config: %s", raw)
	}
	if strings.Contains(raw, "secret-value") {
		t.Fatalf("canonical config leaked a secret value: %s", raw)
	}
}

func TestParseConfigNormalizesRuntimePolicy(t *testing.T) {
	config, err := ParseConfig(json.RawMessage(`{"servers":[{"identifier":"protected","command":"fixture","runtime":{"timeout_seconds":12,"max_concurrency":3,"failure_threshold":4,"cooldown_seconds":45}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	policy := config.Servers[0].Runtime
	if policy == nil || policy.TimeoutSeconds != 12 || policy.MaxConcurrency != 3 || policy.FailureThreshold != 4 || policy.CooldownSeconds != 45 {
		t.Fatalf("unexpected runtime policy: %#v", policy)
	}
	effective := policy.Effective()
	if effective.Timeout != 12*time.Second || effective.MaxConcurrency != 3 || effective.FailureThreshold != 4 || effective.Cooldown != 45*time.Second {
		t.Fatalf("unexpected effective runtime policy: %#v", effective)
	}
}

func TestParseConfigRejectsInvalidRuntimePolicy(t *testing.T) {
	_, err := ParseConfig(json.RawMessage(`{"servers":[{"identifier":"protected","command":"fixture","runtime":{"max_concurrency":65}}]}`))
	if err == nil || !strings.Contains(err.Error(), "runtime.max_concurrency") {
		t.Fatalf("expected runtime policy validation error, got %v", err)
	}
}

func TestResolveEnvWithLookup(t *testing.T) {
	server := ServerConfig{
		Identifier: "fetch",
		Env: map[string]EnvValue{
			"FETCH_USER_AGENT": LiteralEnv("tiggy-manage-agent"),
			"API_TOKEN":        EnvRef("TMA_MCP_FETCH_TOKEN"),
			"LEGACY_TOKEN":     SecretRef("env:TMA_MCP_LEGACY_TOKEN"),
		},
	}
	resolved, err := ResolveEnvWithLookup(server, func(key string) (string, bool) {
		values := map[string]string{
			"TMA_MCP_FETCH_TOKEN":  "fetch-secret",
			"TMA_MCP_LEGACY_TOKEN": "legacy-secret",
		}
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("resolve env refs: %v", err)
	}
	if resolved["FETCH_USER_AGENT"] != "tiggy-manage-agent" || resolved["API_TOKEN"] != "fetch-secret" || resolved["LEGACY_TOKEN"] != "legacy-secret" {
		t.Fatalf("unexpected resolved env: %#v", resolved)
	}
}

func TestResolveHeadersWithLookup(t *testing.T) {
	server := ServerConfig{
		Identifier: "remote",
		Headers: map[string]EnvValue{
			"Authorization": EnvRef("TMA_MCP_REMOTE_AUTH"),
		},
	}
	resolved, err := ResolveHeadersWithLookup(server, func(key string) (string, bool) {
		if key == "TMA_MCP_REMOTE_AUTH" {
			return "Bearer remote-secret", true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("resolve header refs: %v", err)
	}
	if resolved["Authorization"] != "Bearer remote-secret" {
		t.Fatalf("unexpected resolved headers: %#v", resolved)
	}
}

func TestParseConfigRejectsUnsupportedSecretRef(t *testing.T) {
	_, err := ParseConfig(json.RawMessage(`{
		"servers": [{
			"identifier": "fetch",
			"command": "uvx",
			"env": {
				"API_TOKEN": {"secret_ref": "vault:prod/fetch-token"}
			}
		}]
	}`))
	if err == nil {
		t.Fatalf("expected unsupported secret_ref error")
	}
	if !strings.Contains(err.Error(), "use env:NAME") {
		t.Fatalf("unexpected unsupported secret_ref error: %v", err)
	}
}
