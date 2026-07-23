package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"tiggy-manage-agent/internal/identity"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/observability"
	"tiggy-manage-agent/internal/skillmarketplace"
	"tiggy-manage-agent/internal/skillretention"
	skillspkg "tiggy-manage-agent/internal/skills"
)

const (
	AuthModeDisabled = "disabled"
	AuthModeJWT      = "jwt"
	AuthModeOIDC     = "oidc"
	AuthModeGateway  = "gateway"

	RoleViewer   = "viewer"
	RoleMember   = "member"
	RoleOperator = "operator"
	RoleAdmin    = "admin"

	gatewaySubjectHeader   = "X-TMA-Subject"
	gatewayWorkspaceHeader = "X-TMA-Workspace-ID"
	gatewayOrgHeader       = "X-TMA-Organization-ID"
	gatewayOwnerHeader     = "X-TMA-Owner-ID"
	gatewayRolesHeader     = "X-TMA-Roles"
	gatewayTokenHeader     = "X-TMA-Gateway-Token"
	accessTokenCookie      = "tma_access_token"
)

type AuthConfig struct {
	Mode                 string
	JWTSecret            string
	JWTIssuer            string
	JWTAudience          string
	OIDCIssuer           string
	OIDCAudience         string
	OIDCJWKSURL          string
	OIDCSigningAlgs      []string
	OIDCHTTPTimeout      time.Duration
	OIDCRefreshInterval  time.Duration
	OIDCMaxStale         time.Duration
	OIDCClaimMapping     identity.OIDCClaimMapping
	OIDCWebLoginEnabled  bool
	OIDCWebClientID      string
	OIDCWebClientSecret  string
	OIDCWebRedirectURL   string
	OIDCWebPostLogoutURL string
	OIDCWebSessionSecret string
	OIDCCLIClientID      string
	CookieTrustedOrigins []string
	GatewayToken         string
	GatewayTrustedCIDRs  []string
	LegacyControlToken   string
	WorkerToken          string
	WorkerWorkspaceID    string
	AuthorizationSink    observability.AuthorizationDecisionSink
}

type Principal struct {
	Subject              string   `json:"subject"`
	Username             string   `json:"username,omitempty"`
	OrganizationID       string   `json:"organization_id,omitempty"`
	WorkspaceID          string   `json:"workspace_id"`
	OwnerID              string   `json:"owner_id"`
	Roles                []string `json:"roles"`
	AuthType             string   `json:"auth_type"`
	AuthorizationSources []string `json:"-"`
}

type principalContextKey struct{}

type identityAuthenticator struct {
	config          AuthConfig
	trustedNetworks []*net.IPNet
	oidcVerifier    *oidcVerifierManager
}

type oidcVerifierManager struct {
	mu              sync.RWMutex
	refreshMu       sync.Mutex
	verifier        *oidc.IDTokenVerifier
	newVerifier     func() *oidc.IDTokenVerifier
	freshness       *jwksFreshnessTransport
	refreshInterval time.Duration
	maxStale        time.Duration
}

type jwksFreshnessTransport struct {
	base http.RoundTripper

	mu          sync.RWMutex
	jwksURL     string
	lastSuccess time.Time
}

type jwtClaims struct {
	Subject        string          `json:"sub"`
	Username       string          `json:"preferred_username"`
	Name           string          `json:"name"`
	Email          string          `json:"email"`
	Issuer         string          `json:"iss"`
	Audience       json.RawMessage `json:"aud"`
	ExpiresAt      int64           `json:"exp"`
	NotBefore      int64           `json:"nbf"`
	WorkspaceID    string          `json:"workspace_id"`
	OrganizationID string          `json:"organization_id"`
	OwnerID        string          `json:"owner_id"`
	Roles          json.RawMessage `json:"roles"`
}

func newIdentityAuthenticator(config AuthConfig) (*identityAuthenticator, error) {
	config.Mode = strings.ToLower(strings.TrimSpace(config.Mode))
	if config.Mode == "" {
		config.Mode = AuthModeDisabled
	}
	normalizedOrigins := make([]string, 0, len(config.CookieTrustedOrigins))
	for _, raw := range config.CookieTrustedOrigins {
		origin, err := normalizedRequestOrigin(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted cookie origin %q", raw)
		}
		normalizedOrigins = append(normalizedOrigins, origin.String())
	}
	config.CookieTrustedOrigins = normalizedOrigins
	if config.Mode == AuthModeOIDC {
		if config.OIDCClaimMapping.SubjectClaim == "" {
			config.OIDCClaimMapping = identity.DefaultOIDCClaimMapping()
		}
		if err := config.OIDCClaimMapping.Validate(); err != nil {
			return nil, fmt.Errorf("invalid OIDC claim mapping: %w", err)
		}
	}
	authenticator := &identityAuthenticator{config: config}
	switch config.Mode {
	case AuthModeDisabled:
		return authenticator, nil
	case AuthModeJWT:
		if strings.TrimSpace(config.JWTSecret) == "" {
			return nil, errors.New("JWT auth requires a signing secret")
		}
	case AuthModeOIDC:
		verifier, err := newOIDCVerifier(config)
		if err != nil {
			return nil, err
		}
		authenticator.oidcVerifier = verifier
	case AuthModeGateway:
		if strings.TrimSpace(config.GatewayToken) == "" {
			return nil, errors.New("gateway auth requires a shared token")
		}
		for _, raw := range config.GatewayTrustedCIDRs {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			_, network, err := net.ParseCIDR(raw)
			if err != nil {
				return nil, fmt.Errorf("invalid gateway trusted CIDR %q: %w", raw, err)
			}
			authenticator.trustedNetworks = append(authenticator.trustedNetworks, network)
		}
		if len(authenticator.trustedNetworks) == 0 {
			return nil, errors.New("gateway auth requires at least one trusted proxy CIDR")
		}
	default:
		return nil, fmt.Errorf("unsupported auth mode %q", config.Mode)
	}
	return authenticator, nil
}

