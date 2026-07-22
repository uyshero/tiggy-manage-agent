package managedagents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tiggy-manage-agent/internal/envvars"
	"tiggy-manage-agent/internal/mcpregistry"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/skillmarketplace"
	"tiggy-manage-agent/internal/skillpackage"
	"tiggy-manage-agent/internal/skillretention"
	"tiggy-manage-agent/internal/skills"
)

func TestPostgresSessionRunIdempotencyAndIndexedEvents(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)
	payload := json.RawMessage(`{"content":[{"type":"text","text":"run integration"}]}`)
	requestHash := fmt.Sprintf("%x", sha256.Sum256(payload))

	created, err := store.StartSessionRunContext(t.Context(), session.ID, StartSessionRunInput{
		Payload: payload, IdempotencyKey: "postgres-run-1", RequestHash: requestHash,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if !created.Created || created.Run.ID == "" || created.Run.UserEventSeq == 0 {
		t.Fatalf("unexpected created run: %+v", created)
	}
	if created.Run.AgentID != session.AgentID || created.Run.AgentConfigVersion != session.AgentConfigVersion {
		t.Fatalf("run did not freeze session agent config: %+v", created.Run)
	}

	replayed, err := store.StartSessionRunContext(t.Context(), session.ID, StartSessionRunInput{
		Payload: payload, IdempotencyKey: "postgres-run-1", RequestHash: requestHash,
	})
	if err != nil || replayed.Created || replayed.Run.ID != created.Run.ID {
		t.Fatalf("unexpected idempotent replay: %+v err=%v", replayed, err)
	}
	_, err = store.StartSessionRunContext(t.Context(), session.ID, StartSessionRunInput{
		Payload:        json.RawMessage(`{"content":[{"type":"text","text":"different"}]}`),
		IdempotencyKey: "postgres-run-1", RequestHash: strings.Repeat("f", 64),
	})
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "idempotency_conflict") {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}

	loaded, err := store.GetSessionRunContext(t.Context(), session.ID, created.Run.ID)
	if err != nil || loaded.ID != created.Run.ID || loaded.IdempotencyKey != "postgres-run-1" {
		t.Fatalf("unexpected loaded run: %+v err=%v", loaded, err)
	}
	runs, err := store.ListSessionRunsContext(t.Context(), session.ID)
	if err != nil || len(runs) != 1 || runs[0].ID != created.Run.ID {
		t.Fatalf("unexpected run list: %+v err=%v", runs, err)
	}
	events, err := store.ListSessionRunEventsContext(t.Context(), session.ID, created.Run.ID, 0)
	if err != nil || len(events) != 2 {
		t.Fatalf("unexpected run events: %+v err=%v", events, err)
	}
	for _, event := range events {
		if event.TurnID != created.Run.ID {
			t.Fatalf("event was not indexed by run: %+v", event)
		}
	}
}

func TestPostgresNewRunFollowsLatestAgentConfigUnlessPinned(t *testing.T) {
	store := newPostgresIntegrationStore(t)

	t.Run("follow latest", func(t *testing.T) {
		session := createPostgresIntegrationSession(t, store)
		agent, err := store.GetAgent(session.AgentID)
		if err != nil {
			t.Fatalf("get agent: %v", err)
		}
		updated, err := store.CreateAgentConfigVersion(CreateAgentConfigVersionInput{
			AgentID: agent.ID, ExpectedCurrentVersion: agent.CurrentConfigVersion,
			LLMProvider: agent.ConfigVersion.LLMProvider, LLMModel: agent.ConfigVersion.LLMModel,
			System: "follow latest v2", Tools: agent.ConfigVersion.Tools, MCP: agent.ConfigVersion.MCP, Skills: agent.ConfigVersion.Skills,
		})
		if err != nil {
			t.Fatalf("create config version: %v", err)
		}
		started, err := store.StartSessionRunContext(t.Context(), session.ID, StartSessionRunInput{
			Payload: json.RawMessage(`{"content":[{"type":"text","text":"follow"}]}`),
		})
		if err != nil {
			t.Fatalf("start run: %v", err)
		}
		if started.Run.AgentConfigVersion != updated.CurrentConfigVersion || len(started.Events) != 3 || started.Events[0].Type != EventSessionConfigUpdated {
			t.Fatalf("unexpected auto-follow run: %+v events=%+v", started.Run, started.Events)
		}
		loaded, err := store.GetSessionRunContext(t.Context(), session.ID, started.Run.ID)
		if err != nil || loaded.AgentConfigVersion != updated.CurrentConfigVersion || loaded.AgentID != agent.ID {
			t.Fatalf("loaded run lost agent config: %+v err=%v", loaded, err)
		}
	})

	t.Run("pinned", func(t *testing.T) {
		session := createPostgresIntegrationSession(t, store)
		if _, err := store.UpdateSessionRuntimeSettings(session.ID, UpdateSessionRuntimeSettingsInput{
			RuntimeSettings:  json.RawMessage(`{"agent_config_update_policy":"pinned"}`),
			ExpectedRevision: session.RuntimeSettingsRevision,
		}); err != nil {
			t.Fatalf("pin session: %v", err)
		}
		agent, err := store.GetAgent(session.AgentID)
		if err != nil {
			t.Fatalf("get agent: %v", err)
		}
		if _, err := store.CreateAgentConfigVersion(CreateAgentConfigVersionInput{
			AgentID: agent.ID, ExpectedCurrentVersion: agent.CurrentConfigVersion,
			LLMProvider: agent.ConfigVersion.LLMProvider, LLMModel: agent.ConfigVersion.LLMModel,
			System: "pinned v2", Tools: agent.ConfigVersion.Tools, MCP: agent.ConfigVersion.MCP, Skills: agent.ConfigVersion.Skills,
		}); err != nil {
			t.Fatalf("create config version: %v", err)
		}
		started, err := store.StartSessionRunContext(t.Context(), session.ID, StartSessionRunInput{
			Payload: json.RawMessage(`{"content":[{"type":"text","text":"pinned"}]}`),
		})
		if err != nil {
			t.Fatalf("start run: %v", err)
		}
		if started.Run.AgentConfigVersion != session.AgentConfigVersion || len(started.Events) != 2 {
			t.Fatalf("unexpected pinned run: %+v events=%+v", started.Run, started.Events)
		}
	})
}

func TestPostgresSessionRuntimeSettingsConcurrentUpdateAllowsOneWinner(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, settings := range []json.RawMessage{
		json.RawMessage(`{"intervention_mode":"approve_for_me"}`),
		json.RawMessage(`{"tool_runtime":"local_system"}`),
	} {
		settings := settings
		go func() {
			<-start
			_, err := store.UpdateSessionRuntimeSettings(session.ID, UpdateSessionRuntimeSettingsInput{
				RuntimeSettings: settings, ExpectedRevision: session.RuntimeSettingsRevision,
			})
			results <- err
		}()
	}
	close(start)
	succeeded, conflicted := 0, 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrRevisionConflict):
			conflicted++
		default:
			t.Fatalf("unexpected concurrent update error: %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent updates: succeeded=%d conflicted=%d", succeeded, conflicted)
	}
	loaded, err := store.GetSession(session.ID)
	if err != nil {
		t.Fatalf("get updated session: %v", err)
	}
	if loaded.RuntimeSettingsRevision != session.RuntimeSettingsRevision+1 {
		t.Fatalf("revision = %d, want %d", loaded.RuntimeSettingsRevision, session.RuntimeSettingsRevision+1)
	}
}

