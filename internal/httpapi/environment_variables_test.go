package httpapi

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/envvars"
	"tiggy-manage-agent/internal/runner"
)

func TestEnvironmentVariableAPIStoresEncryptedValueAndNeverReturnsIt(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envvars.MasterKeyEnvironmentVariable, base64.StdEncoding.EncodeToString(key))
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, 0, nil), nil)

	put := httptest.NewRecorder()
	server.ServeHTTP(put, jsonRequest(t, http.MethodPut, "/v1/environment-variables/SERVICE_API_KEY", `{"value":"top-secret-value"}`))
	if put.Code != http.StatusOK {
		t.Fatalf("put variable: %d: %s", put.Code, put.Body.String())
	}
	if strings.Contains(put.Body.String(), "top-secret-value") {
		t.Fatal("put response exposed secret value")
	}
	if len(store.operatorAudits) != 1 || store.operatorAudits[0].Action != "environment_variable.put" || strings.Contains(string(store.operatorAudits[0].Details), "top-secret-value") {
		t.Fatalf("unexpected environment variable audit: %#v", store.operatorAudits)
	}
	record := store.environmentVariables["wksp_default"]["SERVICE_API_KEY"]
	if len(record.Ciphertext) == 0 || strings.Contains(string(record.Ciphertext), "top-secret-value") {
		t.Fatal("store did not persist ciphertext")
	}

	get := httptest.NewRecorder()
	server.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/v1/environment-variables", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("list variables: %d: %s", get.Code, get.Body.String())
	}
	if strings.Contains(get.Body.String(), "top-secret-value") || !strings.Contains(get.Body.String(), "SERVICE_API_KEY") {
		t.Fatalf("unexpected list response: %s", get.Body.String())
	}

	service, err := envvars.NewServiceFromEnvironment(store)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := service.Resolve(t.Context(), "wksp_default")
	if err != nil || resolved["SERVICE_API_KEY"] != "top-secret-value" {
		t.Fatalf("unexpected resolved environment: %#v: %v", resolved, err)
	}
}

func TestEnvironmentVariableAPIRequiresMasterKey(t *testing.T) {
	t.Setenv(envvars.MasterKeyEnvironmentVariable, "")
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, 0, nil), nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/environment-variables", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected service unavailable, got %d: %s", response.Code, response.Body.String())
	}
}

func jsonRequest(t *testing.T, method string, path string, body string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return request
}