func newOIDCVerifier(config AuthConfig) (*oidcVerifierManager, error) {
	issuer := strings.TrimSpace(config.OIDCIssuer)
	audience := strings.TrimSpace(config.OIDCAudience)
	if issuer == "" || audience == "" {
		return nil, errors.New("OIDC auth requires issuer and audience")
	}
	if err := validateHTTPAuthURL("OIDC issuer", issuer); err != nil {
		return nil, err
	}
	issuerURL, _ := url.Parse(issuer)
	requireHTTPS := issuerURL.Scheme == "https"
	algorithms, err := normalizeOIDCSigningAlgorithms(config.OIDCSigningAlgs)
	if err != nil {
		return nil, err
	}
	timeout := config.OIDCHTTPTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	refreshInterval := config.OIDCRefreshInterval
	if refreshInterval <= 0 {
		refreshInterval = 15 * time.Minute
	}
	maxStale := config.OIDCMaxStale
	if maxStale <= 0 {
		maxStale = 24 * time.Hour
	}
	if maxStale < refreshInterval {
		return nil, errors.New("OIDC max stale duration must be greater than or equal to refresh interval")
	}
	freshness := &jwksFreshnessTransport{base: http.DefaultTransport}
	client := &http.Client{Timeout: timeout, Transport: freshness}
	if requireHTTPS {
		client.CheckRedirect = func(request *http.Request, _ []*http.Request) error {
			if request.URL.Scheme != "https" {
				return errors.New("OIDC redirect cannot downgrade from https")
			}
			return nil
		}
	}
	ctx := oidc.ClientContext(context.Background(), client)
	verifierConfig := &oidc.Config{ClientID: audience, SupportedSigningAlgs: algorithms}
	jwksURL := strings.TrimSpace(config.OIDCJWKSURL)
	if jwksURL != "" {
		if err := validateHTTPAuthURL("OIDC JWKS URL", jwksURL); err != nil {
			return nil, err
		}
	} else {
		provider, err := oidc.NewProvider(ctx, issuer)
		if err != nil {
			return nil, fmt.Errorf("OIDC discovery failed: %w", err)
		}
		var metadata struct {
			JWKSURL string `json:"jwks_uri"`
		}
		if err := provider.Claims(&metadata); err != nil || strings.TrimSpace(metadata.JWKSURL) == "" {
			return nil, errors.New("OIDC discovery did not provide jwks_uri")
		}
		jwksURL = strings.TrimSpace(metadata.JWKSURL)
		if err := validateHTTPAuthURL("discovered OIDC JWKS URL", jwksURL); err != nil {
			return nil, err
		}
	}
	parsedJWKSURL, _ := url.Parse(jwksURL)
	if requireHTTPS && parsedJWKSURL.Scheme != "https" {
		return nil, errors.New("OIDC JWKS URL must use https when issuer uses https")
	}
	freshness.setJWKSURL(jwksURL)
	newVerifier := func() *oidc.IDTokenVerifier {
		return oidc.NewVerifier(issuer, oidc.NewRemoteKeySet(ctx, jwksURL), verifierConfig)
	}
	return &oidcVerifierManager{
		verifier: newVerifier(), newVerifier: newVerifier, freshness: freshness,
		refreshInterval: refreshInterval, maxStale: maxStale,
	}, nil
}

func (t *jwksFreshnessTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err == nil && response.StatusCode == http.StatusOK {
		t.mu.Lock()
		if request.URL.String() == t.jwksURL {
			t.lastSuccess = time.Now()
		}
		t.mu.Unlock()
	}
	return response, err
}

func (t *jwksFreshnessTransport) setJWKSURL(raw string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.jwksURL = raw
}

func (t *jwksFreshnessTransport) lastSuccessfulFetch() time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lastSuccess
}

