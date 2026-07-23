package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/skillmarketplace"
	"tiggy-manage-agent/internal/skillpackage"
	skillspkg "tiggy-manage-agent/internal/skills"
	"tiggy-manage-agent/internal/tools"
)

func TestPostgresInternalMarketplaceHTTPPreviewAndInstallLocalFS(t *testing.T) {
	if os.Getenv("TMA_RUN_POSTGRES_TESTS") != "1" {
		t.Skip("set TMA_RUN_POSTGRES_TESTS=1 to run Postgres integration tests")
	}
	databaseURL := os.Getenv("TMA_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TMA_DATABASE_URL to run Postgres integration tests")
	}

	adminDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	t.Cleanup(func() { _ = adminDB.Close() })
	if err := adminDB.PingContext(t.Context()); err != nil {
		t.Fatalf("ping integration database: %v", err)
	}
	var catalogMigrationInstalled bool
	if err := adminDB.QueryRowContext(t.Context(), `SELECT to_regprocedure('tma_skill_catalog_version_visible(text,integer)') IS NOT NULL`).Scan(&catalogMigrationInstalled); err != nil || !catalogMigrationInstalled {
		t.Fatalf("catalog migration 000063 is not installed: installed=%v err=%v", catalogMigrationInstalled, err)
	}

	store, err := managedagents.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatalf("open Postgres store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	objectClient, err := objectstore.NewLocalFSClient(objectstore.Config{
		Provider: objectstore.ProviderLocalFS, RootDir: t.TempDir(), Bucket: "skill-packages",
	})
	if err != nil {
		t.Fatalf("create local package object store: %v", err)
	}
	if err := store.ConfigureSkillPackageStorage(objectClient, "skill-packages"); err != nil {
		t.Fatalf("configure Skill package storage: %v", err)
	}

	suffix := strconv.FormatInt(time.Now().UTC().UnixNano(), 36)
	publisherWorkspace := "wksp_catalog_publisher_" + suffix
	consumerWorkspace := "wksp_catalog_consumer_" + suffix
	for _, workspace := range []string{publisherWorkspace, consumerWorkspace} {
		if _, err := adminDB.ExecContext(t.Context(), `
			INSERT INTO workspaces (id, org_id, name, created_at)
			VALUES ($1, 'org_default', $2, CURRENT_TIMESTAMP)
		`, workspace, workspace); err != nil {
			t.Fatalf("create catalog integration workspace %s: %v", workspace, err)
		}
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, query := range []string{
			`DELETE FROM operator_audit_log WHERE workspace_id IN ($1, $2)`,
			`DELETE FROM sessions WHERE workspace_id IN ($1, $2)`,
			`DELETE FROM skill_marketplace_entries WHERE workspace_id IN ($1, $2)`,
			`DELETE FROM skills WHERE workspace_id IN ($1, $2)`,
			`DELETE FROM object_refs WHERE workspace_id IN ($1, $2)`,
			`DELETE FROM environments WHERE workspace_id IN ($1, $2)`,
			`DELETE FROM agents WHERE workspace_id IN ($1, $2)`,
			`DELETE FROM workspaces WHERE id IN ($1, $2)`,
		} {
			_, _ = adminDB.ExecContext(ctx, query, publisherWorkspace, consumerWorkspace)
		}
	})

	publisherCtx, err := managedagents.ContextWithDatabaseAccessScope(t.Context(), managedagents.AccessScope{WorkspaceID: publisherWorkspace})
	if err != nil {
		t.Fatalf("create publisher scope: %v", err)
	}
	publisherSkill, err := store.CreateSkill(publisherCtx, skillspkg.CreateSkillInput{
		WorkspaceID: publisherWorkspace, Identifier: "postgres-catalog-review", Title: "Postgres Catalog Review",
		Description: "Organization-local review workflow.", CreatedBy: "publisher",
	})
	if err != nil {
		t.Fatalf("create publisher Skill: %v", err)
	}
	publisherVersion, err := store.CreateSkillVersion(publisherCtx, skillspkg.CreateVersionInput{
		SkillID: publisherSkill.ID, ContentFormat: "markdown",
		ContentText: "---\nname: Postgres Catalog Review\ndescription: Organization-local review workflow.\nlicense: MIT\n---\nReview changes without external network access.\n",
		Assets:      json.RawMessage(`{"files":[{"path":"references/checklist.md","content":"# Checklist\n","content_type":"text/markdown","size":12}]}`),
		CreatedBy:   "publisher",
	})
	if err != nil {
		t.Fatalf("create publisher Skill version: %v", err)
	}
	if publisherVersion.PackageFormat != skillpackage.FormatV1 || publisherVersion.PackageObjectRefID == "" {
		t.Fatalf("publisher version did not persist a standard package: %#v", publisherVersion)
	}
	entry, err := store.CreateMarketplaceEntry(publisherCtx, skillmarketplace.CreateMarketplaceEntryInput{
		WorkspaceID: publisherWorkspace, SkillID: publisherSkill.ID, SkillVersion: publisherVersion.Version,
		Summary: "Approved local review package.", Category: "Engineering", Tags: []string{"review", "local"}, CreatedBy: "publisher",
	})
	if err != nil {
		t.Fatalf("create catalog draft: %v", err)
	}
	entry, err = store.TransitionMarketplaceEntry(publisherCtx, skillmarketplace.TransitionMarketplaceEntryInput{
		WorkspaceID: publisherWorkspace, EntryID: entry.ID,
		TargetStatus: skillmarketplace.MarketplaceEntryStatusPendingReview, Actor: "publisher",
	})
	if err != nil {
		t.Fatalf("submit catalog entry: %v", err)
	}
	entry, err = store.TransitionMarketplaceEntry(publisherCtx, skillmarketplace.TransitionMarketplaceEntryInput{
		WorkspaceID: publisherWorkspace, EntryID: entry.ID,
		TargetStatus: skillmarketplace.MarketplaceEntryStatusPublished, Actor: "admin", Note: "approved",
	})
	if err != nil {
		t.Fatalf("publish catalog entry: %v", err)
	}

	agent, err := store.CreateAgent(managedagents.CreateAgentInput{
		WorkspaceID: consumerWorkspace, Name: "Catalog Consumer " + suffix, LLMProvider: "fake", LLMModel: "fake-demo",
	})
	if err != nil {
		t.Fatalf("create consumer Agent: %v", err)
	}
	environment, err := store.CreateEnvironment(managedagents.CreateEnvironmentInput{
		WorkspaceID: consumerWorkspace, Name: "Catalog Consumer Env " + suffix, Config: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create consumer Environment: %v", err)
	}
	session, err := store.CreateSession(managedagents.CreateSessionInput{
		WorkspaceID: consumerWorkspace, AgentID: agent.ID, EnvironmentID: environment.ID, CreatedBy: "member",
	})
	if err != nil {
		t.Fatalf("create consumer Session: %v", err)
	}

	service := newSkillsToolServiceWithDependencies(store, nil, skillmarketplace.Policy{}, objectClient, "skill-packages")
	server := &Server{
		mux: http.NewServeMux(), store: store, objectStore: objectClient, logger: slog.Default(),
		skillsToolService: service,
	}
	server.routes()
	discovered, err := service.Discover(t.Context(), tools.SkillsDiscoverRequest{
		SessionID: session.ID, Query: "review", Category: "engineering", Tags: []string{"LOCAL"}, Limit: 10,
	})
	if err != nil || discovered.Provider != skillmarketplace.CatalogProvider || len(discovered.Items) != 1 || discovered.Items[0].CatalogEntryID != entry.ID {
		t.Fatalf("unexpected Postgres skills_discover catalog result: result=%#v err=%v", discovered, err)
	}
	browse := getJSON[struct {
		Provider string `json:"provider"`
		Count    int    `json:"count"`
		Items    []struct {
			ID string `json:"id"`
		} `json:"items"`
	}](t, server.mux, "/v1/skills/marketplace/internal?session_id="+session.ID+"&query=review&tag=local")
	if browse.Provider != skillmarketplace.CatalogProvider || browse.Count != 1 || browse.Items[0].ID != entry.ID {
		t.Fatalf("unexpected Postgres catalog browse: %#v", browse)
	}

	source := skillmarketplace.Source{Provider: skillmarketplace.CatalogProvider, CatalogEntryID: entry.ID}
	previewResponse := marketplaceHTTPRequest(t, server.mux, http.MethodPost, "/v1/skills/marketplace/internal/preview", map[string]any{
		"session_id": session.ID, "source": source,
	})
	if previewResponse.Code != http.StatusOK {
		t.Fatalf("preview Postgres catalog package: %d %s", previewResponse.Code, previewResponse.Body.String())
	}
	var preview tools.SkillsPreviewResponse
	decodeMarketplaceHTTPResponse(t, previewResponse, &preview)
	if preview.InstallState != "new_install" || !preview.Policy.Allowed || preview.Source.CatalogSkillID != publisherSkill.ID || preview.Revision == "" {
		t.Fatalf("unexpected Postgres catalog preview: %#v", preview)
	}

	installResponse := marketplaceHTTPRequest(t, server.mux, http.MethodPost, "/v1/skills/marketplace/internal/install", map[string]any{
		"session_id": session.ID, "identifier": preview.Identifier, "source": source,
		"policy_revision": preview.Policy.PolicyRevision,
	})
	if installResponse.Code != http.StatusCreated {
		t.Fatalf("install Postgres catalog package: %d %s", installResponse.Code, installResponse.Body.String())
	}
	var installed tools.SkillsInstallResponse
	decodeMarketplaceHTTPResponse(t, installResponse, &installed)
	if installed.Skill.WorkspaceID != consumerWorkspace || installed.Skill.SourceType != skillspkg.SourceTypeCatalog ||
		installed.Skill.SourceLocator != publisherSkill.ID || installed.Version.SourceRef != entry.ID || installed.Version.SourceRevision != preview.Revision {
		t.Fatalf("unexpected installed catalog provenance: %#v", installed)
	}

	consumerCtx, err := managedagents.ContextWithDatabaseAccessScope(t.Context(), managedagents.AccessScope{WorkspaceID: consumerWorkspace})
	if err != nil {
		t.Fatalf("create consumer scope: %v", err)
	}
	persisted, err := store.GetSkillByIdentifier(consumerCtx, consumerWorkspace, preview.Identifier)
	if err != nil || persisted.ID != installed.Skill.ID || persisted.SourceType != skillspkg.SourceTypeCatalog {
		t.Fatalf("catalog install was not persisted for consumer: skill=%#v err=%v", persisted, err)
	}
}
