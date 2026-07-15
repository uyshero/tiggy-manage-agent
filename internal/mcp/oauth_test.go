package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBearerAuthParamParsesResourceMetadata(t *testing.T) {
	header := `Bearer realm="mcp", error="invalid_token", resource_metadata="https://mcp.example.test/.well-known/oauth-protected-resource"`
	got := bearerAuthParam(header, "resource_metadata")
	if got != "https://mcp.example.test/.well-known/oauth-protected-resource" {
		t.Fatalf("unexpected resource_metadata: %q", got)
	}
}

func TestDiscoverOAuthMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			writeHTTPTestJSON(t, w, map[string]any{
				"resource":              serverResourceURL(r),
				"authorization_servers": []string{serverResourceURL(r) + "/issuer"},
				"scopes_supported":      []string{"mcp.read", "mcp.write"},
			})
		case "/issuer/.well-known/oauth-authorization-server":
			writeHTTPTestJSON(t, w, map[string]any{
				"issuer":                   serverResourceURL(r) + "/issuer",
				"authorization_endpoint":   serverResourceURL(r) + "/issuer/authorize",
				"token_endpoint":           serverResourceURL(r) + "/issuer/token",
				"registration_endpoint":    serverResourceURL(r) + "/issuer/register",
				"scopes_supported":         []string{"mcp.read", "mcp.write"},
				"response_types_supported": []string{"code"},
			})
		default:
			t.Fatalf("unexpected metadata path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	discovery, err := discoverOAuthMetadata(t.Context(), server.Client(), server.URL+"/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatalf("discover oauth metadata: %v", err)
	}
	if len(discovery.AuthorizationServerMetadata) != 1 {
		t.Fatalf("unexpected authorization server metadata: %#v", discovery)
	}
	info := discovery.AuthorizationServerMetadata[0]
	if info.AuthorizationEndpoint != server.URL+"/issuer/authorize" || info.TokenEndpoint != server.URL+"/issuer/token" {
		t.Fatalf("unexpected authorization endpoints: %#v", info)
	}
	if got := formatOAuthDiscovery(discovery); !strings.Contains(got, "authorization_endpoint="+server.URL+"/issuer/authorize") {
		t.Fatalf("unexpected formatted discovery: %q", got)
	}
}

func TestStreamableHTTPUnauthorizedDiscoversOAuthMetadata(t *testing.T) {
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mcp":
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+serverURL+`/.well-known/oauth-protected-resource"`)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("missing token"))
		case "/.well-known/oauth-protected-resource":
			w.Header().Set("Content-Type", "application/json")
			writeHTTPTestJSON(t, w, map[string]any{
				"resource":              serverURL + "/mcp",
				"authorization_servers": []string{serverURL + "/issuer"},
				"scopes_supported":      []string{"mcp.read"},
			})
		case "/issuer/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			writeHTTPTestJSON(t, w, map[string]any{
				"issuer":                 serverURL + "/issuer",
				"authorization_endpoint": serverURL + "/issuer/authorize",
				"token_endpoint":         serverURL + "/issuer/token",
				"scopes_supported":       []string{"mcp.read"},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	serverURL = server.URL
	defer server.Close()

	_, _, err := Client{Transport: TransportStreamableHTTP, URL: server.URL + "/mcp", HTTPClient: server.Client()}.ListTools(t.Context())
	if err == nil {
		t.Fatalf("expected unauthorized error")
	}
	text := err.Error()
	if !strings.Contains(text, "authorization_endpoint="+server.URL+"/issuer/authorize") || !strings.Contains(text, "token_endpoint="+server.URL+"/issuer/token") {
		t.Fatalf("expected oauth metadata in unauthorized error, got: %v", err)
	}
	if strings.Contains(text, "secret") {
		t.Fatalf("unauthorized error leaked a secret-looking value: %v", err)
	}
}

func TestStreamableHTTPOAuthTokenRequestUsesEgressPolicy(t *testing.T) {
	policy, err := NewEgressPolicy(EgressPolicyConfig{AllowHTTP: true})
	if err != nil {
		t.Fatalf("new egress policy: %v", err)
	}
	_, _, err = Client{
		Transport: TransportStreamableHTTP,
		URL:       "https://8.8.8.8/mcp",
		OAuth: &OAuthClientCredentials{
			TokenURL:     "http://169.254.169.254/token",
			ClientID:     "client-id",
			ClientSecret: "client-secret",
		},
		OAuthCache:   NewOAuthTokenCache(),
		EgressPolicy: policy,
	}.ListTools(t.Context())
	if err == nil || !IsEgressBlocked(err) {
		t.Fatalf("expected OAuth token endpoint egress rejection, got %v", err)
	}
	if strings.Contains(err.Error(), "169.254.169.254") {
		t.Fatalf("egress rejection leaked blocked OAuth URL: %v", err)
	}
	if summary := policy.Summary(); summary.BlockedTotal != 1 {
		t.Fatalf("expected one blocked OAuth request, got %+v", summary)
	}
}