func (m *oidcVerifierManager) Verify(ctx context.Context, rawToken string) (*oidc.IDToken, error) {
	m.mu.RLock()
	current := m.verifier
	m.mu.RUnlock()
	lastFetch := m.freshness.lastSuccessfulFetch()
	if lastFetch.IsZero() || time.Since(lastFetch) < m.refreshInterval {
		return current.Verify(ctx, rawToken)
	}

	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	lastFetch = m.freshness.lastSuccessfulFetch()
	if time.Since(lastFetch) < m.refreshInterval {
		m.mu.RLock()
		current = m.verifier
		m.mu.RUnlock()
		return current.Verify(ctx, rawToken)
	}

	candidate := m.newVerifier()
	token, refreshErr := candidate.Verify(ctx, rawToken)
	refreshedAt := m.freshness.lastSuccessfulFetch()
	if refreshedAt.After(lastFetch) {
		m.mu.Lock()
		m.verifier = candidate
		m.mu.Unlock()
		return token, refreshErr
	}
	if time.Since(lastFetch) > m.maxStale {
		return nil, fmt.Errorf("OIDC JWKS cache exceeded maximum stale duration: %w", refreshErr)
	}
	return current.Verify(ctx, rawToken)
}

func normalizeOIDCSigningAlgorithms(values []string) ([]string, error) {
	if len(values) == 0 {
		values = []string{"RS256", "ES256"}
	}
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		algorithm := strings.ToUpper(strings.TrimSpace(value))
		if algorithm != "RS256" && algorithm != "ES256" {
			return nil, fmt.Errorf("unsupported OIDC signing algorithm %q", value)
		}
		if !seen[algorithm] {
			seen[algorithm] = true
			result = append(result, algorithm)
		}
	}
	return result, nil
}

func validateHTTPAuthURL(name string, raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("%s must be an absolute http or https URL without credentials or fragment", name)
	}
	return nil
}

func PrincipalFromRequest(r *http.Request) (Principal, bool) {
	if r == nil {
		return Principal{}, false
	}
	principal, ok := r.Context().Value(principalContextKey{}).(Principal)
	return principal, ok && principal.Subject != ""
}

func (s *Server) getCurrentPrincipal(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFromRequest(r)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "principal": principal})
}

type authClientConfiguration struct {
	Mode string                   `json:"mode"`
	OIDC *oidcClientConfiguration `json:"oidc,omitempty"`
}

type oidcClientConfiguration struct {
	Issuer              string   `json:"issuer"`
	Audience            string   `json:"audience"`
	ClientID            string   `json:"client_id"`
	Scopes              []string `json:"scopes"`
	DeviceAuthorization bool     `json:"device_authorization"`
}

func (s *Server) getAuthClientConfiguration(w http.ResponseWriter, _ *http.Request) {
	configuration := authClientConfiguration{Mode: AuthModeDisabled}
	if s == nil || s.authenticator == nil {
		writeJSON(w, http.StatusOK, configuration)
		return
	}
	configuration.Mode = s.authenticator.config.Mode
	if configuration.Mode == AuthModeOIDC {
		clientID := strings.TrimSpace(s.authenticator.config.OIDCCLIClientID)
		configuration.OIDC = &oidcClientConfiguration{
			Issuer:              strings.TrimSpace(s.authenticator.config.OIDCIssuer),
			Audience:            strings.TrimSpace(s.authenticator.config.OIDCAudience),
			ClientID:            clientID,
			Scopes:              []string{oidc.ScopeOpenID, "profile", "email"},
			DeviceAuthorization: clientID != "",
		}
	}
	writeJSON(w, http.StatusOK, configuration)
}

func (p Principal) HasRole(required string) bool {
	requiredLevel := roleLevel(required)
	for _, role := range p.Roles {
		if roleLevel(role) >= requiredLevel {
			return true
		}
	}
	return false
}

func roleLevel(role string) int {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case RoleAdmin:
		return 4
	case RoleOperator:
		return 3
	case RoleMember:
		return 2
	case RoleViewer:
		return 1
	default:
		return 0
	}
}

func (a *identityAuthenticator) authenticate(r *http.Request) (Principal, error) {
	if a == nil || a.config.Mode == AuthModeDisabled {
		return Principal{}, nil
	}
	if bearerTokenMatches(r.Header.Get("Authorization"), a.config.LegacyControlToken) {
		return Principal{
			Subject: "legacy-control", WorkspaceID: managedagents.DefaultWorkspaceID,
			OwnerID: "legacy-control", Roles: []string{RoleAdmin}, AuthType: "legacy-control",
			AuthorizationSources: []string{"legacy_control_token"},
		}, nil
	}
	switch a.config.Mode {
	case AuthModeJWT, AuthModeOIDC:
		token := bearerToken(r.Header.Get("Authorization"))
		if token == "" {
			if cookie, err := r.Cookie(accessTokenCookie); err == nil {
				token = strings.TrimSpace(cookie.Value)
			}
		}
		if a.config.Mode == AuthModeOIDC {
			return a.authenticateOIDC(r.Context(), token)
		}
		return a.authenticateJWT(token)
	case AuthModeGateway:
		return a.authenticateGateway(r)
	default:
		return Principal{}, errors.New("authentication is not configured")
	}
}

