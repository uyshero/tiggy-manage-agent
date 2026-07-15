package httpapi

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"golang.org/x/oauth2"

	"tiggy-manage-agent/internal/identity"
)

func TestSafeOIDCReturnTo(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"":                               "/app",
		"/app":                           "/app",
		"/app?view=settings#permissions": "/app?view=settings#permissions",
		"//attacker.example/path":        "/app",
		"https://attacker.example/path":  "/app",
		"relative/path":                  "/app",
	}
	for input, expected := range tests {
		if actual := safeOIDCReturnTo(input); actual != expected {
			t.Fatalf("safeOIDCReturnTo(%q) = %q, want %q", input, actual, expected)
		}
	}
}

func TestOIDCWebLoginEncryptedSessionRejectsTampering(t *testing.T) {
	t.Parallel()
	block, err := aes.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	login := &oidcWebLogin{aead: aead}
	original := oidcLoginTransaction{State: "state", Nonce: "nonce", Verifier: "verifier", ReturnTo: "/app", ExpiresAt: 42}
	sealed, err := login.sealJSON(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded oidcLoginTransaction
	if err := login.openJSON(sealed, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != original {
		t.Fatalf("decoded transaction = %#v, want %#v", decoded, original)
	}
	tampered, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil {
		t.Fatal(err)
	}
	tampered[len(tampered)-1] ^= 0x01
	if err := login.openJSON(base64.RawURLEncoding.EncodeToString(tampered), &decoded); err == nil {
		t.Fatal("expected tampered session to be rejected")
	}
}

func TestOIDCWebLoginStartsPKCETransaction(t *testing.T) {
	t.Parallel()
	login := newTestOIDCWebLogin(t, true)
	request := httptest.NewRequest(http.MethodGet, "https://tma.example/auth/login?return_to=%2Fapp%3Fview%3Dsettings%23permissions", nil)
	response := httptest.NewRecorder()

	login.login(response, request)

	result := response.Result()
	defer result.Body.Close()
	if result.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want %d", result.StatusCode, http.StatusFound)
	}
	location, err := url.Parse(result.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	query := location.Query()
	for key, expected := range map[string]string{
		"client_id":             "tma-web",
		"redirect_uri":          "https://tma.example/auth/callback",
		"response_type":         "code",
		"code_challenge_method": "S256",
	} {
		if actual := query.Get(key); actual != expected {
			t.Fatalf("authorization query %s = %q, want %q", key, actual, expected)
		}
	}
	if query.Get("state") == "" || query.Get("nonce") == "" || query.Get("code_challenge") == "" {
		t.Fatalf("authorization URL is missing PKCE transaction values: %s", location.String())
	}
	cookie := responseCookie(t, result.Cookies(), oidcTransactionCookie)
	if cookie.Path != "/auth/callback" || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected transaction cookie attributes: %#v", cookie)
	}
	if cookie.MaxAge != int(oidcTransactionTTL.Seconds()) {
		t.Fatalf("transaction cookie max age = %d", cookie.MaxAge)
	}
	var transaction oidcLoginTransaction
	if err := login.openJSON(cookie.Value, &transaction); err != nil {
		t.Fatal(err)
	}
	if transaction.State != query.Get("state") || transaction.Nonce != query.Get("nonce") {
		t.Fatalf("transaction does not match authorization URL: %#v", transaction)
	}
	if transaction.ReturnTo != "/app?view=settings#permissions" {
		t.Fatalf("transaction return_to = %q", transaction.ReturnTo)
	}
	if oauth2.S256ChallengeFromVerifier(transaction.Verifier) != query.Get("code_challenge") {
		t.Fatal("PKCE verifier does not match the public code challenge")
	}
	if transaction.ExpiresAt <= time.Now().Unix() {
		t.Fatal("transaction should expire in the future")
	}
}

func TestOIDCWebLoginRejectsInvalidCallbackTransactions(t *testing.T) {
	t.Parallel()
	login := newTestOIDCWebLogin(t, false)

	t.Run("provider error", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/auth/callback?error=access_denied", nil)
		response := httptest.NewRecorder()
		login.callback(response, request)
		if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "access_denied") {
			t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
		}
	})

	t.Run("missing transaction", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/auth/callback?state=state&code=code", nil)
		response := httptest.NewRecorder()
		login.callback(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
		}
	})

	t.Run("expired transaction", func(t *testing.T) {
		sealed, err := login.sealJSON(oidcLoginTransaction{State: "state", Nonce: "nonce", Verifier: "verifier", ExpiresAt: time.Now().Add(-time.Minute).Unix()})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodGet, "/auth/callback?state=state&code=code", nil)
		request.AddCookie(&http.Cookie{Name: oidcTransactionCookie, Value: sealed})
		response := httptest.NewRecorder()
		login.callback(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
		}
		cleared := responseCookie(t, response.Result().Cookies(), oidcTransactionCookie)
		if cleared.MaxAge >= 0 {
			t.Fatalf("expired transaction cookie was not cleared: %#v", cleared)
		}
	})

	t.Run("state mismatch", func(t *testing.T) {
		sealed, err := login.sealJSON(oidcLoginTransaction{State: "expected", Nonce: "nonce", Verifier: "verifier", ExpiresAt: time.Now().Add(time.Minute).Unix()})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodGet, "/auth/callback?state=provided&code=code", nil)
		request.AddCookie(&http.Cookie{Name: oidcTransactionCookie, Value: sealed})
		response := httptest.NewRecorder()
		login.callback(response, request)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "state does not match") {
			t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
		}
	})
}