func TestFetchOAuthClientCredentialsTokenClientSecretPost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected token method: %s", r.Method)
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Fatalf("unexpected Accept header: %q", r.Header.Get("Accept"))
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse token form: %v", err)
		}
		assertOAuthFormValue(t, r, "grant_type", "client_credentials")
		assertOAuthFormValue(t, r, "client_id", "client-id")
		assertOAuthFormValue(t, r, "client_secret", "client-secret")
		assertOAuthFormValue(t, r, "scope", "mcp.read mcp.write")
		assertOAuthFormValue(t, r, "audience", "https://mcp.example.test")
		assertOAuthFormValue(t, r, "resource", "https://mcp.example.test/mcp")
		w.Header().Set("Content-Type", "application/json")
		writeHTTPTestJSON(t, w, map[string]any{
			"access_token": "access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer server.Close()

	token, err := fetchOAuthClientCredentialsToken(t.Context(), server.Client(), &OAuthClientCredentials{
		TokenURL:     server.URL,
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		Scopes:       []string{"mcp.read", "mcp.write"},
		Audience:     "https://mcp.example.test",
		Resource:     "https://mcp.example.test/mcp",
	})
	if err != nil {
		t.Fatalf("fetch oauth token: %v", err)
	}
	if token.AccessToken != "access-token" || !strings.EqualFold(token.TokenType, "Bearer") {
		t.Fatalf("unexpected oauth token: %#v", token)
	}
}

func TestFetchOAuthClientCredentialsTokenClientSecretBasic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientID, clientSecret, ok := r.BasicAuth()
		if !ok || clientID != "client-id" || clientSecret != "client-secret" {
			t.Fatalf("unexpected basic auth: ok=%v clientID=%q clientSecret=%q", ok, clientID, clientSecret)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse token form: %v", err)
		}
		if r.Form.Get("client_id") != "" || r.Form.Get("client_secret") != "" {
			t.Fatalf("client_secret_basic should not send client credentials in form: %#v", r.Form)
		}
		assertOAuthFormValue(t, r, "grant_type", "client_credentials")
		w.Header().Set("Content-Type", "application/json")
		writeHTTPTestJSON(t, w, map[string]any{"access_token": "basic-token"})
	}))
	defer server.Close()

	token, err := fetchOAuthClientCredentialsToken(t.Context(), server.Client(), &OAuthClientCredentials{
		TokenURL:                server.URL,
		ClientID:                "client-id",
		ClientSecret:            "client-secret",
		TokenEndpointAuthMethod: "client_secret_basic",
	})
	if err != nil {
		t.Fatalf("fetch oauth token with basic auth: %v", err)
	}
	if token.AccessToken != "basic-token" {
		t.Fatalf("unexpected oauth token: %#v", token)
	}
}

func TestFetchOAuthRefreshTokenToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected token method: %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse token form: %v", err)
		}
		assertOAuthFormValue(t, r, "grant_type", "refresh_token")
		assertOAuthFormValue(t, r, "refresh_token", "refresh-token")
		assertOAuthFormValue(t, r, "client_id", "client-id")
		assertOAuthFormValue(t, r, "client_secret", "client-secret")
		w.Header().Set("Content-Type", "application/json")
		writeHTTPTestJSON(t, w, map[string]any{
			"access_token": "refreshed-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer server.Close()

	token, err := fetchOAuthToken(t.Context(), server.Client(), &OAuthClientCredentials{
		TokenURL:     server.URL,
		GrantType:    "refresh_token",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RefreshToken: "refresh-token",
	})
	if err != nil {
		t.Fatalf("fetch oauth refresh token: %v", err)
	}
	if token.AccessToken != "refreshed-token" {
		t.Fatalf("unexpected oauth refresh token: %#v", token)
	}
}

func TestFetchOAuthClientCredentialsTokenErrorDoesNotLeakBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_client","client_secret":"client-secret","access_token":"leaked-token"}`))
	}))
	defer server.Close()

	_, err := fetchOAuthClientCredentialsToken(t.Context(), server.Client(), &OAuthClientCredentials{
		TokenURL:     server.URL,
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	})
	if err == nil {
		t.Fatalf("expected oauth token error")
	}
	text := err.Error()
	if strings.Contains(text, "client-secret") || strings.Contains(text, "leaked-token") || strings.Contains(text, "invalid_client") {
		t.Fatalf("oauth token error leaked response body: %v", err)
	}
}

