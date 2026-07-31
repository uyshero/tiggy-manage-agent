package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"tiggy-manage-agent/internal/managedagents"
)

const (
	delegatedTokenPrefix = "tma_obo_"
	delegatedTokenType   = "TMA-OBO+JWT"
	accessTokenTypeURN   = "urn:ietf:params:oauth:token-type:access_token"
)

type tokenExchangeRequest struct {
	GrantType          string `json:"grant_type"`
	SubjectToken       string `json:"subject_token"`
	SubjectTokenType   string `json:"subject_token_type"`
	RequestedTokenType string `json:"requested_token_type,omitempty"`
	Scope              string `json:"scope"`
}

type tokenExchangeResponse struct {
	AccessToken     string `json:"access_token"`
	IssuedTokenType string `json:"issued_token_type"`
	TokenType       string `json:"token_type"`
	ExpiresIn       int64  `json:"expires_in"`
	Scope           string `json:"scope"`
}

type delegatedClaims struct {
	Subject             string   `json:"sub"`
	Username            string   `json:"preferred_username,omitempty"`
	Issuer              string   `json:"iss"`
	Audience            string   `json:"aud"`
	ExpiresAt           int64    `json:"exp"`
	IssuedAt            int64    `json:"iat"`
	NotBefore           int64    `json:"nbf"`
	JWTID               string   `json:"jti"`
	TokenUse            string   `json:"token_use"`
	OrganizationID      string   `json:"organization_id,omitempty"`
	WorkspaceID         string   `json:"workspace_id"`
	OwnerID             string   `json:"owner_id"`
	Roles               []string `json:"roles"`
	Scopes              []string `json:"scopes"`
	ServiceIdentityID   string   `json:"service_identity_id"`
	ServiceCredentialID string   `json:"service_credential_id"`
}

