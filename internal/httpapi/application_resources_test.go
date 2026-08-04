package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"tiggy-manage-agent/internal/managedagents"
)

func TestBindApplicationResourceUsesAuthenticatedApplicationIdentity(t *testing.T) {
	request := httptest.NewRequest("POST", "/v2/agents", nil)
	request = request.WithContext(context.WithValue(request.Context(), principalContextKey{}, Principal{
		Subject: "service_identity:svc_000001", WorkspaceID: "wksp_000001",
		ServiceIdentityID: "svc_000001", AuthType: AuthTypeServiceCredential,
	}))
	appID := ""
	if err := bindApplicationResource(request, &appID); err != nil {
		t.Fatal(err)
	}
	if appID != "svc_000001" {
		t.Fatalf("app_id = %q, want authenticated application identity", appID)
	}
	appID = "svc_other"
	if err := bindApplicationResource(request, &appID); !errors.Is(err, managedagents.ErrForbidden) {
		t.Fatalf("cross-application app_id error = %v, want forbidden", err)
	}
}

func TestApplicationResourceFilterRequiresBothRequestedFields(t *testing.T) {
	if !matchesApplicationResource("svc_1", "agent/interviewer", "svc_1", "agent/interviewer") {
		t.Fatal("matching application resource was rejected")
	}
	if matchesApplicationResource("svc_1", "agent/interviewer", "svc_1", "agent/writer") {
		t.Fatal("mismatched external_ref was accepted")
	}
}
