package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const oauthTokenRefreshSkew = 30 * time.Second

type OAuthDiscovery struct {
	ProtectedResourceMetadataURL string                      `json:"protected_resource_metadata_url,omitempty"`
	Resource                     string                      `json:"resource,omitempty"`
	AuthorizationServers         []string                    `json:"authorization_servers,omitempty"`
	AuthorizationServerMetadata  []AuthorizationServerInfo   `json:"authorization_server_metadata,omitempty"`
	ScopesSupported              []string                    `json:"scopes_supported,omitempty"`
	RawProtectedResource         ProtectedResourceMetadata   `json:"-"`
	RawAuthorizationServers      []AuthorizationServerConfig `json:"-"`
}

type ProtectedResourceMetadata struct {
	Resource             string   `json:"resource,omitempty"`
	AuthorizationServers []string `json:"authorization_servers,omitempty"`
	ScopesSupported      []string `json:"scopes_supported,omitempty"`
}

type AuthorizationServerInfo struct {
	Issuer                string   `json:"issuer,omitempty"`
	MetadataURL           string   `json:"metadata_url,omitempty"`
	AuthorizationEndpoint string   `json:"authorization_endpoint,omitempty"`
	TokenEndpoint         string   `json:"token_endpoint,omitempty"`
	RegistrationEndpoint  string   `json:"registration_endpoint,omitempty"`
	ScopesSupported       []string `json:"scopes_supported,omitempty"`
}

type AuthorizationServerConfig struct {
	Issuer                             string   `json:"issuer,omitempty"`
	AuthorizationEndpoint              string   `json:"authorization_endpoint,omitempty"`
	TokenEndpoint                      string   `json:"token_endpoint,omitempty"`
	RegistrationEndpoint               string   `json:"registration_endpoint,omitempty"`
	ScopesSupported                    []string `json:"scopes_supported,omitempty"`
	ResponseTypesSupported             []string `json:"response_types_supported,omitempty"`
	CodeChallengeMethodsSupported      []string `json:"code_challenge_methods_supported,omitempty"`
	TokenEndpointAuthMethodsSupported  []string `json:"token_endpoint_auth_methods_supported,omitempty"`
	GrantTypesSupported                []string `json:"grant_types_supported,omitempty"`
	RevocationEndpoint                 string   `json:"revocation_endpoint,omitempty"`
	IntrospectionEndpoint              string   `json:"introspection_endpoint,omitempty"`
	DeviceAuthorizationEndpoint        string   `json:"device_authorization_endpoint,omitempty"`
	PushedAuthorizationRequestEndpoint string   `json:"pushed_authorization_request_endpoint,omitempty"`
}

type OAuthToken struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type,omitempty"`
	ExpiresIn   int64  `json:"expires_in,omitempty"`
	Scope       string `json:"scope,omitempty"`
}

type OAuthTokenCache struct {
	mu        sync.Mutex
	token     OAuthToken
	expiresAt time.Time
	key       string
	now       func() time.Time
}

func NewOAuthTokenCache() *OAuthTokenCache {
	return &OAuthTokenCache{}
}

func (c *OAuthTokenCache) ClientCredentialsToken(ctx context.Context, client *http.Client, credentials *OAuthClientCredentials) (OAuthToken, error) {
	return c.Token(ctx, client, credentials)
}

func (c *OAuthTokenCache) Token(ctx context.Context, client *http.Client, credentials *OAuthClientCredentials) (OAuthToken, error) {
	if c == nil {
		return fetchOAuthToken(ctx, client, credentials)
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.currentTime()
	key := oauthTokenCacheKey(credentials)
	if c.key == key && c.cachedTokenValid(now) {
		return c.token, nil
	}
	token, err := fetchOAuthToken(ctx, client, credentials)
	if err != nil {
		return OAuthToken{}, err
	}
	c.store(now, key, token)
	return token, nil
}

func (c *OAuthTokenCache) currentTime() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *OAuthTokenCache) cachedTokenValid(now time.Time) bool {
	return strings.TrimSpace(c.token.AccessToken) != "" && !c.expiresAt.IsZero() && now.Before(c.expiresAt)
}

func (c *OAuthTokenCache) store(now time.Time, key string, token OAuthToken) {
	c.token = OAuthToken{}
	c.expiresAt = time.Time{}
	c.key = ""
	if token.ExpiresIn <= 0 {
		return
	}
	c.token = token
	c.expiresAt = oauthTokenUsableUntil(now, time.Duration(token.ExpiresIn)*time.Second)
	c.key = key
}

func oauthTokenUsableUntil(now time.Time, lifetime time.Duration) time.Time {
	if lifetime <= 0 {
		return time.Time{}
	}
	skew := oauthTokenRefreshSkew
	if lifetime <= 2*skew {
		skew = lifetime / 10
	}
	return now.Add(lifetime - skew)
}