func TestPostgresWorkspaceToolPermissionPolicyRevisionAndRuntimeResolution(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	ctx := context.Background()
	workspaceID := createPostgresIntegrationWorkspace(t, store, "tool-permissions")

	initial, err := store.GetWorkspaceToolPermissionPolicyContext(ctx, workspaceID)
	if err != nil {
		t.Fatalf("get default workspace tool permissions: %v", err)
	}
	if initial.Revision != 1 || string(initial.Policy) != `{"permission_rules": []}` && string(initial.Policy) != `{"permission_rules":[]}` {
		t.Fatalf("unexpected default policy: %+v policy=%s", initial, initial.Policy)
	}
	policyJSON := json.RawMessage(`{"permission_rules":[{"id":"deny-secrets","tool":"default.edit_file","argument":"path","pattern":"/workspace/secrets/**","behavior":"deny"}]}`)
	updated, err := store.UpdateWorkspaceToolPermissionPolicyContext(ctx, UpdateWorkspaceToolPermissionPolicyInput{
		WorkspaceID: workspaceID, Policy: policyJSON, ExpectedRevision: initial.Revision, UpdatedBy: "integration-test",
	})
	if err != nil {
		t.Fatalf("update workspace tool permissions: %v", err)
	}
	if updated.Revision != 2 || updated.UpdatedBy != "integration-test" {
		t.Fatalf("unexpected updated policy: %+v", updated)
	}
	if _, err := store.UpdateWorkspaceToolPermissionPolicyContext(ctx, UpdateWorkspaceToolPermissionPolicyInput{
		WorkspaceID: workspaceID, Policy: json.RawMessage(`{"permission_rules":[]}`), ExpectedRevision: 1, UpdatedBy: "stale-writer",
	}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale update error = %v, want revision conflict", err)
	}

	suffix := time.Now().UTC().Format("20060102150405.000000000")
	agent, err := store.CreateAgent(CreateAgentInput{
		WorkspaceID: workspaceID, Name: "tool-policy-agent-" + suffix, Model: "test-model", System: "integration test",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	environment, err := store.CreateEnvironment(CreateEnvironmentInput{
		WorkspaceID: workspaceID, Name: "tool-policy-env-" + suffix, Config: json.RawMessage(`{"type":"integration"}`),
	})
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	session, err := store.CreateSession(CreateSessionInput{
		WorkspaceID: workspaceID, AgentID: agent.ID, EnvironmentID: environment.ID, CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = $1`, session.ID)
		_, _ = store.db.ExecContext(ctx, `DELETE FROM environments WHERE id = $1`, environment.ID)
		_, _ = store.db.ExecContext(ctx, `DELETE FROM agents WHERE id = $1`, agent.ID)
	})
	runtimeConfig, err := store.ResolveAgentRuntimeConfig(session.ID)
	if err != nil {
		t.Fatalf("resolve runtime config: %v", err)
	}
	var runtimePolicyValue any
	var expectedPolicyValue any
	if json.Unmarshal(runtimeConfig.WorkspaceToolPolicy, &runtimePolicyValue) != nil || json.Unmarshal(policyJSON, &expectedPolicyValue) != nil || !reflect.DeepEqual(runtimePolicyValue, expectedPolicyValue) {
		t.Fatalf("runtime workspace policy = %s, want %s", runtimeConfig.WorkspaceToolPolicy, policyJSON)
	}
}

func TestPostgresSkillRegistryVersionsAndUsage(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	ctx := context.Background()
	workspaceID := createPostgresIntegrationWorkspace(t, store, "skills")
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(ctx, `DELETE FROM sessions WHERE workspace_id = $1`, workspaceID)
		_, _ = store.db.ExecContext(ctx, `DELETE FROM environments WHERE workspace_id = $1`, workspaceID)
		_, _ = store.db.ExecContext(ctx, `DELETE FROM agents WHERE workspace_id = $1`, workspaceID)
		_, _ = store.db.ExecContext(ctx, `DELETE FROM skills WHERE workspace_id = $1`, workspaceID)
		_, _ = store.db.ExecContext(ctx, `DELETE FROM workspaces WHERE id = $1`, workspaceID)
	})
	created, err := store.CreateSkill(ctx, skills.CreateSkillInput{
		WorkspaceID: workspaceID, Identifier: "code-review", Title: "Code Review", CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatalf("create skill: %v", err)
	}
	first, err := store.CreateSkillVersion(ctx, skills.CreateVersionInput{
		SkillID: created.ID, ContentFormat: "hybrid", Manifest: json.RawMessage(`{"system_role":"Review carefully."}`),
		ContentText: "Inspect behavior.", CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatalf("create first skill version: %v", err)
	}
	second, err := store.CreateSkillVersion(ctx, skills.CreateVersionInput{
		SkillID: created.ID, ContentFormat: "markdown", ContentText: "Inspect regressions.", CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatalf("create second skill version: %v", err)
	}
	if first.Version != 1 || second.Version != 2 || first.Checksum == second.Checksum {
		t.Fatalf("unexpected versions: first=%#v second=%#v", first, second)
	}
	remote, err := store.CreateSkill(ctx, skills.CreateSkillInput{
		WorkspaceID: workspaceID, Identifier: "remote-review", Title: "Remote Review", CreatedBy: "integration-test",
		SourceType: skills.SourceTypeGitHub, SourceLocator: "acme/review-skill", SourcePath: "SKILL.md",
	})
	if err != nil {
		t.Fatalf("create remote skill: %v", err)
	}
	remoteVersion, err := store.CreateSkillVersion(ctx, skills.CreateVersionInput{
		SkillID: remote.ID, ContentFormat: "markdown", ContentText: "Review from GitHub.", CreatedBy: "integration-test",
		SourceRef: "v1", SourceRevision: "blob123", SourceURL: "https://github.com/acme/review-skill/blob/v1/SKILL.md",
	})
	if err != nil {
		t.Fatalf("create remote skill version: %v", err)
	}
	loadedRemote, err := store.GetSkill(ctx, remote.ID)
	if err != nil {
		t.Fatalf("get remote skill: %v", err)
	}
	loadedRemoteVersion, err := store.GetSkillVersion(ctx, remote.ID, remoteVersion.Version)
	if err != nil {
		t.Fatalf("get remote skill version: %v", err)
	}
	if loadedRemote.SourceType != skills.SourceTypeGitHub || loadedRemote.SourceLocator != "acme/review-skill" || loadedRemote.SourcePath != "SKILL.md" {
		t.Fatalf("unexpected remote skill provenance: %#v", loadedRemote)
	}
	if loadedRemoteVersion.SourceRef != "v1" || loadedRemoteVersion.SourceRevision != "blob123" || loadedRemoteVersion.SourceURL == "" {
		t.Fatalf("unexpected remote version provenance: %#v", loadedRemoteVersion)
	}
	if _, err := store.CreateAgent(CreateAgentInput{
		WorkspaceID: workspaceID, Name: "Invalid Skill Agent", LLMProvider: "fake", LLMModel: "fake-demo",
		Skills: json.RawMessage(`{"enabled":[{"skill":"code-review"}]}`),
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected missing skill version rejection, got %v", err)
	}
	if _, err := store.CreateAgent(CreateAgentInput{
		WorkspaceID: workspaceID, Name: "Unknown Skill Version Agent", LLMProvider: "fake", LLMModel: "fake-demo",
		Skills: json.RawMessage(`{"enabled":[{"skill":"code-review","version":99}]}`),
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected unknown skill version rejection, got %v", err)
	}
	versions, err := store.ListSkillVersions(ctx, created.ID)
	if err != nil || len(versions) != 2 || versions[0].Version != 2 {
		t.Fatalf("unexpected version list: %#v err=%v", versions, err)
	}

	agent, err := store.CreateAgent(CreateAgentInput{
		WorkspaceID: workspaceID, Name: "Skill Usage Agent", LLMProvider: "fake", LLMModel: "fake-demo", System: "test",
		Skills: json.RawMessage(`{"enabled":[{"skill":"code-review","version":1}]}`),
	})
	if err != nil {
		t.Fatalf("create skill usage agent: %v", err)
	}
	if !strings.Contains(string(agent.ConfigVersion.Skills), `"mode":"summary"`) || !strings.Contains(string(agent.ConfigVersion.Skills), `"priority":100`) {
		t.Fatalf("expected normalized store binding, got %s", agent.ConfigVersion.Skills)
	}
	legacyAgent, err := store.CreateAgent(CreateAgentInput{
		WorkspaceID: workspaceID, Name: "Legacy Skill Agent", LLMProvider: "fake", LLMModel: "fake-demo", System: "legacy",
	})
	if err != nil {
		t.Fatalf("create legacy test agent: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE agent_config_versions SET skills_json = $3 WHERE agent_id = $1 AND version = $2`, legacyAgent.ID, 1, json.RawMessage(`["code-review"]`)); err != nil {
		t.Fatalf("seed legacy skills config: %v", err)
	}
	legacyAgent, err = store.GetAgent(legacyAgent.ID)
	if err != nil {
		t.Fatalf("reload legacy agent: %v", err)
	}
	legacyAgent, err = store.CreateAgentConfigVersion(CreateAgentConfigVersionInput{
		AgentID: legacyAgent.ID, LLMProvider: legacyAgent.ConfigVersion.LLMProvider, LLMModel: legacyAgent.ConfigVersion.LLMModel,
		System: "legacy system updated", Skills: legacyAgent.ConfigVersion.Skills,
	})
	if err != nil || string(legacyAgent.ConfigVersion.Skills) != `["code-review"]` {
		t.Fatalf("expected unchanged legacy config preservation, agent=%#v err=%v", legacyAgent, err)
	}
	environment, err := store.CreateEnvironment(CreateEnvironmentInput{WorkspaceID: workspaceID, Name: "Skill Usage Env", Config: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("create skill usage environment: %v", err)
	}
	session, err := store.CreateSession(CreateSessionInput{
		WorkspaceID: workspaceID, AgentID: agent.ID, EnvironmentID: environment.ID, CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatalf("create skill usage session: %v", err)
	}
	usage := skills.Usage{
		WorkspaceID: workspaceID, SessionID: session.ID, TurnID: "turn_skills", AgentID: agent.ID,
		AgentConfigVersion: agent.CurrentConfigVersion, SkillID: created.ID, SkillIdentifier: created.Identifier,
		SkillVersion: first.Version, RequestedMode: skills.ModeFull, RenderedMode: skills.ModeSummary,
		Priority: 100, EstimatedTokens: 12, Status: skills.UsageDegraded,
	}
	if err := store.RecordSkillUsages(ctx, []skills.Usage{usage}); err != nil {
		t.Fatalf("record skill usage: %v", err)
	}
	usage.EstimatedTokens = 10
	if err := store.RecordSkillUsages(ctx, []skills.Usage{usage}); err != nil {
		t.Fatalf("update skill usage: %v", err)
	}
	usages, err := store.ListSkillUsages(ctx, session.ID, "turn_skills")
	if err != nil || len(usages) != 1 || usages[0].EstimatedTokens != 10 {
		t.Fatalf("unexpected skill usages: %#v err=%v", usages, err)
	}
	if _, err := store.ArchiveSkill(ctx, created.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected active Agent binding to block archive, got %v", err)
	}
	agent, err = store.CreateAgentConfigVersion(CreateAgentConfigVersionInput{
		AgentID: agent.ID, LLMProvider: agent.ConfigVersion.LLMProvider, LLMModel: agent.ConfigVersion.LLMModel,
		System: agent.ConfigVersion.System, Tools: agent.ConfigVersion.Tools, MCP: agent.ConfigVersion.MCP,
		Skills: json.RawMessage(`{"enabled":[]}`),
	})
	if err != nil {
		t.Fatalf("disable skill before archive: %v", err)
	}
	legacyAgent, err = store.CreateAgentConfigVersion(CreateAgentConfigVersionInput{
		AgentID: legacyAgent.ID, LLMProvider: legacyAgent.ConfigVersion.LLMProvider, LLMModel: legacyAgent.ConfigVersion.LLMModel,
		System: legacyAgent.ConfigVersion.System, Tools: legacyAgent.ConfigVersion.Tools, MCP: legacyAgent.ConfigVersion.MCP,
		Skills: json.RawMessage(`{"enabled":[]}`),
	})
	if err != nil {
		t.Fatalf("disable legacy skill binding before archive: %v", err)
	}
	archived, err := store.ArchiveSkill(ctx, created.ID)
	if err != nil || archived.Status != skills.StatusArchived {
		t.Fatalf("archive skill: %#v err=%v", archived, err)
	}
	if _, err := store.CreateSkillVersion(ctx, skills.CreateVersionInput{SkillID: created.ID, ContentText: "late"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected archived publish conflict, got %v", err)
	}
}

func TestPostgresSkillPackageStorageLocalFSE2E(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	ctx := context.Background()
	workspaceID := createPostgresIntegrationWorkspace(t, store, "skill_packages")
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(ctx, `DELETE FROM skill_asset_gc_tombstones WHERE workspace_id = $1`, workspaceID)
		_, _ = store.db.ExecContext(ctx, `DELETE FROM skill_asset_gc_runs WHERE workspace_id = $1`, workspaceID)
		_, _ = store.db.ExecContext(ctx, `DELETE FROM skills WHERE workspace_id = $1`, workspaceID)
		_, _ = store.db.ExecContext(ctx, `DELETE FROM object_refs WHERE workspace_id = $1`, workspaceID)
		_, _ = store.db.ExecContext(ctx, `DELETE FROM workspaces WHERE id = $1`, workspaceID)
	})
	skill, err := store.CreateSkill(ctx, skills.CreateSkillInput{
		WorkspaceID: workspaceID, Identifier: "package-storage", Title: "Package Storage", CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatalf("create package storage skill: %v", err)
	}
	legacyContent := "# Package Storage\n\nRead the standard package.\n"
	legacy, err := store.CreateSkillVersion(ctx, skills.CreateVersionInput{
		SkillID: skill.ID, ContentFormat: "markdown", ContentText: legacyContent,
		Assets:    json.RawMessage(`{"files":[{"path":"references/guide.md","content":"# Guide\n","content_type":"text/markdown","size":8}]}`),
		CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatalf("create legacy skill version: %v", err)
	}
	if legacy.PackageFormat != skillpackage.FormatLegacyDB {
		t.Fatalf("expected legacy package format before storage configuration, got %#v", legacy)
	}
	client, err := objectstore.NewLocalFSClient(objectstore.Config{
		Provider: objectstore.ProviderLocalFS, RootDir: t.TempDir(), Bucket: "skill-packages",
	})
	if err != nil {
		t.Fatalf("create local package object store: %v", err)
	}
	if err := store.ConfigureSkillPackageStorage(client, "skill-packages"); err != nil {
		t.Fatalf("configure package storage: %v", err)
	}
	backfill, err := store.BackfillSkillPackages(ctx, skills.PackageBackfillInput{WorkspaceID: workspaceID, Limit: 10}, "integration-test")
	if err != nil || backfill.Scanned != 1 || backfill.Migrated != 1 {
		t.Fatalf("backfill legacy package: result=%#v err=%v", backfill, err)
	}
	loaded, err := store.GetSkillVersion(ctx, skill.ID, legacy.Version)
	if err != nil {
		t.Fatalf("load backfilled package: %v", err)
	}
	if loaded.PackageFormat != skillpackage.FormatV1 || loaded.PackageRoot == "" || loaded.PackageObjectRefID == "" || loaded.SkillMDObjectRefID == "" {
		t.Fatalf("backfilled package metadata is incomplete: %#v", loaded)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE skill_versions SET content_text = 'stale database body' WHERE id = $1`, loaded.ID); err != nil {
		t.Fatalf("seed stale database body: %v", err)
	}
	loaded, err = store.GetSkillVersion(ctx, skill.ID, legacy.Version)
	if err != nil || loaded.ContentText != legacyContent {
		t.Fatalf("package read did not take precedence over database fallback: version=%#v err=%v", loaded, err)
	}
	var fileCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM skill_version_package_files WHERE skill_version_id = $1`, loaded.ID).Scan(&fileCount); err != nil {
		t.Fatalf("count package files: %v", err)
	}
	if fileCount != 3 {
		t.Fatalf("expected SKILL.md, text asset, and archive refs, got %d", fileCount)
	}
	newVersion, err := store.CreateSkillVersion(ctx, skills.CreateVersionInput{
		SkillID: skill.ID, ContentFormat: "markdown", ContentText: "# Package Storage v2\n",
		Assets:    json.RawMessage(`{"files":[{"path":"examples/input.md","content":"example\n","content_type":"text/markdown","size":8}]}`),
		CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatalf("publish package-native skill version: %v", err)
	}
	expectedRoot, err := skillpackage.PackageRoot(workspaceID, skill.Identifier, 2)
	if err != nil {
		t.Fatalf("resolve expected package root: %v", err)
	}
	if newVersion.Version != 2 || newVersion.PackageFormat != skillpackage.FormatV1 ||
		newVersion.PackageRoot != expectedRoot || newVersion.PackageObjectRefID == "" {
		t.Fatalf("unexpected package-native version: %#v", newVersion)
	}
	archiveRef, err := store.GetObjectRef(loaded.PackageObjectRefID)
	if err != nil {
		t.Fatalf("load package archive ref: %v", err)
	}
	archive, err := client.GetObject(ctx, objectstore.GetObjectInput{Bucket: archiveRef.Bucket, Key: archiveRef.ObjectKey, Version: archiveRef.ObjectVersion})
	if err != nil {
		t.Fatalf("load package archive: %v", err)
	}
	defer archive.Body.Close()
	archiveBody, err := io.ReadAll(archive.Body)
	if err != nil || len(archiveBody) == 0 || archiveRef.ContentType != "application/zip" {
		t.Fatalf("unexpected package archive: bytes=%d ref=%#v err=%v", len(archiveBody), archiveRef, err)
	}
	secondBackfill, err := store.BackfillSkillPackages(ctx, skills.PackageBackfillInput{WorkspaceID: workspaceID, Limit: 10}, "integration-test")
	if err != nil || secondBackfill.Migrated != 0 {
		t.Fatalf("expected idempotent package backfill: result=%#v err=%v", secondBackfill, err)
	}
	retention, err := skillretention.NewService(store, client, skillretention.Policy{Enabled: true, RetentionDays: 1, DeleteLimit: 10})
	if err != nil {
		t.Fatalf("create package retention service: %v", err)
	}
	activePreview, err := retention.Preview(ctx, workspaceID, 10)
	if err != nil || len(activePreview.Candidates) != 0 {
		t.Fatalf("active package must be protected from GC: preview=%#v err=%v", activePreview, err)
	}
	if _, err := store.ArchiveSkill(ctx, skill.ID); err != nil {
		t.Fatalf("archive package skill: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE skills SET archived_at = $2 WHERE id = $1`, skill.ID, time.Now().UTC().Add(-48*time.Hour)); err != nil {
		t.Fatalf("age archived package: %v", err)
	}
	archivedPreview, err := retention.Preview(ctx, workspaceID, 10)
	if err != nil || len(archivedPreview.Candidates) != 6 {
		t.Fatalf("expected six archived package GC candidates: preview=%#v err=%v", archivedPreview, err)
	}
	gcResult, err := retention.Run(ctx, skillretention.RunRequest{WorkspaceID: workspaceID, RequestedBy: "integration-test"})
	if err != nil || gcResult.Run.DeletedCount != 6 || gcResult.Run.FailedCount != 0 {
		t.Fatalf("delete archived package: result=%#v err=%v", gcResult, err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM skill_version_package_files WHERE skill_version_id = $1`, loaded.ID).Scan(&fileCount); err != nil || fileCount != 0 {
		t.Fatalf("package file refs remain after GC: count=%d err=%v", fileCount, err)
	}
	var remainingObjectRefs int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM object_refs WHERE workspace_id = $1`, workspaceID).Scan(&remainingObjectRefs); err != nil || remainingObjectRefs != 0 {
		t.Fatalf("package object refs remain after GC: count=%d err=%v", remainingObjectRefs, err)
	}
}

func TestPostgresMarketplacePolicyVersionsAndPrecedence(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	ctx := context.Background()
	suffix := strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "_")
	organizationID := "org_policy_" + suffix
	workspaceID := "wksp_policy_" + suffix
	if _, err := store.db.ExecContext(ctx, `INSERT INTO organizations (id, name) VALUES ($1, $2)`, organizationID, "Policy Test"); err != nil {
		t.Fatalf("create policy organization: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO workspaces (id, org_id, name) VALUES ($1, $2, $3)`, workspaceID, organizationID, "Policy Workspace"); err != nil {
		t.Fatalf("create policy workspace: %v", err)
	}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM skill_marketplace_policies WHERE organization_id = $1 OR workspace_id = $2`, organizationID, workspaceID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM workspaces WHERE id = $1`, workspaceID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM organizations WHERE id = $1`, organizationID)
	})

	organization, organizationVersion, err := store.CreateMarketplacePolicy(ctx, skillmarketplace.CreatePolicyInput{
		ScopeType: skillmarketplace.PolicyScopeOrganization, OrganizationID: organizationID,
		Config: skillmarketplace.Policy{AllowedOwners: []string{"acme"}}, CreatedBy: "integration",
	})
	if err != nil || organizationVersion.Version != 1 || organizationVersion.Checksum == "" {
		t.Fatalf("create organization policy: policy=%#v version=%#v err=%v", organization, organizationVersion, err)
	}
	effective, err := store.ResolveMarketplacePolicy(ctx, workspaceID)
	if err != nil || effective.Policy.ID != organization.ID || effective.Source != skillmarketplace.PolicyScopeOrganization {
		t.Fatalf("resolve organization policy: effective=%#v err=%v", effective, err)
	}

	workspace, workspaceVersion, err := store.CreateMarketplacePolicy(ctx, skillmarketplace.CreatePolicyInput{
		ScopeType: skillmarketplace.PolicyScopeWorkspace, WorkspaceID: workspaceID,
		Config: skillmarketplace.Policy{AllowedRepositories: []string{"trusted/review"}}, CreatedBy: "integration",
	})
	if err != nil {
		t.Fatalf("create workspace policy: %v", err)
	}
	if _, _, err := store.CreateMarketplacePolicy(ctx, skillmarketplace.CreatePolicyInput{
		ScopeType: skillmarketplace.PolicyScopeWorkspace, WorkspaceID: workspaceID,
		Config: skillmarketplace.Policy{}, CreatedBy: "integration",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected duplicate active scope conflict, got %v", err)
	}
	effective, err = store.ResolveMarketplacePolicy(ctx, workspaceID)
	if err != nil || effective.Policy.ID != workspace.ID || effective.Revision != workspaceVersion.Checksum {
		t.Fatalf("resolve workspace policy: effective=%#v err=%v", effective, err)
	}

	version2, err := store.PublishMarketplacePolicyVersion(ctx, workspace.ID,
		skillmarketplace.Policy{AllowedOwners: []string{"trusted"}, RequireCommitSHA: true}, "integration")
	if err != nil || version2.Version != 2 || version2.Checksum == workspaceVersion.Checksum {
		t.Fatalf("publish policy version: version=%#v err=%v", version2, err)
	}
	storedVersion, err := store.GetMarketplacePolicyVersion(ctx, workspace.ID, 2)
	if err != nil || storedVersion.Checksum != version2.Checksum || !storedVersion.Config.RequireCommitSHA {
		t.Fatalf("get policy version: version=%#v err=%v", storedVersion, err)
	}
	archived, err := store.ArchiveMarketplacePolicy(ctx, workspace.ID)
	if err != nil || archived.Status != skillmarketplace.PolicyStatusArchived {
		t.Fatalf("archive workspace policy: policy=%#v err=%v", archived, err)
	}
	effective, err = store.ResolveMarketplacePolicy(ctx, workspaceID)
	if err != nil || effective.Policy.ID != organization.ID {
		t.Fatalf("expected organization fallback after archive: effective=%#v err=%v", effective, err)
	}
}

func TestPostgresSkillAssetRetentionPolicyVersionsAndPrecedence(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	ctx := context.Background()
	suffix := strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "_")
	organizationID := "org_retention_" + suffix
	workspaceID := "wksp_retention_" + suffix
	if _, err := store.db.ExecContext(ctx, `INSERT INTO organizations (id, name) VALUES ($1, $2)`, organizationID, "Retention Test"); err != nil {
		t.Fatalf("create retention organization: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO workspaces (id, org_id, name) VALUES ($1, $2, $3)`, workspaceID, organizationID, "Retention Workspace"); err != nil {
		t.Fatalf("create retention workspace: %v", err)
	}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM skill_asset_retention_policies WHERE organization_id = $1 OR workspace_id = $2`, organizationID, workspaceID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM workspaces WHERE id = $1`, workspaceID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM organizations WHERE id = $1`, organizationID)
	})
	organization, organizationVersion, err := store.CreateSkillAssetRetentionPolicy(ctx, skillretention.CreatePolicyInput{
		ScopeType: skillretention.ScopeOrganization, OrganizationID: organizationID,
		Config: skillretention.Policy{Enabled: false, RetentionDays: 90, DeleteLimit: 50}, CreatedBy: "integration-test",
	})
	if err != nil || organizationVersion.Version != 1 {
		t.Fatalf("create organization retention policy: policy=%#v version=%#v err=%v", organization, organizationVersion, err)
	}
	effective, found, err := store.ResolveSkillAssetRetentionPolicy(ctx, workspaceID)
	if err != nil || !found || effective.Policy.ID != organization.ID || effective.Source != skillretention.ScopeOrganization {
		t.Fatalf("resolve organization retention policy: effective=%#v found=%t err=%v", effective, found, err)
	}
	workspace, workspaceVersion, err := store.CreateSkillAssetRetentionPolicy(ctx, skillretention.CreatePolicyInput{
		ScopeType: skillretention.ScopeWorkspace, WorkspaceID: workspaceID,
		Config: skillretention.Policy{Enabled: true, RetentionDays: 30, DeleteLimit: 25}, CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatalf("create workspace retention policy: %v", err)
	}
	if _, _, err := store.CreateSkillAssetRetentionPolicy(ctx, skillretention.CreatePolicyInput{
		ScopeType: skillretention.ScopeWorkspace, WorkspaceID: workspaceID,
		Config: skillretention.Policy{Enabled: true, RetentionDays: 10, DeleteLimit: 10}, CreatedBy: "integration-test",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected duplicate active retention policy conflict, got %v", err)
	}
	effective, found, err = store.ResolveSkillAssetRetentionPolicy(ctx, workspaceID)
	if err != nil || !found || effective.Policy.ID != workspace.ID || effective.Revision != workspaceVersion.Checksum {
		t.Fatalf("resolve workspace retention policy: effective=%#v found=%t err=%v", effective, found, err)
	}
	version2, err := store.PublishSkillAssetRetentionPolicyVersion(ctx, workspace.ID,
		skillretention.Policy{Enabled: true, RetentionDays: 45, DeleteLimit: 10}, "integration-test")
	if err != nil || version2.Version != 2 || version2.Checksum == workspaceVersion.Checksum {
		t.Fatalf("publish retention policy v2: version=%#v err=%v", version2, err)
	}
	storedV1, err := store.GetSkillAssetRetentionPolicyVersion(ctx, workspace.ID, 1)
	if err != nil || storedV1.Config.RetentionDays != 30 || storedV1.Checksum != workspaceVersion.Checksum {
		t.Fatalf("retention v1 was not immutable: version=%#v err=%v", storedV1, err)
	}
	effective, found, err = store.ResolveSkillAssetRetentionPolicy(ctx, workspaceID)
	if err != nil || !found || effective.Version.Version != 2 || effective.Config.RetentionDays != 45 {
		t.Fatalf("resolve retention v2: effective=%#v found=%t err=%v", effective, found, err)
	}
	if _, err := store.ArchiveSkillAssetRetentionPolicy(ctx, workspace.ID); err != nil {
		t.Fatalf("archive workspace retention policy: %v", err)
	}
	effective, found, err = store.ResolveSkillAssetRetentionPolicy(ctx, workspaceID)
	if err != nil || !found || effective.Policy.ID != organization.ID {
		t.Fatalf("expected organization retention fallback: effective=%#v found=%t err=%v", effective, found, err)
	}
	if _, err := store.ArchiveSkillAssetRetentionPolicy(ctx, organization.ID); err != nil {
		t.Fatalf("archive organization retention policy: %v", err)
	}
	_, found, err = store.ResolveSkillAssetRetentionPolicy(ctx, workspaceID)
	if err != nil || found {
		t.Fatalf("expected Server fallback signal after all policies archived: found=%t err=%v", found, err)
	}
}

func TestPostgresSkillAssetRetentionGCDeletesEligibleObjects(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	ctx := context.Background()
	workspaceID := createPostgresIntegrationWorkspace(t, store, "skill_asset_gc")
	client, err := objectstore.NewLocalFSClient(objectstore.Config{
		Provider: objectstore.ProviderLocalFS, RootDir: t.TempDir(), Bucket: "skill-assets",
	})
	if err != nil {
		t.Fatalf("create local object store: %v", err)
	}

	type seededObject struct {
		ref     ObjectRef
		content []byte
	}
	seedObject := func(name string) seededObject {
		t.Helper()
		content := []byte("skill-asset-" + name)
		checksum := fmt.Sprintf("%x", sha256.Sum256(content))
		key := "integration/" + workspaceID + "/" + name + ".pdf"
		put, err := client.PutObject(ctx, objectstore.PutObjectInput{
			Bucket: "skill-assets", Key: key, Body: bytes.NewReader(content), ContentType: "application/pdf",
			SizeBytes: int64(len(content)), ChecksumSHA256: checksum,
		})
		if err != nil {
			t.Fatalf("put %s: %v", name, err)
		}
		metadata, _ := json.Marshal(map[string]string{
			"kind": "skill_asset", "scan_provider": "integration-scanner", "scan_version": "1.0",
		})
		ref, err := store.CreateObjectRef(CreateObjectRefInput{
			WorkspaceID: workspaceID, StorageProvider: objectstore.ProviderLocalFS,
			Bucket: put.Bucket, ObjectKey: put.Key, ObjectVersion: put.Version,
			ContentType: "application/pdf", SizeBytes: int64(len(content)), ChecksumSHA256: checksum,
			Metadata: metadata, CreatedBy: "integration-test",
		})
		if err != nil {
			t.Fatalf("create object ref %s: %v", name, err)
		}
		return seededObject{ref: ref, content: content}
	}
	assetJSON := func(object seededObject, path string) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"files":[{"path":%q,"binary":true,"object_ref_id":%q,"content_type":"application/pdf","size":%d,"checksum_sha256":%q,"scan_status":"passed"}]}`,
			path, object.ref.ID, len(object.content), object.ref.ChecksumSHA256))
	}

	archivedObject := seedObject("archived")
	orphanObject := seedObject("orphan")
	protectedObject := seedObject("protected")
	archivedSkill, err := store.CreateSkill(ctx, skills.CreateSkillInput{
		WorkspaceID: workspaceID, Identifier: "archived-gc-skill", Title: "Archived GC Skill", CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatalf("create archived skill: %v", err)
	}
	if _, err := store.CreateSkillVersion(ctx, skills.CreateVersionInput{
		SkillID: archivedSkill.ID, ContentFormat: "markdown", ContentText: "archived",
		Assets: assetJSON(archivedObject, "archived.pdf"), CreatedBy: "integration-test",
	}); err != nil {
		t.Fatalf("create archived skill version: %v", err)
	}
	if _, err := store.ArchiveSkill(ctx, archivedSkill.ID); err != nil {
		t.Fatalf("archive skill: %v", err)
	}
	protectedSkill, err := store.CreateSkill(ctx, skills.CreateSkillInput{
		WorkspaceID: workspaceID, Identifier: "protected-gc-skill", Title: "Protected GC Skill", CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatalf("create protected skill: %v", err)
	}
	if _, err := store.CreateSkillVersion(ctx, skills.CreateVersionInput{
		SkillID: protectedSkill.ID, ContentFormat: "markdown", ContentText: "active",
		Assets: assetJSON(protectedObject, "protected.pdf"), CreatedBy: "integration-test",
	}); err != nil {
		t.Fatalf("create protected skill version: %v", err)
	}
	old := time.Now().UTC().Add(-48 * time.Hour)
	if _, err := store.db.ExecContext(ctx, `UPDATE skills SET archived_at = $2 WHERE id = $1`, archivedSkill.ID, old); err != nil {
		t.Fatalf("backdate archived skill: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE object_refs SET created_at = $2 WHERE id IN ($1, $3, $4)`, archivedObject.ref.ID, old, orphanObject.ref.ID, protectedObject.ref.ID); err != nil {
		t.Fatalf("backdate object refs: %v", err)
	}

	policy, _, err := store.CreateSkillAssetRetentionPolicy(ctx, skillretention.CreatePolicyInput{
		ScopeType: skillretention.ScopeWorkspace, WorkspaceID: workspaceID,
		Config: skillretention.Policy{Enabled: true, RetentionDays: 1, DeleteLimit: 10}, CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatalf("create retention policy: %v", err)
	}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM skill_asset_gc_tombstones WHERE workspace_id = $1`, workspaceID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM skill_asset_gc_runs WHERE workspace_id = $1`, workspaceID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM skill_asset_retention_policies WHERE id = $1`, policy.ID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM skills WHERE workspace_id = $1`, workspaceID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM object_refs WHERE workspace_id = $1`, workspaceID)
		_ = client.DeleteObject(context.Background(), objectstore.DeleteObjectInput{
			Bucket: protectedObject.ref.Bucket, Key: protectedObject.ref.ObjectKey, Version: protectedObject.ref.ObjectVersion,
		})
	})

	service, err := skillretention.NewService(store, client, skillretention.DefaultPolicy())
	if err != nil {
		t.Fatalf("create retention service: %v", err)
	}
	preview, err := service.Preview(ctx, workspaceID, 10)
	if err != nil {
		t.Fatalf("preview GC: %v", err)
	}
	if preview.Effective.Policy.ID != policy.ID || preview.CandidateCount != 2 {
		t.Fatalf("unexpected preview: %#v", preview)
	}
	for _, candidate := range preview.Candidates {
		if candidate.ObjectRefID == protectedObject.ref.ID {
			t.Fatalf("active skill object must not be a candidate: %#v", candidate)
		}
	}

	release, err := store.AcquireSkillAssetGCLock(ctx, workspaceID)
	if err != nil {
		t.Fatalf("acquire first GC lock: %v", err)
	}
	if _, err := store.AcquireSkillAssetGCLock(ctx, workspaceID); !errors.Is(err, skillretention.ErrConflict) {
		t.Fatalf("expected concurrent GC lock conflict, got %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("release GC lock: %v", err)
	}

	result, err := service.Run(ctx, skillretention.RunRequest{WorkspaceID: workspaceID, RequestedBy: "integration-test"})
	if err != nil {
		t.Fatalf("run GC: %v", err)
	}
	if result.Run.Status != skillretention.RunStatusSucceeded || result.Run.DeletedCount != 2 || result.Run.BytesDeleted <= 0 {
		t.Fatalf("unexpected GC run: %#v", result.Run)
	}
	for _, object := range []seededObject{archivedObject, orphanObject} {
		if _, err := store.GetObjectRef(object.ref.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected object ref %s deleted, got %v", object.ref.ID, err)
		}
		if _, err := client.GetObject(ctx, objectstore.GetObjectInput{Bucket: object.ref.Bucket, Key: object.ref.ObjectKey}); !errors.Is(err, objectstore.ErrNotFound) {
			t.Fatalf("expected object %s deleted, got %v", object.ref.ID, err)
		}
	}
	if _, err := store.GetObjectRef(protectedObject.ref.ID); err != nil {
		t.Fatalf("protected object ref was removed: %v", err)
	}
	tombstones, err := store.ListSkillAssetGCTombstones(ctx, skillretention.ListTombstonesInput{WorkspaceID: workspaceID, Limit: 10})
	if err != nil || len(tombstones) != 2 {
		t.Fatalf("unexpected tombstones: %#v err=%v", tombstones, err)
	}
	for _, tombstone := range tombstones {
		if tombstone.ScanProvider != "integration-scanner" || tombstone.ScanVersion != "1.0" || tombstone.DeletedBy != "integration-test" {
			t.Fatalf("tombstone lost provenance: %#v", tombstone)
		}
	}

	retryObject := seedObject("retry")
	if _, err := store.db.ExecContext(ctx, `UPDATE object_refs SET created_at = $2 WHERE id = $1`, retryObject.ref.ID, old); err != nil {
		t.Fatalf("backdate retry object: %v", err)
	}
	flakyClient := &failOnceDeleteObjectStore{Client: client}
	retryService, err := skillretention.NewService(store, flakyClient, skillretention.DefaultPolicy())
	if err != nil {
		t.Fatalf("create retry retention service: %v", err)
	}
	failedRun, err := retryService.Run(ctx, skillretention.RunRequest{WorkspaceID: workspaceID, RequestedBy: "integration-test"})
	if err != nil {
		t.Fatalf("run expected partial GC: %v", err)
	}
	if failedRun.Run.Status != skillretention.RunStatusFailed || failedRun.Run.FailedCount != 1 {
		t.Fatalf("unexpected failed run: %#v", failedRun.Run)
	}
	if _, err := store.GetObjectRef(retryObject.ref.ID); err != nil {
		t.Fatalf("failed deletion removed object ref: %v", err)
	}
	retriedRun, err := retryService.Run(ctx, skillretention.RunRequest{WorkspaceID: workspaceID, RequestedBy: "integration-test"})
	if err != nil || retriedRun.Run.DeletedCount != 1 {
		t.Fatalf("retry GC did not recover: run=%#v err=%v", retriedRun.Run, err)
	}

	missingObject := seedObject("already-missing")
	if _, err := store.db.ExecContext(ctx, `UPDATE object_refs SET created_at = $2 WHERE id = $1`, missingObject.ref.ID, old); err != nil {
		t.Fatalf("backdate missing object: %v", err)
	}
	if err := client.DeleteObject(ctx, objectstore.DeleteObjectInput{
		Bucket: missingObject.ref.Bucket, Key: missingObject.ref.ObjectKey, Version: missingObject.ref.ObjectVersion,
	}); err != nil {
		t.Fatalf("seed missing object state: %v", err)
	}
	missingRun, err := service.Run(ctx, skillretention.RunRequest{WorkspaceID: workspaceID, RequestedBy: "integration-test"})
	if err != nil || missingRun.Run.DeletedCount != 1 || len(missingRun.Items) != 1 || !missingRun.Items[0].ObjectWasMissing {
		t.Fatalf("missing object GC was not idempotent: result=%#v err=%v", missingRun, err)
	}
}

func TestPostgresSkillAssetRetentionGCS3E2E(t *testing.T) {
	if os.Getenv("TMA_RUN_S3_GC_TESTS") != "1" {
		t.Skip("set TMA_RUN_S3_GC_TESTS=1 to run the real S3 Skill asset GC test")
	}
	store := newPostgresIntegrationStore(t)
	ctx := context.Background()
	workspaceID := createPostgresIntegrationWorkspace(t, store, "skill_asset_gc_s3")
	config := objectstore.Config{
		Provider: objectstore.ProviderS3, Endpoint: defaultTestEnv("TMA_OBJECT_STORAGE_ENDPOINT", "http://localhost:9000"),
		Region: defaultTestEnv("TMA_OBJECT_STORAGE_REGION", "local"), Bucket: defaultTestEnv("TMA_OBJECT_STORAGE_BUCKET", "tma-artifacts"),
		AccessKey: defaultTestEnv("TMA_OBJECT_STORAGE_ACCESS_KEY", "tma"), SecretKey: defaultTestEnv("TMA_OBJECT_STORAGE_SECRET_KEY", "tma-secret"),
		UsePathStyle: true,
	}
	client, err := objectstore.NewS3Client(config)
	if err != nil {
		t.Fatalf("create S3 client: %v", err)
	}
	content := []byte("real-s3-skill-asset-gc-" + workspaceID)
	checksum := fmt.Sprintf("%x", sha256.Sum256(content))
	key := "skills-gc-e2e/" + workspaceID + "/orphan.pdf"
	put, err := client.PutObject(ctx, objectstore.PutObjectInput{
		Bucket: config.Bucket, Key: key, Body: bytes.NewReader(content), ContentType: "application/pdf",
		SizeBytes: int64(len(content)), ChecksumSHA256: checksum,
	})
	if err != nil {
		t.Fatalf("put real S3 object: %v", err)
	}
	metadata, _ := json.Marshal(map[string]string{
		"kind": "skill_asset", "scan_provider": "s3-e2e", "scan_version": "1.0",
	})
	objectRef, err := store.CreateObjectRef(CreateObjectRefInput{
		WorkspaceID: workspaceID, StorageProvider: objectstore.ProviderS3, Bucket: config.Bucket,
		ObjectKey: key, ObjectVersion: put.Version, ContentType: "application/pdf",
		SizeBytes: int64(len(content)), ChecksumSHA256: checksum, Metadata: metadata, CreatedBy: "s3-e2e",
	})
	if err != nil {
		_ = client.DeleteObject(ctx, objectstore.DeleteObjectInput{Bucket: config.Bucket, Key: key, Version: put.Version})
		t.Fatalf("create S3 object ref: %v", err)
	}
	policy, _, err := store.CreateSkillAssetRetentionPolicy(ctx, skillretention.CreatePolicyInput{
		ScopeType: skillretention.ScopeWorkspace, WorkspaceID: workspaceID,
		Config: skillretention.Policy{Enabled: true, RetentionDays: 1, DeleteLimit: 10}, CreatedBy: "s3-e2e",
	})
	if err != nil {
		t.Fatalf("create S3 retention policy: %v", err)
	}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM skill_asset_gc_tombstones WHERE workspace_id = $1`, workspaceID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM skill_asset_gc_runs WHERE workspace_id = $1`, workspaceID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM skill_asset_retention_policies WHERE id = $1`, policy.ID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM object_refs WHERE workspace_id = $1`, workspaceID)
		_ = client.DeleteObject(context.Background(), objectstore.DeleteObjectInput{Bucket: config.Bucket, Key: key, Version: put.Version})
	})
	if _, err := store.db.ExecContext(ctx, `UPDATE object_refs SET created_at = $2 WHERE id = $1`, objectRef.ID, time.Now().UTC().Add(-48*time.Hour)); err != nil {
		t.Fatalf("backdate S3 object ref: %v", err)
	}
	service, err := skillretention.NewService(store, client, skillretention.DefaultPolicy())
	if err != nil {
		t.Fatalf("create S3 retention service: %v", err)
	}
	preview, err := service.Preview(ctx, workspaceID, 10)
	if err != nil || preview.CandidateCount != 1 || preview.Candidates[0].ObjectRefID != objectRef.ID {
		t.Fatalf("unexpected S3 GC preview: %#v err=%v", preview, err)
	}
	result, err := service.Run(ctx, skillretention.RunRequest{WorkspaceID: workspaceID, RequestedBy: "s3-e2e"})
	if err != nil || result.Run.DeletedCount != 1 || result.Run.Status != skillretention.RunStatusSucceeded {
		t.Fatalf("unexpected S3 GC run: %#v err=%v", result, err)
	}
	if _, err := client.GetObject(ctx, objectstore.GetObjectInput{Bucket: config.Bucket, Key: key, Version: put.Version}); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("real S3 object still exists after GC: %v", err)
	}
	if _, err := store.GetObjectRef(objectRef.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("S3 object ref still exists after GC: %v", err)
	}
	tombstones, err := store.ListSkillAssetGCTombstones(ctx, skillretention.ListTombstonesInput{WorkspaceID: workspaceID, Limit: 10})
	if err != nil || len(tombstones) != 1 || tombstones[0].ScanProvider != "s3-e2e" {
		t.Fatalf("unexpected S3 tombstone: %#v err=%v", tombstones, err)
	}
}

func defaultTestEnv(key string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

type failOnceDeleteObjectStore struct {
	objectstore.Client
	mu     sync.Mutex
	failed bool
}

func (c *failOnceDeleteObjectStore) DeleteObject(ctx context.Context, input objectstore.DeleteObjectInput) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.failed {
		c.failed = true
		return errors.New("temporary object store delete failure")
	}
	return c.Client.DeleteObject(ctx, input)
}

func TestPostgresStoreRecordsAndFiltersOperatorAudit(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)
	record, err := store.RecordOperatorAudit(RecordOperatorAuditInput{
		WorkspaceID:   session.WorkspaceID,
		SessionID:     session.ID,
		PrincipalID:   "control:test-principal",
		OperatorLabel: "integration-operator",
		Role:          "admin",
		Action:        "agent.task_group.cancel",
		ResourceType:  "task_group",
		ResourceID:    "sgrp_integration",
		Outcome:       "succeeded",
		Details:       json.RawMessage(`{"reason":"integration test"}`),
	})
	if err != nil {
		t.Fatalf("record operator audit: %v", err)
	}
	t.Cleanup(func() {
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM operator_audit_log WHERE id = $1`, record.ID); err != nil {
			t.Fatalf("cleanup operator audit: %v", err)
		}
	})

	records, err := store.ListOperatorAudit(ListOperatorAuditInput{
		SessionID: session.ID,
		Action:    "agent.task_group.cancel",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("list operator audit: %v", err)
	}
	if len(records) != 1 || records[0].ID != record.ID || records[0].OperatorLabel != "integration-operator" {
		t.Fatalf("unexpected operator audit records: %#v", records)
	}
	if !json.Valid(records[0].Details) || !strings.Contains(string(records[0].Details), "integration test") {
		t.Fatalf("unexpected operator audit details: %s", records[0].Details)
	}
}

func TestPostgresStorePersistsAgentDeliberationAcrossStoreRestart(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	parent := createPostgresIntegrationSession(t, store)
	group, err := store.CreateSubagentTaskGroup(CreateSubagentTaskGroupInput{
		WorkspaceID: parent.WorkspaceID, OwnerID: parent.OwnerID, ParentSessionID: parent.ID,
		ParentTurnID: "turn_deliberation_persistence", Strategy: SubagentTaskGroupStrategyAllCompleted,
		ResultReducer: SubagentTaskGroupReducerJSONList, PlannedCount: 2,
	})
	if err != nil {
		t.Fatalf("create deliberation task group: %v", err)
	}
	created, err := store.CreateAgentDeliberation(CreateAgentDeliberationInput{
		Deliberation: AgentDeliberation{
			WorkspaceID: parent.WorkspaceID, OwnerID: parent.OwnerID, ParentSessionID: parent.ID,
			ParentTurnID: "turn_deliberation_persistence", IdempotencyKey: "postgres-restart",
			Objective: "Verify restart persistence", Strategy: "expert_panel", MaxTokens: 20000, MaxSeconds: 300,
			ModeratorAgentID: parent.AgentID, ModeratorEnvironmentID: parent.EnvironmentID,
			Plan: json.RawMessage(`{"strategy":"expert_panel"}`),
		},
		Participants: []AgentDeliberationParticipant{
			{RoleID: "architect", RoleTitle: "Architect", Goal: "Propose", AgentID: parent.AgentID, EnvironmentID: parent.EnvironmentID},
			{RoleID: "reviewer", RoleTitle: "Reviewer", Goal: "Challenge", AgentID: parent.AgentID, EnvironmentID: parent.EnvironmentID},
		},
	})
	if err != nil {
		t.Fatalf("create deliberation: %v", err)
	}
	if _, err := store.CreateAgentDeliberationRound(AgentDeliberationRound{
		DeliberationID: created.ID, RoundNumber: 1, RoundType: "independent", Status: "running", TaskGroupID: group.ID,
	}); err != nil {
		t.Fatalf("create deliberation round: %v", err)
	}
	if _, err := store.UpsertAgentDeliberationContribution(AgentDeliberationContribution{
		DeliberationID: created.ID, RoundNumber: 1, ParticipantIndex: 0, TaskGroupID: group.ID,
		ItemIndex: 0, Status: "completed", ContributionText: "Use staged rollout",
		ContributionJSON: json.RawMessage(`{"position":"staged rollout"}`), RetryCount: 1,
	}); err != nil {
		t.Fatalf("upsert deliberation contribution: %v", err)
	}
	if _, err := store.UpdateAgentDeliberationRound(created.ID, 1, UpdateAgentDeliberationRoundInput{
		Status: "completed", Summary: json.RawMessage(`{"agreements":["staged rollout"]}`),
		Questions: json.RawMessage(`{"reviewer":["Define rollback"]}`), Complete: true,
	}); err != nil {
		t.Fatalf("complete deliberation round: %v", err)
	}
	if _, err := store.UpdateAgentDeliberation(created.ID, UpdateAgentDeliberationInput{
		Status: AgentDeliberationStatusCompleted, Phase: AgentDeliberationPhaseCompleted, FinalGroupID: group.ID,
		FinalResult: json.RawMessage(`{"recommendation":"staged rollout"}`),
	}); err != nil {
		t.Fatalf("complete deliberation: %v", err)
	}

	restartedStore := newPostgresIntegrationStore(t)
	reloaded, err := restartedStore.GetAgentDeliberation(created.ID)
	if err != nil {
		t.Fatalf("reload deliberation after store restart: %v", err)
	}
	participants, err := restartedStore.ListAgentDeliberationParticipants(created.ID)
	if err != nil {
		t.Fatalf("reload deliberation participants: %v", err)
	}
	rounds, err := restartedStore.ListAgentDeliberationRounds(created.ID)
	if err != nil {
		t.Fatalf("reload deliberation rounds: %v", err)
	}
	contributions, err := restartedStore.ListAgentDeliberationContributions(created.ID, 0)
	if err != nil {
		t.Fatalf("reload deliberation contributions: %v", err)
	}
	if reloaded.Status != AgentDeliberationStatusCompleted || !strings.Contains(string(reloaded.FinalResult), "staged rollout") {
		t.Fatalf("unexpected reloaded deliberation: %#v", reloaded)
	}
	if len(participants) != 2 || len(rounds) != 1 || len(contributions) != 1 || contributions[0].RetryCount != 1 {
		t.Fatalf("unexpected reloaded deliberation state: participants=%#v rounds=%#v contributions=%#v", participants, rounds, contributions)
	}
	if rounds[0].CompletedAt == nil || !strings.Contains(string(rounds[0].Questions), "Define rollback") {
		t.Fatalf("unexpected reloaded moderation state: %#v", rounds[0])
	}
}

func TestPostgresStoreConcurrentSubagentCreationDoesNotExceedPerTurnLimit(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	parent := createPostgresIntegrationSession(t, store)
	t.Cleanup(func() {
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM sessions WHERE parent_session_id = $1`, parent.ID); err != nil {
			t.Fatalf("cleanup child sessions: %v", err)
		}
	})

	limits := SubagentLimits{MaxDepth: 3, MaxChildrenPerTurn: 5, MaxChildrenPerSession: 100}
	const attempts = 20
	start := make(chan struct{})
	errorsByAttempt := make(chan error, attempts)
	var successes atomic.Int64
	var waitGroup sync.WaitGroup
	for index := 0; index < attempts; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			_, err := store.CreateSubagentSession(CreateSubagentSessionInput{
				Session: CreateSessionInput{
					AgentID:         parent.AgentID,
					EnvironmentID:   parent.EnvironmentID,
					ParentSessionID: parent.ID,
					ParentTurnID:    "turn_concurrent",
					CreatedBy:       "integration-test",
				},
				Limits: limits,
			})
			if err == nil {
				successes.Add(1)
				return
			}
			errorsByAttempt <- err
		}()
	}
	close(start)
	waitGroup.Wait()
	close(errorsByAttempt)

	if got := successes.Load(); got != int64(limits.MaxChildrenPerTurn) {
		t.Fatalf("expected %d successful creates, got %d", limits.MaxChildrenPerTurn, got)
	}
	for err := range errorsByAttempt {
		var violation SubagentQuotaViolation
		if !errors.As(err, &violation) || violation.Type != "subagent_turn_fanout_limit" {
			t.Fatalf("expected fanout quota violation, got %T: %v", err, err)
		}
	}
	var childCount int
	if err := store.db.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM sessions WHERE parent_session_id = $1 AND parent_turn_id = $2
	`, parent.ID, "turn_concurrent").Scan(&childCount); err != nil {
		t.Fatalf("count child sessions: %v", err)
	}
	if childCount != limits.MaxChildrenPerTurn {
		t.Fatalf("expected exactly %d child sessions, got %d", limits.MaxChildrenPerTurn, childCount)
	}
}

func TestPostgresStoreConcurrentSubagentStartsDoNotExceedWorkspaceActiveLimit(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	parent := createPostgresIntegrationSession(t, store)
	t.Cleanup(func() {
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM sessions WHERE parent_session_id = $1`, parent.ID); err != nil {
			t.Fatalf("cleanup child sessions: %v", err)
		}
	})

	const attempts = 20
	children := make([]Session, 0, attempts)
	for index := 0; index < attempts; index++ {
		child, err := store.CreateSubagentSession(CreateSubagentSessionInput{
			Session: CreateSessionInput{
				AgentID:         parent.AgentID,
				EnvironmentID:   parent.EnvironmentID,
				ParentSessionID: parent.ID,
				ParentTurnID:    "turn_active",
				CreatedBy:       "integration-test",
			},
			Limits: SubagentLimits{MaxDepth: 3, MaxChildrenPerTurn: attempts, MaxChildrenPerSession: attempts},
		})
		if err != nil {
			t.Fatalf("create child %d: %v", index, err)
		}
		children = append(children, child)
	}

	limits := SubagentLimits{WorkspaceActiveLimit: 5, UserActiveLimit: attempts}
	start := make(chan struct{})
	errorsByAttempt := make(chan error, attempts)
	var successes atomic.Int64
	var waitGroup sync.WaitGroup
	for index := range children {
		waitGroup.Add(1)
		go func(child Session) {
			defer waitGroup.Done()
			<-start
			_, err := store.StartSubagentTurn(StartSubagentTurnInput{
				SessionID: child.ID,
				Payload:   json.RawMessage(`{"content":[{"type":"text","text":"run"}]}`),
				Limits:    limits,
			})
			if err == nil {
				successes.Add(1)
				return
			}
			errorsByAttempt <- err
		}(children[index])
	}
	close(start)
	waitGroup.Wait()
	close(errorsByAttempt)

	if got := successes.Load(); got != int64(limits.WorkspaceActiveLimit) {
		t.Fatalf("expected %d successful starts, got %d", limits.WorkspaceActiveLimit, got)
	}
	for err := range errorsByAttempt {
		var violation SubagentQuotaViolation
		if !errors.As(err, &violation) || violation.Type != "subagent_workspace_active_limit" {
			t.Fatalf("expected workspace active quota violation, got %T: %v", err, err)
		}
	}
	var activeCount int
	if err := store.db.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM sessions WHERE parent_session_id = $1 AND status = $2
	`, parent.ID, SessionStatusRunning).Scan(&activeCount); err != nil {
		t.Fatalf("count active child sessions: %v", err)
	}
	if activeCount != limits.WorkspaceActiveLimit {
		t.Fatalf("expected exactly %d running child sessions, got %d", limits.WorkspaceActiveLimit, activeCount)
	}
}

func TestPostgresStoreQueuesAndPromotesSubagentStart(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	parent := createPostgresIntegrationSession(t, store)
	t.Cleanup(func() {
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM sessions WHERE parent_session_id = $1`, parent.ID); err != nil {
			t.Fatalf("cleanup child sessions: %v", err)
		}
	})
	limits := SubagentLimits{
		MaxDepth: 3, MaxChildrenPerTurn: 5, MaxChildrenPerSession: 5,
		WorkspaceActiveLimit: 1, UserActiveLimit: 1, WorkspaceQueuedLimit: 5, UserQueuedLimit: 5, QueueTimeoutSeconds: 60,
	}
	children := make([]Session, 0, 3)
	for index := 0; index < 3; index++ {
		child, err := store.CreateSubagentSession(CreateSubagentSessionInput{
			Session: CreateSessionInput{AgentID: parent.AgentID, EnvironmentID: parent.EnvironmentID, ParentSessionID: parent.ID, ParentTurnID: "turn_queue"},
			Limits:  limits,
		})
		if err != nil {
			t.Fatalf("create child %d: %v", index, err)
		}
		children = append(children, child)
	}
	firstEvents, err := store.StartSubagentTurn(StartSubagentTurnInput{
		SessionID: children[0].ID, Payload: json.RawMessage(`{"content":[{"type":"text","text":"first"}]}`), Limits: limits,
	})
	if err != nil {
		t.Fatalf("start first child: %v", err)
	}
	firstTurnID := payloadString(firstEvents[1].Payload, "turn_id")
	queued, err := store.EnqueueSubagentStart(EnqueueSubagentStartInput{
		SessionID: children[1].ID, ParentSessionID: parent.ID, ParentTurnID: "turn_queue",
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"second"}]}`), Limits: limits,
	})
	if err != nil || queued.Status != "pending" {
		t.Fatalf("queue second child: request=%+v err=%v", queued, err)
	}
	promotions, err := store.PromoteSubagentStarts(PromoteSubagentStartsInput{Limit: 1})
	if err != nil || len(promotions) != 0 {
		t.Fatalf("expected active quota to keep request pending, promotions=%+v err=%v", promotions, err)
	}
	if _, err := store.CompleteSessionTurn(children[0].ID, firstTurnID, json.RawMessage(`{"content":[{"type":"text","text":"done"}]}`)); err != nil {
		t.Fatalf("complete first child: %v", err)
	}
	promotions, err = store.PromoteSubagentStarts(PromoteSubagentStartsInput{Limit: 1})
	if err != nil || len(promotions) != 1 {
		t.Fatalf("promote queued child: promotions=%+v err=%v", promotions, err)
	}
	if promotions[0].Request.ID != queued.ID || promotions[0].Request.Status != "started" || promotions[0].Request.TurnID == "" {
		t.Fatalf("unexpected promotion: %+v", promotions[0])
	}
	second, err := store.GetSession(children[1].ID)
	if err != nil || second.Status != SessionStatusRunning {
		t.Fatalf("expected second child running, session=%+v err=%v", second, err)
	}
	expiring, err := store.EnqueueSubagentStart(EnqueueSubagentStartInput{
		SessionID: children[2].ID, ParentSessionID: parent.ID, ParentTurnID: "turn_queue",
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"expire"}]}`), Limits: limits,
	})
	if err != nil {
		t.Fatalf("queue expiring child: %v", err)
	}
	if _, err := store.db.ExecContext(context.Background(), `UPDATE subagent_start_requests SET expires_at = CURRENT_TIMESTAMP - interval '1 second' WHERE id = $1`, expiring.ID); err != nil {
		t.Fatalf("expire queued request: %v", err)
	}
	if _, err := store.PromoteSubagentStarts(PromoteSubagentStartsInput{Limit: 1}); err != nil {
		t.Fatalf("sweep expired request: %v", err)
	}
	var expiredStatus string
	if err := store.db.QueryRowContext(context.Background(), `SELECT status FROM subagent_start_requests WHERE id = $1`, expiring.ID).Scan(&expiredStatus); err != nil || expiredStatus != "expired" {
		t.Fatalf("expected expired request, status=%q err=%v", expiredStatus, err)
	}
	expiredEvents, err := store.ListEvents(children[2].ID, 0)
	if err != nil {
		t.Fatalf("list expired child events: %v", err)
	}
	foundExpired := false
	for _, event := range expiredEvents {
		if event.Type == EventRuntimeSubagentStartExpired {
			foundExpired = true
		}
	}
	if !foundExpired {
		t.Fatalf("expected expiration event, got %+v", expiredEvents)
	}
}

func TestPostgresStoreReapsOrphanSubagentsAfterParentTerminates(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	parent := createPostgresIntegrationSession(t, store)
	t.Cleanup(func() {
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM sessions WHERE parent_session_id = $1`, parent.ID); err != nil {
			t.Fatalf("cleanup child sessions: %v", err)
		}
	})

	child, err := store.CreateSubagentSession(CreateSubagentSessionInput{
		Session: CreateSessionInput{
			AgentID:         parent.AgentID,
			EnvironmentID:   parent.EnvironmentID,
			ParentSessionID: parent.ID,
			ParentTurnID:    "turn_orphan",
			CreatedBy:       "integration-test",
		},
		Limits: SubagentLimits{MaxDepth: 3, MaxChildrenPerTurn: 5, MaxChildrenPerSession: 5},
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	started, err := store.StartSubagentTurn(StartSubagentTurnInput{
		SessionID: child.ID,
		Payload:   json.RawMessage(`{"content":[{"type":"text","text":"run child"}]}`),
		Limits:    SubagentLimits{WorkspaceActiveLimit: 5, UserActiveLimit: 5},
	})
	if err != nil {
		t.Fatalf("start child: %v", err)
	}
	childTurnID := payloadString(started[1].Payload, "turn_id")
	if childTurnID == "" {
		t.Fatal("expected child turn id")
	}

	if _, err := store.ArchiveSession(parent.ID); err != nil {
		t.Fatalf("archive parent: %v", err)
	}
	reaped, err := store.ReapOrphanSubagents(ReapOrphanSubagentsInput{Limit: 10})
	if err != nil {
		t.Fatalf("reap orphan subagents: %v", err)
	}
	if len(reaped) != 1 || reaped[0].Session.ID != child.ID || reaped[0].Reason != "orphaned_parent_terminated" {
		t.Fatalf("unexpected reaped orphan result: %+v", reaped)
	}

	reloaded, err := store.GetSession(child.ID)
	if err != nil {
		t.Fatalf("reload child: %v", err)
	}
	if reloaded.Status != SessionStatusTerminated || reloaded.ArchivedAt == nil {
		t.Fatalf("expected terminated child session, got %+v", reloaded)
	}

	var turnStatus string
	if err := store.db.QueryRowContext(context.Background(), `
		SELECT status FROM session_turns WHERE session_id = $1 AND id = $2
	`, child.ID, childTurnID).Scan(&turnStatus); err != nil {
		t.Fatalf("load child turn status: %v", err)
	}
	if turnStatus != TurnStatusInterrupted {
		t.Fatalf("expected interrupted child turn after orphan reap, got %q", turnStatus)
	}

	events, err := store.ListEvents(child.ID, 0)
	if err != nil {
		t.Fatalf("list child events: %v", err)
	}
	foundTerminated := false
	for _, event := range events {
		if event.Type != EventSessionStatusTerminated {
			continue
		}
		foundTerminated = true
		if payloadString(event.Payload, "reason") != "orphaned_parent_terminated" {
			t.Fatalf("expected orphan termination reason, got payload %s", string(event.Payload))
		}
	}
	if !foundTerminated {
		t.Fatalf("expected child terminated event, got %+v", events)
	}
}

func TestPostgresStoreCompletesSessionTurn(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)

	startEvents, err := store.AppendEvents(session.ID, []AppendEventInput{{
		Type:    EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"complete me"}]}`),
	}})
	if err != nil {
		t.Fatalf("append user.message: %v", err)
	}
	turnID := payloadString(startEvents[1].Payload, "turn_id")
	if turnID == "" {
		t.Fatal("expected user.message to include turn_id")
	}

	completedEvents, err := store.CompleteSessionTurn(session.ID, turnID, json.RawMessage(`{"content":[{"type":"text","text":"done"}]}`))
	if err != nil {
		t.Fatalf("complete turn: %v", err)
	}
	if len(completedEvents) != 2 {
		t.Fatalf("expected 2 completion events, got %d", len(completedEvents))
	}
	if completedEvents[0].Type != EventAgentMessage {
		t.Fatalf("expected first completion event %q, got %q", EventAgentMessage, completedEvents[0].Type)
	}
	if completedEvents[1].Type != EventSessionStatusIdle {
		t.Fatalf("expected second completion event %q, got %q", EventSessionStatusIdle, completedEvents[1].Type)
	}
	if got := payloadString(completedEvents[0].Payload, "turn_id"); got != turnID {
		t.Fatalf("expected agent.message turn_id %q, got %q", turnID, got)
	}

	assertPostgresSessionStatus(t, store, session.ID, SessionStatusIdle)
	status, errorMessage := postgresTurnState(t, store, session.ID, turnID)
	if status != "completed" {
		t.Fatalf("expected turn status completed, got %q", status)
	}
	if errorMessage != "" {
		t.Fatalf("expected empty error_message, got %q", errorMessage)
	}
}

func TestPostgresStoreAppendsRuntimeEventForCurrentTurn(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)

	startEvents, err := store.AppendEvents(session.ID, []AppendEventInput{{
		Type:    EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"runtime step"}]}`),
	}})
	if err != nil {
		t.Fatalf("append user.message: %v", err)
	}
	turnID := payloadString(startEvents[1].Payload, "turn_id")

	runtimeEvents, err := store.AppendRuntimeEvent(session.ID, turnID, AppendEventInput{
		Type:    EventRuntimeStarted,
		Payload: json.RawMessage(`{"message":"runtime started"}`),
	})
	if err != nil {
		t.Fatalf("append runtime event: %v", err)
	}
	if len(runtimeEvents) != 1 {
		t.Fatalf("expected 1 runtime event, got %d", len(runtimeEvents))
	}
	if runtimeEvents[0].Type != EventRuntimeStarted {
		t.Fatalf("expected runtime event %q, got %q", EventRuntimeStarted, runtimeEvents[0].Type)
	}
	if got := payloadString(runtimeEvents[0].Payload, "turn_id"); got != turnID {
		t.Fatalf("expected runtime event turn_id %q, got %q", turnID, got)
	}

	if _, err := store.CompleteSessionTurn(session.ID, turnID, json.RawMessage(`{"content":[{"type":"text","text":"done"}]}`)); err != nil {
		t.Fatalf("complete turn: %v", err)
	}
	lateEvents, err := store.AppendRuntimeEvent(session.ID, turnID, AppendEventInput{
		Type:    EventRuntimeThinking,
		Payload: json.RawMessage(`{"message":"too late"}`),
	})
	if err != nil {
		t.Fatalf("append late runtime event: %v", err)
	}
	if len(lateEvents) != 0 {
		t.Fatalf("expected late runtime event to append no events, got %d", len(lateEvents))
	}
}

func TestPostgresStorePersistsTaskPlanLifecycle(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)
	ctx := context.Background()

	first, err := store.CreateSessionTaskPlanContext(ctx, session.ID, CreateSessionTaskPlanInput{
		TurnID: "turn_plan_1", Goal: "Ship the task planning workflow",
		Items: []string{"Define the state", "Implement the runtime", "Verify the workflow"},
	})
	if err != nil {
		t.Fatalf("create first task plan: %v", err)
	}
	if first.Plan.HandlingMode != TaskPlanModeTracked || len(first.Plan.Items) != 3 || len(first.Events) != 1 || first.Events[0].Type != EventRuntimeTaskPlanCreated {
		t.Fatalf("unexpected first task plan result: %+v", first)
	}
	turnPayload := json.RawMessage(`{"content":[{"type":"text","text":"execute task plan"}]}`)
	started, err := store.StartSessionRunContext(ctx, session.ID, StartSessionRunInput{Payload: turnPayload})
	if err != nil || !started.Created {
		t.Fatalf("start task plan turn: result=%+v err=%v", started, err)
	}
	turnID := started.Run.ID

	created, err := store.CreateSessionTaskPlanContext(ctx, session.ID, CreateSessionTaskPlanInput{
		TurnID: turnID, Goal: "Ship the revised task planning workflow", HandlingMode: TaskPlanModePlanned,
		Items: []string{"Define the schema", "Implement storage", "Expose tools", "Inject context", "Run verification"},
	})
	if err != nil {
		t.Fatalf("create replacement task plan: %v", err)
	}
	if len(created.Events) != 2 || created.Events[0].Type != EventRuntimeTaskPlanSuperseded || created.Events[1].Type != EventRuntimeTaskPlanCreated {
		t.Fatalf("expected superseded and created events, got %+v", created.Events)
	}
	var firstStatus string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM session_task_plans WHERE id = $1`, first.Plan.ID).Scan(&firstStatus); err != nil || firstStatus != TaskPlanStatusSuperseded {
		t.Fatalf("expected first plan superseded, status=%q err=%v", firstStatus, err)
	}

	invalidUpdates := []UpdateSessionTaskItemInput{
		{ItemID: created.Plan.Items[0].ID, Status: TaskItemStatusInProgress},
		{ItemID: created.Plan.Items[1].ID, Status: TaskItemStatusInProgress},
	}
	if _, err := store.UpdateSessionTaskItemsContext(ctx, session.ID, UpdateSessionTaskItemsInput{PlanID: created.Plan.ID, Items: invalidUpdates}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected multiple in_progress items to be rejected, got %v", err)
	}
	toolResultPayload, err := json.Marshal(map[string]any{"data": map[string]any{
		"id": "call_verify_plan", "identifier": "default", "api_name": "run_command", "success": true,
		"artifacts": []map[string]any{{"artifact_id": "art_plan_verification"}},
	}})
	if err != nil {
		t.Fatalf("encode task evidence tool result: %v", err)
	}
	if _, err := store.AppendRuntimeEventContext(ctx, session.ID, turnID, AppendEventInput{Type: EventRuntimeToolResult, Payload: toolResultPayload}); err != nil {
		t.Fatalf("append task evidence tool result: %v", err)
	}
	for _, eventData := range []map[string]any{
		{"id": "call_failed", "identifier": "default", "api_name": "run_command", "success": false},
		{"id": "call_task_self", "identifier": "task", "api_name": "update_items", "success": true},
	} {
		payload, marshalErr := json.Marshal(map[string]any{"data": eventData})
		if marshalErr != nil {
			t.Fatalf("encode rejected task evidence: %v", marshalErr)
		}
		if _, appendErr := store.AppendRuntimeEventContext(ctx, session.ID, turnID, AppendEventInput{Type: EventRuntimeToolResult, Payload: payload}); appendErr != nil {
			t.Fatalf("append rejected task evidence: %v", appendErr)
		}
	}
	missingEvidenceRef := []UpdateSessionTaskItemInput{{
		ItemID: created.Plan.Items[0].ID, Status: TaskItemStatusCompleted, Evidence: "claimed without a real result",
		EvidenceRefs: []TaskEvidenceRefInput{{ToolCallID: "call_missing"}},
	}}
	if _, err := store.UpdateSessionTaskItemsContext(ctx, session.ID, UpdateSessionTaskItemsInput{TurnID: turnID, PlanID: created.Plan.ID, Items: missingEvidenceRef}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected nonexistent evidence ref to be rejected, got %v", err)
	}
	for _, callID := range []string{"call_failed", "call_task_self"} {
		invalidEvidence := []UpdateSessionTaskItemInput{{
			ItemID: created.Plan.Items[0].ID, Status: TaskItemStatusCompleted, Evidence: "invalid evidence",
			EvidenceRefs: []TaskEvidenceRefInput{{ToolCallID: callID}},
		}}
		if _, err := store.UpdateSessionTaskItemsContext(ctx, session.ID, UpdateSessionTaskItemsInput{TurnID: turnID, PlanID: created.Plan.ID, Items: invalidEvidence}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected evidence ref %s to be rejected, got %v", callID, err)
		}
	}

	updates := make([]UpdateSessionTaskItemInput, 0, len(created.Plan.Items))
	for _, item := range created.Plan.Items {
		updates = append(updates, UpdateSessionTaskItemInput{
			ItemID: item.ID, Status: TaskItemStatusCompleted, Evidence: "verified " + item.Description,
			EvidenceRefs: []TaskEvidenceRefInput{{ToolCallID: "call_verify_plan"}},
		})
	}
	updated, err := store.UpdateSessionTaskItemsContext(ctx, session.ID, UpdateSessionTaskItemsInput{TurnID: turnID, PlanID: created.Plan.ID, Items: updates})
	if err != nil {
		t.Fatalf("complete task items: %v", err)
	}
	if len(updated.Events) != 1 || updated.Events[0].Type != EventRuntimeTaskItemsUpdated {
		t.Fatalf("unexpected task update events: %+v", updated.Events)
	}
	regressed, err := store.UpdateSessionTaskItemsContext(ctx, session.ID, UpdateSessionTaskItemsInput{
		TurnID: turnID, PlanID: created.Plan.ID,
		Items: []UpdateSessionTaskItemInput{{ItemID: created.Plan.Items[0].ID, Status: TaskItemStatusInProgress}},
	})
	if err != nil || len(regressed.Plan.Items[0].EvidenceRefs) != 0 {
		t.Fatalf("expected reopening a completed item to clear verified refs: plan=%+v err=%v", regressed.Plan, err)
	}
	if _, err := store.UpdateSessionTaskItemsContext(ctx, session.ID, UpdateSessionTaskItemsInput{
		TurnID: turnID, PlanID: created.Plan.ID,
		Items: []UpdateSessionTaskItemInput{{ItemID: created.Plan.Items[0].ID, Status: TaskItemStatusCompleted, Evidence: "stale evidence"}},
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected stale evidence without a fresh ref to be rejected, got %v", err)
	}
	if _, err := store.UpdateSessionTaskItemsContext(ctx, session.ID, UpdateSessionTaskItemsInput{
		TurnID: turnID, PlanID: created.Plan.ID,
		Items: []UpdateSessionTaskItemInput{{
			ItemID: created.Plan.Items[0].ID, Status: TaskItemStatusCompleted, Evidence: "reverified after reopening",
			EvidenceRefs: []TaskEvidenceRefInput{{ToolCallID: "call_verify_plan"}},
		}},
	}); err != nil {
		t.Fatalf("reverify reopened task item: %v", err)
	}

	completed, err := store.CompleteSessionTaskPlanContext(ctx, session.ID, FinishSessionTaskPlanInput{TurnID: turnID, PlanID: created.Plan.ID})
	if err != nil {
		t.Fatalf("complete task plan: %v", err)
	}
	if completed.Plan.Status != TaskPlanStatusCompleted || completed.Plan.CompletedAt == nil || len(completed.Events) != 1 || completed.Events[0].Type != EventRuntimeTaskPlanCompleted {
		t.Fatalf("unexpected completed task plan: %+v", completed)
	}
	if _, err := store.GetCurrentSessionTaskPlanContext(ctx, session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected no active plan after completion, got %v", err)
	}
	history, err := store.ListSessionTaskPlansContext(ctx, session.ID)
	if err != nil {
		t.Fatalf("list task plan history: %v", err)
	}
	if len(history) != 2 || history[0].ID != created.Plan.ID || history[0].Status != TaskPlanStatusCompleted || history[1].ID != first.Plan.ID || history[1].Status != TaskPlanStatusSuperseded {
		t.Fatalf("unexpected task plan history: %+v", history)
	}
	if len(history[0].Items) != 5 || history[0].Items[0].Evidence == "" || len(history[0].Items[0].EvidenceRefs) != 1 || len(history[1].Items) != 3 {
		t.Fatalf("expected complete items in task plan history: %+v", history)
	}
	ref := history[0].Items[0].EvidenceRefs[0]
	if ref.Kind != TaskEvidenceKindToolResult || ref.TurnID != turnID || ref.ToolCallID != "call_verify_plan" || ref.Tool != "default.run_command" || len(ref.ArtifactIDs) != 1 || ref.ArtifactIDs[0] != "art_plan_verification" {
		t.Fatalf("unexpected canonical task evidence ref: %+v", ref)
	}
}

func TestPostgresStoreStreamsCrossInstanceBurstWithoutLoss(t *testing.T) {
	storeA := newPostgresIntegrationStore(t)
	storeB := newPostgresIntegrationStore(t)
	session := createPostgresIntegrationSession(t, storeA)

	history, err := storeA.ListEvents(session.ID, 0)
	if err != nil {
		t.Fatalf("list initial events: %v", err)
	}
	afterSeq := history[len(history)-1].Seq
	gapEvents, err := storeB.AppendEvents(session.ID, []AppendEventInput{{
		Type:    EventRuntimeThinking,
		Payload: json.RawMessage(`{"message":"committed before subscribe"}`),
	}})
	if err != nil {
		t.Fatalf("append event between snapshot and subscribe: %v", err)
	}
	stream, cancel, err := storeA.SubscribeEvents(session.ID, afterSeq)
	if err != nil {
		t.Fatalf("subscribe from instance A: %v", err)
	}
	defer cancel()

	inputs := make([]AppendEventInput, 64)
	for index := range inputs {
		inputs[index] = AppendEventInput{
			Type:    EventRuntimeThinking,
			Payload: json.RawMessage(`{"message":"burst"}`),
		}
	}
	appended, err := storeB.AppendEvents(session.ID, inputs)
	if err != nil {
		t.Fatalf("append burst from instance B: %v", err)
	}
	expected := append(gapEvents, appended...)

	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for index, want := range expected {
		select {
		case got, ok := <-stream:
			if !ok {
				t.Fatalf("stream closed after %d of %d events", index, len(expected))
			}
			if got.ID != want.ID || got.Seq != want.Seq {
				t.Fatalf("event %d mismatch: got %+v, want %+v", index, got, want)
			}
		case <-timeout.C:
			t.Fatalf("timed out after receiving %d of %d cross-instance events", index, len(expected))
		}
	}

	// Model an SDK losing its connection to Server A while Server B keeps writing.
	lastSeq := expected[len(expected)-1].Seq
	cancel()
	disconnectedEvents, err := storeB.AppendEvents(session.ID, []AppendEventInput{
		{Type: EventRuntimeThinking, Payload: json.RawMessage(`{"message":"written while disconnected 1"}`)},
		{Type: EventRuntimeThinking, Payload: json.RawMessage(`{"message":"written while disconnected 2"}`)},
	})
	if err != nil {
		t.Fatalf("append events while instance A client is disconnected: %v", err)
	}
	resumed, cancelResumed, err := storeA.SubscribeEvents(session.ID, lastSeq)
	if err != nil {
		t.Fatalf("resume subscription through instance A: %v", err)
	}
	defer cancelResumed()
	for index, want := range disconnectedEvents {
		select {
		case got, ok := <-resumed:
			if !ok {
				t.Fatalf("resumed stream closed after %d of %d events", index, len(disconnectedEvents))
			}
			if got.ID != want.ID || got.Seq != want.Seq {
				t.Fatalf("resumed event %d mismatch: got %+v, want %+v", index, got, want)
			}
		case <-timeout.C:
			t.Fatalf("timed out resuming after %d of %d disconnected events", index, len(disconnectedEvents))
		}
	}
	select {
	case duplicate := <-resumed:
		t.Fatalf("resumed stream emitted duplicate event: %+v", duplicate)
	case <-time.After(eventCatchUpInterval + 100*time.Millisecond):
	}
}

func TestPostgresStoreInterruptedTurnSkipsLateCompletion(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)

	startEvents, err := store.AppendEvents(session.ID, []AppendEventInput{{
		Type:    EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"interrupt me"}]}`),
	}})
	if err != nil {
		t.Fatalf("append user.message: %v", err)
	}
	turnID := payloadString(startEvents[1].Payload, "turn_id")

	interruptEvents, err := store.AppendEvents(session.ID, []AppendEventInput{{Type: EventUserInterrupt}})
	if err != nil {
		t.Fatalf("append user.interrupt: %v", err)
	}
	if len(interruptEvents) != 3 {
		t.Fatalf("expected 3 interrupt events, got %d", len(interruptEvents))
	}

	lateEvents, err := store.CompleteSessionTurn(session.ID, turnID, json.RawMessage(`{"content":[{"type":"text","text":"too late"}]}`))
	if err != nil {
		t.Fatalf("late complete turn: %v", err)
	}
	if len(lateEvents) != 0 {
		t.Fatalf("expected late completion to append no events, got %d", len(lateEvents))
	}

	assertPostgresSessionStatus(t, store, session.ID, SessionStatusIdle)
	status, _ := postgresTurnState(t, store, session.ID, turnID)
	if status != "interrupted" {
		t.Fatalf("expected turn status interrupted, got %q", status)
	}
	assertNoPostgresAgentMessageForTurn(t, store, session.ID, turnID)
}

