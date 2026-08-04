package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/appmanifest"
)

type applicationManifestTestStore struct {
	*testStore
	input appmanifest.PublishInput
}

func (s *applicationManifestTestStore) PublishApplicationManifest(_ context.Context, input appmanifest.PublishInput) (appmanifest.PublishResult, error) {
	s.input = input
	return appmanifest.PublishResult{
		SchemaVersion: appmanifest.SchemaVersionV1, Revision: input.Manifest.Revision,
		Checksum: strings.Repeat("a", 64), Resources: []appmanifest.ResourceResult{},
	}, nil
}

func TestPublishApplicationManifestBindsAuthenticatedApplication(t *testing.T) {
	store := &applicationManifestTestStore{testStore: newTestStore()}
	server := &Server{store: store}
	request := httptest.NewRequest(http.MethodPost, "/v2/application-manifests/publish", strings.NewReader(`{
		"manifest":{"schema_version":"tma.application-manifest.v1","revision":"1","environments":[{"external_ref":"environment/default","name":"Default"}]}
	}`))
	request = request.WithContext(context.WithValue(request.Context(), principalContextKey{}, Principal{
		Subject: "service_identity:svc_app", WorkspaceID: "wksp_1", ServiceIdentityID: "svc_app", AuthType: AuthTypeServiceCredential,
	}))
	response := httptest.NewRecorder()
	server.publishApplicationManifest(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("publish status = %d body=%s", response.Code, response.Body.String())
	}
	if store.input.AppID != "svc_app" || store.input.WorkspaceID != "wksp_1" || store.input.Manifest.Revision != "1" {
		t.Fatalf("published input = %+v", store.input)
	}
}

func TestApplicationManifestServiceScope(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v2/application-manifests/publish", nil)
	scope, mapped := serviceIdentityScopeForRequest(request)
	if !mapped || scope != "applications:publish" {
		t.Fatalf("manifest scope = %q mapped=%t", scope, mapped)
	}
}

func TestPublishApplicationManifestRejectsDelegatedToken(t *testing.T) {
	store := &applicationManifestTestStore{testStore: newTestStore()}
	server := &Server{store: store}
	request := httptest.NewRequest(http.MethodPost, "/v2/application-manifests/publish", strings.NewReader(`{"manifest":{}}`))
	request = request.WithContext(context.WithValue(request.Context(), principalContextKey{}, Principal{
		Subject: "user_1", WorkspaceID: "wksp_1", ServiceIdentityID: "svc_app", AuthType: AuthTypeDelegated,
	}))
	response := httptest.NewRecorder()
	server.publishApplicationManifest(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("delegated publish status = %d body=%s", response.Code, response.Body.String())
	}
}