func TestOIDCWebLoginSetsAndClearsSecureTokenCookies(t *testing.T) {
	t.Parallel()
	login := newTestOIDCWebLogin(t, true)
	response := httptest.NewRecorder()
	token := &oauth2.Token{AccessToken: "access.jwt.value", RefreshToken: "refresh-secret", Expiry: time.Now().Add(time.Hour)}
	if err := login.setTokenCookies(response, token); err != nil {
		t.Fatal(err)
	}
	cookies := response.Result().Cookies()
	access := responseCookie(t, cookies, accessTokenCookie)
	if access.Path != "/" || !access.HttpOnly || !access.Secure || access.SameSite != http.SameSiteLaxMode || access.MaxAge < 3500 {
		t.Fatalf("unexpected access cookie: %#v", access)
	}
	refresh := responseCookie(t, cookies, oidcRefreshCookie)
	if refresh.Path != "/auth" || !refresh.HttpOnly || !refresh.Secure || refresh.SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected refresh cookie: %#v", refresh)
	}
	if strings.Contains(refresh.Value, token.RefreshToken) {
		t.Fatal("refresh token must not appear in plaintext in the cookie")
	}
	var decodedRefresh string
	if err := login.openJSON(refresh.Value, &decodedRefresh); err != nil || decodedRefresh != token.RefreshToken {
		t.Fatalf("encrypted refresh token did not round trip: value=%q err=%v", decodedRefresh, err)
	}

	clearResponse := httptest.NewRecorder()
	login.clearSessionCookies(clearResponse)
	for _, name := range []string{accessTokenCookie, oidcRefreshCookie} {
		cookie := responseCookie(t, clearResponse.Result().Cookies(), name)
		if cookie.MaxAge >= 0 || cookie.Value != "" || !cookie.HttpOnly || !cookie.Secure {
			t.Fatalf("cookie %s was not securely cleared: %#v", name, cookie)
		}
	}
}