func (a *identityAuthenticator) authenticateOIDC(ctx context.Context, token string) (Principal, error) {
	if strings.TrimSpace(token) == "" {
		return Principal{}, errors.New("valid bearer token required")
	}
	if a == nil || a.oidcVerifier == nil {
		return Principal{}, errors.New("OIDC verifier is not configured")
	}
	verified, err := a.oidcVerifier.Verify(ctx, token)
	if err != nil {
		return Principal{}, fmt.Errorf("OIDC token rejected: %w", err)
	}
	var claims map[string]any
	if err := verified.Claims(&claims); err != nil {
		return Principal{}, errors.New("invalid OIDC claims")
	}
	resolved, err := a.config.OIDCClaimMapping.Resolve(claims)
	if err != nil {
		return Principal{}, fmt.Errorf("OIDC identity rejected: %w", err)
	}
	return normalizePrincipal(Principal{
		Subject: resolved.Subject, Username: identityUsername(claimStringValue(claims, "preferred_username"), claimStringValue(claims, "name"), claimStringValue(claims, "email")), OrganizationID: resolved.OrganizationID, WorkspaceID: resolved.WorkspaceID,
		OwnerID: resolved.OwnerID, Roles: resolved.Roles, AuthType: AuthModeOIDC,
		AuthorizationSources: resolved.AuthorizationSources,
	})
}

func (a *identityAuthenticator) authenticateJWT(token string) (Principal, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Principal{}, errors.New("valid bearer token required")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Principal{}, errors.New("invalid JWT header")
	}
	var header struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil || header.Algorithm != "HS256" {
		return Principal{}, errors.New("JWT must use HS256")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Principal{}, errors.New("invalid JWT signature")
	}
	mac := hmac.New(sha256.New, []byte(a.config.JWTSecret))
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	expected := mac.Sum(nil)
	if len(signature) != len(expected) || subtle.ConstantTimeCompare(signature, expected) != 1 {
		return Principal{}, errors.New("invalid JWT signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Principal{}, errors.New("invalid JWT claims")
	}
	var claims jwtClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Principal{}, errors.New("invalid JWT claims")
	}
	now := time.Now().Unix()
	if claims.ExpiresAt == 0 || now >= claims.ExpiresAt {
		return Principal{}, errors.New("JWT is expired")
	}
	if claims.NotBefore > 0 && now < claims.NotBefore {
		return Principal{}, errors.New("JWT is not active")
	}
	if issuer := strings.TrimSpace(a.config.JWTIssuer); issuer != "" && claims.Issuer != issuer {
		return Principal{}, errors.New("JWT issuer does not match")
	}
	if audience := strings.TrimSpace(a.config.JWTAudience); audience != "" && !jwtAudienceContains(claims.Audience, audience) {
		return Principal{}, errors.New("JWT audience does not match")
	}
	roles := jsonStringList(claims.Roles)
	sources := []string{"jwt_claim:sub", "jwt_claim:workspace_id", "jwt_claim:roles"}
	if strings.TrimSpace(claims.OrganizationID) != "" {
		sources = append(sources, "jwt_claim:organization_id")
	}
	if strings.TrimSpace(claims.OwnerID) != "" {
		sources = append(sources, "jwt_claim:owner_id")
	}
	return normalizePrincipal(Principal{
		Subject: claims.Subject, Username: identityUsername(claims.Username, claims.Name, claims.Email), OrganizationID: claims.OrganizationID, WorkspaceID: claims.WorkspaceID, OwnerID: claims.OwnerID,
		Roles: roles, AuthType: AuthModeJWT, AuthorizationSources: sources,
	})
}

func (a *identityAuthenticator) authenticateGateway(r *http.Request) (Principal, error) {
	remoteIP := requestRemoteIP(r)
	trusted := false
	for _, network := range a.trustedNetworks {
		if remoteIP != nil && network.Contains(remoteIP) {
			trusted = true
			break
		}
	}
	if !trusted {
		return Principal{}, errors.New("request did not come from a trusted gateway")
	}
	provided := strings.TrimSpace(r.Header.Get(gatewayTokenHeader))
	expected := strings.TrimSpace(a.config.GatewayToken)
	if len(provided) != len(expected) || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		return Principal{}, errors.New("invalid gateway token")
	}
	sources := []string{
		"gateway_header:" + gatewaySubjectHeader,
		"gateway_header:" + gatewayWorkspaceHeader,
		"gateway_header:" + gatewayRolesHeader,
	}
	if strings.TrimSpace(r.Header.Get(gatewayOrgHeader)) != "" {
		sources = append(sources, "gateway_header:"+gatewayOrgHeader)
	}
	if strings.TrimSpace(r.Header.Get(gatewayOwnerHeader)) != "" {
		sources = append(sources, "gateway_header:"+gatewayOwnerHeader)
	}
	return normalizePrincipal(Principal{
		Subject:        strings.TrimSpace(r.Header.Get(gatewaySubjectHeader)),
		OrganizationID: strings.TrimSpace(r.Header.Get(gatewayOrgHeader)),
		WorkspaceID:    strings.TrimSpace(r.Header.Get(gatewayWorkspaceHeader)),
		OwnerID:        strings.TrimSpace(r.Header.Get(gatewayOwnerHeader)),
		Roles:          splitRoles(r.Header.Get(gatewayRolesHeader)), AuthType: AuthModeGateway,
		AuthorizationSources: sources,
	})
}

