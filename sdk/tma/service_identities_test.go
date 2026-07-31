package tma

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServiceIdentityServiceLifecycle(t *testing.T) {
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.EscapedPath())
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v2/service-identities/scopes":
			fmt.Fprint(w, `{"scopes":["agents:read","agents:write"]}`)
		case r.URL.Path == "/v2/service-identities" && r.Method == http.MethodGet:
			fmt.Fprint(w, `{"service_identities":[]}`)
		case r.URL.Path == "/v2/service-identities" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, serviceIdentitySDKFixture)
		case strings.HasSuffix(r.URL.Path, "/credentials/cred/1"):
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/credentials") && r.Method == http.MethodGet:
			fmt.Fprint(w, `{"credentials":[]}`)
		case strings.HasSuffix(r.URL.Path, "/credentials") && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"credential":{"id":"cred/1","workspace_id":"wksp_1","service_identity_id":"svc/1","name":"deployment","token_prefix":"tma_svc_abcd","status":"active","created_by":"admin","created_at":"2026-07-31T00:00:00Z"},"token":"tma_svc_locator.secret"}`)
		default:
			fmt.Fprint(w, serviceIdentitySDKFixture)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if scopes, err := client.ServiceIdentities.Scopes(ctx); err != nil || len(scopes) != 2 {
		t.Fatalf("scopes=%v err=%v", scopes, err)
	}
	if _, err := client.ServiceIdentities.List(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ServiceIdentities.Create(ctx, CreateServiceIdentityRequest{Name: "knowledge", Scopes: []string{"agents:read"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ServiceIdentities.Get(ctx, "svc/1"); err != nil {
		t.Fatal(err)
	}
	status := "disabled"
	if _, err := client.ServiceIdentities.Update(ctx, "svc/1", UpdateServiceIdentityRequest{Status: &status}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ServiceIdentities.Credentials(ctx, "svc/1"); err != nil {
		t.Fatal(err)
	}
	created, err := client.ServiceIdentities.CreateCredential(ctx, "svc/1", CreateServiceCredentialRequest{Name: "deployment"})
	if err != nil || created.Token != "tma_svc_locator.secret" {
		t.Fatalf("created credential=%+v err=%v", created, err)
	}
	if err := client.ServiceIdentities.RevokeCredential(ctx, "svc/1", "cred/1"); err != nil {
		t.Fatal(err)
	}
	for _, request := range requests {
		if strings.Contains(request, "svc/1") || strings.Contains(request, "cred/1") {
			t.Fatalf("service identity identifier was not escaped: %s", request)
		}
	}
}

const serviceIdentitySDKFixture = `{"id":"svc/1","workspace_id":"wksp_1","kind":"application","name":"knowledge","role":"member","scopes":["agents:read"],"status":"active","created_by":"admin","created_at":"2026-07-31T00:00:00Z","updated_at":"2026-07-31T00:00:00Z"}`