func TestOIDCWebLoginRefreshAndLogoutRequireTrustedOrigin(t *testing.T) {
	t.Parallel()
	login := newTestOIDCWebLogin(t, true)
	login.authenticator = &identityAuthenticator{config: AuthConfig{CookieTrustedOrigins: []string{"https://tma.example"}}}

	for _, endpoint := range []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
	}{
		{name: "refresh", handler: login.refresh},
		{name: "logout", handler: login.logout},
	} {
		t.Run(endpoint.name+" rejects missing origin", func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "https://tma.example/auth/"+endpoint.name, nil)
			response := httptest.NewRecorder()
			endpoint.handler(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
			}
		})
		t.Run(endpoint.name+" rejects foreign origin", func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "https://tma.example/auth/"+endpoint.name, nil)
			request.Header.Set("Origin", "https://attacker.example")
			response := httptest.NewRecorder()
			endpoint.handler(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
			}
		})
	}

	t.Run("refresh rejects tampered session", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "https://tma.example/auth/refresh", nil)
		request.Header.Set("Origin", "https://tma.example")
		request.AddCookie(&http.Cookie{Name: oidcRefreshCookie, Value: "tampered"})
		response := httptest.NewRecorder()
		login.refresh(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
		}
		responseCookie(t, response.Result().Cookies(), accessTokenCookie)
		responseCookie(t, response.Result().Cookies(), oidcRefreshCookie)
	})

	t.Run("logout clears session and uses provider endpoint", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "https://tma.example/auth/logout", nil)
		request.Header.Set("Origin", "https://tma.example")
		response := httptest.NewRecorder()
		login.logout(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
		}
		var payload map[string]string
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		logoutURL, err := url.Parse(payload["redirect_url"])
		if err != nil {
			t.Fatal(err)
		}
		if logoutURL.String() != "https://idp.example/logout?client_id=tma-web&post_logout_redirect_uri=https%3A%2F%2Ftma.example%2Fapp" {
			t.Fatalf("logout URL = %q", logoutURL.String())
		}
		responseCookie(t, response.Result().Cookies(), accessTokenCookie)
		responseCookie(t, response.Result().Cookies(), oidcRefreshCookie)
	})
}

