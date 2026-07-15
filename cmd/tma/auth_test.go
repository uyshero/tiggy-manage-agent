package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type memoryCredentialStore struct {
	mu          sync.Mutex
	credentials map[string]storedCredential
}

func newMemoryCredentialStore() *memoryCredentialStore {
	return &memoryCredentialStore{credentials: map[string]storedCredential{}}
}

func (s *memoryCredentialStore) Load(baseURL string) (storedCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	credential, ok := s.credentials[normalizeBaseURL(baseURL)]
	if !ok {
		return storedCredential{}, errCredentialNotFound
	}
	return credential, nil
}

func (s *memoryCredentialStore) Save(baseURL string, credential storedCredential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.credentials[normalizeBaseURL(baseURL)] = credential
	return nil
}

func (s *memoryCredentialStore) Delete(baseURL string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := normalizeBaseURL(baseURL)
	if _, ok := s.credentials[key]; !ok {
		return errCredentialNotFound
	}
	delete(s.credentials, key)
	return nil
}

func TestAuthDeviceLoginStatusAndLogout(t *testing.T) {
	store := newMemoryCredentialStore()
	revoked := false
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/auth/config":
			fmt.Fprintf(w, `{"mode":"oidc","oidc":{"issuer":%q,"audience":"tma-api","client_id":"tma-cli","scopes":["openid","profile","email"],"device_authorization":true}}`, server.URL+"/oidc")
		case "/oidc/.well-known/openid-configuration":
			fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"device_authorization_endpoint":%q,"revocation_endpoint":%q,"jwks_uri":%q,"id_token_signing_alg_values_supported":["RS256"]}`,
				server.URL+"/oidc", server.URL+"/authorize", server.URL+"/token", server.URL+"/device", server.URL+"/revoke", server.URL+"/jwks")
		case "/device":
			if err := r.ParseForm(); err != nil || r.Form.Get("client_id") != "tma-cli" {
				t.Errorf("unexpected device request: form=%v err=%v", r.Form, err)
			}
			fmt.Fprintf(w, `{"device_code":"device-secret","user_code":"ABCD-EFGH","verification_uri":%q,"verification_uri_complete":%q,"expires_in":300,"interval":1}`,
				server.URL+"/verify", server.URL+"/verify?user_code=ABCD-EFGH")
		case "/token":
			if err := r.ParseForm(); err != nil || r.Form.Get("device_code") != "device-secret" {
				t.Errorf("unexpected token request: form=%v err=%v", r.Form, err)
			}
			fmt.Fprint(w, `{"access_token":"access-secret","refresh_token":"refresh-secret","token_type":"Bearer","expires_in":300}`)
		case "/v2/auth/me":
			if r.Header.Get("Authorization") != "Bearer access-secret" {
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(w, `{"error":{"code":"unauthorized","message":"invalid token","request_id":"req_auth","retryable":false}}`)
				return
			}
			fmt.Fprint(w, `{"authenticated":true,"principal":{"subject":"user_1","workspace_id":"wksp_default","owner_id":"user_1","roles":["operator"],"auth_type":"oidc"}}`)
		case "/revoke":
			if err := r.ParseForm(); err != nil || r.Form.Get("token") != "refresh-secret" || r.Form.Get("token_type_hint") != "refresh_token" || r.Form.Get("client_id") != "tma-cli" {
				t.Errorf("unexpected revocation request: form=%v err=%v", r.Form, err)
			}
			revoked = true
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected auth request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{}`)
		}
	}))
	defer server.Close()

	client := &apiClient{baseURL: server.URL, http: server.Client(), streamHTTP: server.Client(), credentials: store}
	output := captureStdout(t, func() {
		if err := commandAuthLogin(client, []string{"--no-browser", "--timeout", "5s"}); err != nil {
			t.Fatalf("auth login: %v", err)
		}
	})
	if strings.Contains(output, "access-secret") || strings.Contains(output, "refresh-secret") {
		t.Fatalf("auth login output leaked token: %s", output)
	}
	credential, err := store.Load(server.URL)
	if err != nil || credential.AccessToken != "access-secret" || credential.RefreshToken != "refresh-secret" || credential.ClientID != "tma-cli" {
		t.Fatalf("stored credential=%+v err=%v", credential, err)
	}
	status := captureStdout(t, func() {
		if err := commandAuthStatus(client); err != nil {
			t.Fatalf("auth status: %v", err)
		}
	})
	if !strings.Contains(status, `"authenticated": true`) || strings.Contains(status, "access-secret") || strings.Contains(status, "refresh-secret") {
		t.Fatalf("unexpected auth status: %s", status)
	}
	captureStdout(t, func() {
		if err := commandAuthLogout(client); err != nil {
			t.Fatalf("auth logout: %v", err)
		}
	})
	if _, err := store.Load(server.URL); !errors.Is(err, errCredentialNotFound) {
		t.Fatalf("credential still present after logout: %v", err)
	}
	if !revoked {
		t.Fatal("auth logout did not revoke the refresh token")
	}
}