func TestPostgresStoreSessionTurnLeaseRecoveryAndRemoteInterrupt(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)

	events, err := store.AppendEvents(session.ID, []AppendEventInput{{
		Type:    EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"lease me"}]}`),
	}})
	if err != nil {
		t.Fatalf("append user message: %v", err)
	}
	turnID := payloadString(events[len(events)-1].Payload, "turn_id")

	first, err := store.ClaimSessionTurns(ClaimSessionTurnsInput{LeaseOwner: "instance-a", LeaseDuration: time.Minute, Limit: 1})
	if err != nil {
		t.Fatalf("claim first lease: %v", err)
	}
	if len(first) != 1 || first[0].SessionID != session.ID || first[0].TurnID != turnID || first[0].Attempt != 1 {
		t.Fatalf("unexpected first claim: %+v", first)
	}
	if first[0].Scope.WorkspaceID != session.WorkspaceID || first[0].Scope.OwnerID != session.OwnerID {
		t.Fatalf("persistent turn claim lost Session scope: work=%+v session=%+v", first[0], session)
	}
	second, err := store.ClaimSessionTurns(ClaimSessionTurnsInput{LeaseOwner: "instance-b", LeaseDuration: time.Minute, Limit: 1})
	if err != nil {
		t.Fatalf("claim while leased: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("expected active lease to exclude second instance, got %+v", second)
	}

	if _, err := store.db.ExecContext(context.Background(), `UPDATE session_turns SET lease_expires_at = $3 WHERE session_id = $1 AND id = $2`, session.ID, turnID, time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatalf("expire first lease: %v", err)
	}
	recovered, err := store.ClaimSessionTurns(ClaimSessionTurnsInput{LeaseOwner: "instance-b", LeaseDuration: time.Minute, Limit: 1})
	if err != nil {
		t.Fatalf("recover expired lease: %v", err)
	}
	if len(recovered) != 1 || recovered[0].Attempt != 2 {
		t.Fatalf("expected expired turn to be recovered on attempt 2, got %+v", recovered)
	}
	active, err := store.RenewSessionTurnLease(RenewSessionTurnLeaseInput{SessionID: session.ID, TurnID: turnID, LeaseOwner: "instance-a", LeaseDuration: time.Minute})
	if err != nil {
		t.Fatalf("renew stale owner: %v", err)
	}
	if active {
		t.Fatal("expected stale lease owner to be fenced out")
	}

	if _, err := store.AppendEvents(session.ID, []AppendEventInput{{Type: EventUserInterrupt}}); err != nil {
		t.Fatalf("append remote interrupt: %v", err)
	}
	active, err = store.RenewSessionTurnLease(RenewSessionTurnLeaseInput{SessionID: session.ID, TurnID: turnID, LeaseOwner: "instance-b", LeaseDuration: time.Minute})
	if err != nil {
		t.Fatalf("renew interrupted turn: %v", err)
	}
	if active {
		t.Fatal("expected interrupt persisted by another instance to stop lease renewal")
	}
}

func TestPostgresStoreSessionTurnClaimRestoresApprovedIntervention(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)
	events, err := store.AppendEvents(session.ID, []AppendEventInput{{
		Type:    EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"approve me"}]}`),
	}})
	if err != nil {
		t.Fatalf("append user message: %v", err)
	}
	turnID := payloadString(events[len(events)-1].Payload, "turn_id")
	if _, err := store.SaveSessionIntervention(session.ID, SaveSessionInterventionInput{
		TurnID:            turnID,
		CallID:            "call_approved",
		ToolIdentifier:    "standard.read_file",
		APIName:           "read_file",
		Arguments:         json.RawMessage(`{"path":"README.md"}`),
		InterventionMode:  "request_approval",
		Reason:            "test",
		Continuation:      json.RawMessage(`[{"role":"assistant","content":[]}]`),
		ContinuationRound: 2,
	}); err != nil {
		t.Fatalf("save intervention: %v", err)
	}
	if err := store.MarkSessionTurnWaitingApproval(session.ID, turnID); err != nil {
		t.Fatalf("mark waiting approval: %v", err)
	}
	if _, err := store.DecideSessionIntervention(session.ID, DecideSessionInterventionInput{
		TurnID: turnID, CallID: "call_approved", Status: InterventionStatusApproved, DecisionReason: "safe",
	}); err != nil {
		t.Fatalf("approve intervention: %v", err)
	}
	retried, err := store.DecideSessionIntervention(session.ID, DecideSessionInterventionInput{
		TurnID: turnID, CallID: "call_approved", Status: InterventionStatusApproved, DecisionReason: "retry",
	})
	if err != nil {
		t.Fatalf("retry approved intervention: %v", err)
	}
	if retried.Intervention.Status != InterventionStatusApproved || len(retried.Events) != 0 {
		t.Fatalf("expected idempotent approved retry without duplicate event, got %+v", retried)
	}

	claimed, err := store.ClaimSessionTurns(ClaimSessionTurnsInput{LeaseOwner: "instance-resume", LeaseDuration: time.Minute, Limit: 1})
	if err != nil {
		t.Fatalf("claim resumed turn: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ResumeIntervention == nil {
		t.Fatalf("expected claimed intervention resume, got %+v", claimed)
	}
	resume := claimed[0].ResumeIntervention
	if resume.CallID != "call_approved" || resume.Status != InterventionStatusApproved || resume.DecisionReason != "safe" || resume.ContinuationRound != 2 {
		t.Fatalf("unexpected claimed intervention: %+v", resume)
	}
	if _, err := store.AppendEvents(session.ID, []AppendEventInput{{Type: EventUserInterrupt}}); err != nil {
		t.Fatalf("cleanup interrupt: %v", err)
	}
}

func TestPostgresStorePlanApprovalIsUniqueAndResumable(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)
	events, err := store.AppendEvents(session.ID, []AppendEventInput{{
		Type: EventUserMessage, Payload: json.RawMessage(`{"content":[{"type":"text","text":"review plan"}]}`),
	}})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	turnID := payloadString(events[len(events)-1].Payload, "turn_id")
	if _, err := store.SaveSessionIntervention(session.ID, SaveSessionInterventionInput{
		TurnID: turnID, CallID: "call_plan", ToolIdentifier: "interaction", APIName: "request_plan_approval",
		Arguments: json.RawMessage(`{"plan_id":"plan_000001"}`), Kind: InterventionKindPlanApproval,
		Request:          json.RawMessage(`{"plan":{"id":"plan_000001","goal":"Ship safely","items":[]}}`),
		InterventionMode: "request_plan_approval", Reason: "plan_review",
		Continuation: json.RawMessage(`[{"role":"assistant","content":[]}]`),
	}); err != nil {
		t.Fatalf("save plan approval: %v", err)
	}
	if _, err := store.SaveSessionIntervention(session.ID, SaveSessionInterventionInput{
		TurnID: turnID, CallID: "call_plan_duplicate", ToolIdentifier: "interaction", APIName: "request_plan_approval",
		Kind: InterventionKindPlanApproval, InterventionMode: "request_plan_approval",
	}); err == nil {
		t.Fatal("expected only one pending plan approval per turn")
	}
	if err := store.MarkSessionTurnWaitingApproval(session.ID, turnID); err != nil {
		t.Fatalf("mark waiting approval: %v", err)
	}
	decision, err := store.DecideSessionIntervention(session.ID, DecideSessionInterventionInput{
		TurnID: turnID, CallID: "call_plan", Status: InterventionStatusRejected, DecisionReason: "reduce scope",
	})
	if err != nil {
		t.Fatalf("reject plan approval: %v", err)
	}
	if len(decision.Events) != 1 || decision.Events[0].Type != EventRuntimePlanApprovalRejected {
		t.Fatalf("expected dedicated plan rejection event, got %+v", decision.Events)
	}
	claimed, err := store.ClaimSessionTurns(ClaimSessionTurnsInput{LeaseOwner: "instance-plan-resume", LeaseDuration: time.Minute, Limit: 1})
	if err != nil {
		t.Fatalf("claim plan resume: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ResumeIntervention == nil || claimed[0].ResumeIntervention.Kind != InterventionKindPlanApproval || claimed[0].ResumeIntervention.Status != InterventionStatusRejected {
		t.Fatalf("expected rejected plan decision to resume same turn, got %+v", claimed)
	}
}

func TestPostgresStoreRetryOldDecisionDoesNotOverrideNewPendingIntervention(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)
	events, err := store.AppendEvents(session.ID, []AppendEventInput{{
		Type:    EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"two approvals"}]}`),
	}})
	if err != nil {
		t.Fatalf("append user message: %v", err)
	}
	turnID := payloadString(events[len(events)-1].Payload, "turn_id")
	if _, err := store.SaveSessionIntervention(session.ID, SaveSessionInterventionInput{
		TurnID: turnID, CallID: "call_first", ToolIdentifier: "default", APIName: "read_file", InterventionMode: "request_approval",
	}); err != nil {
		t.Fatalf("save first intervention: %v", err)
	}
	if err := store.MarkSessionTurnWaitingApproval(session.ID, turnID); err != nil {
		t.Fatalf("mark first waiting approval: %v", err)
	}
	if _, err := store.DecideSessionIntervention(session.ID, DecideSessionInterventionInput{
		TurnID: turnID, CallID: "call_first", Status: InterventionStatusApproved,
	}); err != nil {
		t.Fatalf("approve first intervention: %v", err)
	}
	if _, err := store.SaveSessionIntervention(session.ID, SaveSessionInterventionInput{
		TurnID: turnID, CallID: "call_second", ToolIdentifier: "default", APIName: "edit_file", InterventionMode: "request_approval",
	}); err != nil {
		t.Fatalf("save second intervention: %v", err)
	}
	if err := store.MarkSessionTurnWaitingApproval(session.ID, turnID); err != nil {
		t.Fatalf("mark second waiting approval: %v", err)
	}
	if _, err := store.DecideSessionIntervention(session.ID, DecideSessionInterventionInput{
		TurnID: turnID, CallID: "call_first", Status: InterventionStatusApproved,
	}); err != nil {
		t.Fatalf("retry first intervention: %v", err)
	}

	claimed, err := store.ClaimSessionTurns(ClaimSessionTurnsInput{LeaseOwner: "instance-old-retry", LeaseDuration: time.Minute, Limit: 1})
	if err != nil {
		t.Fatalf("claim after old retry: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("expected second pending approval to block old resume, got %+v", claimed)
	}
	pending, err := store.ListSessionInterventions(session.ID, InterventionStatusPending)
	if err != nil {
		t.Fatalf("list pending interventions: %v", err)
	}
	if len(pending) != 1 || pending[0].CallID != "call_second" {
		t.Fatalf("expected second intervention to remain pending, got %+v", pending)
	}
	if _, err := store.AppendEvents(session.ID, []AppendEventInput{{Type: EventUserInterrupt}}); err != nil {
		t.Fatalf("cleanup interrupt: %v", err)
	}
}

func TestPostgresStoreInterruptRejectsPendingTurnInterventions(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)
	started, err := store.AppendEvents(session.ID, []AppendEventInput{{
		Type:    EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"wait for approval"}]}`),
	}})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	turnID := payloadString(started[len(started)-1].Payload, "turn_id")
	if _, err := store.SaveSessionIntervention(session.ID, SaveSessionInterventionInput{
		TurnID: turnID, CallID: "call_interrupt", ToolIdentifier: "default", APIName: "edit_file", InterventionMode: "request_approval",
	}); err != nil {
		t.Fatalf("save intervention: %v", err)
	}
	if err := store.MarkSessionTurnWaitingApproval(session.ID, turnID); err != nil {
		t.Fatalf("mark waiting approval: %v", err)
	}

	events, err := store.AppendEvents(session.ID, []AppendEventInput{{Type: EventUserInterrupt}})
	if err != nil {
		t.Fatalf("interrupt turn: %v", err)
	}
	if len(events) != 5 || events[2].Type != EventRuntimeToolInterventionRejected || events[3].Type != EventRuntimeToolResult || events[4].Type != EventSessionStatusIdle {
		t.Fatalf("expected interrupt to reject and close the pending tool chain before idle, got %+v", events)
	}
	var toolResult struct {
		TurnID string `json:"turn_id"`
		Data   struct {
			ID         string `json:"id"`
			Identifier string `json:"identifier"`
			APIName    string `json:"api_name"`
			Status     string `json:"status"`
			Success    bool   `json:"success"`
			Reason     string `json:"reason"`
			Retryable  bool   `json:"retryable"`
			Error      struct {
				Type string `json:"type"`
			} `json:"error"`
		} `json:"data"`
	}
	if err := json.Unmarshal(events[3].Payload, &toolResult); err != nil {
		t.Fatalf("decode interrupted tool result: %v", err)
	}
	if toolResult.TurnID != turnID || toolResult.Data.ID != "call_interrupt" || toolResult.Data.Identifier != "default" || toolResult.Data.APIName != "edit_file" {
		t.Fatalf("unexpected interrupted tool result identity: %+v", toolResult)
	}
	if toolResult.Data.Status != "canceled" || toolResult.Data.Success || toolResult.Data.Reason != "user_interrupted" || toolResult.Data.Retryable || toolResult.Data.Error.Type != "tool_canceled" {
		t.Fatalf("unexpected interrupted tool result outcome: %+v", toolResult.Data)
	}
	pending, err := store.ListSessionInterventions(session.ID, InterventionStatusPending)
	if err != nil {
		t.Fatalf("list pending interventions: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected no pending interventions after interrupt, got %+v", pending)
	}
	rejected, err := store.ListSessionInterventions(session.ID, InterventionStatusRejected)
	if err != nil {
		t.Fatalf("list rejected interventions: %v", err)
	}
	if len(rejected) != 1 || rejected[0].DecisionReason != "turn interrupted by user" || rejected[0].DecidedAt == nil {
		t.Fatalf("expected interrupted intervention to be rejected, got %+v", rejected)
	}
}

func TestPostgresStoreFailedTurnReturnsSessionToIdle(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)

	startEvents, err := store.AppendEvents(session.ID, []AppendEventInput{{
		Type:    EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"fail me"}]}`),
	}})
	if err != nil {
		t.Fatalf("append user.message: %v", err)
	}
	turnID := payloadString(startEvents[1].Payload, "turn_id")

	failedEvents, err := store.FailSessionTurn(session.ID, turnID, "command turn failed")
	if err != nil {
		t.Fatalf("fail turn: %v", err)
	}
	if len(failedEvents) != 1 {
		t.Fatalf("expected 1 failure event, got %d", len(failedEvents))
	}
	if failedEvents[0].Type != EventSessionStatusIdle {
		t.Fatalf("expected failure to append %q, got %q", EventSessionStatusIdle, failedEvents[0].Type)
	}
	if got := payloadString(failedEvents[0].Payload, "last_turn_status"); got != "failed" {
		t.Fatalf("expected last_turn_status failed, got %q", got)
	}
	if got := payloadString(failedEvents[0].Payload, "reason"); got != "command turn failed" {
		t.Fatalf("expected failure reason, got %q", got)
	}

	assertPostgresSessionStatus(t, store, session.ID, SessionStatusIdle)
	status, errorMessage := postgresTurnState(t, store, session.ID, turnID)
	if status != "failed" {
		t.Fatalf("expected turn status failed, got %q", status)
	}
	if !strings.Contains(errorMessage, "command turn failed") {
		t.Fatalf("expected error_message to contain failure reason, got %q", errorMessage)
	}

	retryEvents, err := store.AppendEvents(session.ID, []AppendEventInput{{
		Type:    EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"retry"}]}`),
	}})
	if err != nil {
		t.Fatalf("append retry user.message after failed turn: %v", err)
	}
	if len(retryEvents) != 2 {
		t.Fatalf("expected retry to append 2 events, got %d", len(retryEvents))
	}
}

func TestPostgresStoreObjectRefsAndSessionArtifacts(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)

	object, err := store.CreateObjectRef(CreateObjectRefInput{
		WorkspaceID:    session.WorkspaceID,
		Bucket:         "tma-integration",
		ObjectKey:      "integration/" + session.ID + "/artifact.txt",
		ContentType:    "text/plain",
		SizeBytes:      12,
		ChecksumSHA256: "abc123",
		Metadata:       json.RawMessage(`{"source":"integration"}`),
		CreatedBy:      "integration-test",
	})
	if err != nil {
		t.Fatalf("create object ref: %v", err)
	}
	t.Cleanup(func() {
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM session_artifacts WHERE object_ref_id = $1`, object.ID); err != nil {
			t.Fatalf("cleanup session artifacts for object %s: %v", object.ID, err)
		}
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM object_refs WHERE id = $1`, object.ID); err != nil {
			t.Fatalf("cleanup object ref %s: %v", object.ID, err)
		}
	})

	fetched, err := store.GetObjectRef(object.ID)
	if err != nil {
		t.Fatalf("get object ref: %v", err)
	}
	if fetched.Bucket != object.Bucket || fetched.ObjectKey != object.ObjectKey || fetched.Visibility != ObjectVisibilityWorkspace {
		t.Fatalf("unexpected object ref: %+v", fetched)
	}

	artifact, err := store.CreateSessionArtifact(CreateSessionArtifactInput{
		SessionID:    session.ID,
		ObjectRefID:  object.ID,
		TurnID:       "turn_000001",
		ToolCallID:   "call_write",
		Name:         "artifact.txt",
		ArtifactType: ArtifactTypeFile,
		Metadata:     json.RawMessage(`{"preview":"hello"}`),
		CreatedBy:    "integration-test",
	})
	if err != nil {
		t.Fatalf("create session artifact: %v", err)
	}
	if artifact.WorkspaceID != session.WorkspaceID || artifact.EnvironmentID != session.EnvironmentID {
		t.Fatalf("unexpected artifact: %+v", artifact)
	}

	artifacts, err := store.ListSessionArtifacts(session.ID)
	if err != nil {
		t.Fatalf("list session artifacts: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].ID != artifact.ID || artifacts[0].ObjectRefID != object.ID {
		t.Fatalf("unexpected artifacts: %+v", artifacts)
	}
}

func TestPostgresStoreReapsExpiredWorkerWork(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	workspaceID := createPostgresIntegrationWorkspace(t, store, "reap-worker-work")
	worker, err := store.RegisterWorker(RegisterWorkerInput{
		WorkspaceID:  workspaceID,
		Name:         "integration-worker-" + time.Now().UTC().Format("20060102150405.000000000"),
		WorkerType:   WorkerTypeLocal,
		RegisteredBy: "integration-test",
		LeaseSeconds: 30,
	})
	if err != nil {
		t.Fatalf("register worker: %v", err)
	}
	t.Cleanup(func() {
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM worker_work WHERE workspace_id = $1`, workspaceID); err != nil {
			t.Fatalf("cleanup worker work for workspace %s: %v", workspaceID, err)
		}
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM workers WHERE id = $1`, worker.ID); err != nil {
			t.Fatalf("cleanup worker %s: %v", worker.ID, err)
		}
	})

	queued, err := store.EnqueueWorkerWork(EnqueueWorkerWorkInput{
		WorkspaceID: workspaceID,
		WorkerID:    worker.ID,
		WorkType:    WorkerWorkTypeSandboxCommand,
		Payload:     json.RawMessage(`{"command":"sh","args":["-c","sleep 100"]}`),
	})
	if err != nil {
		t.Fatalf("enqueue worker work: %v", err)
	}
	polled, err := store.PollWorkerWork(worker.ID, PollWorkerWorkInput{LeaseSeconds: 1})
	if err != nil {
		t.Fatalf("poll worker work: %v", err)
	}
	if polled == nil || polled.ID != queued.ID || polled.Status != WorkerWorkStatusLeased {
		t.Fatalf("expected leased work, got %+v", polled)
	}

	expiredAt := time.Unix(0, 0).UTC()
	if _, err := store.db.ExecContext(context.Background(), `UPDATE worker_work SET lease_expires_at = $1 WHERE id = $2`, expiredAt, queued.ID); err != nil {
		t.Fatalf("force expired lease: %v", err)
	}
	expired, err := store.ReapExpiredWorkerWork(ReapExpiredWorkerWorkInput{Limit: 1})
	if err != nil {
		t.Fatalf("reap expired worker work: %v", err)
	}
	if len(expired) != 1 || expired[0].ID != queued.ID {
		t.Fatalf("expected only test work to expire, got %+v", expired)
	}
	if expired[0].Status != WorkerWorkStatusFailed || expired[0].CompletedAt == nil || !strings.Contains(expired[0].ErrorMessage, "worker work lease expired") {
		t.Fatalf("unexpected expired work: %+v", expired[0])
	}
}