func (s *Server) exchangeDelegatedToken(w http.ResponseWriter, r *http.Request) {
	actor, ok := PrincipalFromRequest(r)
	if !ok || actor.AuthType != AuthTypeServiceCredential || actor.ServiceIdentityID == "" || actor.ServiceCredentialID == "" {
		s.auditAuthorizationDecision(r, actor, "denied", "token_exchange_service_identity_required", "", nil)
		writeV2Error(w, requestIDFromRequest(r), http.StatusForbidden, "service_identity_required", "service identity credential required", false, nil)
		return
	}
	if strings.TrimSpace(s.authenticator.config.DelegationSigningSecret) == "" {
		writeV2Error(w, requestIDFromRequest(r), http.StatusServiceUnavailable, "delegation_unavailable", "delegated token exchange is not configured", false, nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var input tokenExchangeRequest
	if err := decodeJSON(r, &input); err != nil {
		writeV2Error(w, requestIDFromRequest(r), http.StatusBadRequest, "invalid_request", err.Error(), false, nil)
		return
	}
	if strings.TrimSpace(input.GrantType) != "urn:ietf:params:oauth:grant-type:token-exchange" ||
		strings.TrimSpace(input.SubjectTokenType) != accessTokenTypeURN ||
		(input.RequestedTokenType != "" && strings.TrimSpace(input.RequestedTokenType) != accessTokenTypeURN) {
		writeV2Error(w, requestIDFromRequest(r), http.StatusBadRequest, "invalid_request", "unsupported token exchange parameters", false, nil)
		return
	}
	user, err := s.authenticateExchangeSubject(r.Context(), strings.TrimSpace(input.SubjectToken))
	if err != nil {
		s.auditAuthorizationDecision(r, actor, "denied", "token_exchange_subject_rejected", "", err)
		writeV2Error(w, requestIDFromRequest(r), http.StatusUnauthorized, "invalid_subject_token", "subject token rejected", false, nil)
		return
	}
	if user.WorkspaceID != actor.WorkspaceID {
		s.auditAuthorizationDecision(r, actor, "denied", "token_exchange_workspace_mismatch", "", nil)
		writeV2Error(w, requestIDFromRequest(r), http.StatusForbidden, "workspace_mismatch", "service identity and user must belong to the same workspace", false, nil)
		return
	}
	requestedScopes := normalizedStringList(strings.Fields(input.Scope))
	if len(requestedScopes) == 0 {
		writeV2Error(w, requestIDFromRequest(r), http.StatusBadRequest, "invalid_scope", "scope is required", false, nil)
		return
	}
	for _, scope := range requestedScopes {
		if !actor.HasScope(scope) {
			s.auditAuthorizationDecision(r, actor, "denied", "token_exchange_scope_denied", scope, nil)
			writeV2Error(w, requestIDFromRequest(r), http.StatusForbidden, "invalid_scope", "requested scope exceeds service identity grants", false, map[string]any{"scope": scope})
			return
		}
	}
	token, claims, err := s.issueDelegatedToken(user, actor, requestedScopes)
	if err != nil {
		writeV2Error(w, requestIDFromRequest(r), http.StatusInternalServerError, "token_issue_failed", "delegated token could not be issued", true, nil)
		return
	}
	auditPrincipal := user
	auditPrincipal.AuthType = AuthTypeDelegated
	auditPrincipal.ServiceIdentityID = actor.ServiceIdentityID
	auditPrincipal.ServiceCredentialID = actor.ServiceCredentialID
	auditPrincipal.DelegationID = claims.JWTID
	auditPrincipal.Scopes = requestedScopes
	auditPrincipal.AuthorizationSources = append(auditPrincipal.AuthorizationSources, "token_exchange")
	s.auditAuthorizationDecision(r, auditPrincipal, "allowed", "token_exchange", strings.Join(requestedScopes, " "), nil)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, tokenExchangeResponse{
		AccessToken: token, IssuedTokenType: accessTokenTypeURN, TokenType: "Bearer",
		ExpiresIn: int64(s.authenticator.config.DelegationTTL / time.Second), Scope: strings.Join(requestedScopes, " "),
	})
}

func (s *Server) authenticateExchangeSubject(ctx context.Context, token string) (Principal, error) {
	if strings.HasPrefix(token, delegatedTokenPrefix) || strings.HasPrefix(token, serviceCredentialPrefix) {
		return Principal{}, errors.New("subject token must represent an end user")
	}
	var principal Principal
	var err error
	switch s.authenticator.config.Mode {
	case AuthModeJWT:
		principal, err = s.authenticator.authenticateJWT(token)
	case AuthModeOIDC:
		principal, err = s.authenticator.authenticateOIDC(ctx, token)
	default:
		return Principal{}, errors.New("token exchange requires jwt or oidc user authentication")
	}
	if err != nil {
		return Principal{}, err
	}
	principal, err = s.resolveWorkspaceMembership(ctx, principal)
	if err != nil {
		return Principal{}, err
	}
	if !principal.HasRole(RoleViewer) {
		return Principal{}, errors.New("active user workspace membership required")
	}
	return principal, nil
}

func (s *Server) issueDelegatedToken(user, actor Principal, scopes []string) (string, delegatedClaims, error) {
	now := time.Now().UTC()
	jtiBytes := make([]byte, 16)
	if _, err := rand.Read(jtiBytes); err != nil {
		return "", delegatedClaims{}, err
	}
	claims := delegatedClaims{
		Subject: user.Subject, Username: user.Username, Issuer: strings.TrimSpace(s.authenticator.config.DelegationIssuer),
		Audience: strings.TrimSpace(s.authenticator.config.DelegationAudience), ExpiresAt: now.Add(s.authenticator.config.DelegationTTL).Unix(),
		IssuedAt: now.Unix(), NotBefore: now.Add(-5 * time.Second).Unix(), JWTID: hex.EncodeToString(jtiBytes), TokenUse: "delegation",
		OrganizationID: user.OrganizationID, WorkspaceID: user.WorkspaceID, OwnerID: user.OwnerID, Roles: normalizedStringList(user.Roles),
		Scopes: normalizedStringList(scopes), ServiceIdentityID: actor.ServiceIdentityID, ServiceCredentialID: actor.ServiceCredentialID,
	}
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": delegatedTokenType})
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", delegatedClaims{}, err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(s.authenticator.config.DelegationSigningSecret))
	_, _ = mac.Write([]byte(unsigned))
	return delegatedTokenPrefix + unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), claims, nil
}

