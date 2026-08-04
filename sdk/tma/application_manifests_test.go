package tma

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestApplicationManifestsPublish(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/application-manifests/publish" {
			t.Fatalf("unexpected manifest request %s %s", r.Method, r.URL.Path)
		}
		var request PublishApplicationManifestRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.AppID != "svc/app" || request.Manifest.SchemaVersion != ApplicationManifestSchemaV1 ||
			request.Manifest.Environments[0].ExternalRef != "environment/default" || request.Manifest.Agents[0].EnvironmentRef != "environment/default" {
			t.Fatalf("unexpected manifest payload: %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"schema_version":"tma.application-manifest.v1","revision":"1","checksum_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","resources":[{"type":"environment","external_ref":"environment/default","resource_id":"env_1","status":"created"}]}`)
	}))
	defer server.Close()
	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.ApplicationManifests.Publish(t.Context(), PublishApplicationManifestRequest{
		AppID: "svc/app",
		Manifest: ApplicationManifest{
			SchemaVersion: ApplicationManifestSchemaV1, Revision: "1",
			Environments: []ApplicationManifestEnvironment{{ExternalRef: "environment/default", Name: "Default", Config: json.RawMessage(`{}`)}},
			Agents:       []ApplicationManifestAgent{{ExternalRef: "agent/main", EnvironmentRef: "environment/default", Name: "Main", LLMModel: "fake-demo", System: "Assist"}},
		},
	})
	if err != nil || len(result.Resources) != 1 || result.Resources[0].Status != "created" {
		t.Fatalf("manifest result = %+v err=%v", result, err)
	}
}