func TestPostgresStoreCancelsWorkerWork(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	workspaceID := createPostgresIntegrationWorkspace(t, store, "cancel-worker-work")
	worker, err := store.RegisterWorker(RegisterWorkerInput{
		WorkspaceID:  workspaceID,
		Name:         "integration-cancel-worker-" + time.Now().UTC().Format("20060102150405.000000000"),
		WorkerType:   WorkerTypeLocal,
		RegisteredBy: "integration-test",
		LeaseSeconds: 30,
	})
	if err != nil {
		t.Fatalf("register worker: %v", err)
	}
	t.Cleanup(func() {
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM worker_work WHERE workspace_id = $1`, workspaceID); err != nil {
			t.Fatalf("cleanup worker work for workspace %s: %v", workspaceID, err)
		}
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM workers WHERE id = $1`, worker.ID); err != nil {
			t.Fatalf("cleanup worker %s: %v", worker.ID, err)
		}
	})

	queued, err := store.EnqueueWorkerWork(EnqueueWorkerWorkInput{
		WorkspaceID: workspaceID,
		WorkerID:    worker.ID,
		WorkType:    WorkerWorkTypeSandboxCommand,
		Payload:     json.RawMessage(`{"command":"sh","args":["-c","sleep 100"]}`),
	})
	if err != nil {
		t.Fatalf("enqueue worker work: %v", err)
	}
	polled, err := store.PollWorkerWork(worker.ID, PollWorkerWorkInput{LeaseSeconds: 30})
	if err != nil {
		t.Fatalf("poll worker work: %v", err)
	}
	if polled == nil || polled.ID != queued.ID || polled.Status != WorkerWorkStatusLeased {
		t.Fatalf("expected leased work, got %+v", polled)
	}

	canceled, err := store.CancelWorkerWork(queued.ID, CancelWorkerWorkInput{Reason: "integration canceled"})
	if err != nil {
		t.Fatalf("cancel worker work: %v", err)
	}
	if canceled.Status != WorkerWorkStatusCanceled || canceled.ErrorMessage != "integration canceled" || canceled.CompletedAt == nil {
		t.Fatalf("unexpected canceled work: %+v", canceled)
	}
	heartbeat, err := store.HeartbeatWorkerWork(worker.ID, queued.ID, WorkerWorkHeartbeatInput{LeaseSeconds: 30})
	if err != nil {
		t.Fatalf("heartbeat canceled worker work: %v", err)
	}
	if heartbeat.Status != WorkerWorkStatusCanceled {
		t.Fatalf("expected heartbeat to return canceled work, got %+v", heartbeat)
	}

	completed, err := store.CompleteWorkerWork(worker.ID, queued.ID, CompleteWorkerWorkInput{
		Success: true,
		Result:  json.RawMessage(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("complete canceled worker work: %v", err)
	}
	if completed.Status != WorkerWorkStatusCanceled || string(completed.Result) == `{"ok":true}` {
		t.Fatalf("expected canceled work result to be preserved, got %+v result=%s", completed, string(completed.Result))
	}

	again, err := store.CancelWorkerWork(queued.ID, CancelWorkerWorkInput{Reason: "second reason"})
	if err != nil {
		t.Fatalf("cancel terminal worker work: %v", err)
	}
	if again.ErrorMessage != "integration canceled" {
		t.Fatalf("expected terminal cancel to preserve reason, got %+v", again)
	}

	requeued, err := store.RequeueWorkerWork(queued.ID, RequeueWorkerWorkInput{ClearWorker: true})
	if err != nil {
		t.Fatalf("requeue canceled worker work: %v", err)
	}
	if requeued.ID == queued.ID || requeued.Status != WorkerWorkStatusPending || requeued.WorkerID != "" {
		t.Fatalf("unexpected requeued work: %+v", requeued)
	}
	if requeued.WorkspaceID != queued.WorkspaceID || requeued.WorkType != queued.WorkType || string(requeued.Payload) != string(queued.Payload) {
		t.Fatalf("requeued work did not preserve original data: original=%+v requeued=%+v", queued, requeued)
	}
	if string(requeued.Result) != `{}` || requeued.StartedAt != nil || requeued.CompletedAt != nil || requeued.LeaseExpiresAt != nil {
		t.Fatalf("requeued work did not reset execution fields: %+v result=%s", requeued, string(requeued.Result))
	}
}

func TestPostgresStoreReapsExpiredWorkers(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	worker, err := store.RegisterWorker(RegisterWorkerInput{
		Name:         "integration-expired-worker-" + time.Now().UTC().Format("20060102150405.000000000"),
		WorkerType:   WorkerTypeLocal,
		RegisteredBy: "integration-test",
		LeaseSeconds: 30,
	})
	if err != nil {
		t.Fatalf("register worker: %v", err)
	}
	t.Cleanup(func() {
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM workers WHERE id = $1`, worker.ID); err != nil {
			t.Fatalf("cleanup worker %s: %v", worker.ID, err)
		}
	})

	expiredAt := time.Unix(0, 0).UTC()
	if _, err := store.db.ExecContext(context.Background(), `UPDATE workers SET lease_expires_at = $1 WHERE id = $2`, expiredAt, worker.ID); err != nil {
		t.Fatalf("force expired lease: %v", err)
	}
	expired, err := store.ReapExpiredWorkers(ReapExpiredWorkersInput{Limit: 1})
	if err != nil {
		t.Fatalf("reap expired workers: %v", err)
	}
	if len(expired) != 1 || expired[0].ID != worker.ID {
		t.Fatalf("expected only test worker to expire, got %+v", expired)
	}
	if expired[0].Status != WorkerStatusOffline {
		t.Fatalf("expected expired worker offline, got %+v", expired[0])
	}

	fetched, err := store.GetWorker(worker.ID)
	if err != nil {
		t.Fatalf("get worker: %v", err)
	}
	if fetched.Status != WorkerStatusOffline {
		t.Fatalf("expected fetched worker offline, got %+v", fetched)
	}
}

func TestPostgresStoreScopedTenantIsolation(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	alphaWorkspace := createPostgresIntegrationWorkspace(t, store, "scope-alpha")
	betaWorkspace := createPostgresIntegrationWorkspace(t, store, "scope-beta")
	suffix := strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")

	createAgent := func(workspaceID, name string) Agent {
		agent, err := store.CreateAgent(CreateAgentInput{
			WorkspaceID: workspaceID, Name: name + "-" + suffix, Model: "test-model", System: "scope test",
		})
		if err != nil {
			t.Fatalf("create %s agent: %v", name, err)
		}
		return agent
	}
	createEnvironment := func(workspaceID, name string) Environment {
		environment, err := store.CreateEnvironment(CreateEnvironmentInput{
			WorkspaceID: workspaceID, Name: name + "-" + suffix, Config: json.RawMessage(`{"type":"scope-test"}`),
		})
		if err != nil {
			t.Fatalf("create %s environment: %v", name, err)
		}
		return environment
	}
	createSession := func(workspaceID, ownerID string, agent Agent, environment Environment) Session {
		session, err := store.CreateSession(CreateSessionInput{
			WorkspaceID: workspaceID, OwnerID: ownerID, AgentID: agent.ID, EnvironmentID: environment.ID, CreatedBy: ownerID,
		})
		if err != nil {
			t.Fatalf("create %s session: %v", ownerID, err)
		}
		return session
	}

	alphaAgent := createAgent(alphaWorkspace, "alpha")
	betaAgent := createAgent(betaWorkspace, "beta")
	alphaEnvironment := createEnvironment(alphaWorkspace, "alpha")
	betaEnvironment := createEnvironment(betaWorkspace, "beta")
	alphaSession := createSession(alphaWorkspace, "owner-alpha", alphaAgent, alphaEnvironment)
	alphaPeerSession := createSession(alphaWorkspace, "owner-peer", alphaAgent, alphaEnvironment)
	betaSession := createSession(betaWorkspace, "owner-beta", betaAgent, betaEnvironment)
	alphaObject, err := store.CreateObjectRef(CreateObjectRefInput{
		WorkspaceID: alphaWorkspace, Bucket: "scope-test", ObjectKey: suffix + "/alpha.txt",
	})
	if err != nil {
		t.Fatalf("create alpha object: %v", err)
	}
	alphaArtifact, err := store.CreateSessionArtifact(CreateSessionArtifactInput{
		WorkspaceID: alphaWorkspace, SessionID: alphaSession.ID, ObjectRefID: alphaObject.ID,
		Name: "alpha-artifact.txt", ArtifactType: ArtifactTypeFile,
	})
	if err != nil {
		t.Fatalf("create alpha artifact: %v", err)
	}
	alphaWorker, err := store.RegisterWorker(RegisterWorkerInput{
		WorkspaceID: alphaWorkspace, Name: "alpha-worker-" + suffix, WorkerType: WorkerTypeLocal, RegisteredBy: "scope-test",
	})
	if err != nil {
		t.Fatalf("create alpha worker: %v", err)
	}
	betaWorker, err := store.RegisterWorker(RegisterWorkerInput{
		WorkspaceID: betaWorkspace, Name: "beta-worker-" + suffix, WorkerType: WorkerTypeLocal, RegisteredBy: "scope-test",
	})
	if err != nil {
		t.Fatalf("create beta worker: %v", err)
	}
	alphaWork, err := store.EnqueueWorkerWork(EnqueueWorkerWorkInput{
		WorkspaceID: alphaWorkspace, WorkerID: alphaWorker.ID, WorkType: WorkerWorkTypeToolExecution, Payload: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create alpha work: %v", err)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		for _, statement := range []struct {
			query string
			args  []any
		}{
			{`DELETE FROM worker_work WHERE id = $1`, []any{alphaWork.ID}},
			{`DELETE FROM workers WHERE id IN ($1, $2)`, []any{alphaWorker.ID, betaWorker.ID}},
			{`DELETE FROM session_artifacts WHERE id = $1`, []any{alphaArtifact.ID}},
			{`DELETE FROM object_refs WHERE id = $1`, []any{alphaObject.ID}},
			{`DELETE FROM sessions WHERE id IN ($1, $2, $3)`, []any{alphaSession.ID, alphaPeerSession.ID, betaSession.ID}},
			{`DELETE FROM environments WHERE id IN ($1, $2)`, []any{alphaEnvironment.ID, betaEnvironment.ID}},
			{`DELETE FROM agents WHERE id IN ($1, $2)`, []any{alphaAgent.ID, betaAgent.ID}},
		} {
			if _, err := store.db.ExecContext(ctx, statement.query, statement.args...); err != nil {
				t.Fatalf("cleanup scoped tenant fixture: %v", err)
			}
		}
	})

	alphaScope := AccessScope{WorkspaceID: alphaWorkspace}
	alphaOwnerScope := AccessScope{WorkspaceID: alphaWorkspace, OwnerID: "owner-alpha"}
	betaScope := AccessScope{WorkspaceID: betaWorkspace}
	for name, check := range map[string]func() error{
		"agent":             func() error { _, err := store.GetAgentScoped(alphaAgent.ID, betaScope); return err },
		"session workspace": func() error { _, err := store.GetSessionScoped(alphaSession.ID, betaScope); return err },
		"session owner":     func() error { _, err := store.GetSessionScoped(alphaPeerSession.ID, alphaOwnerScope); return err },
		"object":            func() error { _, err := store.GetObjectRefScoped(alphaObject.ID, betaScope); return err },
		"worker":            func() error { _, err := store.GetWorkerScoped(alphaWorker.ID, betaScope); return err },
		"worker work":       func() error { _, err := store.GetWorkerWorkScoped(alphaWork.ID, betaScope); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := check(); !errors.Is(err, ErrForbidden) {
				t.Fatalf("expected scoped lookup to be forbidden, got %v", err)
			}
		})
	}

	agents, err := store.ListAgentsScoped(alphaScope)
	if err != nil {
		t.Fatalf("list scoped agents: %v", err)
	}
	if !containsAgentID(agents, alphaAgent.ID) || containsAgentID(agents, betaAgent.ID) {
		t.Fatalf("scoped agents leaked another workspace: %+v", agents)
	}
	sessions, err := store.ListSessionsScoped(ListSessionsInput{IncludeArchived: true, Limit: 100}, alphaOwnerScope)
	if err != nil {
		t.Fatalf("list scoped sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != alphaSession.ID {
		t.Fatalf("scoped sessions leaked another owner or workspace: %+v", sessions)
	}
	workers, err := store.ListWorkersScoped(ListWorkersInput{}, alphaScope)
	if err != nil {
		t.Fatalf("list scoped workers: %v", err)
	}
	if !containsWorkerID(workers, alphaWorker.ID) || containsWorkerID(workers, betaWorker.ID) {
		t.Fatalf("scoped workers leaked another workspace: %+v", workers)
	}
	betaContext, err := ContextWithDatabaseAccessScope(context.Background(), betaScope)
	if err != nil {
		t.Fatalf("create beta database context: %v", err)
	}
	alphaOwnerContext, err := ContextWithDatabaseAccessScope(context.Background(), alphaOwnerScope)
	if err != nil {
		t.Fatalf("create alpha owner database context: %v", err)
	}
	if session, err := store.GetSessionContext(alphaOwnerContext, alphaSession.ID); err != nil || session.ID != alphaSession.ID {
		t.Fatalf("get owner-scoped Session through context: session=%+v err=%v", session, err)
	}
	if _, err := store.GetSessionContext(alphaOwnerContext, alphaPeerSession.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected context owner isolation, got %v", err)
	}
	contextSessions, err := store.ListSessionsContext(alphaOwnerContext, ListSessionsInput{IncludeArchived: true, Limit: 100})
	if err != nil || len(contextSessions) != 1 || contextSessions[0].ID != alphaSession.ID {
		t.Fatalf("context Session list leaked another owner or workspace: sessions=%+v err=%v", contextSessions, err)
	}
	pinned := true
	updatedSession, err := store.UpdateSessionMetadataContext(alphaOwnerContext, alphaSession.ID, UpdateSessionMetadataInput{Pinned: &pinned})
	if err != nil || updatedSession.PinnedAt == nil {
		t.Fatalf("update owner-scoped Session metadata: session=%+v err=%v", updatedSession, err)
	}
	updatedSession, err = store.UpdateSessionRuntimeSettingsContext(alphaOwnerContext, alphaSession.ID, UpdateSessionRuntimeSettingsInput{RuntimeSettings: json.RawMessage(`{"temperature":0.2}`), ExpectedRevision: alphaSession.RuntimeSettingsRevision})
	if err != nil || string(updatedSession.RuntimeSettings) != `{"temperature": 0.2}` {
		t.Fatalf("update owner-scoped Session runtime settings: session=%+v err=%v", updatedSession, err)
	}
	if events, err := store.ListEventsContext(alphaOwnerContext, alphaSession.ID, 0); err != nil || len(events) < 2 {
		t.Fatalf("list owner-scoped Session events: events=%+v err=%v", events, err)
	}
	savedSummary, err := store.SaveSessionSummaryContext(alphaOwnerContext, alphaSession.ID, UpsertSessionSummaryInput{SummaryText: "owner scoped summary", SourceUntilSeq: 1})
	if err != nil || savedSummary.SessionID != alphaSession.ID {
		t.Fatalf("save owner-scoped Session summary: summary=%+v err=%v", savedSummary, err)
	}
	if summary, err := store.GetSessionSummaryContext(alphaOwnerContext, alphaSession.ID); err != nil || summary.SummaryText != savedSummary.SummaryText {
		t.Fatalf("get owner-scoped Session summary: summary=%+v err=%v", summary, err)
	}
	stream, cancelStream, err := store.SubscribeEventsContext(alphaOwnerContext, alphaSession.ID, 0)
	if err != nil || stream == nil || cancelStream == nil {
		t.Fatalf("subscribe owner-scoped Session events: stream=%v cancel=%v err=%v", stream != nil, cancelStream != nil, err)
	}
	cancelStream()
	for name, check := range map[string]func() error{
		"context subagent session create": func() error {
			_, err := store.CreateSubagentSessionContext(alphaOwnerContext, CreateSubagentSessionInput{Session: CreateSessionInput{
				ParentSessionID: alphaPeerSession.ID, AgentID: alphaAgent.ID, EnvironmentID: alphaEnvironment.ID,
			}})
			return err
		},
		"context session config upgrade": func() error {
			_, err := store.UpgradeSessionAgentConfigContext(alphaOwnerContext, alphaPeerSession.ID, UpgradeSessionAgentConfigInput{ToCurrent: true})
			return err
		},
		"context subagent turn start": func() error {
			_, err := store.StartSubagentTurnContext(alphaOwnerContext, StartSubagentTurnInput{SessionID: alphaPeerSession.ID, Payload: json.RawMessage(`{}`)})
			return err
		},
		"context session summary get": func() error {
			_, err := store.GetSessionSummaryContext(alphaOwnerContext, alphaPeerSession.ID)
			return err
		},
		"context session summary save": func() error {
			_, err := store.SaveSessionSummaryContext(alphaOwnerContext, alphaPeerSession.ID, UpsertSessionSummaryInput{SummaryText: "blocked"})
			return err
		},
		"context session summary upsert": func() error {
			_, err := store.UpsertSessionSummaryContext(alphaOwnerContext, alphaPeerSession.ID, UpsertSessionSummaryInput{SummaryText: "blocked"})
			return err
		},
		"context session intervention save": func() error {
			_, err := store.SaveSessionInterventionContext(alphaOwnerContext, alphaPeerSession.ID, SaveSessionInterventionInput{
				TurnID: "turn_blocked", CallID: "call_blocked", ToolIdentifier: "blocked", APIName: "blocked", InterventionMode: "required",
			})
			return err
		},
		"context session intervention list": func() error {
			_, err := store.ListSessionInterventionsContext(alphaOwnerContext, alphaPeerSession.ID, "")
			return err
		},
		"context session intervention decide": func() error {
			_, err := store.DecideSessionInterventionContext(alphaOwnerContext, alphaPeerSession.ID, DecideSessionInterventionInput{
				TurnID: "turn_blocked", CallID: "call_blocked", Status: InterventionStatusRejected,
			})
			return err
		},
		"context session waiting approval": func() error {
			return store.MarkSessionTurnWaitingApprovalContext(alphaOwnerContext, alphaPeerSession.ID, "turn_blocked")
		},
		"context runtime config resolve": func() error {
			_, err := store.ResolveAgentRuntimeConfigContext(alphaOwnerContext, alphaPeerSession.ID)
			return err
		},
		"context conversation list": func() error {
			_, err := store.ListConversationMessagesContext(alphaOwnerContext, alphaPeerSession.ID, 1)
			return err
		},
		"context session llm usage": func() error {
			_, err := store.GetSessionLLMUsageContext(alphaOwnerContext, alphaPeerSession.ID)
			return err
		},
		"context runtime event append": func() error {
			_, err := store.AppendRuntimeEventContext(alphaOwnerContext, alphaPeerSession.ID, "turn_blocked", AppendEventInput{Type: EventRuntimeFailed})
			return err
		},
		"context session turn complete": func() error {
			_, err := store.CompleteSessionTurnContext(alphaOwnerContext, alphaPeerSession.ID, "turn_blocked", json.RawMessage(`{}`))
			return err
		},
		"context session turn fail": func() error {
			_, err := store.FailSessionTurnContext(alphaOwnerContext, alphaPeerSession.ID, "turn_blocked", "blocked")
			return err
		},
		"context session metadata update": func() error {
			_, err := store.UpdateSessionMetadataContext(alphaOwnerContext, alphaPeerSession.ID, UpdateSessionMetadataInput{Pinned: &pinned})
			return err
		},
		"context session runtime settings update": func() error {
			_, err := store.UpdateSessionRuntimeSettingsContext(alphaOwnerContext, alphaPeerSession.ID, UpdateSessionRuntimeSettingsInput{RuntimeSettings: json.RawMessage(`{}`), ExpectedRevision: alphaPeerSession.RuntimeSettingsRevision})
			return err
		},
		"context session archive": func() error {
			_, err := store.ArchiveSessionContext(alphaOwnerContext, alphaPeerSession.ID)
			return err
		},
		"context session restore": func() error {
			_, err := store.RestoreSessionContext(alphaOwnerContext, alphaPeerSession.ID)
			return err
		},
		"context session delete": func() error {
			return store.DeleteSessionContext(alphaOwnerContext, alphaPeerSession.ID)
		},
		"context session append events": func() error {
			_, err := store.AppendEventsContext(alphaOwnerContext, alphaPeerSession.ID, []AppendEventInput{{Type: EventAgentMessage, Payload: json.RawMessage(`{}`)}})
			return err
		},
		"context session list events": func() error {
			_, err := store.ListEventsContext(alphaOwnerContext, alphaPeerSession.ID, 0)
			return err
		},
		"context session subscribe events": func() error {
			_, cancel, err := store.SubscribeEventsContext(alphaOwnerContext, alphaPeerSession.ID, 0)
			if cancel != nil {
				cancel()
			}
			return err
		},
		"context agent get": func() error {
			_, err := store.GetAgentContext(betaContext, alphaAgent.ID)
			return err
		},
		"context agent update": func() error {
			_, err := store.UpdateAgentContext(betaContext, UpdateAgentInput{AgentID: alphaAgent.ID, Name: "blocked"})
			return err
		},
		"context agent config list": func() error {
			_, err := store.ListAgentConfigVersionsContext(betaContext, alphaAgent.ID)
			return err
		},
		"context agent config create": func() error {
			_, err := store.CreateAgentConfigVersionContext(betaContext, CreateAgentConfigVersionInput{
				AgentID: alphaAgent.ID, LLMProvider: alphaAgent.ConfigVersion.LLMProvider,
				LLMModel: alphaAgent.ConfigVersion.LLMModel, System: alphaAgent.ConfigVersion.System,
			})
			return err
		},
		"context agent ensure": func() error {
			_, err := store.EnsureAgentContext(betaContext, EnsureAgentInput{
				ID: alphaAgent.ID, WorkspaceID: alphaWorkspace, Name: alphaAgent.Name,
				LLMProvider: alphaAgent.ConfigVersion.LLMProvider, LLMModel: alphaAgent.ConfigVersion.LLMModel,
			})
			return err
		},
		"context object get": func() error { _, err := store.GetObjectRefContext(betaContext, alphaObject.ID); return err },
		"context object count": func() error {
			_, err := store.CountSessionArtifactsByObjectRefContext(betaContext, alphaObject.ID)
			return err
		},
		"context object delete": func() error { return store.DeleteObjectRefContext(betaContext, alphaObject.ID) },
		"context artifact get": func() error {
			_, err := store.GetSessionArtifactContext(betaContext, alphaSession.ID, alphaArtifact.ID)
			return err
		},
		"context artifact list": func() error {
			_, err := store.ListSessionArtifactsContext(betaContext, alphaSession.ID)
			return err
		},
		"context artifact delete": func() error {
			return store.DeleteSessionArtifactContext(betaContext, alphaSession.ID, alphaArtifact.ID)
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := check(); !errors.Is(err, ErrForbidden) {
				t.Fatalf("expected context-scoped operation to be forbidden, got %v", err)
			}
		})
	}
	if _, err := store.CreateObjectRefContext(betaContext, CreateObjectRefInput{
		WorkspaceID: alphaWorkspace, Bucket: "scope-test", ObjectKey: suffix + "/blocked.txt",
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected context-derived create workspace rejection, got %v", err)
	}
	if _, err := store.CreateAgentContext(betaContext, CreateAgentInput{
		WorkspaceID: alphaWorkspace, Name: "blocked-agent", Model: "test-model",
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected context-derived Agent workspace rejection, got %v", err)
	}
	if _, err := store.CreateSessionContext(alphaOwnerContext, CreateSessionInput{
		WorkspaceID: alphaWorkspace, OwnerID: "owner-peer", AgentID: alphaAgent.ID, EnvironmentID: alphaEnvironment.ID,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected context-derived Session owner rejection, got %v", err)
	}
	betaAgents, err := store.ListAgentsContext(betaContext)
	if err != nil {
		t.Fatalf("list context-scoped beta Agents: %v", err)
	}
	if containsAgentID(betaAgents, alphaAgent.ID) || !containsAgentID(betaAgents, betaAgent.ID) {
		t.Fatalf("context-scoped Agent list leaked another workspace: %+v", betaAgents)
	}
}

func TestPostgresEnvironmentVariablesOwnerIsolation(t *testing.T) {
	adminStore := newPostgresIntegrationStore(t)
	workspaceID := createPostgresIntegrationWorkspace(t, adminStore, "environment-owner-rls")
	suffix := strings.ReplaceAll(time.Now().UTC().Format("150405.000000000"), ".", "")
	role := "tma_env_rls_" + suffix
	password := "tma_env_rls_password_32_bytes"
	if _, err := adminStore.db.ExecContext(context.Background(), `CREATE ROLE `+role+` LOGIN PASSWORD '`+password+`'`); err != nil {
		t.Fatalf("create environment RLS test role: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminStore.db.ExecContext(context.Background(), `DROP OWNED BY `+role)
		if _, err := adminStore.db.ExecContext(context.Background(), `DROP ROLE `+role); err != nil {
			t.Errorf("drop environment RLS test role: %v", err)
		}
	})
	if _, err := adminStore.db.ExecContext(context.Background(), `GRANT USAGE ON SCHEMA public TO `+role); err != nil {
		t.Fatalf("grant environment RLS schema access: %v", err)
	}
	if _, err := adminStore.db.ExecContext(context.Background(), `
		GRANT SELECT, INSERT, UPDATE, DELETE ON managed_environment_variables TO `+role); err != nil {
		t.Fatalf("grant environment RLS table access: %v", err)
	}

	databaseURL, err := url.Parse(os.Getenv("TMA_DATABASE_URL"))
	if err != nil {
		t.Fatalf("parse environment RLS database URL: %v", err)
	}
	databaseURL.User = url.UserPassword(role, password)
	restrictedDB, err := sql.Open("pgx", databaseURL.String())
	if err != nil {
		t.Fatalf("open environment RLS database: %v", err)
	}
	t.Cleanup(func() { _ = restrictedDB.Close() })
	if err := restrictedDB.PingContext(t.Context()); err != nil {
		t.Fatalf("ping environment RLS database: %v", err)
	}
	restrictedStore := &PostgresStore{db: restrictedDB}

	workspaceContext, err := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: workspaceID})
	if err != nil {
		t.Fatal(err)
	}
	aliceContext, err := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: workspaceID, OwnerID: "owner-alice"})
	if err != nil {
		t.Fatal(err)
	}
	bobContext, err := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: workspaceID, OwnerID: "owner-bob"})
	if err != nil {
		t.Fatal(err)
	}
	for ctx, input := range map[context.Context]envvars.EncryptedVariable{
		workspaceContext: {WorkspaceID: workspaceID, Name: "SHARED_KEY", Ciphertext: []byte("shared")},
		aliceContext:     {WorkspaceID: workspaceID, Name: "ALICE_KEY", Ciphertext: []byte("alice")},
		bobContext:       {WorkspaceID: workspaceID, Name: "BOB_KEY", Ciphertext: []byte("bob")},
	} {
		if _, err := restrictedStore.UpsertEncryptedEnvironmentVariable(ctx, input); err != nil {
			t.Fatalf("create environment variable %s: %v", input.Name, err)
		}
	}

	aliceRecords, err := restrictedStore.ListEncryptedEnvironmentVariables(aliceContext, workspaceID)
	if err != nil {
		t.Fatalf("list Alice environment variables: %v", err)
	}
	aliceNames := make(map[string]string, len(aliceRecords))
	for _, record := range aliceRecords {
		aliceNames[record.Name] = record.OwnerID
	}
	if len(aliceNames) != 2 || aliceNames["SHARED_KEY"] != "" || aliceNames["ALICE_KEY"] != "owner-alice" {
		t.Fatalf("Alice environment scope was not shared plus personal: %+v", aliceNames)
	}
	if _, visible := aliceNames["BOB_KEY"]; visible {
		t.Fatalf("Alice could see Bob's personal environment variable: %+v", aliceNames)
	}

	workspaceRecords, err := restrictedStore.ListEncryptedEnvironmentVariables(workspaceContext, workspaceID)
	if err != nil || len(workspaceRecords) != 1 || workspaceRecords[0].Name != "SHARED_KEY" || workspaceRecords[0].OwnerID != "" {
		t.Fatalf("workspace environment scope exposed personal variables: records=%+v err=%v", workspaceRecords, err)
	}
	if err := restrictedStore.DeleteEncryptedEnvironmentVariable(aliceContext, workspaceID, "SHARED_KEY"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Alice could delete a shared environment variable: %v", err)
	}
	if _, err := restrictedStore.UpsertEncryptedEnvironmentVariable(aliceContext, envvars.EncryptedVariable{
		WorkspaceID: workspaceID, OwnerID: "owner-bob", Name: "BOB_KEY", Ciphertext: []byte("blocked"),
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Alice could update Bob's environment variable: %v", err)
	}
	if err := restrictedStore.DeleteEncryptedEnvironmentVariable(aliceContext, workspaceID, "ALICE_KEY"); err != nil {
		t.Fatalf("Alice could not delete her personal environment variable: %v", err)
	}
}

func TestPostgresTenantTablesForceWorkspaceRLS(t *testing.T) {
	adminStore := newPostgresIntegrationStore(t)
	if err := adminStore.ValidateDatabaseTenantIsolation(context.Background()); err == nil || !strings.Contains(err.Error(), "superuser") {
		t.Fatalf("expected migration/admin role to be rejected for production runtime use, got %v", err)
	}
	alphaWorkspace := createPostgresIntegrationWorkspace(t, adminStore, "rls-alpha")
	betaWorkspace := createPostgresIntegrationWorkspace(t, adminStore, "rls-beta")
	suffix := strings.ReplaceAll(time.Now().UTC().Format("150405.000000000"), ".", "")
	otherOrganization := "org_rls_other_" + suffix
	otherWorkspace := "wksp_rls_other_" + suffix
	if _, err := adminStore.db.ExecContext(context.Background(), `INSERT INTO organizations (id, name) VALUES ($1, 'RLS Other Organization')`, otherOrganization); err != nil {
		t.Fatalf("create cross-organization RLS fixture: %v", err)
	}
	if _, err := adminStore.db.ExecContext(context.Background(), `INSERT INTO workspaces (id, org_id, name) VALUES ($1, $2, 'RLS Other Workspace')`, otherWorkspace, otherOrganization); err != nil {
		t.Fatalf("create cross-organization RLS workspace: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminStore.db.ExecContext(context.Background(), `DELETE FROM workspaces WHERE id = $1`, otherWorkspace)
		_, _ = adminStore.db.ExecContext(context.Background(), `DELETE FROM organizations WHERE id = $1`, otherOrganization)
	})
	alphaAgent, err := adminStore.CreateAgent(CreateAgentInput{
		WorkspaceID: alphaWorkspace, Name: "rls-alpha-agent-" + suffix, Model: "test-model", System: "RLS test",
	})
	if err != nil {
		t.Fatalf("create alpha RLS agent: %v", err)
	}
	betaAgent, err := adminStore.CreateAgent(CreateAgentInput{
		WorkspaceID: betaWorkspace, Name: "rls-beta-agent-" + suffix, Model: "test-model", System: "RLS test",
	})
	if err != nil {
		t.Fatalf("create beta RLS agent: %v", err)
	}
	alphaEnvironment, err := adminStore.CreateEnvironment(CreateEnvironmentInput{
		WorkspaceID: alphaWorkspace, Name: "rls-alpha-environment-" + suffix, Config: json.RawMessage(`{"type":"rls-test"}`),
	})
	if err != nil {
		t.Fatalf("create alpha RLS environment: %v", err)
	}
	betaEnvironment, err := adminStore.CreateEnvironment(CreateEnvironmentInput{
		WorkspaceID: betaWorkspace, Name: "rls-beta-environment-" + suffix, Config: json.RawMessage(`{"type":"rls-test"}`),
	})
	if err != nil {
		t.Fatalf("create beta RLS environment: %v", err)
	}
	alphaSession, err := adminStore.CreateSession(CreateSessionInput{
		WorkspaceID: alphaWorkspace, OwnerID: "owner-alpha", AgentID: alphaAgent.ID,
		EnvironmentID: alphaEnvironment.ID, CreatedBy: "owner-alpha",
	})
	if err != nil {
		t.Fatalf("create alpha RLS session: %v", err)
	}
	alphaPeerSession, err := adminStore.CreateSession(CreateSessionInput{
		WorkspaceID: alphaWorkspace, OwnerID: "owner-peer", AgentID: alphaAgent.ID,
		EnvironmentID: alphaEnvironment.ID, CreatedBy: "owner-peer",
	})
	if err != nil {
		t.Fatalf("create alpha peer RLS session: %v", err)
	}
	betaSession, err := adminStore.CreateSession(CreateSessionInput{
		WorkspaceID: betaWorkspace, OwnerID: "owner-beta", AgentID: betaAgent.ID,
		EnvironmentID: betaEnvironment.ID, CreatedBy: "owner-beta",
	})
	if err != nil {
		t.Fatalf("create beta RLS session: %v", err)
	}
	betaTurnEvents, err := adminStore.AppendEvents(betaSession.ID, []AppendEventInput{{
		Type: EventUserMessage, Payload: json.RawMessage(`{"content":[{"type":"text","text":"beta RLS turn"}]}`),
	}})
	if err != nil {
		t.Fatalf("create beta RLS turn: %v", err)
	}
	betaTurnID := payloadString(betaTurnEvents[len(betaTurnEvents)-1].Payload, "turn_id")
	betaTraceID := "trace_rls_beta_" + suffix
	if err := adminStore.UpsertTraceIndex(UpsertTraceIndexInput{Trace: TraceIndexEntry{
		TraceID: betaTraceID, SessionID: betaSession.ID, TurnID: betaTurnID,
		StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("create beta RLS trace: %v", err)
	}
	betaTaskGroup, err := adminStore.CreateSubagentTaskGroup(CreateSubagentTaskGroupInput{
		WorkspaceID: betaWorkspace, OwnerID: "owner-beta", ParentSessionID: betaSession.ID,
		ParentTurnID: betaTurnID, Strategy: SubagentTaskGroupStrategyAllCompleted,
		ResultReducer: SubagentTaskGroupReducerConcatText, PlannedCount: 1,
	})
	if err != nil {
		t.Fatalf("create beta RLS task group: %v", err)
	}
	betaDeliberation, err := adminStore.CreateAgentDeliberation(CreateAgentDeliberationInput{
		Deliberation: AgentDeliberation{
			WorkspaceID: betaWorkspace, OwnerID: "owner-beta", ParentSessionID: betaSession.ID,
			ParentTurnID: betaTurnID, Objective: "beta RLS deliberation", Strategy: "expert_panel",
			ModeratorAgentID: betaAgent.ID, ModeratorEnvironmentID: betaEnvironment.ID,
		},
		Participants: []AgentDeliberationParticipant{
			{RoleID: "beta_primary", RoleTitle: "Primary", Goal: "Propose", AgentID: betaAgent.ID, EnvironmentID: betaEnvironment.ID},
			{RoleID: "beta_reviewer", RoleTitle: "Reviewer", Goal: "Review", AgentID: betaAgent.ID, EnvironmentID: betaEnvironment.ID},
		},
	})
	if err != nil {
		t.Fatalf("create beta RLS deliberation: %v", err)
	}
	if _, err := adminStore.CreateAgentDeliberationRound(AgentDeliberationRound{
		DeliberationID: betaDeliberation.ID, RoundNumber: 1, RoundType: "independent_brainstorm",
		Status: "running", TaskGroupID: betaTaskGroup.ID,
	}); err != nil {
		t.Fatalf("create beta RLS deliberation round: %v", err)
	}
	peerDeliberation, err := adminStore.CreateAgentDeliberation(CreateAgentDeliberationInput{
		Deliberation: AgentDeliberation{
			WorkspaceID: alphaWorkspace, OwnerID: "owner-peer", ParentSessionID: alphaPeerSession.ID,
			Objective: "peer RLS deliberation", Strategy: "expert_panel",
			ModeratorAgentID: alphaAgent.ID, ModeratorEnvironmentID: alphaEnvironment.ID,
		},
		Participants: []AgentDeliberationParticipant{
			{RoleID: "peer_primary", RoleTitle: "Primary", Goal: "Propose", AgentID: alphaAgent.ID, EnvironmentID: alphaEnvironment.ID},
			{RoleID: "peer_reviewer", RoleTitle: "Reviewer", Goal: "Review", AgentID: alphaAgent.ID, EnvironmentID: alphaEnvironment.ID},
		},
	})
	if err != nil {
		t.Fatalf("create peer RLS deliberation: %v", err)
	}
	betaSkill, err := adminStore.CreateSkill(context.Background(), skills.CreateSkillInput{
		WorkspaceID: betaWorkspace, Identifier: "rls-beta-skill-" + suffix,
		Title: "Beta RLS Skill", CreatedBy: "owner-beta",
	})
	if err != nil {
		t.Fatalf("create beta RLS skill: %v", err)
	}
	betaSkillVersion, err := adminStore.CreateSkillVersion(context.Background(), skills.CreateVersionInput{
		SkillID: betaSkill.ID, ContentFormat: "markdown", ContentText: "beta RLS skill", CreatedBy: "owner-beta",
	})
	if err != nil {
		t.Fatalf("create beta RLS skill version: %v", err)
	}
	betaMarketplacePolicy, _, err := adminStore.CreateMarketplacePolicy(context.Background(), skillmarketplace.CreatePolicyInput{
		ScopeType: skillmarketplace.PolicyScopeWorkspace, WorkspaceID: betaWorkspace,
		Config: skillmarketplace.Policy{AllowedOwners: []string{"beta"}}, CreatedBy: "owner-beta",
	})
	if err != nil {
		t.Fatalf("create beta RLS marketplace policy: %v", err)
	}
	betaRetentionPolicy, betaRetentionVersion, err := adminStore.CreateSkillAssetRetentionPolicy(context.Background(), skillretention.CreatePolicyInput{
		ScopeType: skillretention.ScopeWorkspace, WorkspaceID: betaWorkspace,
		Config: skillretention.Policy{Enabled: true, RetentionDays: 30, DeleteLimit: 10}, CreatedBy: "owner-beta",
	})
	if err != nil {
		t.Fatalf("create beta RLS retention policy: %v", err)
	}
	betaRetentionEffective := skillretention.EffectivePolicy{
		Source: skillretention.ScopeWorkspace, Policy: betaRetentionPolicy, Version: betaRetentionVersion,
		Config: betaRetentionVersion.Config, Revision: betaRetentionVersion.Checksum,
	}
	betaGCRun, _, err := adminStore.StartSkillAssetGCRun(context.Background(), skillretention.StartRunInput{
		WorkspaceID: betaWorkspace, Effective: betaRetentionEffective,
		RequestedBy: "owner-beta", StartedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create beta RLS GC run: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, statement := range []struct {
			query string
			args  []any
		}{
			{`DELETE FROM skill_asset_gc_tombstones WHERE workspace_id IN ($1, $2)`, []any{alphaWorkspace, betaWorkspace}},
			{`DELETE FROM skill_asset_gc_runs WHERE workspace_id IN ($1, $2)`, []any{alphaWorkspace, betaWorkspace}},
			{`DELETE FROM skill_asset_retention_policies WHERE workspace_id IN ($1, $2) OR organization_id = 'org_default'`, []any{alphaWorkspace, betaWorkspace}},
			{`DELETE FROM skill_marketplace_entries WHERE workspace_id IN ($1, $2)`, []any{alphaWorkspace, betaWorkspace}},
			{`DELETE FROM skill_marketplace_policies WHERE workspace_id IN ($1, $2) OR organization_id = 'org_default'`, []any{alphaWorkspace, betaWorkspace}},
			{`DELETE FROM session_artifacts WHERE workspace_id IN ($1, $2)`, []any{alphaWorkspace, betaWorkspace}},
			{`DELETE FROM observability_exporter_runs WHERE workspace_id IN ($1, $2)`, []any{alphaWorkspace, betaWorkspace}},
			{`DELETE FROM operator_audit_log WHERE workspace_id IN ($1, $2)`, []any{alphaWorkspace, betaWorkspace}},
			{`DELETE FROM security_audit_outbox WHERE workspace_id IN ($1, $2) OR id LIKE $3`, []any{alphaWorkspace, betaWorkspace, "saud_rls_" + suffix + "%"}},
			{`DELETE FROM worker_work WHERE workspace_id IN ($1, $2)`, []any{alphaWorkspace, betaWorkspace}},
			{`DELETE FROM workers WHERE workspace_id IN ($1, $2)`, []any{alphaWorkspace, betaWorkspace}},
			{`DELETE FROM sessions WHERE workspace_id IN ($1, $2)`, []any{alphaWorkspace, betaWorkspace}},
			{`DELETE FROM skills WHERE workspace_id IN ($1, $2)`, []any{alphaWorkspace, betaWorkspace}},
			{`DELETE FROM object_refs WHERE workspace_id IN ($1, $2)`, []any{alphaWorkspace, betaWorkspace}},
			{`DELETE FROM mcp_registry_servers WHERE workspace_id IN ($1, $2)`, []any{alphaWorkspace, betaWorkspace}},
			{`DELETE FROM environments WHERE workspace_id IN ($1, $2)`, []any{alphaWorkspace, betaWorkspace}},
			{`DELETE FROM agents WHERE workspace_id IN ($1, $2)`, []any{alphaWorkspace, betaWorkspace}},
		} {
			if _, err := adminStore.db.ExecContext(ctx, statement.query, statement.args...); err != nil {
				t.Errorf("cleanup tenant RLS fixture: %v", err)
			}
		}
	})
	role := "tma_rls_test_" + suffix
	password := "tma_rls_test_password_32_bytes"
	if _, err := adminStore.db.ExecContext(context.Background(), `CREATE ROLE `+role+` LOGIN PASSWORD '`+password+`'`); err != nil {
		t.Fatalf("create RLS test role: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminStore.db.ExecContext(context.Background(), `DROP OWNED BY `+role)
		if _, err := adminStore.db.ExecContext(context.Background(), `DROP ROLE `+role); err != nil {
			t.Fatalf("drop RLS test role: %v", err)
		}
	})
	if _, err := adminStore.db.ExecContext(context.Background(), `GRANT USAGE ON SCHEMA public TO `+role); err != nil {
		t.Fatalf("grant schema access to RLS test role: %v", err)
	}
	if _, err := adminStore.db.ExecContext(context.Background(), `GRANT SELECT ON workspaces, organizations TO `+role); err != nil {
		t.Fatalf("grant tenant directory access to RLS test role: %v", err)
	}
	if _, err := adminStore.db.ExecContext(context.Background(), `
		GRANT SELECT, INSERT, UPDATE, DELETE
		ON agent_deliberation_contributions, agent_deliberation_participants, agent_deliberation_rounds, agent_deliberations,
		agents, agent_config_versions, agent_loop_states, agent_schedule_runs, agent_schedules, environments, managed_environment_variables,
			llm_usage_records, mcp_registry_servers, mcp_registry_server_versions, object_refs,
			observability_exporter_runs, operator_audit_log, security_audit_outbox, session_artifacts,
		session_events, session_interventions, session_summaries, session_task_items, session_task_plans, session_turn_skill_usages, session_turns, sessions,
		skill_asset_gc_items, skill_asset_gc_runs, skill_asset_gc_tombstones,
		skill_asset_retention_policies, skill_asset_retention_policy_versions,
		skill_marketplace_entries, skill_marketplace_policies, skill_marketplace_policy_versions,
		skill_version_package_files, skill_versions, skills,
		subagent_start_requests, subagent_task_group_items, subagent_task_groups, tool_permission_audit_records, trace_indexes, trace_span_indexes,
		worker_work, workers, workspace_tool_permission_policies TO `+role); err != nil {
		t.Fatalf("grant tenant table access to RLS test role: %v", err)
	}
	if _, err := adminStore.db.ExecContext(context.Background(), `
		GRANT SELECT ON session_summaries, session_events, llm_providers, llm_models TO `+role); err != nil {
		t.Fatalf("grant session lookup access to RLS test role: %v", err)
	}
	if _, err := adminStore.db.ExecContext(context.Background(), `GRANT INSERT ON session_events TO `+role); err != nil {
		t.Fatalf("grant session creation access to RLS test role: %v", err)
	}
	if _, err := adminStore.db.ExecContext(context.Background(), `GRANT SELECT, INSERT, UPDATE, DELETE ON session_turns TO `+role); err != nil {
		t.Fatalf("grant Session turn access to RLS test role: %v", err)
	}
	if _, err := adminStore.db.ExecContext(context.Background(), `
		GRANT USAGE ON SEQUENCE tma_agent_id_seq, tma_agent_deliberation_id_seq, tma_agent_schedule_id_seq, tma_agent_schedule_run_id_seq, tma_environment_id_seq, tma_session_id_seq, tma_event_id_seq, tma_llm_usage_id_seq,
		tma_mcp_registry_server_id_seq, tma_mcp_registry_version_id_seq,
			tma_object_ref_id_seq, tma_observability_exporter_run_id_seq, tma_operator_audit_id_seq,
			tma_session_artifact_id_seq, tma_skill_asset_gc_item_id_seq,
		tma_skill_asset_gc_run_id_seq, tma_skill_asset_gc_tombstone_id_seq,
		tma_skill_asset_retention_policy_id_seq, tma_skill_asset_retention_policy_version_id_seq,
		tma_skill_id_seq, tma_skill_marketplace_entry_id_seq,
		tma_skill_marketplace_policy_id_seq, tma_skill_marketplace_policy_version_id_seq,
		tma_skill_usage_id_seq, tma_skill_version_id_seq, tma_subagent_start_request_id_seq,
		tma_subagent_task_group_id_seq, tma_task_item_id_seq, tma_task_plan_id_seq,
		tma_worker_id_seq, tma_worker_work_id_seq TO `+role); err != nil {
		t.Fatalf("grant tenant object sequence access to RLS test role: %v", err)
	}

	databaseURL, err := url.Parse(os.Getenv("TMA_DATABASE_URL"))
	if err != nil {
		t.Fatalf("parse integration database URL: %v", err)
	}
	databaseURL.User = url.UserPassword(role, password)
	restrictedStore, err := NewPostgresStore(databaseURL.String())
	if err != nil {
		t.Fatalf("open restricted RLS store: %v", err)
	}
	t.Cleanup(func() {
		if err := restrictedStore.Close(); err != nil {
			t.Fatalf("close restricted RLS store: %v", err)
		}
	})
	if err := restrictedStore.ValidateDatabaseTenantIsolation(context.Background()); err != nil {
		t.Fatalf("validate restricted production runtime role: %v", err)
	}
	controlProviderID := "control-provider-" + suffix
	controlModelID := "control-model-" + suffix
	t.Cleanup(func() {
		_, _ = adminStore.db.ExecContext(context.Background(), `DELETE FROM llm_models WHERE provider_id = $1`, controlProviderID)
		_, _ = adminStore.db.ExecContext(context.Background(), `DELETE FROM llm_providers WHERE id = $1`, controlProviderID)
	})
	if _, err := restrictedStore.db.ExecContext(context.Background(), `
		INSERT INTO llm_providers (id, provider_type) VALUES ($1, 'fake')
	`, "raw-"+controlProviderID); err == nil {
		t.Fatal("expected runtime role direct LLM provider INSERT to be rejected")
	}
	controlProvider, err := restrictedStore.UpsertLLMProvider(UpsertLLMProviderInput{
		ID: controlProviderID, ProviderType: "fake", Enabled: true,
	})
	if err != nil || controlProvider.ID != controlProviderID {
		t.Fatalf("upsert LLM provider through control helper: provider=%+v err=%v", controlProvider, err)
	}
	controlModel, err := restrictedStore.CreateLLMModel(UpsertLLMModelInput{
		ProviderID: controlProviderID, Model: controlModelID, ContextWindowTokens: 4096,
	})
	if err != nil || controlModel.ProviderID != controlProviderID || controlModel.Model != controlModelID || controlModel.Revision != 1 {
		t.Fatalf("create LLM model through control helper: model=%+v err=%v", controlModel, err)
	}
	controlModel, err = restrictedStore.UpdateLLMModel(UpdateLLMModelInput{
		UpsertLLMModelInput: UpsertLLMModelInput{
			ProviderID: controlProviderID, Model: controlModelID, ContextWindowTokens: 8192,
		},
		ExpectedRevision: 1,
	})
	if err != nil || controlModel.ContextWindowTokens != 8192 || controlModel.Revision != 2 {
		t.Fatalf("conditionally update LLM model through control helper: model=%+v err=%v", controlModel, err)
	}
	if _, err := restrictedStore.UpdateLLMModel(UpdateLLMModelInput{
		UpsertLLMModelInput: UpsertLLMModelInput{
			ProviderID: controlProviderID, Model: controlModelID, ContextWindowTokens: 16384,
		},
		ExpectedRevision: 1,
	}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("expected stale restricted-role model revision conflict, got %v", err)
	}
	defaultVision := true
	firstVision, err := restrictedStore.CreateLLMModel(UpsertLLMModelInput{
		ProviderID: controlProviderID, Model: "vision-one-" + suffix, ContextWindowTokens: 4096,
		CapabilityType: LLMModelCapabilityTextImage, IsDefaultVision: &defaultVision,
	})
	if err != nil || firstVision.Revision != 1 || !firstVision.IsDefaultVision {
		t.Fatalf("create first default vision model through control helper: model=%+v err=%v", firstVision, err)
	}
	secondVision, err := restrictedStore.CreateLLMModel(UpsertLLMModelInput{
		ProviderID: controlProviderID, Model: "vision-two-" + suffix, ContextWindowTokens: 4096,
		CapabilityType: LLMModelCapabilityTextImage, IsDefaultVision: &defaultVision,
	})
	if err != nil || secondVision.Revision != 1 || !secondVision.IsDefaultVision {
		t.Fatalf("switch default vision model through control helper: model=%+v err=%v", secondVision, err)
	}
	controlModels, err := restrictedStore.ListLLMModels(controlProviderID)
	if err != nil {
		t.Fatalf("list control models after default vision switch: %v", err)
	}
	for _, model := range controlModels {
		switch model.Model {
		case firstVision.Model:
			firstVision = model
		case secondVision.Model:
			secondVision = model
		}
	}
	if firstVision.IsDefaultVision || firstVision.Revision != 2 {
		t.Fatalf("expected displaced default vision model revision to advance: %+v", firstVision)
	}
	if err := restrictedStore.DeleteLLMModelIfRevision(controlProviderID, firstVision.Model, firstVision.Revision); err != nil {
		t.Fatalf("delete displaced default vision model: %v", err)
	}
	if err := restrictedStore.DeleteLLMModelIfRevision(controlProviderID, secondVision.Model, secondVision.Revision); err != nil {
		t.Fatalf("delete current default vision model: %v", err)
	}
	if disabled, err := restrictedStore.SetLLMProviderEnabled(controlProviderID, false); err != nil || disabled.Enabled {
		t.Fatalf("disable LLM provider through control helper: provider=%+v err=%v", disabled, err)
	}
	if err := restrictedStore.DeleteLLMModelIfRevision(controlProviderID, controlModelID, 1); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("expected stale restricted-role model delete conflict, got %v", err)
	}
	if err := restrictedStore.DeleteLLMModelIfRevision(controlProviderID, controlModelID, controlModel.Revision); err != nil {
		t.Fatalf("conditionally delete LLM model through control helper: %v", err)
	}
	if err := restrictedStore.DeleteLLMProvider(controlProviderID); err != nil {
		t.Fatalf("delete LLM provider through control helper: %v", err)
	}

	alphaContext, err := ContextWithDatabaseAccessScope(context.Background(), AccessScope{WorkspaceID: alphaWorkspace, OwnerID: "owner-alpha"})
	if err != nil {
		t.Fatalf("create alpha database scope: %v", err)
	}
	betaContext, err := ContextWithDatabaseAccessScope(context.Background(), AccessScope{WorkspaceID: betaWorkspace, OwnerID: "owner-beta"})
	if err != nil {
		t.Fatalf("create beta database scope: %v", err)
	}
	otherOrganizationContext, err := ContextWithDatabaseAccessScope(context.Background(), AccessScope{WorkspaceID: otherWorkspace})
	if err != nil {
		t.Fatalf("create cross-organization database scope: %v", err)
	}
	alphaOperatorContext, err := ContextWithDatabaseAccessScope(context.Background(), AccessScope{WorkspaceID: alphaWorkspace})
	if err != nil {
		t.Fatalf("create alpha operator database scope: %v", err)
	}
	atomicProviderID := "atomic-control-provider-" + suffix
	atomicModelID := "atomic-control-model-" + suffix
	rollbackProviderID := "atomic-rollback-provider-" + suffix
	t.Cleanup(func() {
		_, _ = adminStore.db.ExecContext(context.Background(), `DELETE FROM llm_models WHERE provider_id = $1`, atomicProviderID)
		_, _ = adminStore.db.ExecContext(context.Background(), `DELETE FROM llm_providers WHERE id IN ($1, $2)`, atomicProviderID, rollbackProviderID)
	})
	atomicAudit := func(action string, resourceType string, resourceID string) RecordOperatorAuditInput {
		return RecordOperatorAuditInput{
			WorkspaceID: alphaWorkspace, PrincipalID: "operator-atomic", Role: "admin",
			Action: action, ResourceType: resourceType, ResourceID: resourceID, Outcome: "succeeded",
		}
	}
	atomicProvider, err := restrictedStore.CreateLLMProviderWithAuditContext(alphaOperatorContext, UpsertLLMProviderInput{
		ID: atomicProviderID, ProviderType: "openai", BaseURL: "https://atomic.example.test?token=private",
		APIKeyEnv: "TMA_ATOMIC_PRIVATE_KEY", Enabled: true,
	}, atomicAudit("llm.atomic.provider.create", "llm_provider", atomicProviderID))
	if err != nil || atomicProvider.ID != atomicProviderID || atomicProvider.Revision != 1 {
		t.Fatalf("atomically create LLM provider and audit: provider=%+v err=%v", atomicProvider, err)
	}
	if _, err := restrictedStore.CreateLLMProviderWithAuditContext(alphaOperatorContext, UpsertLLMProviderInput{
		ID: atomicProviderID, ProviderType: "openai", Enabled: true,
	}, atomicAudit("llm.atomic.provider.create_duplicate", "llm_provider", atomicProviderID)); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected duplicate atomic provider creation conflict, got %v", err)
	}
	atomicProvider, err = restrictedStore.UpdateLLMProviderWithAuditContext(alphaOperatorContext, UpdateLLMProviderInput{
		UpsertLLMProviderInput: UpsertLLMProviderInput{
			ID: atomicProviderID, ProviderType: "openai", BaseURL: "https://atomic-updated.example.test",
			APIKeyEnv: "TMA_ATOMIC_PRIVATE_KEY", Enabled: true,
		},
		ExpectedRevision: 1,
	}, atomicAudit("llm.atomic.provider.update", "llm_provider", atomicProviderID))
	if err != nil || atomicProvider.Revision != 2 {
		t.Fatalf("atomically update LLM provider revision: provider=%+v err=%v", atomicProvider, err)
	}
	if _, err := restrictedStore.UpdateLLMProviderWithAuditContext(alphaOperatorContext, UpdateLLMProviderInput{
		UpsertLLMProviderInput: UpsertLLMProviderInput{
			ID: atomicProviderID, ProviderType: "openai", BaseURL: "https://stale.example.test", Enabled: true,
		},
		ExpectedRevision: 1,
	}, atomicAudit("llm.atomic.provider.update_stale", "llm_provider", atomicProviderID)); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("expected stale atomic provider revision conflict, got %v", err)
	}
	if _, err := restrictedStore.UpsertLLMProviderWithAuditContext(alphaOperatorContext, UpsertLLMProviderInput{
		ID: rollbackProviderID, ProviderType: "openai", Enabled: true,
	}, RecordOperatorAuditInput{WorkspaceID: alphaWorkspace, Action: "llm.atomic.rollback", ResourceType: "llm_provider"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid atomic audit to reject provider mutation, got %v", err)
	}
	if _, err := restrictedStore.GetLLMProvider(rollbackProviderID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("provider mutation survived failed atomic audit: %v", err)
	}
	crossWorkspaceAudit := atomicAudit("llm.atomic.rollback_after_mutation", "llm_provider", rollbackProviderID)
	crossWorkspaceAudit.SessionID = betaSession.ID
	if _, err := restrictedStore.UpsertLLMProviderWithAuditContext(alphaOperatorContext, UpsertLLMProviderInput{
		ID: rollbackProviderID, ProviderType: "openai", Enabled: true,
	}, crossWorkspaceAudit); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected cross-workspace audit to roll back provider mutation, got %v", err)
	}
	if _, err := restrictedStore.GetLLMProvider(rollbackProviderID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("provider mutation survived post-write atomic audit rejection: %v", err)
	}
	atomicModel, err := restrictedStore.CreateLLMModelWithAuditContext(alphaOperatorContext, UpsertLLMModelInput{
		ProviderID: atomicProviderID, Model: atomicModelID, ContextWindowTokens: 4096,
	}, atomicAudit("llm.atomic.model.create", "llm_model", atomicProviderID+"/"+atomicModelID))
	if err != nil || atomicModel.ContextWindowTokens != 4096 || atomicModel.Revision != 1 {
		t.Fatalf("atomically create LLM model and audit: model=%+v err=%v", atomicModel, err)
	}
	atomicModel, err = restrictedStore.UpdateLLMModelWithAuditContext(alphaOperatorContext, UpdateLLMModelInput{
		UpsertLLMModelInput: UpsertLLMModelInput{
			ProviderID: atomicProviderID, Model: atomicModelID, ContextWindowTokens: 8192,
		},
		ExpectedRevision: 1,
	}, atomicAudit("llm.atomic.model.update", "llm_model", atomicProviderID+"/"+atomicModelID))
	if err != nil || atomicModel.ContextWindowTokens != 8192 || atomicModel.Revision != 2 {
		t.Fatalf("atomically update LLM model and audit: model=%+v err=%v", atomicModel, err)
	}
	if _, err := restrictedStore.UpdateLLMModelWithAuditContext(alphaOperatorContext, UpdateLLMModelInput{
		UpsertLLMModelInput: UpsertLLMModelInput{
			ProviderID: atomicProviderID, Model: atomicModelID, ContextWindowTokens: 16384,
		},
		ExpectedRevision: 1,
	}, atomicAudit("llm.atomic.model.update_stale", "llm_model", atomicProviderID+"/"+atomicModelID)); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("expected stale atomic model revision conflict, got %v", err)
	}
	if atomicProvider, err = restrictedStore.SetLLMProviderEnabledIfRevisionWithAuditContext(alphaOperatorContext, atomicProviderID, false, 2,
		atomicAudit("llm.atomic.provider.disable", "llm_provider", atomicProviderID)); err != nil || atomicProvider.Enabled {
		t.Fatalf("atomically disable LLM provider and audit: provider=%+v err=%v", atomicProvider, err)
	}
	if _, err := restrictedStore.SetLLMProviderEnabledIfRevisionWithAuditContext(alphaOperatorContext, atomicProviderID, true, 2,
		atomicAudit("llm.atomic.provider.enable_stale", "llm_provider", atomicProviderID)); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("expected stale atomic provider enable conflict, got %v", err)
	}
	if err := restrictedStore.DeleteLLMModelIfRevisionWithAuditContext(alphaOperatorContext, atomicProviderID, atomicModelID, atomicModel.Revision,
		atomicAudit("llm.atomic.model.delete", "llm_model", atomicProviderID+"/"+atomicModelID)); err != nil {
		t.Fatalf("atomically delete LLM model and audit: %v", err)
	}
	if err := restrictedStore.DeleteLLMProviderIfRevisionWithAuditContext(alphaOperatorContext, atomicProviderID, atomicProvider.Revision,
		atomicAudit("llm.atomic.provider.delete", "llm_provider", atomicProviderID)); err != nil {
		t.Fatalf("atomically delete LLM provider and audit: %v", err)
	}
	atomicAudits, err := restrictedStore.ListOperatorAuditContext(alphaOperatorContext, ListOperatorAuditInput{PrincipalID: "operator-atomic", Limit: 20})
	if err != nil || len(atomicAudits) != 7 {
		t.Fatalf("expected seven atomic LLM mutation audits: audits=%+v err=%v", atomicAudits, err)
	}
	encodedAtomicAudits, err := json.Marshal(atomicAudits)
	if err != nil {
		t.Fatalf("encode atomic LLM audits: %v", err)
	}
	if strings.Contains(string(encodedAtomicAudits), "token=private") || strings.Contains(string(encodedAtomicAudits), "TMA_ATOMIC_PRIVATE_KEY") {
		t.Fatalf("atomic LLM audit leaked connection configuration: %s", encodedAtomicAudits)
	}
	alphaPeerContext, err := ContextWithDatabaseAccessScope(context.Background(), AccessScope{WorkspaceID: alphaWorkspace, OwnerID: "owner-peer"})
	if err != nil {
		t.Fatalf("create alpha peer database scope: %v", err)
	}
	alphaAudit, err := restrictedStore.RecordOperatorAuditContext(alphaOperatorContext, RecordOperatorAuditInput{
		WorkspaceID: alphaWorkspace, SessionID: alphaSession.ID, PrincipalID: "operator-alpha",
		Action: "rls.audit.alpha", ResourceType: "session", ResourceID: alphaSession.ID, Outcome: "succeeded",
	})
	if err != nil || alphaAudit.WorkspaceID != alphaWorkspace {
		t.Fatalf("record alpha operator audit through RLS: audit=%+v err=%v", alphaAudit, err)
	}
	betaAudit, err := restrictedStore.RecordOperatorAuditContext(betaContext, RecordOperatorAuditInput{
		WorkspaceID: betaWorkspace, SessionID: betaSession.ID, PrincipalID: "operator-beta",
		Action: "rls.audit.beta", ResourceType: "session", ResourceID: betaSession.ID, Outcome: "succeeded",
	})
	if err != nil || betaAudit.WorkspaceID != betaWorkspace {
		t.Fatalf("record beta operator audit through RLS: audit=%+v err=%v", betaAudit, err)
	}
	alphaAudits, err := restrictedStore.ListOperatorAuditContext(alphaOperatorContext, ListOperatorAuditInput{Action: "rls.audit.alpha", Limit: 20})
	if err != nil || len(alphaAudits) != 1 || alphaAudits[0].ID != alphaAudit.ID {
		t.Fatalf("alpha operator audit scope leaked or hid rows: audits=%+v err=%v", alphaAudits, err)
	}
	betaAudits, err := restrictedStore.ListOperatorAuditContext(betaContext, ListOperatorAuditInput{Action: "rls.audit.beta", Limit: 20})
	if err != nil || len(betaAudits) != 1 || betaAudits[0].ID != betaAudit.ID {
		t.Fatalf("beta operator audit scope leaked or hid rows: audits=%+v err=%v", betaAudits, err)
	}
	if _, err := restrictedStore.RecordOperatorAuditContext(alphaOperatorContext, RecordOperatorAuditInput{
		WorkspaceID: alphaWorkspace, SessionID: betaSession.ID, PrincipalID: "operator-alpha",
		Action: "rls.audit.cross", ResourceType: "session", ResourceID: betaSession.ID, Outcome: "failed",
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected cross-workspace operator audit session rejection, got %v", err)
	}
	securityAuditAt := time.Now().UTC()
	alphaSecurityAuditID := "saud_rls_" + suffix + "_alpha"
	alphaSecurityAudit, err := restrictedStore.RecordSecurityAuditOutboxContext(alphaOperatorContext, RecordSecurityAuditOutboxInput{
		ID: alphaSecurityAuditID, WorkspaceID: alphaWorkspace,
		Payload:            json.RawMessage(`{"id":"` + alphaSecurityAuditID + `","workspace_id":"` + alphaWorkspace + `","outcome":"allowed"}`),
		IntegrityAlgorithm: "sha256", IntegrityDigest: "digest-alpha", CreatedAt: securityAuditAt,
	})
	if err != nil || alphaSecurityAudit.WorkspaceID != alphaWorkspace {
		t.Fatalf("record alpha security audit outbox through RLS: event=%+v err=%v", alphaSecurityAudit, err)
	}
	betaSecurityAuditID := "saud_rls_" + suffix + "_beta"
	betaSecurityAudit, err := restrictedStore.RecordSecurityAuditOutboxContext(betaContext, RecordSecurityAuditOutboxInput{
		ID: betaSecurityAuditID, WorkspaceID: betaWorkspace,
		Payload:            json.RawMessage(`{"id":"` + betaSecurityAuditID + `","workspace_id":"` + betaWorkspace + `","outcome":"allowed"}`),
		IntegrityAlgorithm: "sha256", IntegrityDigest: "digest-beta", CreatedAt: securityAuditAt,
	})
	if err != nil || betaSecurityAudit.WorkspaceID != betaWorkspace {
		t.Fatalf("record beta security audit outbox through RLS: event=%+v err=%v", betaSecurityAudit, err)
	}
	globalSecurityAuditID := "saud_rls_" + suffix + "_global"
	untrustedWorkspaceID := "wksp_untrusted_" + suffix
	globalSecurityAudit, err := restrictedStore.RecordSecurityAuditOutbox(RecordSecurityAuditOutboxInput{
		ID: globalSecurityAuditID, WorkspaceID: untrustedWorkspaceID,
		Payload:            json.RawMessage(`{"id":"` + globalSecurityAuditID + `","workspace_id":"` + untrustedWorkspaceID + `","outcome":"denied","reason":"authentication_failed"}`),
		IntegrityAlgorithm: "sha256", IntegrityDigest: "digest-global", CreatedAt: securityAuditAt,
	})
	if err != nil || globalSecurityAudit.WorkspaceID != "" {
		t.Fatalf("record global security audit outbox through RLS: event=%+v err=%v", globalSecurityAudit, err)
	}
	alphaSecurityStats, err := restrictedStore.GetSecurityAuditOutboxStatsContext(alphaOperatorContext, securityAuditAt.Add(time.Second))
	if err != nil || alphaSecurityStats.Pending != 1 || alphaSecurityStats.Delivering != 0 {
		t.Fatalf("alpha security audit stats leaked or hid rows: stats=%+v err=%v", alphaSecurityStats, err)
	}
	betaSecurityStats, err := restrictedStore.GetSecurityAuditOutboxStatsContext(betaContext, securityAuditAt.Add(time.Second))
	if err != nil || betaSecurityStats.Pending != 1 || betaSecurityStats.Delivering != 0 {
		t.Fatalf("beta security audit stats leaked or hid rows: stats=%+v err=%v", betaSecurityStats, err)
	}
	claimedSecurityAudits, err := restrictedStore.ClaimSecurityAuditOutboxContext(alphaOperatorContext, ClaimSecurityAuditOutboxInput{
		LeaseOwner: "security-audit-alpha", Now: securityAuditAt.Add(time.Second),
		LeaseDuration: time.Minute, MaxAttempts: 3, Limit: 10,
	})
	if err != nil || len(claimedSecurityAudits) != 1 || claimedSecurityAudits[0].ID != alphaSecurityAuditID {
		t.Fatalf("alpha security audit claim crossed scope: events=%+v err=%v", claimedSecurityAudits, err)
	}
	if updated, err := restrictedStore.CompleteSecurityAuditOutboxContext(alphaOperatorContext, CompleteSecurityAuditOutboxInput{
		IDs: []string{alphaSecurityAuditID}, LeaseOwner: "security-audit-alpha", At: securityAuditAt.Add(2 * time.Second),
	}); err != nil || updated != 1 {
		t.Fatalf("complete alpha security audit through RLS: updated=%d err=%v", updated, err)
	}
	if _, err := restrictedStore.RecordSecurityAuditOutboxContext(alphaOperatorContext, RecordSecurityAuditOutboxInput{
		ID: "saud_rls_" + suffix + "_cross", WorkspaceID: betaWorkspace,
		Payload:            json.RawMessage(`{"id":"cross","workspace_id":"` + betaWorkspace + `","outcome":"denied"}`),
		IntegrityAlgorithm: "sha256", IntegrityDigest: "digest-cross", CreatedAt: securityAuditAt,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected cross-workspace security audit outbox rejection, got %v", err)
	}
	restrictedAlphaMCP, err := restrictedStore.CreateMCPRegistryServer(alphaOperatorContext, mcpregistry.CreateInput{
		WorkspaceID: alphaWorkspace, Identifier: "rls-alpha-mcp-" + suffix, Name: "RLS Alpha MCP",
		Config: json.RawMessage(`{"identifier":"rls-alpha-mcp","transport":"stdio","command":"true"}`), CreatedBy: "owner-alpha",
	})
	if err != nil {
		t.Fatalf("create alpha MCP Registry server through RLS: %v", err)
	}
	restrictedBetaMCP, err := restrictedStore.CreateMCPRegistryServer(betaContext, mcpregistry.CreateInput{
		WorkspaceID: betaWorkspace, Identifier: "rls-beta-mcp-" + suffix, Name: "RLS Beta MCP",
		Config: json.RawMessage(`{"identifier":"rls-beta-mcp","transport":"stdio","command":"true"}`), CreatedBy: "owner-beta",
	})
	if err != nil {
		t.Fatalf("create beta MCP Registry server through RLS: %v", err)
	}
	if _, err := restrictedStore.CreateMCPRegistryServer(alphaOperatorContext, mcpregistry.CreateInput{
		WorkspaceID: betaWorkspace, Identifier: "rls-cross-mcp-" + suffix, Name: "Blocked MCP",
		Config: json.RawMessage(`{"identifier":"rls-cross-mcp","transport":"stdio","command":"true"}`),
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected cross-workspace MCP Registry create rejection, got %v", err)
	}
	if loaded, err := restrictedStore.GetMCPRegistryServer(alphaOperatorContext, restrictedAlphaMCP.ID); err != nil || loaded.ID != restrictedAlphaMCP.ID {
		t.Fatalf("get alpha MCP Registry server through RLS: server=%+v err=%v", loaded, err)
	}
	if versions, err := restrictedStore.ListMCPRegistryVersions(alphaOperatorContext, restrictedAlphaMCP.ID); err != nil || len(versions) != 1 || versions[0].Version != 1 {
		t.Fatalf("list alpha MCP Registry versions through RLS: versions=%+v err=%v", versions, err)
	}
	updatedAlphaMCP, err := restrictedStore.UpdateMCPRegistryServer(alphaOperatorContext, mcpregistry.UpdateInput{
		ServerID: restrictedAlphaMCP.ID, Name: restrictedAlphaMCP.Name,
		Config: json.RawMessage(`{"identifier":"rls-alpha-mcp","transport":"stdio","command":"false"}`), UpdatedBy: "owner-alpha",
	})
	if err != nil || updatedAlphaMCP.CurrentVersion != 2 {
		t.Fatalf("publish alpha MCP Registry v2 through RLS: server=%+v err=%v", updatedAlphaMCP, err)
	}
	restoredAlphaMCP, err := restrictedStore.RestoreMCPRegistryVersion(alphaOperatorContext, restrictedAlphaMCP.ID, 1, "owner-alpha")
	if err != nil || restoredAlphaMCP.SourceVersion != 1 || restoredAlphaMCP.PreviousVersion != 2 || restoredAlphaMCP.NewVersion != 3 || restoredAlphaMCP.Server.CurrentVersion != 3 {
		t.Fatalf("restore alpha MCP Registry v1 through RLS: result=%+v err=%v", restoredAlphaMCP, err)
	}
	if versions, err := restrictedStore.ListMCPRegistryVersions(alphaOperatorContext, restrictedAlphaMCP.ID); err != nil || len(versions) != 3 || versions[0].Checksum != versions[2].Checksum {
		t.Fatalf("expected restored MCP Registry version to preserve source checksum: versions=%+v err=%v", versions, err)
	}
	if _, err := restrictedStore.GetMCPRegistryServer(betaContext, restrictedAlphaMCP.ID); !errors.Is(err, mcpregistry.ErrNotFound) {
		t.Fatalf("expected cross-workspace MCP Registry server to be hidden, got %v", err)
	}
	if _, err := restrictedStore.RestoreMCPRegistryVersion(betaContext, restrictedAlphaMCP.ID, 1, "owner-beta"); !errors.Is(err, mcpregistry.ErrNotFound) {
		t.Fatalf("expected cross-workspace MCP Registry restore to be hidden, got %v", err)
	}
	restrictedAlphaEnvironment, err := restrictedStore.CreateEnvironmentContext(alphaContext, CreateEnvironmentInput{
		Name: "restricted-alpha-environment-" + suffix, Config: json.RawMessage(`{"type":"rls-test"}`),
	})
	if err != nil {
		t.Fatalf("create alpha Environment through RLS: %v", err)
	}
	restrictedBetaEnvironment, err := restrictedStore.CreateEnvironmentContext(betaContext, CreateEnvironmentInput{
		WorkspaceID: betaWorkspace, Name: "restricted-beta-environment-" + suffix, Config: json.RawMessage(`{"type":"rls-test"}`),
	})
	if err != nil {
		t.Fatalf("create beta Environment through RLS: %v", err)
	}
	if _, err := restrictedStore.CreateEnvironmentContext(alphaContext, CreateEnvironmentInput{
		WorkspaceID: betaWorkspace, Name: "blocked-cross-scope-environment-" + suffix,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected context-derived Environment workspace mismatch rejection, got %v", err)
	}
	for ctx, input := range map[context.Context]envvars.EncryptedVariable{
		alphaContext: {WorkspaceID: alphaWorkspace, Name: "ALPHA_SECRET", Ciphertext: []byte("alpha-ciphertext")},
		betaContext:  {WorkspaceID: betaWorkspace, Name: "BETA_SECRET", Ciphertext: []byte("beta-ciphertext")},
	} {
		if _, err := restrictedStore.UpsertEncryptedEnvironmentVariable(ctx, input); err != nil {
			t.Fatalf("insert scoped environment variable %s: %v", input.Name, err)
		}
	}
	if _, err := restrictedStore.UpsertEncryptedEnvironmentVariable(alphaContext, envvars.EncryptedVariable{
		WorkspaceID: betaWorkspace, Name: "CROSS_SCOPE_SECRET", Ciphertext: []byte("blocked"),
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected context-derived workspace mismatch rejection, got %v", err)
	}
	alphaRecords, err := restrictedStore.ListEncryptedEnvironmentVariables(alphaContext, alphaWorkspace)
	if err != nil || len(alphaRecords) != 1 || alphaRecords[0].Name != "ALPHA_SECRET" {
		t.Fatalf("unexpected alpha RLS records: %+v err=%v", alphaRecords, err)
	}
	restrictedAlphaAgent, err := restrictedStore.CreateAgentContext(alphaContext, CreateAgentInput{
		WorkspaceID: alphaWorkspace, Name: "restricted-alpha-agent-" + suffix, Model: "test-model",
	})
	if err != nil {
		t.Fatalf("create alpha Agent through RLS: %v", err)
	}
	if agent, err := restrictedStore.GetAgentContext(alphaContext, restrictedAlphaAgent.ID); err != nil || agent.ID != restrictedAlphaAgent.ID {
		t.Fatalf("get alpha Agent through RLS: agent=%+v err=%v", agent, err)
	}
	updatedAgent, err := restrictedStore.CreateAgentConfigVersionContext(alphaContext, CreateAgentConfigVersionInput{
		AgentID: restrictedAlphaAgent.ID, LLMProvider: restrictedAlphaAgent.ConfigVersion.LLMProvider,
		LLMModel: restrictedAlphaAgent.ConfigVersion.LLMModel, System: "updated through RLS",
	})
	if err != nil || updatedAgent.CurrentConfigVersion != 2 {
		t.Fatalf("create alpha Agent config through RLS: agent=%+v err=%v", updatedAgent, err)
	}
	if versions, err := restrictedStore.ListAgentConfigVersionsContext(alphaContext, restrictedAlphaAgent.ID); err != nil || len(versions) != 2 {
		t.Fatalf("list alpha Agent configs through RLS: versions=%+v err=%v", versions, err)
	}
	restrictedAlphaSession, err := restrictedStore.CreateSessionContext(alphaContext, CreateSessionInput{
		AgentID:       restrictedAlphaAgent.ID,
		EnvironmentID: restrictedAlphaEnvironment.ID, CreatedBy: "owner-alpha",
	})
	if err != nil || restrictedAlphaSession.EnvironmentID != restrictedAlphaEnvironment.ID || restrictedAlphaSession.WorkspaceID != alphaWorkspace || restrictedAlphaSession.OwnerID != "owner-alpha" {
		t.Fatalf("create Session with RLS-protected Environment: session=%+v err=%v", restrictedAlphaSession, err)
	}
	restrictedAlphaWorker, err := restrictedStore.RegisterWorkerContext(alphaOperatorContext, RegisterWorkerInput{
		WorkspaceID: betaWorkspace, Name: "restricted-alpha-worker-" + suffix,
		WorkerType: WorkerTypeLocal, RegisteredBy: "owner-alpha", LeaseSeconds: 60,
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected context-derived Worker workspace mismatch rejection, got worker=%+v err=%v", restrictedAlphaWorker, err)
	}
	restrictedAlphaWorker, err = restrictedStore.RegisterWorkerContext(alphaOperatorContext, RegisterWorkerInput{
		Name: "restricted-alpha-worker-" + suffix, WorkerType: WorkerTypeLocal,
		RegisteredBy: "owner-alpha", LeaseSeconds: 60,
	})
	if err != nil || restrictedAlphaWorker.WorkspaceID != alphaWorkspace {
		t.Fatalf("register alpha Worker through RLS: worker=%+v err=%v", restrictedAlphaWorker, err)
	}
	restrictedBetaWorker, err := restrictedStore.RegisterWorkerContext(betaContext, RegisterWorkerInput{
		WorkspaceID: betaWorkspace, Name: "restricted-beta-worker-" + suffix,
		WorkerType: WorkerTypeShared, RegisteredBy: "owner-beta", LeaseSeconds: 60,
	})
	if err != nil || restrictedBetaWorker.WorkspaceID != betaWorkspace {
		t.Fatalf("register beta Worker through RLS: worker=%+v err=%v", restrictedBetaWorker, err)
	}
	if loaded, err := restrictedStore.GetWorkerContext(alphaOperatorContext, restrictedAlphaWorker.ID); err != nil || loaded.ID != restrictedAlphaWorker.ID {
		t.Fatalf("get alpha Worker through RLS: worker=%+v err=%v", loaded, err)
	}
	if loaded, err := restrictedStore.GetWorkerScoped(restrictedAlphaWorker.ID, AccessScope{WorkspaceID: alphaWorkspace}); err != nil || loaded.ID != restrictedAlphaWorker.ID {
		t.Fatalf("RBAC preflight could not get alpha Worker through RLS: worker=%+v err=%v", loaded, err)
	}
	if _, err := restrictedStore.GetWorkerContext(betaContext, restrictedAlphaWorker.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace member could read Worker through RLS: %v", err)
	}
	if _, err := restrictedStore.GetWorkerScoped(restrictedAlphaWorker.ID, AccessScope{WorkspaceID: betaWorkspace}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-workspace RBAC preflight could read Worker through RLS: %v", err)
	}
	alphaWorkers, err := restrictedStore.ListWorkersContext(alphaOperatorContext, ListWorkersInput{})
	if err != nil || len(alphaWorkers) != 1 || alphaWorkers[0].ID != restrictedAlphaWorker.ID {
		t.Fatalf("list alpha Workers through RLS: workers=%+v err=%v", alphaWorkers, err)
	}
	if workers, err := restrictedStore.ListWorkersScoped(ListWorkersInput{}, AccessScope{WorkspaceID: alphaWorkspace}); err != nil || len(workers) != 1 || workers[0].ID != restrictedAlphaWorker.ID {
		t.Fatalf("RBAC preflight could not list alpha Workers through RLS: workers=%+v err=%v", workers, err)
	}
	restrictedAlphaWorker, err = restrictedStore.HeartbeatWorkerContext(alphaOperatorContext, restrictedAlphaWorker.ID, WorkerHeartbeatInput{
		Status: WorkerStatusDraining, LeaseSeconds: 60,
	})
	if err != nil || restrictedAlphaWorker.Status != WorkerStatusDraining {
		t.Fatalf("heartbeat alpha Worker through RLS: worker=%+v err=%v", restrictedAlphaWorker, err)
	}
	restrictedAlphaWorker, err = restrictedStore.HeartbeatWorkerContext(alphaOperatorContext, restrictedAlphaWorker.ID, WorkerHeartbeatInput{
		Status: WorkerStatusOnline, LeaseSeconds: 60,
	})
	if err != nil || restrictedAlphaWorker.Status != WorkerStatusOnline {
		t.Fatalf("restore alpha Worker online through RLS: worker=%+v err=%v", restrictedAlphaWorker, err)
	}
	if _, err := restrictedStore.EnqueueWorkerWorkContext(alphaOperatorContext, EnqueueWorkerWorkInput{
		WorkspaceID: betaWorkspace, WorkerID: restrictedAlphaWorker.ID,
		WorkType: WorkerWorkTypeSandboxCommand, Payload: json.RawMessage(`{"command":"true"}`),
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected context-derived Worker work workspace mismatch rejection, got %v", err)
	}
	for name, input := range map[string]EnqueueWorkerWorkInput{
		"worker":      {WorkerID: restrictedBetaWorker.ID, WorkType: WorkerWorkTypeSandboxCommand},
		"environment": {EnvironmentID: restrictedBetaEnvironment.ID, WorkType: WorkerWorkTypeSandboxCommand},
		"session":     {SessionID: betaSession.ID, WorkType: WorkerWorkTypeSandboxCommand},
	} {
		if _, err := restrictedStore.EnqueueWorkerWorkContext(alphaOperatorContext, input); !errors.Is(err, ErrForbidden) {
			t.Fatalf("expected cross-workspace %s reference rejection, got %v", name, err)
		}
	}
	completedWork, err := restrictedStore.EnqueueWorkerWorkContext(alphaOperatorContext, EnqueueWorkerWorkInput{
		WorkerID: restrictedAlphaWorker.ID, EnvironmentID: restrictedAlphaEnvironment.ID,
		SessionID: restrictedAlphaSession.ID, TurnID: "turn_worker_rls", WorkType: WorkerWorkTypeSandboxCommand,
		Payload: json.RawMessage(`{"command":"true"}`),
	})
	if err != nil || completedWork.WorkspaceID != alphaWorkspace {
		t.Fatalf("enqueue alpha Worker work through RLS: work=%+v err=%v", completedWork, err)
	}
	if _, err := restrictedStore.GetWorkerWorkContext(betaContext, completedWork.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace member could read Worker work through RLS: %v", err)
	}
	if loaded, err := restrictedStore.GetWorkerWorkScoped(completedWork.ID, AccessScope{WorkspaceID: alphaWorkspace}); err != nil || loaded.ID != completedWork.ID {
		t.Fatalf("RBAC preflight could not get alpha Worker work through RLS: work=%+v err=%v", loaded, err)
	}
	if _, err := restrictedStore.GetWorkerWorkScoped(completedWork.ID, AccessScope{WorkspaceID: betaWorkspace}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-workspace RBAC preflight could read Worker work through RLS: %v", err)
	}
	if polled, err := restrictedStore.PollWorkerWorkContext(betaContext, restrictedBetaWorker.ID, PollWorkerWorkInput{LeaseSeconds: 60}); err != nil || polled != nil {
		t.Fatalf("beta Worker polled alpha work: work=%+v err=%v", polled, err)
	}
	polledWork, err := restrictedStore.PollWorkerWorkContext(alphaOperatorContext, restrictedAlphaWorker.ID, PollWorkerWorkInput{LeaseSeconds: 60})
	if err != nil || polledWork == nil || polledWork.ID != completedWork.ID || polledWork.Status != WorkerWorkStatusLeased {
		t.Fatalf("poll alpha Worker work through RLS: work=%+v err=%v", polledWork, err)
	}
	if _, err := restrictedStore.AckWorkerWorkContext(alphaOperatorContext, restrictedAlphaWorker.ID, completedWork.ID); err != nil {
		t.Fatalf("ack alpha Worker work through RLS: %v", err)
	}
	if _, err := restrictedStore.HeartbeatWorkerWorkContext(alphaOperatorContext, restrictedAlphaWorker.ID, completedWork.ID, WorkerWorkHeartbeatInput{LeaseSeconds: 60}); err != nil {
		t.Fatalf("heartbeat alpha Worker work through RLS: %v", err)
	}
	completedWork, err = restrictedStore.CompleteWorkerWorkContext(alphaOperatorContext, restrictedAlphaWorker.ID, completedWork.ID, CompleteWorkerWorkInput{
		Success: true, Result: json.RawMessage(`{"ok":true}`),
	})
	if err != nil || completedWork.Status != WorkerWorkStatusCompleted {
		t.Fatalf("complete alpha Worker work through RLS: work=%+v err=%v", completedWork, err)
	}
	canceledWork, err := restrictedStore.EnqueueWorkerWorkContext(alphaOperatorContext, EnqueueWorkerWorkInput{
		WorkerID: restrictedAlphaWorker.ID, WorkType: WorkerWorkTypeArtifactSync, Payload: json.RawMessage(`{"sync":true}`),
	})
	if err != nil {
		t.Fatalf("enqueue cancelable Worker work through RLS: %v", err)
	}
	canceledWork, err = restrictedStore.CancelWorkerWorkContext(alphaOperatorContext, canceledWork.ID, CancelWorkerWorkInput{Reason: "RLS test cancel"})
	if err != nil || canceledWork.Status != WorkerWorkStatusCanceled {
		t.Fatalf("cancel Worker work through RLS: work=%+v err=%v", canceledWork, err)
	}
	requeuedWork, err := restrictedStore.RequeueWorkerWorkContext(alphaOperatorContext, canceledWork.ID, RequeueWorkerWorkInput{ClearWorker: true})
	if err != nil || requeuedWork.Status != WorkerWorkStatusPending || requeuedWork.WorkerID != "" {
		t.Fatalf("requeue Worker work through RLS: work=%+v err=%v", requeuedWork, err)
	}
	if _, err := restrictedStore.CancelWorkerWorkContext(alphaOperatorContext, requeuedWork.ID, CancelWorkerWorkInput{Reason: "RLS test cleanup"}); err != nil {
		t.Fatalf("cancel requeued Worker work through RLS: %v", err)
	}

	alphaExpiredWork, err := restrictedStore.EnqueueWorkerWorkContext(alphaOperatorContext, EnqueueWorkerWorkInput{
		WorkerID: restrictedAlphaWorker.ID, WorkType: WorkerWorkTypeSandboxCommand,
	})
	if err != nil {
		t.Fatalf("enqueue expiring alpha Worker work: %v", err)
	}
	if _, err := restrictedStore.PollWorkerWorkContext(alphaOperatorContext, restrictedAlphaWorker.ID, PollWorkerWorkInput{LeaseSeconds: 60}); err != nil {
		t.Fatalf("lease expiring alpha Worker work: %v", err)
	}
	betaExpiredWork, err := restrictedStore.EnqueueWorkerWorkContext(betaContext, EnqueueWorkerWorkInput{
		WorkerID: restrictedBetaWorker.ID, EnvironmentID: betaEnvironment.ID, SessionID: betaSession.ID,
		WorkType: WorkerWorkTypeSandboxCommand,
	})
	if err != nil {
		t.Fatalf("enqueue expiring beta Worker work: %v", err)
	}
	if _, err := restrictedStore.PollWorkerWorkContext(betaContext, restrictedBetaWorker.ID, PollWorkerWorkInput{LeaseSeconds: 60}); err != nil {
		t.Fatalf("lease expiring beta Worker work: %v", err)
	}
	expiredAt := time.Unix(0, 0).UTC()
	if _, err := adminStore.db.ExecContext(context.Background(), `UPDATE worker_work SET lease_expires_at = $1 WHERE id IN ($2, $3)`, expiredAt, alphaExpiredWork.ID, betaExpiredWork.ID); err != nil {
		t.Fatalf("expire cross-workspace Worker work fixtures: %v", err)
	}
	expiredWorks, err := restrictedStore.ReapExpiredWorkerWork(ReapExpiredWorkerWorkInput{Limit: 100})
	if err != nil {
		t.Fatalf("run workspace-scoped Worker work background reaper: %v", err)
	}
	expiredWorkIDs := map[string]bool{}
	for _, work := range expiredWorks {
		expiredWorkIDs[work.ID] = true
	}
	if !expiredWorkIDs[alphaExpiredWork.ID] || !expiredWorkIDs[betaExpiredWork.ID] {
		t.Fatalf("background Worker work reaper missed a workspace: expired=%+v", expiredWorks)
	}
	alphaExpiredWorker, err := restrictedStore.RegisterWorkerContext(alphaOperatorContext, RegisterWorkerInput{Name: "expired-alpha-worker-" + suffix, LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("register expiring alpha Worker: %v", err)
	}
	betaExpiredWorker, err := restrictedStore.RegisterWorkerContext(betaContext, RegisterWorkerInput{Name: "expired-beta-worker-" + suffix, LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("register expiring beta Worker: %v", err)
	}
	if _, err := adminStore.db.ExecContext(context.Background(), `UPDATE workers SET lease_expires_at = $1 WHERE id IN ($2, $3)`, expiredAt, alphaExpiredWorker.ID, betaExpiredWorker.ID); err != nil {
		t.Fatalf("expire cross-workspace Worker fixtures: %v", err)
	}
	expiredWorkers, err := restrictedStore.ReapExpiredWorkers(ReapExpiredWorkersInput{Limit: 100})
	if err != nil {
		t.Fatalf("run workspace-scoped Worker background reaper: %v", err)
	}
	expiredWorkerIDs := map[string]bool{}
	for _, worker := range expiredWorkers {
		expiredWorkerIDs[worker.ID] = true
	}
	if !expiredWorkerIDs[alphaExpiredWorker.ID] || !expiredWorkerIDs[betaExpiredWorker.ID] {
		t.Fatalf("background Worker reaper missed a workspace: expired=%+v", expiredWorkers)
	}
	restrictedAlphaWorker, err = restrictedStore.ArchiveWorkerContext(alphaOperatorContext, restrictedAlphaWorker.ID)
	if err != nil || restrictedAlphaWorker.Status != WorkerStatusArchived {
		t.Fatalf("archive alpha Worker through RLS: worker=%+v err=%v", restrictedAlphaWorker, err)
	}
	restrictedAlphaSkill, err := restrictedStore.CreateSkill(alphaContext, skills.CreateSkillInput{
		WorkspaceID: betaWorkspace, Identifier: "restricted-alpha-skill-" + suffix,
		Title: "Restricted Alpha Skill", CreatedBy: "owner-alpha",
	})
	if err != nil {
		t.Fatalf("create Skill through RLS: %v", err)
	}
	if restrictedAlphaSkill.WorkspaceID != alphaWorkspace {
		t.Fatalf("Skill did not derive context workspace: skill=%+v", restrictedAlphaSkill)
	}
	restrictedAlphaSkillVersion, err := restrictedStore.CreateSkillVersion(alphaContext, skills.CreateVersionInput{
		SkillID: restrictedAlphaSkill.ID, ContentFormat: "markdown",
		ContentText: "tenant-scoped skill content", CreatedBy: "owner-alpha",
	})
	if err != nil {
		t.Fatalf("create Skill version through RLS: %v", err)
	}
	if loaded, err := restrictedStore.GetSkill(alphaContext, restrictedAlphaSkill.ID); err != nil || loaded.ID != restrictedAlphaSkill.ID {
		t.Fatalf("get Skill through RLS: skill=%+v err=%v", loaded, err)
	}
	if loaded, err := restrictedStore.GetSkillByIdentifier(alphaContext, alphaWorkspace, restrictedAlphaSkill.Identifier); err != nil || loaded.ID != restrictedAlphaSkill.ID {
		t.Fatalf("get Skill by identifier through RLS: skill=%+v err=%v", loaded, err)
	}
	if versions, err := restrictedStore.ListSkillVersions(alphaContext, restrictedAlphaSkill.ID); err != nil || len(versions) != 1 || versions[0].ID != restrictedAlphaSkillVersion.ID {
		t.Fatalf("list Skill versions through RLS: versions=%+v err=%v", versions, err)
	}
	if _, err := restrictedStore.ListSkills(alphaContext, skills.ListSkillsInput{WorkspaceID: betaWorkspace}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected cross-workspace Skill list rejection, got %v", err)
	}
	if _, err := restrictedStore.GetSkill(betaContext, restrictedAlphaSkill.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace member could read Skill through RLS: %v", err)
	}
	if _, err := restrictedStore.GetSkillVersion(betaContext, restrictedAlphaSkill.ID, restrictedAlphaSkillVersion.Version); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace member could read Skill version through RLS: %v", err)
	}
	if loaded, err := restrictedStore.GetSkill(alphaPeerContext, restrictedAlphaSkill.ID); err != nil || loaded.ID != restrictedAlphaSkill.ID {
		t.Fatalf("workspace peer could not read shared Skill: skill=%+v err=%v", loaded, err)
	}
	if err := restrictedStore.RecordSkillUsages(alphaContext, []skills.Usage{{
		WorkspaceID: betaWorkspace, SessionID: restrictedAlphaSession.ID, TurnID: "turn_skill_rls",
		AgentID: betaAgent.ID, AgentConfigVersion: 99, SkillID: restrictedAlphaSkill.ID,
		SkillIdentifier: betaSkill.Identifier, SkillVersion: restrictedAlphaSkillVersion.Version,
		RequestedMode: skills.ModeFull, RenderedMode: skills.ModeSummary, Priority: 100,
		EstimatedTokens: 12, Status: skills.UsageResolved,
	}}); err != nil {
		t.Fatalf("record Skill usage through RLS: %v", err)
	}
	skillUsages, err := restrictedStore.ListSkillUsages(alphaContext, restrictedAlphaSession.ID, "turn_skill_rls")
	if err != nil || len(skillUsages) != 1 {
		t.Fatalf("list Skill usages through RLS: usages=%+v err=%v", skillUsages, err)
	}
	if skillUsages[0].WorkspaceID != alphaWorkspace || skillUsages[0].AgentID != restrictedAlphaSession.AgentID ||
		skillUsages[0].AgentConfigVersion != restrictedAlphaSession.AgentConfigVersion || skillUsages[0].SkillIdentifier != restrictedAlphaSkill.Identifier {
		t.Fatalf("Skill usage did not derive Session and Skill fields: usage=%+v", skillUsages[0])
	}
	if peerUsages, err := restrictedStore.ListSkillUsages(alphaPeerContext, restrictedAlphaSession.ID, ""); err != nil || len(peerUsages) != 0 {
		t.Fatalf("peer owner could read member Skill usages: usages=%+v err=%v", peerUsages, err)
	}
	marketplaceEntry, err := restrictedStore.CreateMarketplaceEntry(alphaContext, skillmarketplace.CreateMarketplaceEntryInput{
		WorkspaceID: betaWorkspace, SkillID: restrictedAlphaSkill.ID, SkillVersion: restrictedAlphaSkillVersion.Version,
		Summary: "RLS marketplace entry", Category: "Engineering", Tags: []string{"rls"}, CreatedBy: "owner-alpha",
	})
	if err != nil {
		t.Fatalf("create marketplace entry through RLS: %v", err)
	}
	if marketplaceEntry.WorkspaceID != alphaWorkspace {
		t.Fatalf("marketplace entry did not derive context workspace: entry=%+v", marketplaceEntry)
	}
	marketplaceEntry, err = restrictedStore.UpdateMarketplaceEntry(alphaContext, skillmarketplace.UpdateMarketplaceEntryInput{
		WorkspaceID: alphaWorkspace, EntryID: marketplaceEntry.ID, Summary: "Updated RLS entry",
		Category: "Engineering", Tags: []string{"rls", "verified"}, UpdatedBy: "owner-alpha",
	})
	if err != nil || marketplaceEntry.Summary != "Updated RLS entry" {
		t.Fatalf("update marketplace entry through RLS: entry=%+v err=%v", marketplaceEntry, err)
	}
	marketplaceEntry, err = restrictedStore.TransitionMarketplaceEntry(alphaContext, skillmarketplace.TransitionMarketplaceEntryInput{
		WorkspaceID: alphaWorkspace, EntryID: marketplaceEntry.ID,
		TargetStatus: skillmarketplace.MarketplaceEntryStatusPendingReview, Actor: "owner-alpha",
	})
	if err != nil {
		t.Fatalf("submit marketplace entry through RLS: %v", err)
	}
	marketplaceEntry, err = restrictedStore.TransitionMarketplaceEntry(alphaContext, skillmarketplace.TransitionMarketplaceEntryInput{
		WorkspaceID: alphaWorkspace, EntryID: marketplaceEntry.ID,
		TargetStatus: skillmarketplace.MarketplaceEntryStatusPublished, Actor: "owner-alpha", Note: "approved",
	})
	if err != nil || marketplaceEntry.Status != skillmarketplace.MarketplaceEntryStatusPublished {
		t.Fatalf("publish marketplace entry through RLS: entry=%+v err=%v", marketplaceEntry, err)
	}
	if loaded, err := restrictedStore.GetMarketplaceEntry(alphaPeerContext, alphaWorkspace, marketplaceEntry.ID); err != nil || loaded.ID != marketplaceEntry.ID {
		t.Fatalf("workspace peer could not read marketplace entry: entry=%+v err=%v", loaded, err)
	}
	if _, err := restrictedStore.GetMarketplaceEntry(betaContext, betaWorkspace, marketplaceEntry.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace member could read marketplace entry: %v", err)
	}
	if _, err := restrictedStore.ListMarketplaceEntries(alphaContext, skillmarketplace.ListMarketplaceEntriesInput{WorkspaceID: betaWorkspace}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected cross-workspace marketplace list rejection, got %v", err)
	}
	catalogEntries, err := restrictedStore.BrowsePublishedMarketplaceEntries(betaContext, skillmarketplace.BrowseMarketplaceEntriesInput{
		WorkspaceID: betaWorkspace, Query: "restricted alpha", Category: "engineering", Tags: []string{"VERIFIED"}, Limit: 10,
	})
	if err != nil || len(catalogEntries) != 1 || catalogEntries[0].ID != marketplaceEntry.ID {
		t.Fatalf("same-organization workspace could not browse published catalog: entries=%+v err=%v", catalogEntries, err)
	}
	if loaded, err := restrictedStore.GetPublishedMarketplaceEntry(betaContext, betaWorkspace, marketplaceEntry.ID); err != nil || loaded.ID != marketplaceEntry.ID {
		t.Fatalf("same-organization workspace could not get published catalog entry: entry=%+v err=%v", loaded, err)
	}
	if entries, err := restrictedStore.BrowsePublishedMarketplaceEntries(otherOrganizationContext, skillmarketplace.BrowseMarketplaceEntriesInput{
		WorkspaceID: otherWorkspace, Limit: 10,
	}); err != nil || len(entries) != 0 {
		t.Fatalf("cross-organization workspace could browse published catalog: entries=%+v err=%v", entries, err)
	}
	if _, err := restrictedStore.GetPublishedMarketplaceEntry(otherOrganizationContext, otherWorkspace, marketplaceEntry.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-organization workspace could get published catalog entry: %v", err)
	}
	if _, err := restrictedStore.ArchiveSkill(alphaContext, restrictedAlphaSkill.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected published marketplace source archive conflict, got %v", err)
	}
	marketplaceEntry, err = restrictedStore.TransitionMarketplaceEntry(alphaContext, skillmarketplace.TransitionMarketplaceEntryInput{
		WorkspaceID: alphaWorkspace, EntryID: marketplaceEntry.ID,
		TargetStatus: skillmarketplace.MarketplaceEntryStatusWithdrawn, Actor: "owner-alpha", Note: "RLS catalog test complete",
	})
	if err != nil || marketplaceEntry.Status != skillmarketplace.MarketplaceEntryStatusWithdrawn {
		t.Fatalf("withdraw marketplace entry through RLS: entry=%+v err=%v", marketplaceEntry, err)
	}
	if entries, err := restrictedStore.BrowsePublishedMarketplaceEntries(betaContext, skillmarketplace.BrowseMarketplaceEntriesInput{
		WorkspaceID: betaWorkspace, Limit: 10,
	}); err != nil || len(entries) != 0 {
		t.Fatalf("withdrawn catalog entry remained visible: entries=%+v err=%v", entries, err)
	}
	organizationPolicy, organizationPolicyVersion, err := restrictedStore.CreateMarketplacePolicy(alphaContext, skillmarketplace.CreatePolicyInput{
		ScopeType: skillmarketplace.PolicyScopeOrganization, OrganizationID: "org_default",
		Config: skillmarketplace.Policy{AllowedOwners: []string{"org-trusted"}}, CreatedBy: "owner-alpha",
	})
	if err != nil {
		t.Fatalf("create organization marketplace policy through RLS: %v", err)
	}
	workspacePolicy, _, err := restrictedStore.CreateMarketplacePolicy(alphaContext, skillmarketplace.CreatePolicyInput{
		ScopeType: skillmarketplace.PolicyScopeWorkspace, WorkspaceID: betaWorkspace,
		Config: skillmarketplace.Policy{AllowedOwners: []string{"workspace-trusted"}}, CreatedBy: "owner-alpha",
	})
	if err != nil || workspacePolicy.WorkspaceID != alphaWorkspace {
		t.Fatalf("create workspace marketplace policy through RLS: policy=%+v err=%v", workspacePolicy, err)
	}
	workspacePolicyVersion, err := restrictedStore.PublishMarketplacePolicyVersion(alphaContext, workspacePolicy.ID,
		skillmarketplace.Policy{AllowedOwners: []string{"workspace-v2"}}, "owner-alpha")
	if err != nil || workspacePolicyVersion.Version != 2 {
		t.Fatalf("publish workspace marketplace policy version through RLS: version=%+v err=%v", workspacePolicyVersion, err)
	}
	if loaded, err := restrictedStore.GetMarketplacePolicy(betaContext, organizationPolicy.ID); err != nil || loaded.ID != organizationPolicy.ID {
		t.Fatalf("same-organization workspace could not read organization policy: policy=%+v err=%v", loaded, err)
	}
	if _, err := restrictedStore.GetMarketplacePolicy(betaContext, workspacePolicy.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace member could read workspace policy: %v", err)
	}
	if _, err := restrictedStore.GetMarketplacePolicyVersion(betaContext, workspacePolicy.ID, workspacePolicyVersion.Version); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace member could read workspace policy version: %v", err)
	}
	if _, err := restrictedStore.GetMarketplacePolicy(otherOrganizationContext, organizationPolicy.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-organization workspace could read organization policy: %v", err)
	}
	if _, _, err := restrictedStore.CreateMarketplacePolicy(alphaContext, skillmarketplace.CreatePolicyInput{
		ScopeType: skillmarketplace.PolicyScopeOrganization, OrganizationID: otherOrganization,
		Config: skillmarketplace.Policy{AllowedOwners: []string{"blocked"}}, CreatedBy: "owner-alpha",
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected cross-organization policy create rejection, got %v", err)
	}
	if effective, err := restrictedStore.ResolveMarketplacePolicy(alphaContext, alphaWorkspace); err != nil || effective.Policy.ID != workspacePolicy.ID {
		t.Fatalf("workspace policy did not override organization policy: effective=%+v err=%v", effective, err)
	}
	if _, err := restrictedStore.ArchiveMarketplacePolicy(betaContext, betaMarketplacePolicy.ID); err != nil {
		t.Fatalf("archive beta workspace policy through RLS: %v", err)
	}
	if effective, err := restrictedStore.ResolveMarketplacePolicy(betaContext, betaWorkspace); err != nil || effective.Policy.ID != organizationPolicy.ID || effective.Version.ID != organizationPolicyVersion.ID {
		t.Fatalf("organization policy fallback failed through RLS: effective=%+v err=%v", effective, err)
	}
	organizationRetentionPolicy, organizationRetentionVersion, err := restrictedStore.CreateSkillAssetRetentionPolicy(alphaContext, skillretention.CreatePolicyInput{
		ScopeType: skillretention.ScopeOrganization, OrganizationID: "org_default",
		Config: skillretention.Policy{Enabled: true, RetentionDays: 60, DeleteLimit: 20}, CreatedBy: "owner-alpha",
	})
	if err != nil {
		t.Fatalf("create organization retention policy through RLS: %v", err)
	}
	workspaceRetentionPolicy, _, err := restrictedStore.CreateSkillAssetRetentionPolicy(alphaContext, skillretention.CreatePolicyInput{
		ScopeType: skillretention.ScopeWorkspace, WorkspaceID: betaWorkspace,
		Config: skillretention.Policy{Enabled: true, RetentionDays: 30, DeleteLimit: 10}, CreatedBy: "owner-alpha",
	})
	if err != nil || workspaceRetentionPolicy.WorkspaceID != alphaWorkspace {
		t.Fatalf("create workspace retention policy through RLS: policy=%+v err=%v", workspaceRetentionPolicy, err)
	}
	workspaceRetentionVersion, err := restrictedStore.PublishSkillAssetRetentionPolicyVersion(alphaContext, workspaceRetentionPolicy.ID,
		skillretention.Policy{Enabled: true, RetentionDays: 45, DeleteLimit: 15}, "owner-alpha")
	if err != nil || workspaceRetentionVersion.Version != 2 {
		t.Fatalf("publish workspace retention policy version through RLS: version=%+v err=%v", workspaceRetentionVersion, err)
	}
	if loaded, err := restrictedStore.GetSkillAssetRetentionPolicy(betaContext, organizationRetentionPolicy.ID); err != nil || loaded.ID != organizationRetentionPolicy.ID {
		t.Fatalf("same-organization workspace could not read retention policy: policy=%+v err=%v", loaded, err)
	}
	if _, err := restrictedStore.GetSkillAssetRetentionPolicy(betaContext, workspaceRetentionPolicy.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace member could read workspace retention policy: %v", err)
	}
	if _, err := restrictedStore.GetSkillAssetRetentionPolicyVersion(betaContext, workspaceRetentionPolicy.ID, workspaceRetentionVersion.Version); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace member could read workspace retention policy version: %v", err)
	}
	if _, err := restrictedStore.GetSkillAssetRetentionPolicy(otherOrganizationContext, organizationRetentionPolicy.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-organization workspace could read retention policy: %v", err)
	}
	if _, _, err := restrictedStore.CreateSkillAssetRetentionPolicy(alphaContext, skillretention.CreatePolicyInput{
		ScopeType: skillretention.ScopeOrganization, OrganizationID: otherOrganization,
		Config: skillretention.Policy{Enabled: true, RetentionDays: 30, DeleteLimit: 10}, CreatedBy: "owner-alpha",
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected cross-organization retention policy create rejection, got %v", err)
	}
	alphaRetentionEffective, found, err := restrictedStore.ResolveSkillAssetRetentionPolicy(alphaContext, alphaWorkspace)
	if err != nil || !found || alphaRetentionEffective.Policy.ID != workspaceRetentionPolicy.ID {
		t.Fatalf("workspace retention policy did not override organization policy: effective=%+v found=%v err=%v", alphaRetentionEffective, found, err)
	}
	if _, err := restrictedStore.ArchiveSkillAssetRetentionPolicy(betaContext, betaRetentionPolicy.ID); err != nil {
		t.Fatalf("archive beta workspace retention policy through RLS: %v", err)
	}
	betaRetentionFallback, found, err := restrictedStore.ResolveSkillAssetRetentionPolicy(betaContext, betaWorkspace)
	if err != nil || !found || betaRetentionFallback.Policy.ID != organizationRetentionPolicy.ID || betaRetentionFallback.Version.ID != organizationRetentionVersion.ID {
		t.Fatalf("organization retention policy fallback failed: effective=%+v found=%v err=%v", betaRetentionFallback, found, err)
	}
	gcObject, err := restrictedStore.CreateObjectRefContext(alphaContext, CreateObjectRefInput{
		WorkspaceID: alphaWorkspace, Bucket: "rls-gc", ObjectKey: suffix + "/orphaned-skill-asset.bin",
		SizeBytes: 64, Metadata: json.RawMessage(`{"kind":"skill_asset","scan_provider":"test","scan_version":"1"}`),
	})
	if err != nil {
		t.Fatalf("create GC object through RLS: %v", err)
	}
	gcSeedTx, err := restrictedStore.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin GC candidate seed transaction: %v", err)
	}
	if _, err := gcSeedTx.Exec(`SELECT set_config('tma.workspace_id', $1, true)`, alphaWorkspace); err != nil {
		gcSeedTx.Rollback()
		t.Fatalf("set GC candidate seed scope: %v", err)
	}
	gcObjectCreatedAt := time.Now().UTC().Add(-90 * 24 * time.Hour)
	if _, err := gcSeedTx.Exec(`UPDATE object_refs SET created_at = $2 WHERE id = $1`, gcObject.ID, gcObjectCreatedAt); err != nil {
		gcSeedTx.Rollback()
		t.Fatalf("age GC candidate through RLS: %v", err)
	}
	if err := gcSeedTx.Commit(); err != nil {
		t.Fatalf("commit GC candidate seed: %v", err)
	}
	gcCtx, err := restrictedStore.SkillAssetGCWorkspaceContext(context.Background(), alphaWorkspace)
	if err != nil {
		t.Fatalf("bind background GC workspace context: %v", err)
	}
	gcCutoff := time.Now().UTC().Add(-45 * 24 * time.Hour)
	gcCandidates, err := restrictedStore.ListSkillAssetGCCandidates(gcCtx, skillretention.ListCandidatesInput{
		WorkspaceID: alphaWorkspace, Cutoff: gcCutoff, Limit: 10,
	})
	if err != nil || len(gcCandidates) != 1 || gcCandidates[0].ObjectRefID != gcObject.ID {
		t.Fatalf("list GC candidates through RLS: candidates=%+v err=%v", gcCandidates, err)
	}
	releaseGCLock, err := restrictedStore.AcquireSkillAssetGCLock(gcCtx, alphaWorkspace)
	if err != nil {
		t.Fatalf("acquire GC workspace lock: %v", err)
	}
	gcRun, gcItems, err := restrictedStore.StartSkillAssetGCRun(gcCtx, skillretention.StartRunInput{
		WorkspaceID: alphaWorkspace, Effective: alphaRetentionEffective,
		RequestedBy: "system:rls-gc", StartedAt: time.Now().UTC(), Candidates: gcCandidates,
	})
	if err != nil || len(gcItems) != 1 {
		_ = releaseGCLock()
		t.Fatalf("start GC run through RLS: run=%+v items=%+v err=%v", gcRun, gcItems, err)
	}
	claimedCandidate, eligible, _, err := restrictedStore.ClaimSkillAssetGCItem(gcCtx, gcItems[0].ID, gcCutoff)
	if err != nil || !eligible || claimedCandidate.ObjectRefID != gcObject.ID {
		_ = releaseGCLock()
		t.Fatalf("claim GC item through RLS: candidate=%+v eligible=%v err=%v", claimedCandidate, eligible, err)
	}
	tombstone, err := restrictedStore.FinalizeSkillAssetGCItem(gcCtx, gcItems[0].ID, "system:rls-gc", false)
	if err != nil || tombstone.ObjectRefID != gcObject.ID {
		_ = releaseGCLock()
		t.Fatalf("finalize GC item through RLS: tombstone=%+v err=%v", tombstone, err)
	}
	gcRun, err = restrictedStore.FinishSkillAssetGCRun(gcCtx, gcRun.ID)
	if err != nil || gcRun.Status != skillretention.RunStatusSucceeded || gcRun.DeletedCount != 1 {
		_ = releaseGCLock()
		t.Fatalf("finish GC run through RLS: run=%+v err=%v", gcRun, err)
	}
	if err := releaseGCLock(); err != nil {
		t.Fatalf("release GC workspace lock: %v", err)
	}
	loadedGCRun, loadedGCItems, err := restrictedStore.GetSkillAssetGCRun(gcCtx, gcRun.ID)
	if err != nil || loadedGCRun.ID != gcRun.ID || len(loadedGCItems) != 1 || loadedGCItems[0].Status != skillretention.ItemStatusDeleted {
		t.Fatalf("get GC run through RLS: run=%+v items=%+v err=%v", loadedGCRun, loadedGCItems, err)
	}
	if tombstones, err := restrictedStore.ListSkillAssetGCTombstones(gcCtx, skillretention.ListTombstonesInput{WorkspaceID: alphaWorkspace, Limit: 10}); err != nil || len(tombstones) != 1 || tombstones[0].ID != tombstone.ID {
		t.Fatalf("list GC tombstones through RLS: tombstones=%+v err=%v", tombstones, err)
	}
	if _, _, err := restrictedStore.GetSkillAssetGCRun(betaContext, gcRun.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace member could read GC run: %v", err)
	}
	queuedChild, err := restrictedStore.CreateSubagentSessionContext(alphaContext, CreateSubagentSessionInput{Session: CreateSessionInput{
		ParentSessionID: alphaSession.ID, ParentTurnID: "turn_queue_rls", AgentID: alphaAgent.ID,
		EnvironmentID: alphaEnvironment.ID, CreatedBy: "owner-alpha",
	}})
	if err != nil {
		t.Fatalf("create queued child Session through RLS: %v", err)
	}
	queueInput := EnqueueSubagentStartInput{
		SessionID: queuedChild.ID, ParentSessionID: alphaSession.ID, ParentTurnID: "turn_queue_rls",
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"queued through RLS"}]}`),
		Limits:  SubagentLimits{QueueTimeoutSeconds: 60},
	}
	queuedRequest, err := restrictedStore.EnqueueSubagentStartContext(alphaContext, queueInput)
	if err != nil {
		t.Fatalf("enqueue subagent start through RLS: %v", err)
	}
	if pending, err := restrictedStore.GetPendingSubagentStartContext(alphaContext, queuedChild.ID); err != nil || pending.ID != queuedRequest.ID {
		t.Fatalf("read queued subagent start through RLS: pending=%+v err=%v", pending, err)
	}
	if _, err := restrictedStore.GetPendingSubagentStartContext(alphaPeerContext, queuedChild.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("peer owner could read queued subagent start through RLS: %v", err)
	}
	if _, err := restrictedStore.CancelSubagentStartContext(alphaContext, CancelSubagentStartInput{
		SessionID: queuedChild.ID, ParentSessionID: alphaSession.ID, Reason: "RLS cancel test",
	}); err != nil {
		t.Fatalf("cancel queued subagent start through RLS: %v", err)
	}
	queuedRequest, err = restrictedStore.EnqueueSubagentStartContext(alphaContext, queueInput)
	if err != nil {
		t.Fatalf("re-enqueue subagent start through RLS: %v", err)
	}
	promotions, err := restrictedStore.PromoteSubagentStarts(PromoteSubagentStartsInput{Limit: 10})
	if err != nil {
		t.Fatalf("promote subagent start through workspace-scoped RLS scan: %v", err)
	}
	promotedTurnID := ""
	for _, promotion := range promotions {
		if promotion.Request.ID == queuedRequest.ID {
			promotedTurnID = promotion.Request.TurnID
			break
		}
	}
	if promotedTurnID == "" {
		t.Fatalf("workspace-scoped promotion did not find queued request: promotions=%+v", promotions)
	}
	if _, err := restrictedStore.FailSessionTurnContext(alphaContext, queuedChild.ID, promotedTurnID, "expected queue RLS cleanup"); err != nil {
		t.Fatalf("fail promoted subagent turn through RLS: %v", err)
	}
	taskGroup, err := restrictedStore.CreateSubagentTaskGroupContext(alphaContext, CreateSubagentTaskGroupInput{
		WorkspaceID: betaWorkspace, OwnerID: "owner-beta", ParentSessionID: alphaSession.ID,
		ParentTurnID: "turn_group_rls", Strategy: SubagentTaskGroupStrategyAllCompleted,
		ResultReducer: SubagentTaskGroupReducerConcatText, PlannedCount: 1,
	})
	if err != nil {
		t.Fatalf("create task group through RLS: %v", err)
	}
	if taskGroup.WorkspaceID != alphaWorkspace || taskGroup.OwnerID != "owner-alpha" {
		t.Fatalf("task group did not derive parent Session scope: group=%+v", taskGroup)
	}
	groupItem, err := restrictedStore.AppendSubagentTaskGroupItemContext(alphaContext, taskGroup.ID, AppendSubagentTaskGroupItemInput{
		ItemIndex: 0, AgentID: alphaAgent.ID, EnvironmentID: alphaEnvironment.ID, SessionID: queuedChild.ID,
		Title: "RLS item", Message: "RLS item message", InitialState: SubagentTaskGroupItemStateCreated,
	})
	if err != nil {
		t.Fatalf("append task group item through RLS: %v", err)
	}
	updatedGroupItem, err := restrictedStore.UpdateSubagentTaskGroupItemContext(alphaContext, taskGroup.ID, groupItem.ItemIndex, UpdateSubagentTaskGroupItemInput{
		SessionID: queuedChild.ID, Title: groupItem.Title, Message: groupItem.Message, Priority: 1,
		InitialState: SubagentTaskGroupItemStateStarted, IncrementRetry: true,
	})
	if err != nil || updatedGroupItem.RetryCount != 1 {
		t.Fatalf("update task group item through RLS: item=%+v err=%v", updatedGroupItem, err)
	}
	nestedGroup, err := restrictedStore.CreateSubagentTaskGroupContext(alphaContext, CreateSubagentTaskGroupInput{
		ParentSessionID: queuedChild.ID, ParentTurnID: promotedTurnID, ParentGroupID: taskGroup.ID, ParentItemIndex: 0,
		Strategy: SubagentTaskGroupStrategyAnyCompleted, ResultReducer: SubagentTaskGroupReducerFirstSuccess, PlannedCount: 1,
	})
	if err != nil {
		t.Fatalf("create nested task group through RLS: %v", err)
	}
	childGroups, err := restrictedStore.ListChildSubagentTaskGroupsContext(alphaContext, taskGroup.ID, 0)
	if err != nil || len(childGroups) != 1 || childGroups[0].ID != nestedGroup.ID {
		t.Fatalf("list nested task groups through RLS: groups=%+v err=%v", childGroups, err)
	}
	if items, err := restrictedStore.ListSubagentTaskGroupItemsContext(alphaContext, taskGroup.ID); err != nil || len(items) != 1 {
		t.Fatalf("list task group items through RLS: items=%+v err=%v", items, err)
	}
	if _, err := restrictedStore.GetSubagentTaskGroupContext(alphaPeerContext, taskGroup.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("peer owner could read task group through RLS: %v", err)
	}
	operatorGroups, err := restrictedStore.ListSubagentTaskGroupsByParentSessionContext(alphaOperatorContext, alphaSession.ID)
	if err != nil || len(operatorGroups) == 0 || operatorGroups[0].ID != taskGroup.ID {
		t.Fatalf("operator could not read workspace task group: groups=%+v err=%v", operatorGroups, err)
	}
	canceledGroup, err := restrictedStore.CancelSubagentTaskGroupContext(alphaContext, CancelSubagentTaskGroupInput{
		GroupID: taskGroup.ID, ParentSessionID: alphaSession.ID, Reason: "RLS cancellation test",
	})
	if err != nil || canceledGroup.CanceledAt == nil {
		t.Fatalf("cancel task group through RLS: group=%+v err=%v", canceledGroup, err)
	}
	reactivatedGroup, err := restrictedStore.ReactivateSubagentTaskGroupContext(alphaContext, ReactivateSubagentTaskGroupInput{
		GroupID: taskGroup.ID, ParentSessionID: alphaSession.ID,
	})
	if err != nil || reactivatedGroup.CanceledAt != nil {
		t.Fatalf("reactivate task group through RLS: group=%+v err=%v", reactivatedGroup, err)
	}
	deliberation, err := restrictedStore.CreateAgentDeliberationContext(alphaContext, CreateAgentDeliberationInput{
		Deliberation: AgentDeliberation{
			WorkspaceID: betaWorkspace, OwnerID: "owner-beta", ParentSessionID: alphaSession.ID,
			ParentTurnID: "turn_deliberation_rls", IdempotencyKey: "rls-deliberation-" + suffix,
			Objective: "validate deliberation RLS", Strategy: "expert_panel",
			ModeratorAgentID: alphaAgent.ID, ModeratorEnvironmentID: alphaEnvironment.ID,
		},
		Participants: []AgentDeliberationParticipant{
			{RoleID: "primary", RoleTitle: "Primary", Goal: "Propose", AgentID: alphaAgent.ID, EnvironmentID: alphaEnvironment.ID},
			{RoleID: "reviewer", RoleTitle: "Reviewer", Goal: "Review", AgentID: alphaAgent.ID, EnvironmentID: alphaEnvironment.ID},
		},
	})
	if err != nil {
		t.Fatalf("create deliberation through RLS: %v", err)
	}
	if deliberation.WorkspaceID != alphaWorkspace || deliberation.OwnerID != "owner-alpha" {
		t.Fatalf("deliberation did not derive parent Session scope: deliberation=%+v", deliberation)
	}
	if loaded, err := restrictedStore.GetAgentDeliberationByIdempotencyContext(alphaContext, alphaSession.ID, deliberation.IdempotencyKey); err != nil || loaded.ID != deliberation.ID {
		t.Fatalf("get deliberation by idempotency through RLS: deliberation=%+v err=%v", loaded, err)
	}
	if participants, err := restrictedStore.ListAgentDeliberationParticipantsContext(alphaContext, deliberation.ID); err != nil || len(participants) != 2 {
		t.Fatalf("list deliberation participants through RLS: participants=%+v err=%v", participants, err)
	}
	round, err := restrictedStore.CreateAgentDeliberationRoundContext(alphaContext, AgentDeliberationRound{
		DeliberationID: deliberation.ID, RoundNumber: 1, RoundType: "independent_brainstorm",
		Status: "running", TaskGroupID: taskGroup.ID,
	})
	if err != nil {
		t.Fatalf("create deliberation round through RLS: %v", err)
	}
	contribution, err := restrictedStore.UpsertAgentDeliberationContributionContext(alphaContext, AgentDeliberationContribution{
		DeliberationID: deliberation.ID, RoundNumber: 1, ParticipantIndex: 0,
		TaskGroupID: taskGroup.ID, ItemIndex: 0, SessionID: queuedChild.ID, Status: "completed",
		ContributionText: "tenant-scoped contribution", ContributionJSON: json.RawMessage(`{"position":"scoped"}`),
	})
	if err != nil || contribution.DeliberationID != deliberation.ID {
		t.Fatalf("upsert deliberation contribution through RLS: contribution=%+v err=%v", contribution, err)
	}
	if contributions, err := restrictedStore.ListAgentDeliberationContributionsContext(alphaContext, deliberation.ID, 1); err != nil || len(contributions) != 1 {
		t.Fatalf("list deliberation contributions through RLS: contributions=%+v err=%v", contributions, err)
	}
	round, err = restrictedStore.UpdateAgentDeliberationRoundContext(alphaContext, deliberation.ID, 1, UpdateAgentDeliberationRoundInput{
		Status: "completed", Summary: json.RawMessage(`{"summary":"scoped"}`), Questions: json.RawMessage(`{}`), Complete: true,
	})
	if err != nil || round.Status != "completed" {
		t.Fatalf("update deliberation round through RLS: round=%+v err=%v", round, err)
	}
	deliberation, err = restrictedStore.UpdateAgentDeliberationContext(alphaContext, deliberation.ID, UpdateAgentDeliberationInput{
		Status: AgentDeliberationStatusRunning, Phase: AgentDeliberationPhaseFinalizing,
		FinalGroupID: taskGroup.ID, FinalResult: json.RawMessage(`{"result":"scoped"}`),
	})
	if err != nil || deliberation.FinalGroupID != taskGroup.ID {
		t.Fatalf("update deliberation through RLS: deliberation=%+v err=%v", deliberation, err)
	}
	if rounds, err := restrictedStore.ListAgentDeliberationRoundsContext(alphaContext, deliberation.ID); err != nil || len(rounds) != 1 {
		t.Fatalf("list deliberation rounds through RLS: rounds=%+v err=%v", rounds, err)
	}
	if _, err := restrictedStore.GetAgentDeliberationContext(alphaPeerContext, deliberation.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("peer owner could read member deliberation through RLS: %v", err)
	}
	if _, err := restrictedStore.GetAgentDeliberationContext(alphaContext, peerDeliberation.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("member could read peer deliberation through RLS: %v", err)
	}
	if _, err := restrictedStore.GetAgentDeliberationContext(betaContext, deliberation.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace member could read deliberation through RLS: %v", err)
	}
	operatorDeliberations, err := restrictedStore.ListAgentDeliberationsByParentSessionContext(alphaOperatorContext, alphaPeerSession.ID)
	if err != nil || len(operatorDeliberations) != 1 || operatorDeliberations[0].ID != peerDeliberation.ID {
		t.Fatalf("operator could not read workspace deliberation: deliberations=%+v err=%v", operatorDeliberations, err)
	}
	runtimeConfig, err := restrictedStore.ResolveAgentRuntimeConfigContext(alphaContext, alphaSession.ID)
	if err != nil || runtimeConfig.WorkspaceID != alphaWorkspace || runtimeConfig.AgentID != alphaAgent.ID {
		t.Fatalf("resolve alpha runtime config through derived RLS scope: config=%+v err=%v", runtimeConfig, err)
	}
	turnEvents, err := restrictedStore.AppendEventsContext(alphaContext, alphaSession.ID, []AppendEventInput{{
		Type: EventUserMessage, Payload: json.RawMessage(`{"content":[{"type":"text","text":"RLS complete"}]}`),
	}})
	if err != nil {
		t.Fatalf("start Session turn through actual Session RLS: %v", err)
	}
	turnID := payloadString(turnEvents[len(turnEvents)-1].Payload, "turn_id")
	claimed, err := restrictedStore.ClaimSessionTurns(ClaimSessionTurnsInput{LeaseOwner: "rls-test-worker", LeaseDuration: time.Minute, Limit: 100})
	if err != nil {
		t.Fatalf("claim persistent Session turn without Session join: %v", err)
	}
	foundClaim := false
	claimedScope := AccessScope{}
	for _, work := range claimed {
		if work.SessionID == alphaSession.ID && work.TurnID == turnID {
			foundClaim = work.Scope.WorkspaceID == alphaWorkspace && work.Scope.OwnerID == "owner-alpha"
			claimedScope = work.Scope
			break
		}
	}
	if !foundClaim {
		t.Fatalf("persistent Session turn claim lost tenant scope: turn=%s claimed=%+v", turnID, claimed)
	}
	if active, err := restrictedStore.RenewSessionTurnLease(RenewSessionTurnLeaseInput{
		SessionID: alphaSession.ID, TurnID: turnID, Scope: claimedScope, LeaseOwner: "rls-test-worker", LeaseDuration: time.Minute,
	}); err != nil || !active {
		t.Fatalf("renew Session turn lease through child-table RLS: active=%v err=%v", active, err)
	}
	if err := restrictedStore.ReleaseSessionTurnLease(ReleaseSessionTurnLeaseInput{
		SessionID: alphaSession.ID, TurnID: turnID, Scope: claimedScope, LeaseOwner: "rls-test-worker",
	}); err != nil {
		t.Fatalf("release Session turn lease through child-table RLS: %v", err)
	}
	if _, err := restrictedStore.CompleteSessionTurnContext(alphaContext, alphaSession.ID, turnID, json.RawMessage(`{"content":[{"type":"text","text":"done"}]}`)); err != nil {
		t.Fatalf("complete Session turn through actual Session RLS: %v", err)
	}
	exporterFinishedAt := time.Now().UTC()
	alphaExporterRun, err := restrictedStore.RecordObservabilityExporterRunContext(alphaContext, RecordObservabilityExporterRunInput{
		WorkspaceID: alphaWorkspace, Exporter: ObservabilityExporterPerfetto, Status: ObservabilityExporterRunSucceeded,
		SessionID: alphaSession.ID, TurnID: turnID, TraceID: "trace_exporter_alpha_" + suffix,
		StartedAt: exporterFinishedAt, FinishedAt: exporterFinishedAt,
	})
	if err != nil || alphaExporterRun.WorkspaceID != alphaWorkspace {
		t.Fatalf("record alpha observability exporter run through RLS: run=%+v err=%v", alphaExporterRun, err)
	}
	betaExporterRun, err := restrictedStore.RecordObservabilityExporterRunContext(betaContext, RecordObservabilityExporterRunInput{
		WorkspaceID: betaWorkspace, Exporter: ObservabilityExporterOTLP, Status: ObservabilityExporterRunSucceeded,
		SessionID: betaSession.ID, TurnID: betaTurnID, TraceID: "trace_exporter_beta_" + suffix,
		StartedAt: exporterFinishedAt, FinishedAt: exporterFinishedAt,
	})
	if err != nil || betaExporterRun.WorkspaceID != betaWorkspace {
		t.Fatalf("record beta observability exporter run through RLS: run=%+v err=%v", betaExporterRun, err)
	}
	alphaExporterRuns, err := restrictedStore.ListObservabilityExporterRunsContext(alphaContext, ListObservabilityExporterRunsInput{Limit: 20})
	if err != nil || len(alphaExporterRuns) != 1 || alphaExporterRuns[0].ID != alphaExporterRun.ID {
		t.Fatalf("alpha observability exporter scope leaked or hid rows: runs=%+v err=%v", alphaExporterRuns, err)
	}
	betaExporterRuns, err := restrictedStore.ListObservabilityExporterRunsContext(betaContext, ListObservabilityExporterRunsInput{Limit: 20})
	if err != nil || len(betaExporterRuns) != 1 || betaExporterRuns[0].ID != betaExporterRun.ID {
		t.Fatalf("beta observability exporter scope leaked or hid rows: runs=%+v err=%v", betaExporterRuns, err)
	}
	if _, err := restrictedStore.RecordObservabilityExporterRunContext(alphaContext, RecordObservabilityExporterRunInput{
		WorkspaceID: alphaWorkspace, Exporter: ObservabilityExporterOTLP, Status: ObservabilityExporterRunFailed,
		SessionID: betaSession.ID, TurnID: betaTurnID, StartedAt: exporterFinishedAt, FinishedAt: exporterFinishedAt,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected cross-workspace observability exporter session rejection, got %v", err)
	}
	failEvents, err := restrictedStore.AppendEventsContext(alphaContext, alphaSession.ID, []AppendEventInput{{
		Type: EventUserMessage, Payload: json.RawMessage(`{"content":[{"type":"text","text":"RLS fail"}]}`),
	}})
	if err != nil {
		t.Fatalf("start failing Session turn through actual Session RLS: %v", err)
	}
	failTurnID := payloadString(failEvents[len(failEvents)-1].Payload, "turn_id")
	if _, err := restrictedStore.FailSessionTurnContext(alphaContext, alphaSession.ID, failTurnID, "expected RLS test failure"); err != nil {
		t.Fatalf("fail Session turn through actual Session RLS: %v", err)
	}
	usageRecord, err := restrictedStore.RecordLLMUsageContext(alphaContext, RecordLLMUsageInput{
		WorkspaceID: betaWorkspace, AgentID: betaAgent.ID, AgentConfigVersion: 99,
		SessionID: alphaSession.ID, TurnID: turnID, ProviderID: "fake", Model: "test-model",
		InputTokens: 10, OutputTokens: 5, TotalTokens: 15, Status: "completed",
	})
	if err != nil {
		t.Fatalf("record LLM usage through RLS: %v", err)
	}
	if usageRecord.WorkspaceID != alphaWorkspace || usageRecord.AgentID != alphaAgent.ID || usageRecord.AgentConfigVersion != alphaSession.AgentConfigVersion {
		t.Fatalf("LLM usage did not derive Session tenant fields: record=%+v", usageRecord)
	}
	usageReport, err := restrictedStore.GetSessionLLMUsageContext(alphaContext, alphaSession.ID)
	if err != nil || usageReport.Summary.RecordCount != 1 {
		t.Fatalf("read LLM usage through RLS: report=%+v err=%v", usageReport, err)
	}
	traceID := "trace_rls_alpha_" + suffix
	traceTime := time.Now().UTC()
	if err := restrictedStore.UpsertTraceIndexContext(alphaContext, UpsertTraceIndexInput{
		Trace: TraceIndexEntry{TraceID: traceID, WorkspaceID: betaWorkspace, SessionID: alphaSession.ID, TurnID: turnID, StartedAt: traceTime, EndedAt: traceTime},
		Spans: []TraceSpanIndexEntry{{TraceID: traceID, SpanID: "span_rls_alpha", WorkspaceID: betaWorkspace, SessionID: betaSession.ID, TurnID: betaTurnID, Name: "rls span", StartTime: traceTime}},
	}); err != nil {
		t.Fatalf("upsert trace indexes through RLS: %v", err)
	}
	traceRows, err := restrictedStore.ListTraceIndexesContext(alphaContext, ListTraceIndexInput{TraceID: traceID, IncludeArchived: true, Limit: 10})
	if err != nil || len(traceRows) != 1 || traceRows[0].WorkspaceID != alphaWorkspace {
		t.Fatalf("read trace index through RLS: rows=%+v err=%v", traceRows, err)
	}
	spanRows, err := restrictedStore.ListTraceSpanIndexesContext(alphaContext, ListTraceSpanIndexInput{TraceID: traceID, Limit: 10})
	if err != nil || len(spanRows) != 1 || spanRows[0].SessionID != alphaSession.ID || spanRows[0].WorkspaceID != alphaWorkspace {
		t.Fatalf("trace span did not derive trace tenant fields: rows=%+v err=%v", spanRows, err)
	}
	peerTraceRows, err := restrictedStore.ListTraceIndexesContext(alphaPeerContext, ListTraceIndexInput{TraceID: traceID, IncludeArchived: true, Limit: 10})
	if err != nil || len(peerTraceRows) != 0 {
		t.Fatalf("peer owner could read trace index through RLS: rows=%+v err=%v", peerTraceRows, err)
	}
	summary, err := restrictedStore.SaveSessionSummaryContext(alphaContext, alphaSession.ID, UpsertSessionSummaryInput{
		SummaryText: "RLS-protected summary", SourceUntilSeq: failEvents[len(failEvents)-1].Seq,
	})
	if err != nil || summary.SessionID != alphaSession.ID {
		t.Fatalf("save Session summary through child-table RLS: summary=%+v err=%v", summary, err)
	}
	interventionEvents, err := restrictedStore.AppendEventsContext(alphaContext, alphaSession.ID, []AppendEventInput{{
		Type: EventUserMessage, Payload: json.RawMessage(`{"content":[{"type":"text","text":"RLS intervention"}]}`),
	}})
	if err != nil {
		t.Fatalf("start intervention Session turn through child-table RLS: %v", err)
	}
	interventionTurnID := payloadString(interventionEvents[len(interventionEvents)-1].Payload, "turn_id")
	savedIntervention, err := restrictedStore.SaveSessionInterventionContext(alphaContext, alphaSession.ID, SaveSessionInterventionInput{
		TurnID: interventionTurnID, CallID: "call_rls", ToolIdentifier: "rls_tool", APIName: "rls_api", InterventionMode: "required",
	})
	if err != nil || savedIntervention.SessionID != alphaSession.ID {
		t.Fatalf("save Session intervention through child-table RLS: intervention=%+v err=%v", savedIntervention, err)
	}
	if _, err := restrictedStore.FailSessionTurnContext(alphaContext, alphaSession.ID, interventionTurnID, "expected intervention cleanup"); err != nil {
		t.Fatalf("fail intervention Session turn through child-table RLS: %v", err)
	}
	if session, err := restrictedStore.GetSessionContext(alphaContext, alphaSession.ID); err != nil || session.ID != alphaSession.ID {
		t.Fatalf("member could not read own Session through RLS: session=%+v err=%v", session, err)
	}
	if _, err := restrictedStore.GetSessionContext(alphaContext, alphaPeerSession.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member could read peer-owned Session through RLS: %v", err)
	}
	for name, check := range map[string]func() error{
		"events": func() error {
			_, err := restrictedStore.ListEventsContext(alphaContext, alphaPeerSession.ID, 0)
			return err
		},
		"summary": func() error {
			_, err := restrictedStore.GetSessionSummaryContext(alphaContext, alphaPeerSession.ID)
			return err
		},
		"interventions": func() error {
			_, err := restrictedStore.ListSessionInterventionsContext(alphaContext, alphaPeerSession.ID, "")
			return err
		},
	} {
		if err := check(); !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrNotFound) {
			t.Fatalf("member could access peer-owned Session %s through child-table RLS: %v", name, err)
		}
	}
	operatorSessions, err := restrictedStore.ListSessionsContext(alphaOperatorContext, ListSessionsInput{IncludeArchived: true, Limit: 100})
	if err != nil || !containsSessionID(operatorSessions, alphaSession.ID) || !containsSessionID(operatorSessions, alphaPeerSession.ID) || containsSessionID(operatorSessions, betaSession.ID) {
		t.Fatalf("operator Session scope did not remain workspace-wide: sessions=%+v err=%v", operatorSessions, err)
	}
	if peerEvents, err := restrictedStore.ListEventsContext(alphaOperatorContext, alphaPeerSession.ID, 0); err != nil || len(peerEvents) < 2 {
		t.Fatalf("operator could not read peer-owned Session events: events=%+v err=%v", peerEvents, err)
	}
	alphaObject, err := restrictedStore.CreateObjectRefContext(alphaContext, CreateObjectRefInput{
		WorkspaceID: alphaWorkspace, Bucket: "rls-test", ObjectKey: suffix + "/alpha.txt",
	})
	if err != nil {
		t.Fatalf("create alpha object through RLS: %v", err)
	}
	betaObject, err := restrictedStore.CreateObjectRefContext(betaContext, CreateObjectRefInput{
		WorkspaceID: betaWorkspace, Bucket: "rls-test", ObjectKey: suffix + "/beta.txt",
	})
	if err != nil {
		t.Fatalf("create beta object through RLS: %v", err)
	}
	skillFileTx, err := restrictedStore.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin Skill package file RLS transaction: %v", err)
	}
	if _, err := skillFileTx.Exec(`SELECT set_config('tma.workspace_id', $1, true), set_config('tma.owner_id', 'owner-alpha', true)`, alphaWorkspace); err != nil {
		skillFileTx.Rollback()
		t.Fatalf("set Skill package file RLS scope: %v", err)
	}
	if _, err := skillFileTx.Exec(`
		INSERT INTO skill_version_package_files (skill_version_id, path, role, object_ref_id)
		VALUES ($1, 'assets/rls-alpha.txt', 'asset', $2)
	`, restrictedAlphaSkillVersion.ID, alphaObject.ID); err != nil {
		skillFileTx.Rollback()
		t.Fatalf("create Skill package file through RLS: %v", err)
	}
	var skillFileCount int
	if err := skillFileTx.QueryRow(`SELECT COUNT(*) FROM skill_version_package_files WHERE skill_version_id = $1`, restrictedAlphaSkillVersion.ID).Scan(&skillFileCount); err != nil || skillFileCount != 1 {
		skillFileTx.Rollback()
		t.Fatalf("read Skill package file through RLS: count=%d err=%v", skillFileCount, err)
	}
	if err := skillFileTx.Commit(); err != nil {
		t.Fatalf("commit Skill package file RLS transaction: %v", err)
	}
	alphaArtifact, err := restrictedStore.CreateSessionArtifactContext(alphaContext, CreateSessionArtifactInput{
		WorkspaceID: alphaWorkspace, SessionID: alphaSession.ID, ObjectRefID: alphaObject.ID,
		Name: "alpha.txt", ArtifactType: ArtifactTypeFile,
	})
	if err != nil {
		t.Fatalf("create alpha artifact through RLS: %v", err)
	}
	if object, err := restrictedStore.GetObjectRefContext(alphaContext, alphaObject.ID); err != nil || object.ID != alphaObject.ID {
		t.Fatalf("get alpha object through RLS: object=%+v err=%v", object, err)
	}
	if artifact, err := restrictedStore.GetSessionArtifactContext(alphaContext, alphaSession.ID, alphaArtifact.ID); err != nil || artifact.ID != alphaArtifact.ID {
		t.Fatalf("get alpha artifact through RLS: artifact=%+v err=%v", artifact, err)
	}
	if artifacts, err := restrictedStore.ListSessionArtifactsContext(alphaContext, alphaSession.ID); err != nil || len(artifacts) != 1 || artifacts[0].ID != alphaArtifact.ID {
		t.Fatalf("list alpha artifacts through RLS: artifacts=%+v err=%v", artifacts, err)
	}
	if count, err := restrictedStore.CountSessionArtifactsByObjectRefContext(alphaContext, alphaObject.ID); err != nil || count != 1 {
		t.Fatalf("count alpha object artifacts through RLS: count=%d err=%v", count, err)
	}
	for name, check := range map[string]func() error{
		"agent get": func() error {
			_, err := restrictedStore.GetAgentContext(betaContext, restrictedAlphaAgent.ID)
			return err
		},
		"agent config list": func() error {
			_, err := restrictedStore.ListAgentConfigVersionsContext(betaContext, restrictedAlphaAgent.ID)
			return err
		},
		"object get": func() error {
			_, err := restrictedStore.GetObjectRefContext(betaContext, alphaObject.ID)
			return err
		},
		"object count": func() error {
			_, err := restrictedStore.CountSessionArtifactsByObjectRefContext(betaContext, alphaObject.ID)
			return err
		},
		"artifact get": func() error {
			_, err := restrictedStore.GetSessionArtifactContext(betaContext, alphaSession.ID, alphaArtifact.ID)
			return err
		},
		"artifact create with hidden object": func() error {
			_, err := restrictedStore.CreateSessionArtifactContext(alphaContext, CreateSessionArtifactInput{
				WorkspaceID: alphaWorkspace, SessionID: alphaSession.ID, ObjectRefID: betaObject.ID,
				Name: "blocked.txt", ArtifactType: ArtifactTypeFile,
			})
			return err
		},
	} {
		t.Run("RLS cross-scope "+name, func(t *testing.T) {
			if err := check(); !errors.Is(err, ErrForbidden) {
				t.Fatalf("expected forbidden result, got %v", err)
			}
		})
	}
	var unscopedCount int
	if err := restrictedStore.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM managed_environment_variables`).Scan(&unscopedCount); err != nil {
		t.Fatalf("query managed environment variables without scope: %v", err)
	}
	if unscopedCount != 0 {
		t.Fatalf("RLS exposed %d managed environment rows without a transaction scope", unscopedCount)
	}
	for _, table := range []string{"agent_deliberation_contributions", "agent_deliberation_participants", "agent_deliberation_rounds", "agent_deliberations", "agents", "agent_config_versions", "environments", "llm_usage_records", "mcp_registry_servers", "mcp_registry_server_versions", "object_refs", "observability_exporter_runs", "operator_audit_log", "organizations", "security_audit_outbox", "session_artifacts", "session_events", "session_interventions", "session_summaries", "session_task_items", "session_task_plans", "session_turn_skill_usages", "session_turns", "sessions", "skill_asset_gc_items", "skill_asset_gc_runs", "skill_asset_gc_tombstones", "skill_asset_retention_policies", "skill_asset_retention_policy_versions", "skill_marketplace_entries", "skill_marketplace_policies", "skill_marketplace_policy_versions", "skill_version_package_files", "skill_versions", "skills", "subagent_start_requests", "subagent_task_group_items", "subagent_task_groups", "tool_permission_audit_records", "trace_indexes", "trace_span_indexes", "worker_work", "workers", "workspace_tool_permission_policies", "workspaces"} {
		if err := restrictedStore.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table).Scan(&unscopedCount); err != nil {
			t.Fatalf("query %s without scope: %v", table, err)
		}
		if unscopedCount != 0 {
			t.Fatalf("RLS exposed %d %s rows without a transaction scope", unscopedCount, table)
		}
	}

	tx, err := restrictedStore.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin raw RLS verification transaction: %v", err)
	}
	if _, err := tx.Exec(`SELECT set_config('tma.workspace_id', $1, true)`, alphaWorkspace); err != nil {
		t.Fatalf("set raw alpha RLS scope: %v", err)
	}
	if _, err := tx.Exec(`SELECT set_config('tma.owner_id', 'owner-alpha', true)`); err != nil {
		t.Fatalf("set raw alpha owner RLS scope: %v", err)
	}
	var scopedCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM managed_environment_variables`).Scan(&scopedCount); err != nil || scopedCount != 1 {
		t.Fatalf("expected one alpha row through RLS: count=%d err=%v", scopedCount, err)
	}
	if _, err := tx.Exec(`SELECT set_config('tma.owner_id', '', true)`); err != nil {
		t.Fatalf("clear raw alpha owner RLS scope: %v", err)
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM environments WHERE id IN ($1, $2)`, restrictedAlphaEnvironment.ID, restrictedBetaEnvironment.ID).Scan(&scopedCount); err != nil || scopedCount != 1 {
		t.Fatalf("expected only the alpha Environment through RLS: count=%d err=%v", scopedCount, err)
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM mcp_registry_servers WHERE id IN ($1, $2)`, restrictedAlphaMCP.ID, restrictedBetaMCP.ID).Scan(&scopedCount); err != nil || scopedCount != 1 {
		t.Fatalf("expected only the alpha MCP Registry server through RLS: count=%d err=%v", scopedCount, err)
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM workspaces WHERE id IN ($1, $2)`, alphaWorkspace, betaWorkspace).Scan(&scopedCount); err != nil || scopedCount != 1 {
		t.Fatalf("expected only the alpha Workspace directory row through RLS: count=%d err=%v", scopedCount, err)
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM organizations`).Scan(&scopedCount); err != nil || scopedCount != 1 {
		t.Fatalf("expected only the alpha Organization directory row through RLS: count=%d err=%v", scopedCount, err)
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM tma_list_workspace_ids() WHERE workspace_id IN ($1, $2)`, alphaWorkspace, betaWorkspace).Scan(&scopedCount); err != nil || scopedCount != 2 {
		t.Fatalf("tenant directory helper did not enumerate background Workspace scopes: count=%d err=%v", scopedCount, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit raw RLS read transaction: %v", err)
	}
	assertCrossScopeInsertRejected := func(name string, query string, args ...any) {
		t.Helper()
		tx, err := restrictedStore.db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatalf("begin raw %s RLS transaction: %v", name, err)
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`SELECT set_config('tma.workspace_id', $1, true)`, alphaWorkspace); err != nil {
			t.Fatalf("set raw alpha scope for %s: %v", name, err)
		}
		if _, err := tx.Exec(query, args...); err == nil {
			t.Fatalf("expected %s WITH CHECK policy to reject cross-workspace insert", name)
		}
	}
	assertCrossScopeInsertRejected("managed_environment_variables",
		`INSERT INTO managed_environment_variables (workspace_id, name, ciphertext) VALUES ($1, 'RAW_CROSS_SCOPE', $2)`,
		betaWorkspace, []byte("blocked"))
	assertCrossScopeInsertRejected("agents", `
		INSERT INTO agents (id, workspace_id, name, current_config_version)
		VALUES ('agt_raw_cross_scope', $1, 'raw-cross-scope', 1)
	`, betaWorkspace)
	assertCrossScopeInsertRejected("environments", `
		INSERT INTO environments (id, workspace_id, name)
		VALUES ('env_raw_cross_scope', $1, 'raw-cross-scope')
	`, betaWorkspace)
	assertCrossScopeInsertRejected("workers", `
		INSERT INTO workers (id, workspace_id, name)
		VALUES ('wrk_raw_cross_scope', $1, 'raw-cross-scope')
	`, betaWorkspace)
	assertCrossScopeInsertRejected("worker_work_workspace", `
		INSERT INTO worker_work (id, workspace_id, work_type, status)
		VALUES ('work_raw_cross_scope', $1, 'sandbox_command', 'pending')
	`, betaWorkspace)
	assertCrossScopeInsertRejected("worker_work_worker_reference", `
		INSERT INTO worker_work (id, workspace_id, worker_id, work_type, status)
		VALUES ('work_raw_cross_worker', $1, $2, 'sandbox_command', 'pending')
	`, alphaWorkspace, restrictedBetaWorker.ID)
	assertCrossScopeInsertRejected("mcp_registry_servers", `
		INSERT INTO mcp_registry_servers (id, workspace_id, identifier, name, current_version)
		VALUES ('mcps_raw_cross_scope', $1, 'raw-cross-scope', 'raw-cross-scope', 1)
	`, betaWorkspace)
	assertCrossScopeInsertRejected("mcp_registry_server_versions", `
		INSERT INTO mcp_registry_server_versions (id, server_id, version, config_json, checksum_sha256)
		VALUES ('mcpsv_raw_cross_scope', $1, 99, '{}', 'blocked')
	`, restrictedBetaMCP.ID)
	assertCrossScopeInsertRejected("observability_exporter_runs", `
		INSERT INTO observability_exporter_runs (
			id, workspace_id, exporter, status, session_id, turn_id, started_at, finished_at
		) VALUES ('oexp_raw_cross_scope', $1, 'otlp', 'failed', $2, $3, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, betaWorkspace, betaSession.ID, betaTurnID)
	assertCrossScopeInsertRejected("operator_audit_log", `
		INSERT INTO operator_audit_log (
			id, workspace_id, principal_id, action, resource_type, outcome
		) VALUES ('oaud_raw_cross_scope', $1, 'operator-beta', 'raw.cross', 'session', 'failed')
	`, betaWorkspace)
	assertCrossScopeInsertRejected("security_audit_outbox", `
		INSERT INTO security_audit_outbox (
			id, workspace_id, payload_json, integrity_algorithm, integrity_digest
		) VALUES ('saud_raw_cross_scope', $1, $2, 'sha256', 'blocked')
	`, betaWorkspace, `{"id":"saud_raw_cross_scope","workspace_id":"`+betaWorkspace+`","outcome":"denied"}`)
	assertCrossScopeInsertRejected("agent_config_versions", `
		INSERT INTO agent_config_versions (agent_id, version, llm_provider, llm_model)
		VALUES ($1, 99, 'fake', 'test-model')
	`, betaAgent.ID)
	assertCrossScopeInsertRejected("skills", `
		INSERT INTO skills (id, workspace_id, identifier, title, created_by)
		VALUES ('skl_raw_cross_scope', $1, 'raw-cross-scope', 'Raw cross scope', 'owner-beta')
	`, betaWorkspace)
	assertCrossScopeInsertRejected("skill_versions", `
		INSERT INTO skill_versions (id, skill_id, version, checksum_sha256, created_by)
		VALUES ('sklv_raw_cross_scope', $1, 99, 'blocked', 'owner-beta')
	`, betaSkill.ID)
	assertCrossScopeInsertRejected("skill_marketplace_entries", `
		INSERT INTO skill_marketplace_entries (
			id, workspace_id, skill_id, skill_version, created_by, updated_by
		) VALUES ('sment_raw_cross_scope', $1, $2, $3, 'owner-beta', 'owner-beta')
	`, betaWorkspace, betaSkill.ID, betaSkillVersion.Version)
	assertCrossScopeInsertRejected("skill_marketplace_policies_workspace", `
		INSERT INTO skill_marketplace_policies (
			id, scope_type, workspace_id, created_by
		) VALUES ('smpol_raw_cross_workspace', 'workspace', $1, 'owner-beta')
	`, betaWorkspace)
	assertCrossScopeInsertRejected("skill_marketplace_policies_organization", `
		INSERT INTO skill_marketplace_policies (
			id, scope_type, organization_id, created_by
		) VALUES ('smpol_raw_cross_org', 'organization', $1, 'other-org')
	`, otherOrganization)
	assertCrossScopeInsertRejected("skill_marketplace_policy_versions", `
		INSERT INTO skill_marketplace_policy_versions (
			id, policy_id, version, config_json, checksum_sha256, created_by
		) VALUES ('smpv_raw_cross_scope', $1, 99, '{}', 'blocked', 'owner-beta')
	`, betaMarketplacePolicy.ID)
	assertCrossScopeInsertRejected("skill_asset_retention_policies_workspace", `
		INSERT INTO skill_asset_retention_policies (
			id, scope_type, workspace_id, created_by
		) VALUES ('sarp_raw_cross_workspace', 'workspace', $1, 'owner-beta')
	`, betaWorkspace)
	assertCrossScopeInsertRejected("skill_asset_retention_policies_organization", `
		INSERT INTO skill_asset_retention_policies (
			id, scope_type, organization_id, created_by
		) VALUES ('sarp_raw_cross_org', 'organization', $1, 'other-org')
	`, otherOrganization)
	assertCrossScopeInsertRejected("skill_asset_retention_policy_versions", `
		INSERT INTO skill_asset_retention_policy_versions (
			id, policy_id, version, config_json, checksum_sha256, created_by
		) VALUES ('sarpv_raw_cross_scope', $1, 99, '{"enabled":true,"retention_days":30,"delete_limit":10}', 'blocked', 'owner-beta')
	`, betaRetentionPolicy.ID)
	assertCrossScopeInsertRejected("skill_asset_gc_runs", `
		INSERT INTO skill_asset_gc_runs (
			id, workspace_id, policy_source, policy_revision, retention_days, delete_limit, requested_by
		) VALUES ('sagcr_raw_cross_scope', $1, 'server', 'blocked', 30, 10, 'owner-beta')
	`, betaWorkspace)
	assertCrossScopeInsertRejected("skill_asset_gc_items", `
		INSERT INTO skill_asset_gc_items (
			id, run_id, workspace_id, object_ref_id, storage_provider, bucket, object_key,
			reason, eligible_at, object_created_at
		) VALUES ('sagci_raw_cross_scope', $1, $2, $3, 's3', 'rls-test', 'beta.txt',
			'orphaned_skill_asset', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, betaGCRun.ID, betaWorkspace, betaObject.ID)
	assertCrossScopeInsertRejected("skill_asset_gc_tombstones", `
		INSERT INTO skill_asset_gc_tombstones (
			id, run_id, workspace_id, object_ref_id, storage_provider, bucket, object_key,
			reason, deleted_by
		) VALUES ('sagct_raw_cross_scope', $1, $2, 'obj_deleted_beta', 's3', 'rls-test', 'beta.txt',
			'orphaned_skill_asset', 'owner-beta')
	`, betaGCRun.ID, betaWorkspace)
	assertCrossScopeInsertRejected("skill_version_package_files", `
		INSERT INTO skill_version_package_files (skill_version_id, path, role, object_ref_id)
		VALUES ($1, 'assets/raw-cross-scope.txt', 'asset', $2)
	`, betaSkillVersion.ID, betaObject.ID)
	assertCrossScopeInsertRejected("session_turn_skill_usages", `
		INSERT INTO session_turn_skill_usages (
			id, workspace_id, session_id, turn_id, agent_id, agent_config_version,
			skill_id, skill_identifier, skill_version, requested_mode, status
		) VALUES ('sklu_raw_cross_scope', $1, $2, 'turn_raw_cross_scope', $3, $4, $5, $6, $7, 'full', 'resolved')
	`, betaWorkspace, betaSession.ID, betaAgent.ID, betaSession.AgentConfigVersion,
		betaSkill.ID, betaSkill.Identifier, betaSkillVersion.Version)
	assertCrossScopeInsertRejected("object_refs", `
		INSERT INTO object_refs (id, workspace_id, bucket, object_key)
		VALUES ('obj_raw_cross_scope', $1, 'rls-test', 'raw-cross-scope.txt')
	`, betaWorkspace)
	assertCrossScopeInsertRejected("session_artifacts", `
		INSERT INTO session_artifacts (id, workspace_id, session_id, object_ref_id, name)
		VALUES ('art_raw_cross_scope', $1, $2, $3, 'raw-cross-scope.txt')
	`, betaWorkspace, betaSession.ID, betaObject.ID)
	assertCrossScopeInsertRejected("session_events", `
		INSERT INTO session_events (id, session_id, seq, type)
		VALUES ('evt_raw_cross_scope', $1, 9999, 'agent.message')
	`, betaSession.ID)
	assertCrossScopeInsertRejected("session_summaries", `
		INSERT INTO session_summaries (session_id, summary_text)
		VALUES ($1, 'raw cross scope')
	`, betaSession.ID)
	assertCrossScopeInsertRejected("session_turns", `
		INSERT INTO session_turns (session_id, id, workspace_id, owner_id, status)
		VALUES ($1, 'turn_raw_cross_scope', $2, 'owner-beta', 'running')
	`, betaSession.ID, betaWorkspace)
	assertCrossScopeInsertRejected("session_interventions", `
		INSERT INTO session_interventions (session_id, turn_id, call_id, tool_identifier, api_name, intervention_mode)
		VALUES ($1, $2, 'call_raw_cross_scope', 'raw_tool', 'raw_api', 'required')
	`, betaSession.ID, betaTurnID)
	assertCrossScopeInsertRejected("llm_usage_records", `
		INSERT INTO llm_usage_records (id, workspace_id, agent_id, agent_config_version, session_id, turn_id, provider_id, model, status)
		VALUES ('llmu_raw_cross_scope', $1, $2, $3, $4, $5, 'fake', 'test-model', 'completed')
	`, betaWorkspace, betaAgent.ID, betaSession.AgentConfigVersion, betaSession.ID, betaTurnID)
	assertCrossScopeInsertRejected("trace_indexes", `
		INSERT INTO trace_indexes (trace_id, workspace_id, session_id, turn_id, started_at, ended_at)
		VALUES ('trace_raw_cross_scope', $1, $2, $3, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, betaWorkspace, betaSession.ID, betaTurnID)
	assertCrossScopeInsertRejected("trace_span_indexes", `
		INSERT INTO trace_span_indexes (trace_id, span_id, workspace_id, session_id, turn_id, name, start_time)
		VALUES ($1, 'span_raw_cross_scope', $2, $3, $4, 'raw span', CURRENT_TIMESTAMP)
	`, betaTraceID, betaWorkspace, betaSession.ID, betaTurnID)
	assertCrossScopeInsertRejected("subagent_start_requests", `
		INSERT INTO subagent_start_requests (id, workspace_id, owner_id, session_id, parent_session_id, payload_json, expires_at)
		VALUES ('sreq_raw_cross_scope', $1, 'owner-beta', $2, $2, '{}', CURRENT_TIMESTAMP + interval '1 minute')
	`, betaWorkspace, betaSession.ID)
	assertCrossScopeInsertRejected("subagent_task_groups", `
		INSERT INTO subagent_task_groups (id, workspace_id, owner_id, parent_session_id, strategy, result_reducer, planned_count)
		VALUES ('sgrp_raw_cross_scope', $1, 'owner-beta', $2, 'all_completed', 'concat_text', 1)
	`, betaWorkspace, betaSession.ID)
	assertCrossScopeInsertRejected("subagent_task_group_items", `
		INSERT INTO subagent_task_group_items (group_id, item_index, agent_id, environment_id, session_id)
		VALUES ($1, 99, $2, $3, $4)
	`, betaTaskGroup.ID, betaAgent.ID, betaEnvironment.ID, betaSession.ID)
	assertCrossScopeInsertRejected("agent_deliberations", `
		INSERT INTO agent_deliberations (
			id, workspace_id, owner_id, parent_session_id, objective, strategy,
			max_participants, moderator_agent_id, moderator_environment_id
		) VALUES ('dlib_raw_cross_scope', $1, 'owner-beta', $2, 'raw cross scope', 'expert_panel', 2, $3, $4)
	`, betaWorkspace, betaSession.ID, betaAgent.ID, betaEnvironment.ID)
	assertCrossScopeInsertRejected("agent_deliberation_participants", `
		INSERT INTO agent_deliberation_participants (
			deliberation_id, participant_index, role_id, role_title, goal, agent_id, environment_id
		) VALUES ($1, 2, 'raw_cross_scope', 'Raw', 'Blocked', $2, $3)
	`, betaDeliberation.ID, betaAgent.ID, betaEnvironment.ID)
	assertCrossScopeInsertRejected("agent_deliberation_rounds", `
		INSERT INTO agent_deliberation_rounds (deliberation_id, round_number, round_type, status, task_group_id)
		VALUES ($1, 2, 'cross_critique', 'running', $2)
	`, betaDeliberation.ID, betaTaskGroup.ID)
	assertCrossScopeInsertRejected("agent_deliberation_contributions", `
		INSERT INTO agent_deliberation_contributions (
			deliberation_id, round_number, participant_index, task_group_id, item_index, session_id, status
		) VALUES ($1, 1, 0, $2, 0, $3, 'completed')
	`, betaDeliberation.ID, betaTaskGroup.ID, betaSession.ID)
	assertCrossScopeInsertRejected("sessions", `
		INSERT INTO sessions (id, workspace_id, owner_id, agent_id, agent_config_version, environment_id, status, created_by)
		VALUES ('sesn_raw_cross_scope', $1, 'owner-beta', $2, 1, $3, 'idle', 'owner-beta')
	`, betaWorkspace, betaAgent.ID, betaEnvironment.ID)
	ownerTx, err := restrictedStore.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin raw owner RLS transaction: %v", err)
	}
	defer ownerTx.Rollback()
	if _, err := ownerTx.Exec(`SELECT set_config('tma.workspace_id', $1, true), set_config('tma.owner_id', 'owner-alpha', true)`, alphaWorkspace); err != nil {
		t.Fatalf("set raw owner RLS scope: %v", err)
	}
	var ownEventCount int
	if err := ownerTx.QueryRow(`SELECT COUNT(*) FROM session_events WHERE session_id = $1`, alphaSession.ID).Scan(&ownEventCount); err != nil || ownEventCount == 0 {
		t.Fatalf("owner scope could not read own Session events: count=%d err=%v", ownEventCount, err)
	}
	var peerEventCount int
	if err := ownerTx.QueryRow(`SELECT COUNT(*) FROM session_events WHERE session_id = $1`, alphaPeerSession.ID).Scan(&peerEventCount); err != nil || peerEventCount != 0 {
		t.Fatalf("owner scope exposed peer Session events: count=%d err=%v", peerEventCount, err)
	}
	if _, err := ownerTx.Exec(`
		INSERT INTO sessions (id, workspace_id, owner_id, agent_id, agent_config_version, environment_id, status, created_by)
		VALUES ('sesn_raw_cross_owner', $1, 'owner-peer', $2, 1, $3, 'idle', 'owner-peer')
	`, alphaWorkspace, alphaAgent.ID, alphaEnvironment.ID); err == nil {
		t.Fatal("expected sessions WITH CHECK policy to reject cross-owner insert")
	}
	if _, err := ownerTx.Exec(`
		INSERT INTO agent_deliberations (
			id, workspace_id, owner_id, parent_session_id, objective, strategy,
			max_participants, moderator_agent_id, moderator_environment_id
		) VALUES ('dlib_raw_cross_owner', $1, 'owner-peer', $2, 'raw cross owner', 'expert_panel', 2, $3, $4)
	`, alphaWorkspace, alphaPeerSession.ID, alphaAgent.ID, alphaEnvironment.ID); err == nil {
		t.Fatal("expected agent_deliberations WITH CHECK policy to reject cross-owner insert")
	}
}