func oauthTokenCacheKey(credentials *OAuthClientCredentials) string {
	if credentials == nil {
		return ""
	}
	return strings.Join([]string{
		strings.TrimSpace(strings.ToLower(credentials.GrantType)),
		strings.TrimSpace(credentials.TokenURL),
		strings.TrimSpace(credentials.ClientID),
		strings.TrimSpace(credentials.RefreshToken),
		strings.Join(cleanStrings(credentials.Scopes), " "),
		strings.TrimSpace(credentials.Audience),
		strings.TrimSpace(credentials.Resource),
		strings.TrimSpace(strings.ToLower(credentials.TokenEndpointAuthMethod)),
	}, "\x00")
}

func fetchOAuthClientCredentialsToken(ctx context.Context, client *http.Client, credentials *OAuthClientCredentials) (OAuthToken, error) {
	return fetchOAuthToken(ctx, client, credentials)
}

func fetchOAuthToken(ctx context.Context, client *http.Client, credentials *OAuthClientCredentials) (OAuthToken, error) {
	if credentials == nil {
		return OAuthToken{}, fmt.Errorf("oauth token credentials are required")
	}
	if client == nil {
		client = http.DefaultClient
	}
	if err := validateHTTPURL(credentials.TokenURL); err != nil {
		return OAuthToken{}, fmt.Errorf("invalid oauth token_url: %w", err)
	}
	grantType := strings.TrimSpace(strings.ToLower(credentials.GrantType))
	if grantType == "" {
		grantType = "client_credentials"
	}
	form := url.Values{}
	form.Set("grant_type", grantType)
	if len(credentials.Scopes) > 0 {
		form.Set("scope", strings.Join(cleanStrings(credentials.Scopes), " "))
	}
	if strings.TrimSpace(credentials.Audience) != "" {
		form.Set("audience", strings.TrimSpace(credentials.Audience))
	}
	if strings.TrimSpace(credentials.Resource) != "" {
		form.Set("resource", strings.TrimSpace(credentials.Resource))
	}
	authMethod := strings.TrimSpace(strings.ToLower(credentials.TokenEndpointAuthMethod))
	if authMethod == "" {
		authMethod = "client_secret_post"
	}
	switch authMethod {
	case "client_secret_post":
		form.Set("client_id", credentials.ClientID)
		form.Set("client_secret", credentials.ClientSecret)
	case "client_secret_basic":
		// Client authentication is sent via HTTP Basic below.
	default:
		return OAuthToken{}, fmt.Errorf("oauth token_endpoint_auth_method %q is not supported", credentials.TokenEndpointAuthMethod)
	}
	switch grantType {
	case "client_credentials":
		// No additional fields.
	case "refresh_token":
		if strings.TrimSpace(credentials.RefreshToken) == "" {
			return OAuthToken{}, fmt.Errorf("oauth refresh_token is required")
		}
		form.Set("refresh_token", credentials.RefreshToken)
	default:
		return OAuthToken{}, fmt.Errorf("oauth grant_type %q is not supported", credentials.GrantType)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, credentials.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return OAuthToken{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	if authMethod == "client_secret_basic" {
		request.SetBasicAuth(credentials.ClientID, credentials.ClientSecret)
	}
	response, err := client.Do(request)
	if err != nil {
		return OAuthToken{}, fmt.Errorf("request oauth token: %w", sanitizeEgressError(err))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return OAuthToken{}, fmt.Errorf("oauth token endpoint status %d", response.StatusCode)
	}
	contentType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if contentType != "" && strings.ToLower(contentType) != "application/json" {
		return OAuthToken{}, fmt.Errorf("oauth token endpoint unexpected content type %q", response.Header.Get("Content-Type"))
	}
	var token OAuthToken
	if err := json.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(&token); err != nil {
		return OAuthToken{}, fmt.Errorf("decode oauth token response: %w", err)
	}
	token.AccessToken = strings.TrimSpace(token.AccessToken)
	token.TokenType = strings.TrimSpace(token.TokenType)
	if token.AccessToken == "" {
		return OAuthToken{}, fmt.Errorf("oauth token response missing access_token")
	}
	if token.TokenType != "" && !strings.EqualFold(token.TokenType, "Bearer") {
		return OAuthToken{}, fmt.Errorf("oauth token response token_type %q is not supported", token.TokenType)
	}
	return token, nil
}

func discoverOAuthFromUnauthorized(ctx context.Context, client *http.Client, endpoint string, response *http.Response) (OAuthDiscovery, error) {
	metadataURL := protectedResourceMetadataURL(endpoint, response.Header.Values("WWW-Authenticate"))
	if metadataURL == "" {
		return OAuthDiscovery{}, fmt.Errorf("oauth protected resource metadata url not advertised")
	}
	return discoverOAuthMetadata(ctx, client, metadataURL)
}

func discoverOAuthMetadata(ctx context.Context, client *http.Client, metadataURL string) (OAuthDiscovery, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if err := validateHTTPURL(metadataURL); err != nil {
		return OAuthDiscovery{}, fmt.Errorf("invalid protected resource metadata url: %w", err)
	}
	var resource ProtectedResourceMetadata
	if err := getOAuthJSON(ctx, client, metadataURL, &resource); err != nil {
		return OAuthDiscovery{}, fmt.Errorf("fetch protected resource metadata: %w", err)
	}
	discovery := OAuthDiscovery{
		ProtectedResourceMetadataURL: metadataURL,
		Resource:                     strings.TrimSpace(resource.Resource),
		AuthorizationServers:         cleanStrings(resource.AuthorizationServers),
		ScopesSupported:              cleanStrings(resource.ScopesSupported),
		RawProtectedResource:         resource,
	}
	for _, issuer := range discovery.AuthorizationServers {
		metadataEndpoint, err := authorizationServerMetadataURL(issuer)
		if err != nil {
			return OAuthDiscovery{}, err
		}
		var config AuthorizationServerConfig
		if err := getOAuthJSON(ctx, client, metadataEndpoint, &config); err != nil {
			return OAuthDiscovery{}, fmt.Errorf("fetch authorization server metadata %q: %w", issuer, err)
		}
		discovery.RawAuthorizationServers = append(discovery.RawAuthorizationServers, config)
		discovery.AuthorizationServerMetadata = append(discovery.AuthorizationServerMetadata, AuthorizationServerInfo{
			Issuer:                strings.TrimSpace(config.Issuer),
			MetadataURL:           metadataEndpoint,
			AuthorizationEndpoint: strings.TrimSpace(config.AuthorizationEndpoint),
			TokenEndpoint:         strings.TrimSpace(config.TokenEndpoint),
			RegistrationEndpoint:  strings.TrimSpace(config.RegistrationEndpoint),
			ScopesSupported:       cleanStrings(config.ScopesSupported),
		})
	}
	return discovery, nil
}

func protectedResourceMetadataURL(endpoint string, headers []string) string {
	for _, header := range headers {
		if value := bearerAuthParam(header, "resource_metadata"); value != "" {
			return value
		}
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host + "/.well-known/oauth-protected-resource"
}

func bearerAuthParam(header string, key string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	lower := strings.ToLower(header)
	if !strings.HasPrefix(lower, "bearer") {
		return ""
	}
	params := strings.TrimSpace(header[len("bearer"):])
	if strings.HasPrefix(params, ",") {
		params = strings.TrimSpace(params[1:])
	}
	for _, part := range splitAuthParams(params) {
		name, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), key) {
			return strings.Trim(unquoteAuthValue(strings.TrimSpace(value)), " \t\r\n")
		}
	}
	return ""
}

func splitAuthParams(value string) []string {
	var parts []string
	var builder strings.Builder
	quoted := false
	escaped := false
	for _, r := range value {
		switch {
		case escaped:
			builder.WriteRune(r)
			escaped = false
		case r == '\\' && quoted:
			escaped = true
		case r == '"':
			quoted = !quoted
			builder.WriteRune(r)
		case r == ',' && !quoted:
			if part := strings.TrimSpace(builder.String()); part != "" {
				parts = append(parts, part)
			}
			builder.Reset()
		default:
			builder.WriteRune(r)
		}
	}
	if part := strings.TrimSpace(builder.String()); part != "" {
		parts = append(parts, part)
	}
	return parts
}

func unquoteAuthValue(value string) string {
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return value
	}
	value = value[1 : len(value)-1]
	value = strings.ReplaceAll(value, `\"`, `"`)
	value = strings.ReplaceAll(value, `\\`, `\`)
	return value
}

func authorizationServerMetadataURL(issuer string) (string, error) {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	if issuer == "" {
		return "", fmt.Errorf("authorization server issuer is required")
	}
	parsed, err := url.Parse(issuer)
	if err != nil {
		return "", fmt.Errorf("invalid authorization server issuer %q: %w", issuer, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("authorization server issuer %q must use http or https", issuer)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("authorization server issuer %q host is required", issuer)
	}
	return issuer + "/.well-known/oauth-authorization-server", nil
}

func getOAuthJSON(ctx context.Context, client *http.Client, target string, result any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return sanitizeEgressError(err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("status %d%s", response.StatusCode, formatHTTPBody(body))
	}
	contentType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if contentType != "" && strings.ToLower(contentType) != "application/json" {
		return fmt.Errorf("unexpected content type %q", response.Header.Get("Content-Type"))
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1024*1024))
	if err := decoder.Decode(result); err != nil {
		return err
	}
	return nil
}

func formatOAuthDiscovery(discovery OAuthDiscovery) string {
	if len(discovery.AuthorizationServerMetadata) == 0 {
		return ""
	}
	info := discovery.AuthorizationServerMetadata[0]
	parts := []string{}
	if info.Issuer != "" {
		parts = append(parts, "issuer="+info.Issuer)
	}
	if info.AuthorizationEndpoint != "" {
		parts = append(parts, "authorization_endpoint="+info.AuthorizationEndpoint)
	}
	if info.TokenEndpoint != "" {
		parts = append(parts, "token_endpoint="+info.TokenEndpoint)
	}
	if len(info.ScopesSupported) > 0 {
		parts = append(parts, "scopes="+strings.Join(info.ScopesSupported, ","))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}