func TestFetchOAuthClientCredentialsTokenRejectsUnsupportedTokenType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "mac-token",
			"token_type":   "mac",
		})
	}))
	defer server.Close()

	_, err := fetchOAuthClientCredentialsToken(t.Context(), server.Client(), &OAuthClientCredentials{
		TokenURL:     server.URL,
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	})
	if err == nil {
		t.Fatalf("expected unsupported token_type error")
	}
	if !strings.Contains(err.Error(), `token_type "mac" is not supported`) {
		t.Fatalf("unexpected unsupported token_type error: %v", err)
	}
	if strings.Contains(err.Error(), "mac-token") {
		t.Fatalf("unsupported token_type error leaked access token: %v", err)
	}
}

func TestOAuthTokenCacheReusesUntilRefreshWindow(t *testing.T) {
	var tokenRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenRequests++
		w.Header().Set("Content-Type", "application/json")
		writeHTTPTestJSON(t, w, map[string]any{
			"access_token": "cached-token",
			"token_type":   "Bearer",
			"expires_in":   120,
		})
	}))
	defer server.Close()

	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	cache := &OAuthTokenCache{now: func() time.Time { return now }}
	credentials := &OAuthClientCredentials{
		TokenURL:     server.URL,
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	}

	first, err := cache.ClientCredentialsToken(t.Context(), server.Client(), credentials)
	if err != nil {
		t.Fatalf("fetch first cached oauth token: %v", err)
	}
	now = now.Add(89 * time.Second)
	second, err := cache.ClientCredentialsToken(t.Context(), server.Client(), credentials)
	if err != nil {
		t.Fatalf("fetch second cached oauth token: %v", err)
	}
	if first.AccessToken != second.AccessToken || tokenRequests != 1 {
		t.Fatalf("expected cached token reuse, first=%#v second=%#v tokenRequests=%d", first, second, tokenRequests)
	}

	now = now.Add(2 * time.Second)
	if _, err := cache.ClientCredentialsToken(t.Context(), server.Client(), credentials); err != nil {
		t.Fatalf("refresh cached oauth token: %v", err)
	}
	if tokenRequests != 2 {
		t.Fatalf("expected token refresh inside refresh window, got %d requests", tokenRequests)
	}
}

func TestOAuthTokenCacheReusesRefreshTokenGrant(t *testing.T) {
	var tokenRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenRequests++
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse token form: %v", err)
		}
		assertOAuthFormValue(t, r, "grant_type", "refresh_token")
		assertOAuthFormValue(t, r, "refresh_token", "refresh-token")
		w.Header().Set("Content-Type", "application/json")
		writeHTTPTestJSON(t, w, map[string]any{
			"access_token": "cached-refresh-token",
			"token_type":   "Bearer",
			"expires_in":   120,
		})
	}))
	defer server.Close()

	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	cache := &OAuthTokenCache{now: func() time.Time { return now }}
	credentials := &OAuthClientCredentials{
		TokenURL:     server.URL,
		GrantType:    "refresh_token",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RefreshToken: "refresh-token",
	}

	if _, err := cache.Token(t.Context(), server.Client(), credentials); err != nil {
		t.Fatalf("fetch first cached refresh token: %v", err)
	}
	now = now.Add(89 * time.Second)
	if _, err := cache.Token(t.Context(), server.Client(), credentials); err != nil {
		t.Fatalf("fetch second cached refresh token: %v", err)
	}
	if tokenRequests != 1 {
		t.Fatalf("expected refresh token cache reuse, got %d requests", tokenRequests)
	}
}

func TestOAuthTokenCacheDoesNotCacheTokenWithoutExpiry(t *testing.T) {
	var tokenRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenRequests++
		w.Header().Set("Content-Type", "application/json")
		writeHTTPTestJSON(t, w, map[string]any{
			"access_token": "non-expiring-token",
			"token_type":   "Bearer",
		})
	}))
	defer server.Close()

	cache := NewOAuthTokenCache()
	credentials := &OAuthClientCredentials{
		TokenURL:     server.URL,
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	}
	if _, err := cache.Token(t.Context(), server.Client(), credentials); err != nil {
		t.Fatalf("fetch first non-expiring oauth token: %v", err)
	}
	if _, err := cache.Token(t.Context(), server.Client(), credentials); err != nil {
		t.Fatalf("fetch second non-expiring oauth token: %v", err)
	}
	if tokenRequests != 2 {
		t.Fatalf("expected tokens without expires_in to skip cache, got %d requests", tokenRequests)
	}
}

func serverResourceURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func assertOAuthFormValue(t *testing.T, r *http.Request, key string, want string) {
	t.Helper()
	if got := r.Form.Get(key); got != want {
		t.Fatalf("unexpected oauth form value %s: got %q want %q; form=%#v", key, got, want, r.Form)
	}
}