func containsSessionID(sessions []Session, sessionID string) bool {
	for _, session := range sessions {
		if session.ID == sessionID {
			return true
		}
	}
	return false
}

func containsAgentID(agents []Agent, id string) bool {
	for _, agent := range agents {
		if agent.ID == id {
			return true
		}
	}
	return false
}

func containsWorkerID(workers []Worker, id string) bool {
	for _, worker := range workers {
		if worker.ID == id {
			return true
		}
	}
	return false
}

func TestPostgresSecurityAuditOutboxLeaseRetryReplayAndPrune(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	now := time.Now().UTC().Add(-2 * time.Hour)
	suffix := strings.ReplaceAll(time.Now().UTC().Format("150405.000000000"), ".", "")
	ids := []string{"saud_pg_" + suffix + "_1", "saud_pg_" + suffix + "_2", "saud_pg_" + suffix + "_3"}
	t.Cleanup(func() {
		for _, id := range ids {
			_, _ = store.db.ExecContext(context.Background(), `DELETE FROM security_audit_outbox WHERE id = $1`, id)
		}
	})
	for _, id := range ids {
		payload := json.RawMessage(`{"id":"` + id + `","outcome":"allowed"}`)
		if _, err := store.RecordSecurityAuditOutbox(RecordSecurityAuditOutboxInput{
			ID: id, Payload: payload, IntegrityAlgorithm: "hmac-sha256", IntegrityKeyID: "rotation-2026-07",
			IntegrityDigest: "digest-" + id, CreatedAt: now,
		}); err != nil {
			t.Fatalf("record security audit outbox %s: %v", id, err)
		}
	}
	first, err := store.ClaimSecurityAuditOutbox(ClaimSecurityAuditOutboxInput{
		LeaseOwner: "worker-a", Now: now.Add(time.Minute), LeaseDuration: time.Minute, MaxAttempts: 2, Limit: 2,
	})
	if err != nil || len(first) != 2 || first[0].AttemptCount != 1 || first[0].IntegrityKeyID != "rotation-2026-07" {
		t.Fatalf("claim first security audit batch: events=%+v err=%v", first, err)
	}
	second, err := store.ClaimSecurityAuditOutbox(ClaimSecurityAuditOutboxInput{
		LeaseOwner: "worker-b", Now: now.Add(time.Minute), LeaseDuration: time.Minute, MaxAttempts: 2, Limit: 2,
	})
	if err != nil || len(second) != 1 || second[0].ID != ids[2] {
		t.Fatalf("leased events were reclaimed too early: events=%+v err=%v", second, err)
	}
	if _, err := store.CompleteSecurityAuditOutbox(CompleteSecurityAuditOutboxInput{
		IDs: []string{first[0].ID}, LeaseOwner: "worker-a", At: now.Add(90 * time.Minute),
	}); err != nil {
		t.Fatalf("complete security audit event: %v", err)
	}
	retryAt := now.Add(3 * time.Minute)
	if _, err := store.FailSecurityAuditOutbox(FailSecurityAuditOutboxInput{
		IDs: []string{first[1].ID}, LeaseOwner: "worker-a", ErrorMessage: "collector unavailable",
		NextAttemptAt: retryAt, At: now.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("schedule security audit retry: %v", err)
	}
	if events, err := store.ClaimSecurityAuditOutbox(ClaimSecurityAuditOutboxInput{
		LeaseOwner: "worker-c", Now: retryAt.Add(-time.Second), LeaseDuration: time.Minute, MaxAttempts: 2, Limit: 10,
	}); err != nil || len(events) != 1 || events[0].ID != ids[2] {
		t.Fatalf("expected only expired lease before retry due time: events=%+v err=%v", events, err)
	}
	claimedRetry, err := store.ClaimSecurityAuditOutbox(ClaimSecurityAuditOutboxInput{
		LeaseOwner: "worker-d", Now: retryAt, LeaseDuration: time.Minute, MaxAttempts: 2, Limit: 10,
	})
	if err != nil || len(claimedRetry) != 1 || claimedRetry[0].ID != first[1].ID || claimedRetry[0].AttemptCount != 2 {
		t.Fatalf("claim due retry: events=%+v err=%v", claimedRetry, err)
	}
	if _, err := store.FailSecurityAuditOutbox(FailSecurityAuditOutboxInput{
		IDs: []string{claimedRetry[0].ID}, LeaseOwner: "worker-d", ErrorMessage: "maximum attempts",
		DeadLetter: true, At: retryAt,
	}); err != nil {
		t.Fatalf("dead letter security audit event: %v", err)
	}
	replayed, err := store.ReplaySecurityAuditDeadLetters(ReplaySecurityAuditDeadLettersInput{Before: retryAt.Add(time.Second), Limit: 10})
	if err != nil || replayed != 1 {
		t.Fatalf("replay security audit dead letters: replayed=%d err=%v", replayed, err)
	}
	stats, err := store.GetSecurityAuditOutboxStats(time.Now().UTC())
	if err != nil || stats.Pending < 1 || stats.Delivering < 1 || stats.Delivered < 1 {
		t.Fatalf("unexpected security audit outbox stats: %+v err=%v", stats, err)
	}
	keyStats, err := store.ListSecurityAuditIntegrityKeyStats()
	if err != nil || len(keyStats) != 1 || keyStats[0].KeyID != "rotation-2026-07" || keyStats[0].Pending < 1 || keyStats[0].Delivering < 1 || keyStats[0].Delivered < 1 {
		t.Fatalf("unexpected security audit integrity key stats: %+v err=%v", keyStats, err)
	}
	pruned, err := store.PruneDeliveredSecurityAuditOutbox(time.Now().UTC().Add(-time.Minute), 10)
	if err != nil || pruned < 1 {
		t.Fatalf("prune delivered security audit outbox: pruned=%d err=%v", pruned, err)
	}
}

func TestPostgresLLMReferenceIntegritySerializesConcurrentDelete(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	ctx := context.Background()
	suffix := strings.ReplaceAll(time.Now().UTC().Format("150405.000000000"), ".", "")
	providerID := "reference-provider-" + suffix
	baseModel := "base-model-" + suffix
	overrideModel := "override-model-" + suffix
	raceModel := "race-model-" + suffix
	workspaceID := createPostgresIntegrationWorkspace(t, store, "llm-reference")
	raceAgentID := "agt_reference_race_" + suffix
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(ctx, `DELETE FROM sessions WHERE workspace_id = $1`, workspaceID)
		_, _ = store.db.ExecContext(ctx, `DELETE FROM environments WHERE workspace_id = $1`, workspaceID)
		_, _ = store.db.ExecContext(ctx, `DELETE FROM agents WHERE workspace_id = $1`, workspaceID)
		_, _ = store.db.ExecContext(ctx, `DELETE FROM llm_models WHERE provider_id = $1`, providerID)
		_, _ = store.db.ExecContext(ctx, `DELETE FROM llm_providers WHERE id = $1`, providerID)
	})
	if _, err := store.UpsertLLMProvider(UpsertLLMProviderInput{ID: providerID, ProviderType: "fake", Enabled: true}); err != nil {
		t.Fatalf("create LLM reference provider: %v", err)
	}
	for _, modelName := range []string{baseModel, overrideModel, raceModel} {
		if _, err := store.UpsertLLMModel(UpsertLLMModelInput{
			ProviderID: providerID, Model: modelName, ContextWindowTokens: 4096,
		}); err != nil {
			t.Fatalf("create LLM reference model %s: %v", modelName, err)
		}
	}
	agent, err := store.CreateAgent(CreateAgentInput{
		WorkspaceID: workspaceID, Name: "LLM reference agent", LLMProvider: providerID, LLMModel: baseModel,
	})
	if err != nil {
		t.Fatalf("create LLM reference agent: %v", err)
	}
	environment, err := store.CreateEnvironment(CreateEnvironmentInput{
		WorkspaceID: workspaceID, Name: "LLM reference environment", Config: json.RawMessage(`{"type":"test"}`),
	})
	if err != nil {
		t.Fatalf("create LLM reference environment: %v", err)
	}
	session, err := store.CreateSession(CreateSessionInput{
		WorkspaceID: workspaceID, AgentID: agent.ID, EnvironmentID: environment.ID, CreatedBy: "reference-test",
	})
	if err != nil {
		t.Fatalf("create LLM reference session: %v", err)
	}
	if _, err := store.UpdateSessionRuntimeSettings(session.ID, UpdateSessionRuntimeSettingsInput{
		RuntimeSettings:  json.RawMessage(`{"llm_provider":"` + providerID + `","llm_model":"` + overrideModel + `"}`),
		ExpectedRevision: session.RuntimeSettingsRevision,
	}); err != nil {
		t.Fatalf("apply session LLM override: %v", err)
	}
	var effectiveProvider, effectiveModel string
	if err := store.db.QueryRowContext(ctx, `
		SELECT effective_llm_provider, effective_llm_model FROM sessions WHERE id = $1
	`, session.ID).Scan(&effectiveProvider, &effectiveModel); err != nil {
		t.Fatalf("read effective session LLM reference: %v", err)
	}
	if effectiveProvider != providerID || effectiveModel != overrideModel {
		t.Fatalf("unexpected effective session LLM reference: %s/%s", effectiveProvider, effectiveModel)
	}
	if err := store.DeleteLLMModel(providerID, overrideModel); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected session override to block model deletion, got %v", err)
	}

	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO agents (id, workspace_id, name, current_config_version) VALUES ($1, $2, 'LLM reference race', 1)
	`, raceAgentID, workspaceID); err != nil {
		t.Fatalf("create race agent shell: %v", err)
	}
	insertTx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin concurrent reference insert: %v", err)
	}
	defer insertTx.Rollback()
	if _, err := insertTx.ExecContext(ctx, `
		INSERT INTO agent_config_versions (agent_id, version, llm_provider, llm_model)
		VALUES ($1, 1, $2, $3)
	`, raceAgentID, providerID, raceModel); err != nil {
		t.Fatalf("insert uncommitted model reference: %v", err)
	}
	deleteConn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve concurrent model deletion connection: %v", err)
	}
	defer deleteConn.Close()
	var deleteBackendPID int
	if err := deleteConn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&deleteBackendPID); err != nil {
		t.Fatalf("read concurrent model deletion backend PID: %v", err)
	}
	deleteDone := make(chan error, 1)
	go func() {
		var deleted bool
		err := deleteConn.QueryRowContext(ctx, `SELECT tma_control_delete_llm_model($1, $2)`, providerID, raceModel).Scan(&deleted)
		deleteDone <- normalizeLLMDeleteReferenceError(err, "llm model "+providerID+"/"+raceModel)
	}()
	lockDeadline := time.Now().Add(5 * time.Second)
	deleteWaitingOnLock := false
	for time.Now().Before(lockDeadline) {
		if err := store.db.QueryRowContext(ctx, `
			SELECT COALESCE(wait_event_type = 'Lock', false)
			FROM pg_stat_activity WHERE pid = $1
		`, deleteBackendPID).Scan(&deleteWaitingOnLock); err != nil {
			t.Fatalf("inspect concurrent model deletion lock: %v", err)
		}
		if deleteWaitingOnLock {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !deleteWaitingOnLock {
		t.Fatal("model deletion did not wait on the uncommitted foreign-key reference")
	}
	if err := insertTx.Commit(); err != nil {
		t.Fatalf("commit concurrent model reference: %v", err)
	}
	select {
	case err := <-deleteDone:
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("expected concurrent model deletion conflict, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("model deletion did not resume after reference transaction committed")
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM agents WHERE id = $1`, raceAgentID); err != nil {
		t.Fatalf("remove concurrent model reference: %v", err)
	}
	if err := store.DeleteLLMModel(providerID, raceModel); err != nil {
		t.Fatalf("delete unreferenced model after concurrent conflict: %v", err)
	}
}