func TestOIDCWebLoginCallbackAndRefreshEndToEnd(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const (
		keyID               = "oidc-web-login"
		transactionState    = "expected-state"
		transactionNonce    = "expected-nonce"
		transactionVerifier = "an-authorization-code-pkce-verifier-that-is-long-enough"
	)
	tokenRequests := make(chan url.Values, 2)
	var provider *httptest.Server
	var accessToken string
	var idToken string
	provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeJSON(w, http.StatusOK, map[string]any{
				"issuer": provider.URL, "jwks_uri": provider.URL + "/jwks",
				"authorization_endpoint":                provider.URL + "/authorize",
				"token_endpoint":                        provider.URL + "/token",
				"end_session_endpoint":                  provider.URL + "/logout",
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case "/jwks":
			writeJSON(w, http.StatusOK, map[string]any{"keys": []jose.JSONWebKey{{
				Key: &privateKey.PublicKey, KeyID: keyID, Algorithm: "RS256", Use: "sig",
			}}})
		case "/token":
			if err := r.ParseForm(); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
				return
			}
			form := make(url.Values, len(r.Form))
			for key, values := range r.Form {
				form[key] = append([]string(nil), values...)
			}
			tokenRequests <- form
			refreshToken := "refresh-2"
			response := map[string]any{
				"access_token": accessToken, "refresh_token": refreshToken,
				"token_type": "Bearer", "expires_in": 3600,
			}
			if form.Get("grant_type") == "authorization_code" {
				response["refresh_token"] = "refresh-1"
				response["id_token"] = idToken
			}
			writeJSON(w, http.StatusOK, response)
		default:
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		}
	}))
	t.Cleanup(provider.Close)

	now := time.Now()
	accessToken = signedOIDCTestToken(t, privateKey, keyID, "RS256", map[string]any{
		"iss": provider.URL, "aud": "tma-api", "sub": "web-user", "exp": now.Add(time.Hour).Unix(),
		"workspace_id": "wksp_web", "owner_id": "web-user", "roles": []string{"member"},
	})
	idToken = signedOIDCTestToken(t, privateKey, keyID, "RS256", map[string]any{
		"iss": provider.URL, "aud": "tma-web", "sub": "web-user", "exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(), "nonce": transactionNonce,
	})
	config := AuthConfig{
		Mode: AuthModeOIDC, OIDCIssuer: provider.URL, OIDCAudience: "tma-api",
		OIDCSigningAlgs: []string{"RS256"}, OIDCHTTPTimeout: 5 * time.Second,
		OIDCRefreshInterval: time.Hour, OIDCMaxStale: 24 * time.Hour,
		OIDCClaimMapping:    identity.DefaultOIDCClaimMapping(),
		OIDCWebLoginEnabled: true, OIDCWebClientID: "tma-web",
		OIDCWebRedirectURL:   "http://tma.example/auth/callback",
		OIDCWebPostLogoutURL: "http://tma.example/app",
		OIDCWebSessionSecret: strings.Repeat("s", 32),
		CookieTrustedOrigins: []string{"http://tma.example"},
	}
	authenticator, err := newIdentityAuthenticator(config)
	if err != nil {
		t.Fatal(err)
	}
	login, err := newOIDCWebLogin(config, authenticator)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := login.sealJSON(oidcLoginTransaction{
		State: transactionState, Nonce: transactionNonce, Verifier: transactionVerifier,
		ReturnTo: "/app#workspace", ExpiresAt: now.Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	callbackRequest := httptest.NewRequest(http.MethodGet, "http://tma.example/auth/callback?state="+transactionState+"&code=authorization-code", nil)
	callbackRequest.AddCookie(&http.Cookie{Name: oidcTransactionCookie, Value: transaction})
	callbackResponse := httptest.NewRecorder()

	login.callback(callbackResponse, callbackRequest)

	if callbackResponse.Code != http.StatusFound || callbackResponse.Header().Get("Location") != "/app#workspace" {
		t.Fatalf("unexpected callback response: status=%d location=%q body=%s", callbackResponse.Code, callbackResponse.Header().Get("Location"), callbackResponse.Body.String())
	}
	codeExchange := <-tokenRequests
	if codeExchange.Get("grant_type") != "authorization_code" || codeExchange.Get("code") != "authorization-code" || codeExchange.Get("code_verifier") != transactionVerifier {
		t.Fatalf("unexpected authorization code exchange: %#v", codeExchange)
	}
	callbackCookies := callbackResponse.Result().Cookies()
	accessCookie := responseCookie(t, callbackCookies, accessTokenCookie)
	refreshCookie := responseCookie(t, callbackCookies, oidcRefreshCookie)
	var storedRefresh string
	if err := login.openJSON(refreshCookie.Value, &storedRefresh); err != nil || storedRefresh != "refresh-1" {
		t.Fatalf("stored refresh token = %q, err=%v", storedRefresh, err)
	}
	principalRequest := httptest.NewRequest(http.MethodGet, "http://tma.example/v1/auth/me", nil)
	principalRequest.AddCookie(accessCookie)
	principal, err := authenticator.authenticate(principalRequest)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Subject != "web-user" || principal.WorkspaceID != "wksp_web" || !principal.HasRole(RoleMember) {
		t.Fatalf("unexpected authenticated principal: %#v", principal)
	}

	refreshRequest := httptest.NewRequest(http.MethodPost, "http://tma.example/auth/refresh", nil)
	refreshRequest.Header.Set("Origin", "http://tma.example")
	refreshRequest.AddCookie(refreshCookie)
	refreshResponse := httptest.NewRecorder()
	login.refresh(refreshResponse, refreshRequest)
	if refreshResponse.Code != http.StatusNoContent {
		t.Fatalf("refresh status = %d, body=%s", refreshResponse.Code, refreshResponse.Body.String())
	}
	refreshExchange := <-tokenRequests
	if refreshExchange.Get("grant_type") != "refresh_token" || refreshExchange.Get("refresh_token") != "refresh-1" {
		t.Fatalf("unexpected refresh exchange: %#v", refreshExchange)
	}
	rotatedRefresh := responseCookie(t, refreshResponse.Result().Cookies(), oidcRefreshCookie)
	if err := login.openJSON(rotatedRefresh.Value, &storedRefresh); err != nil || storedRefresh != "refresh-2" {
		t.Fatalf("rotated refresh token = %q, err=%v", storedRefresh, err)
	}
}

func newTestOIDCWebLogin(t *testing.T, secure bool) *oidcWebLogin {
	t.Helper()
	block, err := aes.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	return &oidcWebLogin{
		oauthConfig: oauth2.Config{
			ClientID:    "tma-web",
			RedirectURL: "https://tma.example/auth/callback",
			Endpoint: oauth2.Endpoint{
				AuthURL:  "https://idp.example/authorize",
				TokenURL: "https://idp.example/token",
			},
			Scopes: []string{"openid", "profile", "email"},
		},
		aead:          aead,
		secureCookies: secure,
		postLogoutURL: "https://tma.example/app",
		endSessionURL: "https://idp.example/logout",
		clientID:      "tma-web",
		authenticator: &identityAuthenticator{},
	}
}

func responseCookie(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("response cookie %q not found", name)
	return nil
}
