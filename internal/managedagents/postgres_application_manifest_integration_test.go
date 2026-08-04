package managedagents

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"tiggy-manage-agent/internal/appmanifest"
)

func TestPostgresApplicationManifestPublishing(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	workspaceID := DefaultWorkspaceID
	ctx, err := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: workspaceID})
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	identity, err := store.CreateServiceIdentity(ctx, CreateServiceIdentityInput{
		WorkspaceID: workspaceID, Kind: ServiceIdentityKindApplication, Name: "manifest-" + suffix,
		Role: WorkspaceRoleOperator, Scopes: []string{ServiceScopeApplicationsPublish}, CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatal(err)
	}

	manifest := appmanifest.Manifest{
		SchemaVersion: appmanifest.SchemaVersionV1,
		Revision:      "revision-1",
		Environments: []appmanifest.EnvironmentSpec{{
			ExternalRef: "environment/default-" + suffix, Labels: map[string]string{"release": "one"},
			Name: "manifest-environment-" + suffix, Config: json.RawMessage(`{"mode":"one"}`),
		}},
		Skills: []appmanifest.SkillSpec{{
			ExternalRef: "skill/interview-" + suffix, Labels: map[string]string{"release": "one"},
			Identifier: "manifest-skill-" + suffix, Title: "Manifest Skill",
			Version: appmanifest.SkillVersionSpec{ContentFormat: "markdown", ContentText: "first"},
		}},
		MCPServers: []appmanifest.MCPServerSpec{{
			ExternalRef: "mcp/repository-" + suffix, Labels: map[string]string{"release": "one"},
			Identifier: "manifest_mcp_" + suffix, Name: "Manifest MCP",
			Config: json.RawMessage(`{"transport":"stdio","command":"true"}`),
		}},
		Agents: []appmanifest.AgentSpec{{
			ExternalRef: "agent/main-" + suffix, Labels: map[string]string{"release": "one"},
			EnvironmentRef: "environment/default-" + suffix, Name: "Manifest Agent " + suffix,
			LLMProvider: "fake", LLMModel: "fake-demo", System: "first", Tools: json.RawMessage(`{}`),
		}},
	}
	publish := func() appmanifest.PublishResult {
		result, publishErr := store.PublishApplicationManifest(ctx, appmanifest.PublishInput{
			WorkspaceID: workspaceID, AppID: identity.ID, PublishedBy: "integration-test", Manifest: manifest,
		})
		if publishErr != nil {
			t.Fatal(publishErr)
		}
		return result
	}
	first := publish()
	if len(first.Resources) != 4 {
		t.Fatalf("first publish resources = %+v", first.Resources)
	}
	for _, resource := range first.Resources {
		if resource.Status != appmanifest.StatusCreated {
			t.Fatalf("first publish status for %s = %s", resource.Type, resource.Status)
		}
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		for index := len(first.Resources) - 1; index >= 0; index-- {
			resource := first.Resources[index]
			table := map[string]string{
				appmanifest.ResourceAgent: "agents", appmanifest.ResourceEnvironment: "environments",
				appmanifest.ResourceSkill: "skills", appmanifest.ResourceMCPServer: "mcp_registry_servers",
			}[resource.Type]
			_, _ = store.db.ExecContext(cleanupCtx, `DELETE FROM `+table+` WHERE id = $1`, resource.ResourceID)
		}
		_, _ = store.db.ExecContext(cleanupCtx, `DELETE FROM service_identities WHERE id = $1`, identity.ID)
	})

	second := publish()
	if second.Checksum != first.Checksum {
		t.Fatalf("idempotent publish checksum changed: %s != %s", second.Checksum, first.Checksum)
	}
	for _, resource := range second.Resources {
		if resource.Status != appmanifest.StatusUnchanged {
			t.Fatalf("second publish status for %s = %s", resource.Type, resource.Status)
		}
	}

	manifest.Revision = "revision-2"
	manifest.Environments[0].Config = json.RawMessage(`{"mode":"two"}`)
	manifest.Skills[0].Version.ContentText = "second"
	manifest.MCPServers[0].Description = "updated"
	manifest.MCPServers[0].Config = json.RawMessage(`{"transport":"stdio","command":"false"}`)
	manifest.Agents[0].System = "second"
	for _, labels := range []map[string]string{
		manifest.Environments[0].Labels, manifest.Skills[0].Labels, manifest.MCPServers[0].Labels, manifest.Agents[0].Labels,
	} {
		labels["release"] = "two"
	}
	third := publish()
	for _, resource := range third.Resources {
		if resource.Status != appmanifest.StatusUpdated {
			t.Fatalf("updated publish status for %s = %s", resource.Type, resource.Status)
		}
		if resource.Type != appmanifest.ResourceEnvironment && resource.Version != 2 {
			t.Fatalf("updated publish version for %s = %d, want 2", resource.Type, resource.Version)
		}
	}
	fourth := publish()
	for _, resource := range fourth.Resources {
		if resource.Status != appmanifest.StatusUnchanged {
			t.Fatalf("fourth publish status for %s = %s", resource.Type, resource.Status)
		}
	}
}
