package identity

import (
	"strings"
	"testing"
)

func TestDefaultOIDCClaimMappingResolvesDirectClaims(t *testing.T) {
	mapping, err := ParseOIDCClaimMapping("")
	if err != nil {
		t.Fatalf("parse default mapping: %v", err)
	}
	resolved, err := mapping.Resolve(map[string]any{
		"sub": "user-1", "organization_id": "org-1", "workspace_id": "wksp-1",
		"roles": []any{"member", "unknown"},
	})
	if err != nil {
		t.Fatalf("resolve direct claims: %v", err)
	}
	if resolved.Subject != "user-1" || resolved.OwnerID != "user-1" || resolved.WorkspaceID != "wksp-1" || len(resolved.Roles) != 1 || resolved.Roles[0] != RoleMember {
		t.Fatalf("unexpected direct identity: %+v", resolved)
	}
	for _, source := range []string{"roles_claim:roles", "subject_claim:sub", "workspace_claim:workspace_id"} {
		if !containsString(resolved.AuthorizationSources, source) {
			t.Fatalf("expected authorization source %q, got %+v", source, resolved.AuthorizationSources)
		}
	}
}

func TestOIDCClaimMappingResolvesNestedRolesAndGroupGrant(t *testing.T) {
	mapping, err := ParseOIDCClaimMapping(`{
		"subject_claim":"oid",
		"organization_claim":"tid",
		"workspace_claim":"",
		"owner_claim":"oid",
		"roles_claim":"realm_access.roles",
		"groups_claim":"groups",
		"role_mappings":{"tma-user":"member"},
		"group_mappings":{"finance-admin":{"organization_id":"org-finance","workspace_id":"wksp-finance","roles":["operator"]}},
		"allowed_workspace_ids":["wksp-finance"],
		"require_group_mapping":true
	}`)
	if err != nil {
		t.Fatalf("parse nested mapping: %v", err)
	}
	resolved, err := mapping.Resolve(map[string]any{
		"oid": "entra-user", "tid": "org-finance", "groups": []any{"unrelated", "finance-admin"},
		"realm_access": map[string]any{"roles": []any{"tma-user", "ignored"}},
	})
	if err != nil {
		t.Fatalf("resolve nested claims: %v", err)
	}
	if resolved.Subject != "entra-user" || resolved.WorkspaceID != "wksp-finance" || resolved.OrganizationID != "org-finance" {
		t.Fatalf("unexpected mapped tenant: %+v", resolved)
	}
	if len(resolved.Roles) != 2 || resolved.Roles[0] != RoleOperator || resolved.Roles[1] != RoleMember {
		t.Fatalf("unexpected mapped roles: %+v", resolved.Roles)
	}
	for _, source := range []string{
		"group_mapping:finance-admin", "role_mapping:tma-user", "roles_claim:realm_access.roles", "workspace_allowlist",
	} {
		if !containsString(resolved.AuthorizationSources, source) {
			t.Fatalf("expected authorization source %q, got %+v", source, resolved.AuthorizationSources)
		}
	}
}

func TestOIDCClaimMappingRejectsTenantConflictsAndUnmappedIdentity(t *testing.T) {
	mapping, err := ParseOIDCClaimMapping(`{
		"workspace_claim":"workspace_id",
		"group_mappings":{
			"alpha":{"workspace_id":"wksp-alpha","roles":["member"]},
			"beta":{"workspace_id":"wksp-beta","roles":["member"]}
		},
		"require_group_mapping":true
	}`)
	if err != nil {
		t.Fatalf("parse conflict mapping: %v", err)
	}
	for name, claims := range map[string]map[string]any{
		"direct conflict": {"sub": "user", "workspace_id": "wksp-other", "groups": []any{"alpha"}, "roles": []any{"member"}},
		"group conflict":  {"sub": "user", "groups": []any{"alpha", "beta"}, "roles": []any{"member"}},
		"unmapped group":  {"sub": "user", "workspace_id": "wksp-alpha", "groups": []any{"unknown"}, "roles": []any{"member"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := mapping.Resolve(claims); err == nil {
				t.Fatal("expected identity resolution to fail")
			}
		})
	}
}

func TestOIDCClaimMappingRejectsUnallowedWorkspaceAndMissingRole(t *testing.T) {
	mapping, err := ParseOIDCClaimMapping(`{"allowed_workspace_ids":["wksp-allowed"]}`)
	if err != nil {
		t.Fatalf("parse allowlist mapping: %v", err)
	}
	if _, err := mapping.Resolve(map[string]any{"sub": "user", "workspace_id": "wksp-other", "roles": []any{"member"}}); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected workspace allowlist rejection, got %v", err)
	}
	if _, err := mapping.Resolve(map[string]any{"sub": "user", "workspace_id": "wksp-allowed", "roles": []any{"external-only"}}); err == nil || !strings.Contains(err.Error(), "supported role") {
		t.Fatalf("expected missing role rejection, got %v", err)
	}
}

func TestParseOIDCClaimMappingRejectsUnknownFieldsAndDetectsTenantRestriction(t *testing.T) {
	if _, err := ParseOIDCClaimMapping(`{"unknown":true}`); err == nil {
		t.Fatal("expected unknown mapping field to fail")
	}
	direct, err := ParseOIDCClaimMapping("")
	if err != nil {
		t.Fatalf("parse direct mapping: %v", err)
	}
	if direct.HasTenantRestriction() {
		t.Fatal("default direct mapping should not count as a server-side tenant restriction")
	}
	restricted, err := ParseOIDCClaimMapping(`{"allowed_workspace_ids":["wksp-1"]}`)
	if err != nil {
		t.Fatalf("parse restricted mapping: %v", err)
	}
	if !restricted.HasTenantRestriction() {
		t.Fatal("workspace allowlist should count as a tenant restriction")
	}
}
