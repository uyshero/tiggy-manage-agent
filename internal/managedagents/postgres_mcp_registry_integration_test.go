package managedagents

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/mcpregistry"
)

func TestPostgresMCPRegistryRestoreWithRLS(t *testing.T) {
	adminStore := newPostgresIntegrationStore(t)
	alphaWorkspace := createPostgresIntegrationWorkspace(t, adminStore, "mcp-restore-alpha")
	betaWorkspace := createPostgresIntegrationWorkspace(t, adminStore, "mcp-restore-beta")
	suffix := strings.ReplaceAll(time.Now().UTC().Format("150405.000000000"), ".", "")
	role := "tma_mcp_restore_" + suffix
	password := "tma_mcp_restore_password_32_bytes"

	t.Cleanup(func() {
		_, _ = adminStore.db.ExecContext(context.Background(), `DELETE FROM mcp_registry_servers WHERE workspace_id IN ($1, $2)`, alphaWorkspace, betaWorkspace)
		_, _ = adminStore.db.ExecContext(context.Background(), `DELETE FROM workspaces WHERE id IN ($1, $2)`, alphaWorkspace, betaWorkspace)
	})
	if _, err := adminStore.db.ExecContext(context.Background(), `CREATE ROLE `+role+` LOGIN PASSWORD '`+password+`'`); err != nil {
		t.Fatalf("create MCP Registry RLS role: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminStore.db.ExecContext(context.Background(), `DROP OWNED BY `+role)
		if _, err := adminStore.db.ExecContext(context.Background(), `DROP ROLE `+role); err != nil {
			t.Fatalf("drop MCP Registry RLS role: %v", err)
		}
	})
	if _, err := adminStore.db.ExecContext(context.Background(), `GRANT USAGE ON SCHEMA public TO `+role); err != nil {
		t.Fatalf("grant MCP Registry schema access: %v", err)
	}
	if _, err := adminStore.db.ExecContext(context.Background(), `
		GRANT SELECT, INSERT, UPDATE, DELETE ON mcp_registry_servers, mcp_registry_server_versions TO `+role); err != nil {
		t.Fatalf("grant MCP Registry table access: %v", err)
	}
	if _, err := adminStore.db.ExecContext(context.Background(), `GRANT SELECT ON agents, agent_config_versions TO `+role); err != nil {
		t.Fatalf("grant MCP Registry usage-count access: %v", err)
	}
	if _, err := adminStore.db.ExecContext(context.Background(), `
		GRANT USAGE, SELECT ON SEQUENCE tma_mcp_registry_server_id_seq, tma_mcp_registry_version_id_seq TO `+role); err != nil {
		t.Fatalf("grant MCP Registry sequence access: %v", err)
	}

	databaseURL, err := url.Parse(os.Getenv("TMA_DATABASE_URL"))
	if err != nil {
		t.Fatalf("parse integration database URL: %v", err)
	}
	databaseURL.User = url.UserPassword(role, password)
	restrictedStore, err := NewPostgresStore(databaseURL.String())
	if err != nil {
		t.Fatalf("open MCP Registry RLS store: %v", err)
	}
	t.Cleanup(func() {
		if err := restrictedStore.Close(); err != nil {
			t.Fatalf("close MCP Registry RLS store: %v", err)
		}
	})

	alphaContext, err := ContextWithDatabaseAccessScope(context.Background(), AccessScope{WorkspaceID: alphaWorkspace})
	if err != nil {
		t.Fatalf("create alpha MCP Registry scope: %v", err)
	}
	betaContext, err := ContextWithDatabaseAccessScope(context.Background(), AccessScope{WorkspaceID: betaWorkspace})
	if err != nil {
		t.Fatalf("create beta MCP Registry scope: %v", err)
	}
	created, err := restrictedStore.CreateMCPRegistryServer(alphaContext, mcpregistry.CreateInput{
		WorkspaceID: alphaWorkspace, Identifier: "restore_fixture", Name: "Restore Fixture",
		Config: json.RawMessage(`{"identifier":"restore_fixture","transport":"stdio","command":"true"}`), CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatalf("create MCP Registry v1 through restricted role: %v", err)
	}
	updated, err := restrictedStore.UpdateMCPRegistryServer(alphaContext, mcpregistry.UpdateInput{
		ServerID: created.ID, Name: created.Name,
		Config: json.RawMessage(`{"identifier":"restore_fixture","transport":"stdio","command":"false"}`), UpdatedBy: "integration-test",
	})
	if err != nil || updated.CurrentVersion != 2 {
		t.Fatalf("publish MCP Registry v2 through restricted role: server=%+v err=%v", updated, err)
	}
	restored, err := restrictedStore.RestoreMCPRegistryVersion(alphaContext, created.ID, 1, "integration-test")
	if err != nil || restored.SourceVersion != 1 || restored.PreviousVersion != 2 || restored.NewVersion != 3 || restored.Server.CurrentVersion != 3 {
		t.Fatalf("restore MCP Registry v1 through restricted role: result=%+v err=%v", restored, err)
	}
	versions, err := restrictedStore.ListMCPRegistryVersions(alphaContext, created.ID)
	if err != nil || len(versions) != 3 || versions[0].Version != 3 || versions[0].Checksum != versions[2].Checksum {
		t.Fatalf("unexpected restored MCP Registry versions: versions=%+v err=%v", versions, err)
	}
	if _, err := restrictedStore.GetMCPRegistryServer(betaContext, created.ID); !errors.Is(err, mcpregistry.ErrNotFound) {
		t.Fatalf("expected cross-workspace MCP Registry server to be hidden, got %v", err)
	}
	if _, err := restrictedStore.RestoreMCPRegistryVersion(betaContext, created.ID, 1, "integration-test"); !errors.Is(err, mcpregistry.ErrNotFound) {
		t.Fatalf("expected cross-workspace MCP Registry restore to be hidden, got %v", err)
	}
	if _, err := restrictedStore.CreateMCPRegistryServer(alphaContext, mcpregistry.CreateInput{
		WorkspaceID: betaWorkspace, Identifier: "blocked_restore", Name: "Blocked Restore",
		Config: json.RawMessage(`{"identifier":"blocked_restore","transport":"stdio","command":"true"}`),
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected cross-workspace MCP Registry create to be forbidden, got %v", err)
	}

	var unscopedCount int
	if err := restrictedStore.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM mcp_registry_servers`).Scan(&unscopedCount); err != nil || unscopedCount != 0 {
		t.Fatalf("expected unscoped MCP Registry query to expose zero rows: count=%d err=%v", unscopedCount, err)
	}
}
