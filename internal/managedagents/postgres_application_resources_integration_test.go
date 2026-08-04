package managedagents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"tiggy-manage-agent/internal/mcpregistry"
	"tiggy-manage-agent/internal/skills"
)

func TestPostgresApplicationResourceOwnership(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	workspaceID := DefaultWorkspaceID
	ctx, err := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: workspaceID})
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	identity, err := store.CreateServiceIdentity(ctx, CreateServiceIdentityInput{
		WorkspaceID: workspaceID, Kind: ServiceIdentityKindApplication, Name: "application-resources-" + suffix,
		Role: WorkspaceRoleOperator, Scopes: []string{ServiceScopeAgentsWrite}, CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatalf("create application identity: %v", err)
	}

	environment, err := store.CreateEnvironmentContext(ctx, CreateEnvironmentInput{
		WorkspaceID: workspaceID, AppID: identity.ID, ExternalRef: "environment/default-" + suffix,
		Labels: map[string]string{"release": "alpha"}, Name: "application-environment-" + suffix,
		Config: json.RawMessage(`{"type":"integration"}`),
	})
	if err != nil {
		t.Fatalf("create application Environment: %v", err)
	}
	agent, err := store.CreateAgentContext(ctx, CreateAgentInput{
		WorkspaceID: workspaceID, AppID: identity.ID, ExternalRef: "agent/interviewer-" + suffix,
		Labels: map[string]string{"release": "alpha"}, EnvironmentID: environment.ID,
		Name: "application-agent-" + suffix, Model: "test-model", System: "integration test",
	})
	if err != nil {
		t.Fatalf("create application Agent: %v", err)
	}
	session, err := store.CreateSessionContext(ctx, CreateSessionInput{
		WorkspaceID: workspaceID, AppID: identity.ID, ExternalRef: "session/case-" + suffix,
		Labels: map[string]string{"release": "alpha"}, AgentID: agent.ID, EnvironmentID: environment.ID,
		CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatalf("create application Session: %v", err)
	}
	skill, err := store.CreateSkill(ctx, skills.CreateSkillInput{
		WorkspaceID: workspaceID, AppID: identity.ID, ExternalRef: "skill/interview-" + suffix,
		Labels: map[string]string{"release": "alpha"}, Identifier: "application-skill-" + suffix,
		Title: "Application Skill", OwnerType: skills.OwnerTypeWorkspace, Visibility: skills.VisibilityWorkspace,
		SourceType: skills.SourceTypeInline, CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatalf("create application Skill: %v", err)
	}
	mcpServer, err := store.CreateMCPRegistryServer(ctx, mcpregistry.CreateInput{
		WorkspaceID: workspaceID, AppID: identity.ID, ExternalRef: "mcp/repository-" + suffix,
		Labels: map[string]string{"release": "alpha"}, Identifier: "application_mcp_" + suffix,
		Name: "Application MCP", Config: json.RawMessage(`{"identifier":"application_mcp","transport":"stdio","command":"true"}`),
		CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatalf("create application MCP Server: %v", err)
	}

	t.Cleanup(func() {
		cleanupContext := context.Background()
		_, _ = store.db.ExecContext(cleanupContext, `DELETE FROM sessions WHERE id = $1`, session.ID)
		_, _ = store.db.ExecContext(cleanupContext, `DELETE FROM agents WHERE id = $1`, agent.ID)
		_, _ = store.db.ExecContext(cleanupContext, `DELETE FROM environments WHERE id = $1`, environment.ID)
		_, _ = store.db.ExecContext(cleanupContext, `DELETE FROM skills WHERE id = $1`, skill.ID)
		_, _ = store.db.ExecContext(cleanupContext, `DELETE FROM mcp_registry_servers WHERE id = $1`, mcpServer.ID)
		_, _ = store.db.ExecContext(cleanupContext, `DELETE FROM service_identities WHERE id = $1`, identity.ID)
	})

	for resourceType, ownership := range map[string]struct {
		appID       string
		externalRef string
		labels      map[string]string
	}{
		"Environment": {environment.AppID, environment.ExternalRef, environment.Labels},
		"Agent":       {agent.AppID, agent.ExternalRef, agent.Labels},
		"Session":     {session.AppID, session.ExternalRef, session.Labels},
		"Skill":       {skill.AppID, skill.ExternalRef, skill.Labels},
		"MCP":         {mcpServer.AppID, mcpServer.ExternalRef, mcpServer.Labels},
	} {
		if ownership.appID != identity.ID || ownership.externalRef == "" || ownership.labels["release"] != "alpha" {
			t.Fatalf("%s ownership was not preserved: %+v", resourceType, ownership)
		}
	}

	sessions, err := store.ListSessionsContext(ctx, ListSessionsInput{AppID: identity.ID, ExternalRef: session.ExternalRef})
	if err != nil || len(sessions) != 1 || sessions[0].ID != session.ID {
		t.Fatalf("filter Sessions by application reference: sessions=%+v err=%v", sessions, err)
	}
	skillsByApp, err := store.ListSkills(ctx, skills.ListSkillsInput{WorkspaceID: workspaceID, AppID: identity.ID, ExternalRef: skill.ExternalRef})
	if err != nil || len(skillsByApp) != 1 || skillsByApp[0].ID != skill.ID {
		t.Fatalf("filter Skills by application reference: skills=%+v err=%v", skillsByApp, err)
	}
	_, err = store.CreateSessionContext(ctx, CreateSessionInput{
		WorkspaceID: workspaceID, AppID: identity.ID, ExternalRef: session.ExternalRef,
		AgentID: agent.ID, EnvironmentID: environment.ID, CreatedBy: "integration-test",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate application external_ref error = %v, want conflict", err)
	}
}
