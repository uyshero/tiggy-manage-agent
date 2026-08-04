package runner

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"tiggy-manage-agent/internal/envvars"
	"tiggy-manage-agent/internal/managedagents"
)

type applicationEnvironmentTestStore struct {
	records map[string]envvars.EncryptedVariable
}

func (s *applicationEnvironmentTestStore) ListEncryptedEnvironmentVariables(ctx context.Context, workspaceID string) ([]envvars.EncryptedVariable, error) {
	scope, scoped := managedagents.DatabaseAccessScopeFromContext(ctx)
	items := []envvars.EncryptedVariable{}
	for _, record := range s.records {
		if record.WorkspaceID != workspaceID || scoped && scope.WorkspaceID != workspaceID {
			continue
		}
		if record.OwnerID != "" && (!scoped || record.OwnerID != scope.OwnerID) {
			continue
		}
		items = append(items, record)
	}
	return items, nil
}

func (s *applicationEnvironmentTestStore) UpsertEncryptedEnvironmentVariable(_ context.Context, input envvars.EncryptedVariable) (envvars.EncryptedVariable, error) {
	now := time.Now().UTC()
	key := input.WorkspaceID + "\x00" + input.OwnerID + "\x00" + input.Name
	if existing := s.records[key]; !existing.CreatedAt.IsZero() {
		input.CreatedAt = existing.CreatedAt
	} else {
		input.CreatedAt = now
	}
	input.UpdatedAt = now
	s.records[key] = input
	return input, nil
}

func (s *applicationEnvironmentTestStore) DeleteEncryptedEnvironmentVariable(_ context.Context, workspaceID string, name string) error {
	for key, record := range s.records {
		if record.WorkspaceID == workspaceID && record.Name == name {
			delete(s.records, key)
			return nil
		}
	}
	return managedagents.ErrNotFound
}

func TestResolveManagedRuntimeEnvironmentUsesApplicationSecrets(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envvars.MasterKeyEnvironmentVariable, base64.StdEncoding.EncodeToString(key))
	cipher, err := envvars.CipherFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	store := &applicationEnvironmentTestStore{records: make(map[string]envvars.EncryptedVariable)}
	service, err := envvars.NewService(store, cipher)
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := "wksp_runtime_application"
	appID := "svc_biography"
	userOwnerID := "user-owner"
	appOwnerID := envvars.ApplicationOwnerID(appID)
	userContext, err := managedagents.ContextWithDatabaseAccessScope(t.Context(), managedagents.AccessScope{WorkspaceID: workspaceID, OwnerID: userOwnerID})
	if err != nil {
		t.Fatal(err)
	}
	appContext, err := managedagents.ContextWithDatabaseAccessScope(t.Context(), managedagents.AccessScope{WorkspaceID: workspaceID, OwnerID: appOwnerID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Put(userContext, workspaceID, userOwnerID, "SHARED_KEY", "user-value"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Put(userContext, workspaceID, userOwnerID, "USER_ONLY", "user-only"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Put(appContext, workspaceID, appOwnerID, "SHARED_KEY", "application-value"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Put(appContext, workspaceID, appOwnerID, "APP_ONLY", "application-only"); err != nil {
		t.Fatal(err)
	}

	resolved, resolvedCipher, err := resolveManagedRuntimeEnvironment(userContext, store, managedagents.AgentRuntimeConfig{
		WorkspaceID: workspaceID,
		AppID:       appID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolvedCipher == nil || resolved["SHARED_KEY"] != "application-value" || resolved["APP_ONLY"] != "application-only" || resolved["USER_ONLY"] != "user-only" {
		t.Fatalf("unexpected runtime environment: %#v", resolved)
	}
}