func (a *identityAuthenticator) validateCookieCSRF(r *http.Request) error {
	if a == nil || (a.config.Mode != AuthModeJWT && a.config.Mode != AuthModeOIDC) || r == nil || isSafeRequestMethod(r.Method) || bearerToken(r.Header.Get("Authorization")) != "" {
		return nil
	}
	cookie, err := r.Cookie(accessTokenCookie)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return nil
	}
	origin, err := normalizedRequestOrigin(r.Header.Get("Origin"))
	if err != nil {
		return errors.New("cookie-authenticated write requires a valid Origin header")
	}
	if len(a.config.CookieTrustedOrigins) == 0 {
		if !strings.EqualFold(origin.Host, strings.TrimSpace(r.Host)) {
			return errors.New("cookie-authenticated write origin is not trusted")
		}
		return nil
	}
	for _, trusted := range a.config.CookieTrustedOrigins {
		parsed, parseErr := normalizedRequestOrigin(trusted)
		if parseErr == nil && parsed.String() == origin.String() {
			return nil
		}
	}
	return errors.New("cookie-authenticated write origin is not trusted")
}

func normalizedRequestOrigin(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("invalid origin")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("invalid origin scheme")
	}
	return parsed, nil
}

func isSafeRequestMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

func normalizePrincipal(principal Principal) (Principal, error) {
	principal.Subject = strings.TrimSpace(principal.Subject)
	principal.Username = strings.TrimSpace(principal.Username)
	principal.WorkspaceID = strings.TrimSpace(principal.WorkspaceID)
	principal.OwnerID = strings.TrimSpace(principal.OwnerID)
	if principal.Subject == "" || principal.WorkspaceID == "" {
		return Principal{}, errors.New("identity requires subject and workspace_id")
	}
	if principal.OwnerID == "" {
		principal.OwnerID = principal.Subject
	}
	roles := make([]string, 0, len(principal.Roles))
	seen := map[string]struct{}{}
	for _, role := range principal.Roles {
		role = strings.ToLower(strings.TrimSpace(role))
		if roleLevel(role) == 0 {
			continue
		}
		if _, ok := seen[role]; ok {
			continue
		}
		seen[role] = struct{}{}
		roles = append(roles, role)
	}
	if len(roles) == 0 {
		roles = []string{RoleViewer}
	}
	principal.Roles = roles
	principal.AuthorizationSources = normalizedStringList(principal.AuthorizationSources)
	return principal, nil
}

