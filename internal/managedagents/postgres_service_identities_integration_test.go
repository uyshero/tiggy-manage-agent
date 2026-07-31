package managedagents

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"
)

func TestPostgresServiceIdentityCredentialLifecycle(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	workspaceID := createPostgresIntegrationWorkspace(t, store, "service-identities")
	ctx, err := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: workspaceID})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := store.CreateServiceIdentity(ctx, CreateServiceIdentityInput{
		WorkspaceID: workspaceID, Kind: ServiceIdentityKindApplication, Name: "knowledge",
		Role: WorkspaceRoleMember, Scopes: []string{ServiceScopeRetrievalRead, ServiceScopeModelGenerate}, CreatedBy: "admin-1",
	})
	if err != nil || identity.ID == "" || identity.Status != ServiceIdentityStatusActive || len(identity.Scopes) != 2 {
		t.Fatalf("create service identity: identity=%+v err=%v", identity, err)
	}
	secretHash := sha256.Sum256([]byte("test-service-secret"))
	expiresAt := time.Now().UTC().Add(time.Hour)
	credential, err := store.CreateServiceCredential(ctx, CreateServiceCredentialInput{
		WorkspaceID: workspaceID, ServiceIdentityID: identity.ID, Name: "deployment",
		Locator: "locator_service_identity_001", TokenPrefix: "tma_svc_locator_", SecretHash: secretHash[:],
		ExpiresAt: &expiresAt, CreatedBy: "admin-1",
	})
	if err != nil || credential.ID == "" || credential.Status != ServiceCredentialStatusActive {
		t.Fatalf("create service credential: credential=%+v err=%v", credential, err)
	}
	authenticated, err := store.AuthenticateServiceCredential(context.Background(), "locator_service_identity_001", secretHash[:])
	if err != nil || authenticated.Identity.ID != identity.ID || authenticated.CredentialID != credential.ID {
		t.Fatalf("authenticate service credential: authenticated=%+v err=%v", authenticated, err)
	}
	wrongHash := sha256.Sum256([]byte("wrong-service-secret"))
	if _, err := store.AuthenticateServiceCredential(context.Background(), "locator_service_identity_001", wrongHash[:]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong service credential error = %v, want not found", err)
	}
	identity, err = store.UpdateServiceIdentity(ctx, UpdateServiceIdentityInput{
		WorkspaceID: workspaceID, ID: identity.ID, Name: identity.Name, Description: identity.Description,
		Role: identity.Role, Scopes: identity.Scopes, Status: ServiceIdentityStatusDisabled,
	})
	if err != nil || identity.Status != ServiceIdentityStatusDisabled {
		t.Fatalf("disable service identity: identity=%+v err=%v", identity, err)
	}
	if _, err := store.AuthenticateServiceCredential(context.Background(), "locator_service_identity_001", secretHash[:]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled service identity authenticated: %v", err)
	}
	identity, err = store.UpdateServiceIdentity(ctx, UpdateServiceIdentityInput{
		WorkspaceID: workspaceID, ID: identity.ID, Name: identity.Name, Description: identity.Description,
		Role: identity.Role, Scopes: identity.Scopes, Status: ServiceIdentityStatusActive,
	})
	if err != nil {
		t.Fatalf("enable service identity: %v", err)
	}
	if err := store.RevokeServiceCredential(ctx, workspaceID, identity.ID, credential.ID); err != nil {
		t.Fatalf("revoke service credential: %v", err)
	}
	if _, err := store.AuthenticateServiceCredential(context.Background(), "locator_service_identity_001", secretHash[:]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked service credential authenticated: %v", err)
	}
}