func TestAuthLogoutRemovesLocalCredentialWhenRevocationFails(t *testing.T) {
	store := newMemoryCredentialStore()
	var provider *httptest.Server
	provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"revocation_endpoint":%q,"jwks_uri":%q,"id_token_signing_alg_values_supported":["RS256"]}`,
				provider.URL, provider.URL+"/authorize", provider.URL+"/token", provider.URL+"/revoke", provider.URL+"/jwks")
		case "/revoke":
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"error":"temporarily_unavailable"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer provider.Close()

	baseURL := "https://tma.example"
	if err := store.Save(baseURL, storedCredential{
		Issuer: provider.URL, ClientID: "tma-cli", RefreshToken: "refresh-secret",
	}); err != nil {
		t.Fatal(err)
	}
	client := &apiClient{baseURL: baseURL, http: provider.Client(), credentials: store}
	output := captureStdout(t, func() {
		if err := commandAuthLogout(client); err != nil {
			t.Fatalf("auth logout: %v", err)
		}
	})
	if !strings.Contains(output, `"logged_out": true`) || !strings.Contains(output, "remote token revocation failed") {
		t.Fatalf("unexpected auth logout output: %s", output)
	}
	if strings.Contains(output, "refresh-secret") {
		t.Fatalf("auth logout output leaked token: %s", output)
	}
	if _, err := store.Load(baseURL); !errors.Is(err, errCredentialNotFound) {
		t.Fatalf("credential still present after failed revocation: %v", err)
	}
}

func TestKeyringTokenSourceRefreshesAndPersists(t *testing.T) {
	store := newMemoryCredentialStore()
	var provider *httptest.Server
	provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"jwks_uri":%q,"id_token_signing_alg_values_supported":["RS256"]}`,
				provider.URL, provider.URL+"/authorize", provider.URL+"/token", provider.URL+"/jwks")
		case "/token":
			if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh-secret" {
				t.Errorf("unexpected refresh request: form=%v err=%v", r.Form, err)
			}
			fmt.Fprint(w, `{"access_token":"refreshed-secret","refresh_token":"rotated-secret","token_type":"Bearer","expires_in":300}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{}`)
		}
	}))
	defer provider.Close()

	baseURL := "https://tma.example"
	if err := store.Save(baseURL, storedCredential{
		Version: credentialVersion, BaseURL: baseURL, Issuer: provider.URL,
		Audience: "tma-api", ClientID: "tma-cli", Scopes: []string{"openid"},
		AccessToken: "expired-secret", RefreshToken: "refresh-secret", TokenType: "Bearer", Expiry: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	source := &keyringTokenSource{baseURL: baseURL, store: store, httpClient: provider.Client()}
	token, err := source.Token(t.Context())
	if err != nil || token != "refreshed-secret" {
		t.Fatalf("refreshed token=%q err=%v", token, err)
	}
	credential, err := store.Load(baseURL)
	if err != nil || credential.AccessToken != "refreshed-secret" || credential.RefreshToken != "rotated-secret" {
		t.Fatalf("updated credential=%+v err=%v", credential, err)
	}
}