func TestPostgresEmbeddingAndRerankerModelConfiguration(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	ctx := context.Background()
	suffix := strings.ReplaceAll(time.Now().UTC().Format("150405.000000000"), ".", "")
	providerID := "knowledge-model-provider-" + suffix
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(ctx, `DELETE FROM llm_models WHERE provider_id = $1`, providerID)
		_, _ = store.db.ExecContext(ctx, `DELETE FROM llm_providers WHERE id = $1`, providerID)
	})
	if _, err := store.UpsertLLMProvider(UpsertLLMProviderInput{ID: providerID, ProviderType: "openai-compatible", Enabled: true}); err != nil {
		t.Fatalf("create knowledge model provider: %v", err)
	}
	defaultEmbedding := true
	first, err := store.CreateLLMModel(UpsertLLMModelInput{
		ProviderID: providerID, Model: "bge-m3", ContextWindowTokens: 8192,
		CapabilityType: LLMModelCapabilityEmbedding,
		Capabilities: &LLMModelCapabilities{
			Dimensions: 1024, DistanceMetric: LLMEmbeddingDistanceCosine, Normalized: true,
			MaxBatchSize: 32, Protocol: "openai_embeddings",
		},
		IsDefaultEmbedding: &defaultEmbedding,
	})
	if err != nil || !first.IsDefaultEmbedding || first.Capabilities.Dimensions != 1024 {
		t.Fatalf("create first default embedding model: model=%+v err=%v", first, err)
	}
	second, err := store.CreateLLMModel(UpsertLLMModelInput{
		ProviderID: providerID, Model: "embedding-small", ContextWindowTokens: 8192,
		CapabilityType: LLMModelCapabilityEmbedding,
		Capabilities: &LLMModelCapabilities{
			Dimensions: 1536, DistanceMetric: LLMEmbeddingDistanceCosine,
			MaxBatchSize: 64, Protocol: "openai_embeddings",
		},
		IsDefaultEmbedding: &defaultEmbedding,
	})
	if err != nil || !second.IsDefaultEmbedding {
		t.Fatalf("switch default embedding model: model=%+v err=%v", second, err)
	}
	defaultReranker := true
	reranker, err := store.CreateLLMModel(UpsertLLMModelInput{
		ProviderID: providerID, Model: "bge-reranker-v2-m3", ContextWindowTokens: 8192,
		CapabilityType:    LLMModelCapabilityReranker,
		Capabilities:      &LLMModelCapabilities{MaxCandidates: 50, Protocol: "jina_rerank"},
		IsDefaultReranker: &defaultReranker,
	})
	if err != nil || !reranker.IsDefaultReranker || reranker.Capabilities.MaxCandidates != 50 {
		t.Fatalf("create default reranker model: model=%+v err=%v", reranker, err)
	}
	models, err := store.ListLLMModels(providerID)
	if err != nil {
		t.Fatal(err)
	}
	embeddingDefaults, rerankerDefaults := 0, 0
	for _, model := range models {
		if model.IsDefaultEmbedding {
			embeddingDefaults++
		}
		if model.IsDefaultReranker {
			rerankerDefaults++
		}
		if model.Model == first.Model && (model.IsDefaultEmbedding || model.Revision != first.Revision+1) {
			t.Fatalf("unexpected displaced embedding model: before=%+v after=%+v", first, model)
		}
	}
	if embeddingDefaults != 1 || rerankerDefaults != 1 {
		t.Fatalf("unexpected default model counts: embedding=%d reranker=%d models=%+v", embeddingDefaults, rerankerDefaults, models)
	}
	if _, err := store.CreateAgent(CreateAgentInput{
		WorkspaceID: DefaultWorkspaceID, Name: "invalid embedding agent", LLMProvider: providerID, LLMModel: second.Model,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected embedding model to be rejected as an Agent runtime model, got %v", err)
	}
}

