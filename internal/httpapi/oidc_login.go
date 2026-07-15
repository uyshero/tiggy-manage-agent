package httpapi

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	oidcTransactionCookie = "tma_oidc_transaction"
	oidcRefreshCookie     = "tma_refresh_token"
	oidcTransactionTTL    = 5 * time.Minute
)

type oidcWebLogin struct {
	oauthConfig     oauth2.Config
	idTokenVerifier *oidc.IDTokenVerifier
	aead            cipher.AEAD
	secureCookies   bool
	postLogoutURL   string
	endSessionURL   string
	clientID        string
	authenticator   *identityAuthenticator
}

type oidcLoginTransaction struct {
	State     string `json:"state"`
	Nonce     string `json:"nonce"`
	Verifier  string `json:"verifier"`
	ReturnTo  string `json:"return_to"`
	ExpiresAt int64  `json:"expires_at"`
}

func newOIDCWebLogin(config AuthConfig, authenticator *identityAuthenticator) (*oidcWebLogin, error) {
	if !config.OIDCWebLoginEnabled {
		return nil, nil
	}
	if config.Mode != AuthModeOIDC {
		return nil, errors.New("browser OIDC login requires OIDC authentication mode")
	}
	if strings.TrimSpace(config.OIDCWebClientID) == "" || strings.TrimSpace(config.OIDCWebRedirectURL) == "" {
		return nil, errors.New("browser OIDC login requires client ID and redirect URL")
	}
	if len(config.OIDCWebSessionSecret) < 32 {
		return nil, errors.New("browser OIDC login session secret must be at least 32 bytes")
	}
	redirectURL, err := url.Parse(config.OIDCWebRedirectURL)
	if err != nil || redirectURL.Scheme == "" || redirectURL.Host == "" {
		return nil, errors.New("browser OIDC redirect URL is invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), config.OIDCHTTPTimeout)
	defer cancel()
	provider, err := oidc.NewProvider(ctx, strings.TrimSpace(config.OIDCIssuer))
	if err != nil {
		return nil, fmt.Errorf("browser OIDC discovery failed: %w", err)
	}
	var metadata struct {
		EndSessionEndpoint string `json:"end_session_endpoint"`
	}
	_ = provider.Claims(&metadata)
	key := sha256.Sum256([]byte(config.OIDCWebSessionSecret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("create browser OIDC session cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create browser OIDC session AEAD: %w", err)
	}
	postLogoutURL := strings.TrimSpace(config.OIDCWebPostLogoutURL)
	if postLogoutURL == "" {
		postLogoutURL = redirectURL.Scheme + "://" + redirectURL.Host + "/app"
	}
	return &oidcWebLogin{
		oauthConfig: oauth2.Config{
			ClientID:     strings.TrimSpace(config.OIDCWebClientID),
			ClientSecret: strings.TrimSpace(config.OIDCWebClientSecret),
			Endpoint:     provider.Endpoint(),
			RedirectURL:  redirectURL.String(),
			Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
		},
		idTokenVerifier: provider.Verifier(&oidc.Config{ClientID: strings.TrimSpace(config.OIDCWebClientID)}),
		aead:            aead,
		secureCookies:   strings.EqualFold(redirectURL.Scheme, "https"),
		postLogoutURL:   postLogoutURL,
		endSessionURL:   strings.TrimSpace(metadata.EndSessionEndpoint),
		clientID:        strings.TrimSpace(config.OIDCWebClientID),
		authenticator:   authenticator,
	}, nil
}

func (l *oidcWebLogin) login(w http.ResponseWriter, r *http.Request) {
	state, err := randomURLToken(32)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create login state"})
		return
	}
	nonce, err := randomURLToken(32)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create login nonce"})
		return
	}
	verifier := oauth2.GenerateVerifier()
	transaction := oidcLoginTransaction{
		State: state, Nonce: nonce, Verifier: verifier,
		ReturnTo:  safeOIDCReturnTo(r.URL.Query().Get("return_to")),
		ExpiresAt: time.Now().Add(oidcTransactionTTL).Unix(),
	}
	encoded, err := l.sealJSON(transaction)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create login transaction"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: oidcTransactionCookie, Value: encoded, Path: "/auth/callback",
		HttpOnly: true, Secure: l.secureCookies, SameSite: http.SameSiteLaxMode,
		MaxAge: int(oidcTransactionTTL.Seconds()),
	})
	authorizationURL := l.oauthConfig.AuthCodeURL(
		state,
		oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("nonce", nonce),
	)
	http.Redirect(w, r, authorizationURL, http.StatusFound)
}

func (l *oidcWebLogin) callback(w http.ResponseWriter, r *http.Request) {
	if providerError := strings.TrimSpace(r.URL.Query().Get("error")); providerError != "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "OIDC login rejected: " + providerError})
		return
	}
	cookie, err := r.Cookie(oidcTransactionCookie)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "OIDC login transaction is missing"})
		return
	}
	var transaction oidcLoginTransaction
	if err := l.openJSON(cookie.Value, &transaction); err != nil || transaction.ExpiresAt < time.Now().Unix() {
		l.clearCookie(w, oidcTransactionCookie, "/auth/callback")
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "OIDC login transaction is invalid or expired"})
		return
	}
	providedState := r.URL.Query().Get("state")
	if len(providedState) != len(transaction.State) || subtle.ConstantTimeCompare([]byte(providedState), []byte(transaction.State)) != 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "OIDC login state does not match"})
		return
	}
	token, err := l.oauthConfig.Exchange(r.Context(), r.URL.Query().Get("code"), oauth2.VerifierOption(transaction.Verifier))
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "OIDC authorization code exchange failed"})
		return
	}
	rawIDToken, _ := token.Extra("id_token").(string)
	idToken, err := l.idTokenVerifier.Verify(r.Context(), rawIDToken)
	if err != nil || idToken.Nonce != transaction.Nonce {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "OIDC ID token validation failed"})
		return
	}
	if _, err := l.authenticator.authenticateOIDC(r.Context(), token.AccessToken); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	if err := l.setTokenCookies(w, token); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "persist OIDC session"})
		return
	}
	l.clearCookie(w, oidcTransactionCookie, "/auth/callback")
	http.Redirect(w, r, safeOIDCReturnTo(transaction.ReturnTo), http.StatusFound)
}