func identityUsername(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func claimStringValue(claims map[string]any, name string) string {
	value, _ := claims[name].(string)
	return strings.TrimSpace(value)
}

func (s *Server) identityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s == nil || s.authenticator == nil || isPublicRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		if s.authenticator.config.Mode == AuthModeDisabled {
			ctx, err := managedagents.ContextWithDatabaseAccessScope(r.Context(), managedagents.AccessScope{WorkspaceID: managedagents.DefaultWorkspaceID})
			if err != nil {
				writeError(w, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		if isWorkerCredentialRequest(r) && bearerTokenMatches(r.Header.Get("Authorization"), s.workerAuthToken) {
			if err := s.authorizeWorkerCredentialRequest(r); err != nil {
				s.auditAuthorizationDecision(r, Principal{AuthType: "worker-token", AuthorizationSources: []string{"worker_bearer_token"}}, "denied", "worker_scope_denied", "", err)
				writeError(w, err)
				return
			}
			workspaceID := strings.TrimSpace(s.authenticator.config.WorkerWorkspaceID)
			if workspaceID == "" {
				workspaceID = managedagents.DefaultWorkspaceID
			}
			ctx, err := managedagents.ContextWithDatabaseAccessScope(r.Context(), managedagents.AccessScope{WorkspaceID: workspaceID})
			if err != nil {
				s.auditAuthorizationDecision(r, Principal{AuthType: "worker-token", AuthorizationSources: []string{"worker_bearer_token"}}, "denied", "identity_boundary", "", err)
				writeError(w, err)
				return
			}
			s.auditAuthorizationDecision(r, Principal{AuthType: "worker-token", AuthorizationSources: []string{"worker_bearer_token"}}, "allowed", "worker_credential", "", nil)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		principal, err := s.authenticator.authenticate(r)
		if err != nil {
			s.auditAuthorizationDecision(r, Principal{AuthType: s.authenticator.config.Mode}, "denied", "authentication_failed", "", err)
			w.Header().Set("WWW-Authenticate", `Bearer realm="tma"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
			return
		}
		if err := s.authenticator.validateCookieCSRF(r); err != nil {
			s.auditAuthorizationDecision(r, principal, "denied", "csrf_rejected", "", err)
			writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		}
		requiredRole := RoleViewer
		if !isSafeRequestMethod(r.Method) {
			requiredRole = RoleMember
		}
		if !principal.HasRole(requiredRole) {
			s.auditAuthorizationDecision(r, principal, "denied", "role_required", requiredRole, nil)
			writeJSON(w, http.StatusForbidden, map[string]string{"error": requiredRole + " role required"})
			return
		}
		ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
		ctx, err = managedagents.ContextWithDatabaseAccessScope(ctx, accessScopeForPrincipal(principal))
		if err != nil {
			s.auditAuthorizationDecision(r, principal, "denied", "identity_boundary", requiredRole, err)
			writeError(w, err)
			return
		}
		scopedRequest := r.WithContext(ctx)
		if err := s.authorizeResourceRequest(scopedRequest, principal); err != nil {
			s.auditAuthorizationDecision(r, principal, authorizationOutcomeForError(err), authorizationReasonForResourceError(err), requiredRole, err)
			writeError(w, err)
			return
		}
		s.auditAuthorizationDecision(r, principal, "allowed", "identity_boundary", requiredRole, nil)
		next.ServeHTTP(w, scopedRequest)
	})
}

func (s *Server) authorizeResourceRequest(r *http.Request, principal Principal) error {
	segments := splitRequestPath(r.URL.Path)
	scope := accessScopeForPrincipal(principal)
	if len(segments) >= 3 && isUserAPIVersion(segments[0]) && segments[1] == "sessions" {
		_, err := s.store.GetSessionScoped(segments[2], scope)
		return err
	}
	if len(segments) >= 3 && isUserAPIVersion(segments[0]) && segments[1] == "agents" && segments[2] != "default" && segments[2] != "import" {
		agent, err := s.store.GetAgentScoped(segments[2], agentAccessScopeForPrincipal(principal))
		if err != nil {
			return err
		}
		if !isSafeRequestMethod(r.Method) && agent.OwnerType == managedagents.AgentOwnerWorkspace && !principal.HasRole(RoleOperator) {
			return fmt.Errorf("%w: operator role required to modify Workspace-shared Agents", managedagents.ErrForbidden)
		}
		return nil
	}
	if len(segments) >= 3 && isUserAPIVersion(segments[0]) && segments[1] == "object-refs" {
		_, err := s.store.GetObjectRefScoped(segments[2], scope)
		return err
	}
	if len(segments) >= 3 && isUserAPIVersion(segments[0]) && segments[1] == "skills" && segments[2] != "marketplace" && segments[2] != "resolve-preview" {
		if registry, ok := s.store.(skillspkg.Registry); ok {
			skill, err := registry.GetSkill(r.Context(), segments[2])
			if err != nil {
				return err
			}
			if err := authorizeWorkspacePrincipal(principal, skill.WorkspaceID); err != nil {
				return err
			}
			if skill.OwnerType == skillspkg.OwnerTypeUser && skill.OwnerID != principal.OwnerID {
				return managedagents.ErrForbidden
			}
			if !isSafeRequestMethod(r.Method) && skill.OwnerType != skillspkg.OwnerTypeUser && !principal.HasRole(RoleOperator) {
				return fmt.Errorf("%w: operator role required to modify Workspace Skills", managedagents.ErrForbidden)
			}
			return nil
		}
	}
	if len(segments) >= 3 && isUserAPIVersion(segments[0]) && segments[1] == "skill-marketplace-policies" {
		if store, ok := s.store.(skillmarketplace.PolicyStore); ok {
			policy, err := store.GetMarketplacePolicy(r.Context(), segments[2])
			if err != nil {
				return err
			}
			if policy.WorkspaceID != "" {
				return authorizeWorkspacePrincipal(principal, policy.WorkspaceID)
			}
			return authorizeOrganizationPrincipal(principal, policy.OrganizationID)
		}
	}
	if len(segments) >= 4 && isUserAPIVersion(segments[0]) && segments[1] == "skill-asset-retention" && segments[2] == "policies" {
		if store, ok := s.store.(skillretention.Store); ok {
			policy, err := store.GetSkillAssetRetentionPolicy(r.Context(), segments[3])
			if err != nil {
				return err
			}
			if policy.WorkspaceID != "" {
				return authorizeWorkspacePrincipal(principal, policy.WorkspaceID)
			}
			return authorizeOrganizationPrincipal(principal, policy.OrganizationID)
		}
	}
	if len(segments) >= 3 && isUserAPIVersion(segments[0]) && segments[1] == "workers" && segments[2] != "diagnose" && segments[2] != "reap-expired" {
		_, err := s.store.GetWorkerScoped(segments[2], scope)
		return err
	}
	if len(segments) >= 3 && isUserAPIVersion(segments[0]) && segments[1] == "worker-work" && segments[2] != "reap-expired" {
		_, err := s.store.GetWorkerWorkScoped(segments[2], scope)
		return err
	}
	return nil
}

func isUserAPIVersion(segment string) bool {
	return segment == "v1" || segment == "v2"
}

func (s *Server) authorizeWorkerCredentialRequest(r *http.Request) error {
	segments := splitRequestPath(r.URL.Path)
	if len(segments) < 3 || segments[0] != "v1" || segments[1] != "workers" || segments[2] == "diagnose" {
		return nil
	}
	workspaceID := strings.TrimSpace(s.authenticator.config.WorkerWorkspaceID)
	if workspaceID == "" {
		workspaceID = managedagents.DefaultWorkspaceID
	}
	_, err := s.store.GetWorkerScoped(segments[2], managedagents.AccessScope{WorkspaceID: workspaceID})
	return err
}

func accessScopeForPrincipal(principal Principal) managedagents.AccessScope {
	scope := managedagents.AccessScope{WorkspaceID: principal.WorkspaceID}
	if !principal.HasRole(RoleOperator) {
		scope.OwnerID = principal.OwnerID
	}
	return scope
}

func agentAccessScopeForPrincipal(principal Principal) managedagents.AccessScope {
	return managedagents.AccessScope{WorkspaceID: principal.WorkspaceID, OwnerID: principal.OwnerID}
}

func requestAccessScope(r *http.Request) (managedagents.AccessScope, bool) {
	principal, ok := PrincipalFromRequest(r)
	if !ok {
		return managedagents.AccessScope{}, false
	}
	return accessScopeForPrincipal(principal), true
}

func (s *Server) getAgentForRequest(r *http.Request, id string) (managedagents.Agent, error) {
	if principal, ok := PrincipalFromRequest(r); ok {
		return s.store.GetAgentScoped(id, agentAccessScopeForPrincipal(principal))
	}
	if _, ok := s.store.(managedagents.AgentContextStore); ok {
		return managedagents.GetAgentWithContext(r.Context(), s.store, id)
	}
	return s.store.GetAgent(id)
}

func (s *Server) listAgentsForRequest(r *http.Request) ([]managedagents.Agent, error) {
	if principal, ok := PrincipalFromRequest(r); ok {
		return s.store.ListAgentsScoped(agentAccessScopeForPrincipal(principal))
	}
	if _, ok := s.store.(managedagents.AgentContextStore); ok {
		return managedagents.ListAgentsWithContext(r.Context(), s.store)
	}
	return s.store.ListAgents()
}

func (s *Server) getSessionForRequest(r *http.Request, id string) (managedagents.Session, error) {
	if _, ok := s.store.(managedagents.SessionContextStore); ok {
		return managedagents.GetSessionWithContext(r.Context(), s.store, id)
	}
	if scope, ok := requestAccessScope(r); ok {
		return s.store.GetSessionScoped(id, scope)
	}
	return s.store.GetSession(id)
}

func (s *Server) listSessionsForRequest(r *http.Request, input managedagents.ListSessionsInput) ([]managedagents.Session, error) {
	if _, ok := s.store.(managedagents.SessionContextStore); ok {
		return managedagents.ListSessionsWithContext(r.Context(), s.store, input)
	}
	if scope, ok := requestAccessScope(r); ok {
		return s.store.ListSessionsScoped(input, scope)
	}
	return s.store.ListSessions(input)
}

func (s *Server) getObjectRefForRequest(r *http.Request, id string) (managedagents.ObjectRef, error) {
	if _, ok := s.store.(managedagents.ObjectArtifactContextStore); ok {
		return managedagents.GetObjectRefWithContext(r.Context(), s.store, id)
	}
	if scope, ok := requestAccessScope(r); ok {
		return s.store.GetObjectRefScoped(id, scope)
	}
	return s.store.GetObjectRef(id)
}

func (s *Server) getWorkerForRequest(r *http.Request, id string) (managedagents.Worker, error) {
	if _, ok := s.store.(managedagents.WorkerContextStore); ok {
		return managedagents.GetWorkerWithContext(r.Context(), s.store, id)
	}
	if scope, ok := requestAccessScope(r); ok {
		return s.store.GetWorkerScoped(id, scope)
	}
	return s.store.GetWorker(id)
}

func (s *Server) listWorkersForRequest(r *http.Request, input managedagents.ListWorkersInput) ([]managedagents.Worker, error) {
	if _, ok := s.store.(managedagents.WorkerContextStore); ok {
		return managedagents.ListWorkersWithContext(r.Context(), s.store, input)
	}
	if scope, ok := requestAccessScope(r); ok {
		return s.store.ListWorkersScoped(input, scope)
	}
	return s.store.ListWorkers(input)
}

func (s *Server) getWorkerWorkForRequest(r *http.Request, id string) (managedagents.WorkerWork, error) {
	if _, ok := s.store.(managedagents.WorkerContextStore); ok {
		return managedagents.GetWorkerWorkWithContext(r.Context(), s.store, id)
	}
	if scope, ok := requestAccessScope(r); ok {
		return s.store.GetWorkerWorkScoped(id, scope)
	}
	return s.store.GetWorkerWork(id)
}

func authorizeWorkspacePrincipal(principal Principal, workspaceID string) error {
	if strings.TrimSpace(workspaceID) != principal.WorkspaceID {
		return fmt.Errorf("%w: resource belongs to another workspace", managedagents.ErrForbidden)
	}
	return nil
}

func authorizeOrganizationPrincipal(principal Principal, organizationID string) error {
	if strings.TrimSpace(organizationID) == "" || strings.TrimSpace(organizationID) != principal.OrganizationID {
		return fmt.Errorf("%w: resource belongs to another organization", managedagents.ErrForbidden)
	}
	return nil
}

func authorizeSessionPrincipal(principal Principal, session managedagents.Session) error {
	if err := authorizeWorkspacePrincipal(principal, session.WorkspaceID); err != nil {
		return err
	}
	if principal.HasRole(RoleOperator) || session.OwnerID == "" || session.OwnerID == principal.OwnerID {
		return nil
	}
	return fmt.Errorf("%w: session belongs to another owner", managedagents.ErrForbidden)
}

func requestWorkspaceID(r *http.Request, fallback string) string {
	if principal, ok := PrincipalFromRequest(r); ok {
		return principal.WorkspaceID
	}
	return strings.TrimSpace(fallback)
}

func requestOrganizationID(r *http.Request, fallback string) string {
	if principal, ok := PrincipalFromRequest(r); ok {
		return principal.OrganizationID
	}
	return strings.TrimSpace(fallback)
}

func requestOwnerID(r *http.Request, fallback string) string {
	if principal, ok := PrincipalFromRequest(r); ok {
		return principal.OwnerID
	}
	return strings.TrimSpace(fallback)
}

func requestActorID(r *http.Request, fallback string) string {
	if principal, ok := PrincipalFromRequest(r); ok {
		return principal.Subject
	}
	return strings.TrimSpace(fallback)
}

func auditWorkspaceID(r *http.Request, explicit string) string {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		return explicit
	}
	if principal, ok := PrincipalFromRequest(r); ok {
		return principal.WorkspaceID
	}
	return ""
}

func requestSessionOwnerFilter(r *http.Request) string {
	if principal, ok := PrincipalFromRequest(r); ok && !principal.HasRole(RoleOperator) {
		return principal.OwnerID
	}
	return ""
}

func requestCanViewWorkspaceWide(r *http.Request) bool {
	principal, ok := PrincipalFromRequest(r)
	return !ok || principal.HasRole(RoleOperator)
}

func (s *Server) requestWorkerWorkspaceID(r *http.Request, fallback string) string {
	if principal, ok := PrincipalFromRequest(r); ok {
		return principal.WorkspaceID
	}
	if s != nil && s.authenticator != nil {
		if workspaceID := strings.TrimSpace(s.authenticator.config.WorkerWorkspaceID); workspaceID != "" {
			return workspaceID
		}
	}
	return strings.TrimSpace(fallback)
}

func (s *Server) authorizeSessionID(r *http.Request, sessionID string) error {
	scope, ok := requestAccessScope(r)
	if !ok {
		return nil
	}
	_, err := s.store.GetSessionScoped(strings.TrimSpace(sessionID), scope)
	return err
}

func isPublicRequest(r *http.Request) bool {
	path := r.URL.Path
	return path == "/" || path == "/health" || path == "/app" || path == "/app/" ||
		path == "/v1/auth/config" || path == "/v2/auth/config" ||
		path == "/inspector" || path == "/space" || strings.HasPrefix(path, "/auth/") ||
		strings.HasPrefix(path, "/app/assets/") || strings.HasPrefix(path, "/inspector/assets/") ||
		strings.HasPrefix(path, "/space/assets/")
}

func isWorkerCredentialRequest(r *http.Request) bool {
	segments := splitRequestPath(r.URL.Path)
	if len(segments) == 2 && segments[0] == "v1" && segments[1] == "workers" && r.Method == http.MethodPost {
		return true
	}
	if len(segments) == 3 && segments[0] == "v1" && segments[1] == "workers" && segments[2] == "diagnose" && r.Method == http.MethodPost {
		return true
	}
	if len(segments) < 4 || segments[0] != "v1" || segments[1] != "workers" {
		return false
	}
	if len(segments) == 4 && (segments[3] == "heartbeat" || segments[3] == "archive") && r.Method == http.MethodPost {
		return true
	}
	return len(segments) >= 5 && segments[3] == "work"
}

func splitRequestPath(path string) []string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 1 && parts[0] == "" {
		return nil
	}
	return parts
}

func bearerToken(header string) string {
	scheme, token, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

func jwtAudienceContains(raw json.RawMessage, expected string) bool {
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return single == expected
	}
	var multiple []string
	if json.Unmarshal(raw, &multiple) == nil {
		for _, value := range multiple {
			if value == expected {
				return true
			}
		}
	}
	return false
}

func jsonStringList(raw json.RawMessage) []string {
	var values []string
	if json.Unmarshal(raw, &values) == nil {
		return values
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return splitRoles(value)
	}
	return nil
}

func splitRoles(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == ';' })
}

func requestRemoteIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	return net.ParseIP(host)
}