func newPostgresIntegrationStore(t *testing.T) *PostgresStore {
	t.Helper()

	if os.Getenv("TMA_RUN_POSTGRES_TESTS") != "1" {
		t.Skip("set TMA_RUN_POSTGRES_TESTS=1 to run Postgres integration tests")
	}
	databaseURL := os.Getenv("TMA_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TMA_DATABASE_URL to run Postgres integration tests")
	}

	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatalf("open postgres store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close postgres store: %v", err)
		}
	})

	requirePostgresIntegrationSchema(t, store)
	if _, err := store.UpsertLLMModel(UpsertLLMModelInput{
		ProviderID: "fake", Model: "test-model", ContextWindowTokens: DefaultContextWindowTokens,
	}); err != nil {
		t.Fatalf("ensure Postgres integration test LLM model: %v", err)
	}
	return store
}

func requirePostgresIntegrationSchema(t *testing.T, store *PostgresStore) {
	t.Helper()

	var table sql.NullString
	if err := store.db.QueryRowContext(context.Background(), `SELECT to_regclass('public.session_turns')`).Scan(&table); err != nil {
		t.Fatalf("check postgres schema: %v", err)
	}
	if !table.Valid || table.String == "" {
		t.Fatal("session_turns table missing; run make migrate-up before integration tests")
	}
}