func (l *oidcWebLogin) refresh(w http.ResponseWriter, r *http.Request) {
	if err := validateBrowserWriteOrigin(r, l.authenticator.config.CookieTrustedOrigins); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	cookie, err := r.Cookie(oidcRefreshCookie)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "OIDC refresh session is missing"})
		return
	}
	var refreshToken string
	if err := l.openJSON(cookie.Value, &refreshToken); err != nil || strings.TrimSpace(refreshToken) == "" {
		l.clearSessionCookies(w)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "OIDC refresh session is invalid"})
		return
	}
	token, err := l.oauthConfig.TokenSource(r.Context(), &oauth2.Token{
		RefreshToken: refreshToken,
		Expiry:       time.Unix(1, 0),
	}).Token()
	if err != nil {
		l.clearSessionCookies(w)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "OIDC session refresh failed"})
		return
	}
	if _, err := l.authenticator.authenticateOIDC(r.Context(), token.AccessToken); err != nil {
		l.clearSessionCookies(w)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "refreshed OIDC access token was rejected"})
		return
	}
	if err := l.setTokenCookies(w, token); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "persist refreshed OIDC session"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (l *oidcWebLogin) logout(w http.ResponseWriter, r *http.Request) {
	if err := validateBrowserWriteOrigin(r, l.authenticator.config.CookieTrustedOrigins); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	l.clearSessionCookies(w)
	redirectURL := l.postLogoutURL
	if l.endSessionURL != "" {
		parsed, err := url.Parse(l.endSessionURL)
		if err == nil {
			query := parsed.Query()
			query.Set("client_id", l.clientID)
			query.Set("post_logout_redirect_uri", l.postLogoutURL)
			parsed.RawQuery = query.Encode()
			redirectURL = parsed.String()
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"redirect_url": redirectURL})
}

func (l *oidcWebLogin) setTokenCookies(w http.ResponseWriter, token *oauth2.Token) error {
	if token == nil || strings.TrimSpace(token.AccessToken) == "" {
		return errors.New("OIDC access token is missing")
	}
	maxAge := int(time.Until(token.Expiry).Seconds())
	if maxAge < 1 {
		maxAge = 300
	}
	http.SetCookie(w, &http.Cookie{
		Name: accessTokenCookie, Value: token.AccessToken, Path: "/",
		HttpOnly: true, Secure: l.secureCookies, SameSite: http.SameSiteLaxMode, MaxAge: maxAge,
	})
	refreshToken := strings.TrimSpace(token.RefreshToken)
	if refreshToken == "" {
		return nil
	}
	sealedRefresh, err := l.sealJSON(refreshToken)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: oidcRefreshCookie, Value: sealedRefresh, Path: "/auth",
		HttpOnly: true, Secure: l.secureCookies, SameSite: http.SameSiteLaxMode,
		MaxAge: int((30 * 24 * time.Hour).Seconds()),
	})
	return nil
}

func (l *oidcWebLogin) clearSessionCookies(w http.ResponseWriter) {
	l.clearCookie(w, accessTokenCookie, "/")
	l.clearCookie(w, oidcRefreshCookie, "/auth")
}

func (l *oidcWebLogin) clearCookie(w http.ResponseWriter, name string, path string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: path, HttpOnly: true, Secure: l.secureCookies,
		SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0),
	})
}

func (l *oidcWebLogin) sealJSON(value any) (string, error) {
	plaintext, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, l.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := l.aead.Seal(nonce, nonce, plaintext, nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (l *oidcWebLogin) openJSON(encoded string, target any) error {
	sealed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(sealed) < l.aead.NonceSize() {
		return errors.New("invalid encrypted session")
	}
	nonce, ciphertext := sealed[:l.aead.NonceSize()], sealed[l.aead.NonceSize():]
	plaintext, err := l.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return errors.New("invalid encrypted session")
	}
	return json.Unmarshal(plaintext, target)
}

func randomURLToken(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func safeOIDCReturnTo(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "/app"
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || !strings.HasPrefix(parsed.Path, "/") || strings.HasPrefix(raw, "//") {
		return "/app"
	}
	returnTo := parsed.RequestURI()
	if parsed.Fragment != "" {
		returnTo += "#" + parsed.EscapedFragment()
	}
	return returnTo
}

func validateBrowserWriteOrigin(r *http.Request, trustedOrigins []string) error {
	origin, err := normalizedRequestOrigin(r.Header.Get("Origin"))
	if err != nil {
		return errors.New("browser authentication write requires a valid Origin header")
	}
	if len(trustedOrigins) == 0 {
		if strings.EqualFold(origin.Host, strings.TrimSpace(r.Host)) {
			return nil
		}
		return errors.New("browser authentication write origin is not trusted")
	}
	for _, trusted := range trustedOrigins {
		parsed, parseErr := normalizedRequestOrigin(trusted)
		if parseErr == nil && parsed.String() == origin.String() {
			return nil
		}
	}
	return errors.New("browser authentication write origin is not trusted")
}
