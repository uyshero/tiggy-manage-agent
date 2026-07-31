package managedagents

import (
	"errors"
	"testing"
)

func TestPostgresTenantAdministrationLifecycle(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	ctx := t.Context()

	allowed, err := store.IsPlatformAdmin(ctx, "local-development")
	if err != nil || !allowed {
		t.Fatalf("bootstrap platform administrator check: allowed=%t err=%v", allowed, err)
	}

	assignment, err := store.UpsertPlatformAdmin(ctx, "local-development", PlatformRoleAssignment{
		Subject: "platform-test", DisplayName: "Platform Test", Email: "platform@example.test",
	})
	if err != nil {
		t.Fatalf("upsert platform administrator: %v", err)
	}
	if assignment.Role != PlatformRoleAdmin {
		t.Fatalf("unexpected platform role: %+v", assignment)
	}
	allowed, err = store.IsPlatformAdmin(ctx, assignment.Subject)
	if err != nil || !allowed {
		t.Fatalf("persisted platform administrator check: allowed=%t err=%v", allowed, err)
	}

	workspace, err := store.CreateTenantWorkspace(ctx, assignment.Subject, "Console Integration")
	if err != nil {
		t.Fatalf("create tenant workspace: %v", err)
	}
	if workspace.ID == "" || workspace.Name != "Console Integration" {
		t.Fatalf("unexpected workspace: %+v", workspace)
	}

	firstAdmin, err := store.UpsertWorkspaceMembership(ctx, UpsertWorkspaceMembershipInput{
		WorkspaceID: workspace.ID, Subject: "workspace-admin-1", Role: WorkspaceRoleAdmin, Status: "active",
	})
	if err != nil {
		t.Fatalf("create first workspace administrator: %v", err)
	}
	if _, err := store.UpsertWorkspaceMembership(ctx, UpsertWorkspaceMembershipInput{
		WorkspaceID: workspace.ID, Subject: firstAdmin.Subject, Role: WorkspaceRoleMember, Status: "active",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("downgrade last active administrator error = %v, want conflict", err)
	}

	if _, err := store.UpsertWorkspaceMembership(ctx, UpsertWorkspaceMembershipInput{
		WorkspaceID: workspace.ID, Subject: "workspace-admin-2", Role: WorkspaceRoleAdmin, Status: "active",
	}); err != nil {
		t.Fatalf("create second workspace administrator: %v", err)
	}
	loadedAdmin, err := store.GetWorkspaceMembership(ctx, workspace.ID, "workspace-admin-2")
	if err != nil || loadedAdmin.Role != WorkspaceRoleAdmin || loadedAdmin.Status != "active" {
		t.Fatalf("get workspace membership: membership=%+v err=%v", loadedAdmin, err)
	}
	if _, err := store.UpsertWorkspaceMembership(ctx, UpsertWorkspaceMembershipInput{
		WorkspaceID: workspace.ID, Subject: firstAdmin.Subject, Role: WorkspaceRoleMember, Status: "active",
	}); err != nil {
		t.Fatalf("downgrade administrator with replacement: %v", err)
	}
	members, err := store.ListWorkspaceMemberships(ctx, workspace.ID)
	if err != nil {
		t.Fatalf("list workspace members: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("member count = %d, want 2", len(members))
	}

	workspaces, err := store.ListTenantWorkspaces(ctx, assignment.Subject)
	if err != nil {
		t.Fatalf("list tenant workspaces: %v", err)
	}
	found := false
	for _, item := range workspaces {
		if item.ID == workspace.ID {
			found = item.MemberCount == 2
		}
	}
	if !found {
		t.Fatalf("created workspace missing or member count incorrect: %+v", workspaces)
	}

	if err := store.DeletePlatformAdmin(ctx, "local-development", assignment.Subject); err != nil {
		t.Fatalf("delete platform administrator: %v", err)
	}
	allowed, err = store.IsPlatformAdmin(ctx, assignment.Subject)
	if err != nil || allowed {
		t.Fatalf("deleted platform administrator check: allowed=%t err=%v", allowed, err)
	}
}
