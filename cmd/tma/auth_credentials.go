package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"
)

const credentialVersion = 1
const credentialKeyringService = "tma-cli"

var errCredentialNotFound = errors.New("tma: stored credential not found")

type credentialStore interface {
	Load(string) (storedCredential, error)
	Save(string, storedCredential) error
	Delete(string) error
}

type keyringCredentialStore struct{}

type storedCredential struct {
	Version      int       `json:"version"`
	BaseURL      string    `json:"base_url"`
	Issuer       string    `json:"issuer"`
	Audience     string    `json:"audience,omitempty"`
	ClientID     string    `json:"client_id"`
	Scopes       []string  `json:"scopes,omitempty"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"`
}

func (keyringCredentialStore) Load(baseURL string) (storedCredential, error) {
	encoded, err := keyring.Get(credentialKeyringService, credentialAccount(baseURL))
	if errors.Is(err, keyring.ErrNotFound) {
		return storedCredential{}, errCredentialNotFound
	}
	if err != nil {
		return storedCredential{}, fmt.Errorf("tma: read credential from system keychain: %w", err)
	}
	var credential storedCredential
	if err := json.Unmarshal([]byte(encoded), &credential); err != nil {
		return storedCredential{}, fmt.Errorf("tma: decode stored credential: %w", err)
	}
	if credential.Version != credentialVersion || strings.TrimSpace(credential.BaseURL) != normalizeBaseURL(baseURL) {
		return storedCredential{}, errors.New("tma: stored credential has an unsupported format")
	}
	return credential, nil
}

func (keyringCredentialStore) Save(baseURL string, credential storedCredential) error {
	credential.Version = credentialVersion
	credential.BaseURL = normalizeBaseURL(baseURL)
	encoded, err := json.Marshal(credential)
	if err != nil {
		return fmt.Errorf("tma: encode credential: %w", err)
	}
	if err := keyring.Set(credentialKeyringService, credentialAccount(baseURL), string(encoded)); err != nil {
		return fmt.Errorf("tma: save credential in system keychain: %w", err)
	}
	return nil
}

func (keyringCredentialStore) Delete(baseURL string) error {
	err := keyring.Delete(credentialKeyringService, credentialAccount(baseURL))
	if errors.Is(err, keyring.ErrNotFound) {
		return errCredentialNotFound
	}
	if err != nil {
		return fmt.Errorf("tma: delete credential from system keychain: %w", err)
	}
	return nil
}

func credentialAccount(baseURL string) string {
	digest := sha256.Sum256([]byte(normalizeBaseURL(baseURL)))
	return hex.EncodeToString(digest[:])
}

func normalizeBaseURL(baseURL string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

func (c storedCredential) oauthToken() *oauth2.Token {
	return &oauth2.Token{
		AccessToken: c.AccessToken, RefreshToken: c.RefreshToken,
		TokenType: c.TokenType, Expiry: c.Expiry,
	}
}

func storedCredentialFromToken(baseURL string, configuration authOIDCConfiguration, token *oauth2.Token) storedCredential {
	return storedCredential{
		Version: credentialVersion, BaseURL: normalizeBaseURL(baseURL),
		Issuer: configuration.Issuer, Audience: configuration.Audience,
		ClientID: configuration.ClientID, Scopes: append([]string(nil), configuration.Scopes...),
		AccessToken: token.AccessToken, RefreshToken: token.RefreshToken,
		TokenType: token.TokenType, Expiry: token.Expiry,
	}
}

type authOIDCConfiguration struct {
	Issuer   string
	Audience string
	ClientID string
	Scopes   []string
}

type keyringTokenSource struct {
	mu         sync.Mutex
	baseURL    string
	store      credentialStore
	httpClient *http.Client
}

func (s *keyringTokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	credential, err := s.store.Load(s.baseURL)
	if errors.Is(err, errCredentialNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(credential.AccessToken) != "" && (credential.Expiry.IsZero() || time.Until(credential.Expiry) > time.Minute) {
		return credential.AccessToken, nil
	}
	if strings.TrimSpace(credential.RefreshToken) == "" {
		return "", errors.New("tma: stored access token expired; run `tma auth login`")
	}
	providerContext := ctx
	if s.httpClient != nil {
		providerContext = oidc.ClientContext(ctx, s.httpClient)
	}
	provider, err := oidc.NewProvider(providerContext, credential.Issuer)
	if err != nil {
		return "", fmt.Errorf("tma: discover OIDC provider for token refresh: %w", err)
	}
	configuration := oauth2.Config{ClientID: credential.ClientID, Endpoint: provider.Endpoint(), Scopes: credential.Scopes}
	tokenContext := ctx
	if s.httpClient != nil {
		tokenContext = context.WithValue(ctx, oauth2.HTTPClient, s.httpClient)
	}
	token, err := configuration.TokenSource(tokenContext, credential.oauthToken()).Token()
	if err != nil {
		return "", fmt.Errorf("tma: refresh OIDC token; run `tma auth login` if the session was revoked: %w", err)
	}
	updated := storedCredentialFromToken(s.baseURL, authOIDCConfiguration{
		Issuer: credential.Issuer, Audience: credential.Audience,
		ClientID: credential.ClientID, Scopes: credential.Scopes,
	}, token)
	if updated.RefreshToken == "" {
		updated.RefreshToken = credential.RefreshToken
	}
	if err := s.store.Save(s.baseURL, updated); err != nil {
		return "", err
	}
	return token.AccessToken, nil
}