func (s *Server) authenticateDelegatedToken(r *http.Request) (Principal, bool, error) {
	token := bearerToken(r.Header.Get("Authorization"))
	if !strings.HasPrefix(token, delegatedTokenPrefix) {
		return Principal{}, false, nil
	}
	if s == nil || s.authenticator == nil || strings.TrimSpace(s.authenticator.config.DelegationSigningSecret) == "" {
		return Principal{}, true, errors.New("delegated token authentication is unavailable")
	}
	claims, err := verifyDelegatedToken(strings.TrimPrefix(token, delegatedTokenPrefix), s.authenticator.config)
	if err != nil {
		return Principal{}, true, err
	}
	principal, err := normalizePrincipal(Principal{
		Subject: claims.Subject, Username: claims.Username, OrganizationID: claims.OrganizationID,
		WorkspaceID: claims.WorkspaceID, OwnerID: claims.OwnerID, Roles: claims.Roles,
		ServiceIdentityID: claims.ServiceIdentityID, ServiceCredentialID: claims.ServiceCredentialID,
		DelegationID: claims.JWTID, Scopes: claims.Scopes, AuthType: AuthTypeDelegated,
		AuthorizationSources: []string{"delegated_token", "token_exchange", "service_scope"},
	})
	if err != nil {
		return Principal{}, true, err
	}
	if err := s.validateDelegatedActor(r.Context(), principal); err != nil {
		return Principal{}, true, err
	}
	return principal, true, nil
}

func verifyDelegatedToken(token string, config AuthConfig) (delegatedClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return delegatedClaims{}, errors.New("invalid delegated token")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return delegatedClaims{}, errors.New("invalid delegated token header")
	}
	var header struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil || header.Algorithm != "HS256" || header.Type != delegatedTokenType {
		return delegatedClaims{}, errors.New("invalid delegated token header")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return delegatedClaims{}, errors.New("invalid delegated token signature")
	}
	mac := hmac.New(sha256.New, []byte(config.DelegationSigningSecret))
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	expected := mac.Sum(nil)
	if len(signature) != len(expected) || subtle.ConstantTimeCompare(signature, expected) != 1 {
		return delegatedClaims{}, errors.New("invalid delegated token signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return delegatedClaims{}, errors.New("invalid delegated token claims")
	}
	var claims delegatedClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return delegatedClaims{}, errors.New("invalid delegated token claims")
	}
	now := time.Now().Unix()
	if claims.TokenUse != "delegation" || claims.ExpiresAt == 0 || now >= claims.ExpiresAt || now < claims.NotBefore ||
		claims.Issuer != strings.TrimSpace(config.DelegationIssuer) || claims.Audience != strings.TrimSpace(config.DelegationAudience) ||
		claims.JWTID == "" || claims.ServiceIdentityID == "" || claims.ServiceCredentialID == "" || len(claims.Scopes) == 0 {
		return delegatedClaims{}, errors.New("delegated token claims rejected")
	}
	return claims, nil
}

func (s *Server) validateDelegatedActor(ctx context.Context, principal Principal) error {
	store, ok := s.store.(managedagents.ServiceIdentityStore)
	if !ok {
		return errors.New("delegated token actor validation is unavailable")
	}
	identity, err := store.GetServiceIdentity(ctx, principal.WorkspaceID, principal.ServiceIdentityID)
	if err != nil || identity.Status != managedagents.ServiceIdentityStatusActive {
		return errors.New("delegated token actor is inactive")
	}
	for _, scope := range principal.Scopes {
		granted := false
		for _, current := range identity.Scopes {
			if scope == current {
				granted = true
				break
			}
		}
		if !granted {
			return fmt.Errorf("delegated scope %q is no longer granted", scope)
		}
	}
	credentials, err := store.ListServiceCredentials(ctx, principal.WorkspaceID, principal.ServiceIdentityID)
	if err != nil {
		return errors.New("delegated token credential validation failed")
	}
	for _, credential := range credentials {
		if credential.ID == principal.ServiceCredentialID && credential.Status == managedagents.ServiceCredentialStatusActive &&
			(credential.ExpiresAt == nil || credential.ExpiresAt.After(time.Now())) {
			return nil
		}
	}
	return errors.New("delegated token credential is inactive")
}