func createPostgresIntegrationWorkspace(t *testing.T, store *PostgresStore, prefix string) string {
	t.Helper()

	suffix := time.Now().UTC().Format("20060102150405.000000000")
	workspaceID := "wksp_integration_" + strings.ReplaceAll(prefix, "-", "_") + "_" + suffix
	if _, err := store.db.ExecContext(
		context.Background(),
		`INSERT INTO workspaces (id, org_id, name, created_at) VALUES ($1, 'org_default', $2, $3)`,
		workspaceID,
		prefix+" "+suffix,
		time.Now().UTC(),
	); err != nil {
		t.Fatalf("create integration workspace %s: %v", workspaceID, err)
	}
	t.Cleanup(func() {
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM workspaces WHERE id = $1`, workspaceID); err != nil {
			t.Fatalf("cleanup workspace %s: %v", workspaceID, err)
		}
	})
	return workspaceID
}

func createPostgresIntegrationSession(t *testing.T, store *PostgresStore) Session {
	t.Helper()

	suffix := time.Now().UTC().Format("20060102150405.000000000")
	agent, err := store.CreateAgent(CreateAgentInput{
		Name:   "integration-agent-" + suffix,
		Model:  "test-model",
		System: "integration test",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	environment, err := store.CreateEnvironment(CreateEnvironmentInput{
		Name:   "integration-env-" + suffix,
		Config: json.RawMessage(`{"type":"integration"}`),
	})
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}

	session, err := store.CreateSession(CreateSessionInput{
		AgentID:       agent.ID,
		EnvironmentID: environment.ID,
		Title:         "Postgres integration " + suffix,
		CreatedBy:     "integration-test",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	t.Cleanup(func() {
		cleanupPostgresIntegrationData(t, store, session.ID, agent.ID, environment.ID)
	})
	return session
}

func cleanupPostgresIntegrationData(t *testing.T, store *PostgresStore, sessionID string, agentID string, environmentID string) {
	t.Helper()

	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = $1`, sessionID); err != nil {
		t.Fatalf("cleanup session %s: %v", sessionID, err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM environments WHERE id = $1`, environmentID); err != nil {
		t.Fatalf("cleanup environment %s: %v", environmentID, err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM agents WHERE id = $1`, agentID); err != nil {
		t.Fatalf("cleanup agent %s: %v", agentID, err)
	}
}

func assertPostgresSessionStatus(t *testing.T, store *PostgresStore, sessionID string, expected string) {
	t.Helper()

	session, err := store.GetSession(sessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Status != expected {
		t.Fatalf("expected session status %q, got %q", expected, session.Status)
	}
}

func postgresTurnState(t *testing.T, store *PostgresStore, sessionID string, turnID string) (string, string) {
	t.Helper()

	var status string
	var errorMessage sql.NullString
	err := store.db.QueryRowContext(context.Background(), `
		SELECT status, error_message
		FROM session_turns
		WHERE session_id = $1 AND id = $2
	`, sessionID, turnID).Scan(&status, &errorMessage)
	if err != nil {
		t.Fatalf("query turn state: %v", err)
	}
	return status, errorMessage.String
}

func assertNoPostgresAgentMessageForTurn(t *testing.T, store *PostgresStore, sessionID string, turnID string) {
	t.Helper()

	events, err := store.ListEvents(sessionID, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	for _, event := range events {
		if event.Type == EventAgentMessage && payloadString(event.Payload, "turn_id") == turnID {
			t.Fatalf("did not expect agent.message for interrupted turn %s", turnID)
		}
	}
}
