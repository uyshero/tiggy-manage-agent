package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/observability"
	"tiggy-manage-agent/internal/runner"
)

type tenantAdministrationTestStore struct {
	*testStore
	platformAdmins  map[string]bool
	memberships     map[string]managedagents.WorkspaceMembership
	membershipError error
	membershipReads int
	membershipScope managedagents.AccessScope
	listedWorkspace string
	listedScope     managedagents.AccessScope
}

func workspaceMembershipTestKey(workspaceID string, subject string) string {
	return workspaceID + "\x00" + subject
}

func (s *tenantAdministrationTestStore) GetWorkspaceMembership(ctx context.Context, workspaceID string, subject string) (managedagents.WorkspaceMembership, error) {
	s.membershipReads++
	s.membershipScope, _ = managedagents.DatabaseAccessScopeFromContext(ctx)
	if s.membershipError != nil {
		return managedagents.WorkspaceMembership{}, s.membershipError
	}
	membership, ok := s.memberships[workspaceMembershipTestKey(workspaceID, subject)]
	if !ok {
		return managedagents.WorkspaceMembership{}, managedagents.ErrNotFound
	}
	return membership, nil
}

func (s *tenantAdministrationTestStore) ListWorkspaceMemberships(ctx context.Context, workspaceID string) ([]managedagents.WorkspaceMembership, error) {
	s.listedWorkspace = workspaceID
	s.listedScope, _ = managedagents.DatabaseAccessScopeFromContext(ctx)
	return []managedagents.WorkspaceMembership{{WorkspaceID: workspaceID, Subject: "member-1", Role: managedagents.WorkspaceRoleMember, Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now()}}, nil
}

func (s *tenantAdministrationTestStore) UpsertWorkspaceMembership(_ context.Context, input managedagents.UpsertWorkspaceMembershipInput) (managedagents.WorkspaceMembership, error) {
	return managedagents.WorkspaceMembership{WorkspaceID: input.WorkspaceID, Subject: input.Subject, Role: input.Role, Status: input.Status}, nil
}

func (s *tenantAdministrationTestStore) DeleteWorkspaceMembership(_ context.Context, _, _ string) error {
	return nil
}

func (s *tenantAdministrationTestStore) IsPlatformAdmin(_ context.Context, subject string) (bool, error) {
	return s.platformAdmins[subject], nil
}

func (s *tenantAdministrationTestStore) ListPlatformAdmins(_ context.Context, _ string) ([]managedagents.PlatformRoleAssignment, error) {
	return []managedagents.PlatformRoleAssignment{}, nil
}

func (s *tenantAdministrationTestStore) UpsertPlatformAdmin(_ context.Context, _ string, input managedagents.PlatformRoleAssignment) (managedagents.PlatformRoleAssignment, error) {
	return input, nil
}

func (s *tenantAdministrationTestStore) DeletePlatformAdmin(_ context.Context, _, _ string) error {
	return nil
}

func (s *tenantAdministrationTestStore) ListTenantWorkspaces(_ context.Context, _ string) ([]managedagents.TenantWorkspace, error) {
	return []managedagents.TenantWorkspace{}, nil
}

func (s *tenantAdministrationTestStore) CreateTenantWorkspace(_ context.Context, _ string, name string) (managedagents.TenantWorkspace, error) {
	return managedagents.TenantWorkspace{ID: "wksp_created", Name: name}, nil
}

func newTenantAdministrationTestServer(t *testing.T, platformAdmins map[string]bool) (http.Handler, *tenantAdministrationTestStore) {
	return newTenantAdministrationTestServerWithSink(t, platformAdmins, nil)
}

func newTenantAdministrationTestServerWithSink(t *testing.T, platformAdmins map[string]bool, sink observability.AuthorizationDecisionSink) (http.Handler, *tenantAdministrationTestStore) {
	t.Helper()
	store := &tenantAdministrationTestStore{testStore: newTestStore(), platformAdmins: platformAdmins}
	auth := AuthConfig{
		Mode: AuthModeJWT, JWTSecret: testJWTSecret, JWTIssuer: "https://issuer.example", JWTAudience: "tma-api",
		AuthorizationSink: sink,
	}
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStoreExecutionResolverUnifiedAuthSubagentPolicyAndBinaryScanner(
		store, runner.NewMockRunner(store, time.Millisecond, nil), nil, "fake", "fake-demo",
		objectstore.NewNoopClient(objectstore.Config{}), defaultExecutionResolver(store), "worker-secret", "legacy-control-secret", auth, defaultSubagentPolicy(), nil,
	)
	return server, store
}

func TestWorkspaceMembersUsePrincipalWorkspace(t *testing.T) {
	server, store := newTenantAdministrationTestServer(t, nil)
	token := signedTestJWT(t, "admin-1", "wksp_alpha", "admin-1", []string{RoleAdmin}, nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, authenticatedRequest(t, http.MethodGet, "/v2/workspace/members", token))
	if response.Code != http.StatusOK {
		t.Fatalf("list members returned %d: %s", response.Code, response.Body.String())
	}
	if store.listedWorkspace != "wksp_alpha" {
		t.Fatalf("members were listed from %q instead of principal workspace", store.listedWorkspace)
	}
}

func TestWorkspaceMembersRequireWorkspaceAdmin(t *testing.T) {
	server, _ := newTenantAdministrationTestServer(t, nil)
	token := signedTestJWT(t, "member-1", "wksp_alpha", "member-1", []string{RoleMember}, nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, authenticatedRequest(t, http.MethodGet, "/v2/workspace/members", token))
	if response.Code != http.StatusForbidden {
		t.Fatalf("member list returned %d, want 403: %s", response.Code, response.Body.String())
	}
}

func TestManagedWorkspaceMembershipOverridesTokenRole(t *testing.T) {
	tests := []struct {
		name       string
		tokenRoles []string
		membership managedagents.WorkspaceMembership
		wantAdmin  bool
		wantStatus int
	}{
		{name: "managed downgrade", tokenRoles: []string{RoleAdmin}, membership: managedagents.WorkspaceMembership{Role: managedagents.WorkspaceRoleMember, Status: "active"}, wantStatus: http.StatusOK},
		{name: "managed suspension", tokenRoles: []string{RoleAdmin}, membership: managedagents.WorkspaceMembership{Role: managedagents.WorkspaceRoleAdmin, Status: "suspended"}, wantStatus: http.StatusForbidden},
		{name: "managed promotion", tokenRoles: []string{RoleMember}, membership: managedagents.WorkspaceMembership{Role: managedagents.WorkspaceRoleAdmin, Status: "active"}, wantAdmin: true, wantStatus: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, store := newTenantAdministrationTestServer(t, nil)
			test.membership.WorkspaceID = "wksp_alpha"
			test.membership.Subject = "managed-user"
			store.memberships = map[string]managedagents.WorkspaceMembership{
				workspaceMembershipTestKey("wksp_alpha", "managed-user"): test.membership,
			}
			token := signedTestJWT(t, "managed-user", "wksp_alpha", "managed-user", test.tokenRoles, nil)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, authenticatedRequest(t, http.MethodGet, "/v2/administration/context", token))
			if response.Code != test.wantStatus {
				t.Fatalf("context returned %d, want %d: %s", response.Code, test.wantStatus, response.Body.String())
			}
			if test.wantStatus != http.StatusOK {
				return
			}
			var payload administrationContextResponse
			decodeTestResponse(t, response, &payload)
			if payload.WorkspaceAdmin != test.wantAdmin {
				t.Fatalf("workspace_admin = %t, want %t", payload.WorkspaceAdmin, test.wantAdmin)
			}
		})
	}
}

func TestAdministrationContextRetainsConsoleCompatibilityAlias(t *testing.T) {
	server, _ := newTenantAdministrationTestServer(t, map[string]bool{"admin-1": true})
	token := signedTestJWT(t, "admin-1", "wksp_alpha", "admin-1", []string{RoleAdmin}, nil)
	responses := make([]administrationContextResponse, 0, 2)
	for _, path := range []string{"/v2/administration/context", "/v2/console/context"} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, authenticatedRequest(t, http.MethodGet, path, token))
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s returned %d: %s", path, response.Code, response.Body.String())
		}
		var payload administrationContextResponse
		decodeTestResponse(t, response, &payload)
		responses = append(responses, payload)
	}
	if !reflect.DeepEqual(responses[0], responses[1]) {
		t.Fatalf("administration context alias diverged: primary=%+v alias=%+v", responses[0], responses[1])
	}
}

func TestPlatformAdministrationUsesSeparatePlatformRole(t *testing.T) {
	server, _ := newTenantAdministrationTestServer(t, map[string]bool{"platform-1": true})
	adminToken := signedTestJWT(t, "workspace-admin", "wksp_alpha", "workspace-admin", []string{RoleAdmin}, nil)
	denied := httptest.NewRecorder()
	server.ServeHTTP(denied, authenticatedRequest(t, http.MethodGet, "/v2/platform/workspaces", adminToken))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("workspace admin platform list returned %d, want 403: %s", denied.Code, denied.Body.String())
	}

	platformToken := signedTestJWT(t, "platform-1", "wksp_alpha", "platform-1", []string{RoleViewer}, nil)
	allowed := httptest.NewRecorder()
	server.ServeHTTP(allowed, authenticatedRequest(t, http.MethodGet, "/v2/platform/workspaces", platformToken))
	if allowed.Code != http.StatusOK {
		t.Fatalf("platform admin list returned %d: %s", allowed.Code, allowed.Body.String())
	}

	contextResponse := httptest.NewRecorder()
	server.ServeHTTP(contextResponse, authenticatedRequest(t, http.MethodGet, "/v2/administration/context", platformToken))
	var contextPayload administrationContextResponse
	decodeTestResponse(t, contextResponse, &contextPayload)
	if contextPayload.WorkspaceAdmin || !contextPayload.PlatformAdmin {
		t.Fatalf("platform and workspace roles were not separated: %+v", contextPayload)
	}
}

func TestPlatformAdminViewerCanPerformPlatformWrites(t *testing.T) {
	server, _ := newTenantAdministrationTestServer(t, map[string]bool{"platform-1": true})
	platformViewerToken := signedTestJWT(t, "platform-1", "wksp_alpha", "platform-1", []string{RoleViewer}, nil)

	createWorkspace := httptest.NewRecorder()
	server.ServeHTTP(createWorkspace, authenticatedJSONRequest(t, http.MethodPost, "/v2/platform/workspaces", `{"name":"New Workspace"}`, platformViewerToken))
	if createWorkspace.Code != http.StatusCreated {
		t.Fatalf("platform viewer workspace create returned %d: %s", createWorkspace.Code, createWorkspace.Body.String())
	}

	upsertMember := httptest.NewRecorder()
	server.ServeHTTP(upsertMember, authenticatedJSONRequest(t, http.MethodPut, "/v2/platform/workspaces/wksp_target/members/admin-2", `{"role":"admin","status":"active"}`, platformViewerToken))
	if upsertMember.Code != http.StatusOK {
		t.Fatalf("platform viewer member upsert returned %d: %s", upsertMember.Code, upsertMember.Body.String())
	}

	upsertPlatformAdmin := httptest.NewRecorder()
	server.ServeHTTP(upsertPlatformAdmin, authenticatedJSONRequest(t, http.MethodPut, "/v2/platform/admins/platform-2", `{}`, platformViewerToken))
	if upsertPlatformAdmin.Code != http.StatusOK {
		t.Fatalf("platform viewer administrator upsert returned %d: %s", upsertPlatformAdmin.Code, upsertPlatformAdmin.Body.String())
	}
}

func TestPlatformWriteStillRequiresPlatformRole(t *testing.T) {
	server, _ := newTenantAdministrationTestServer(t, nil)
	workspaceAdminToken := signedTestJWT(t, "workspace-admin", "wksp_alpha", "workspace-admin", []string{RoleAdmin}, nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, authenticatedJSONRequest(t, http.MethodPost, "/v2/platform/workspaces", `{"name":"Blocked"}`, workspaceAdminToken))
	if response.Code != http.StatusForbidden {
		t.Fatalf("workspace administrator platform write returned %d, want 403: %s", response.Code, response.Body.String())
	}
}

func TestSuspendedWorkspaceMemberRetainsOnlyPlatformAdministrationAccess(t *testing.T) {
	sink := &recordingAuthorizationDecisionSink{}
	server, store := newTenantAdministrationTestServerWithSink(t, map[string]bool{"platform-1": true}, sink)
	store.memberships = map[string]managedagents.WorkspaceMembership{
		workspaceMembershipTestKey("wksp_alpha", "platform-1"): {
			WorkspaceID: "wksp_alpha", Subject: "platform-1", Role: managedagents.WorkspaceRoleAdmin, Status: "suspended",
		},
	}
	token := signedTestJWT(t, "platform-1", "wksp_alpha", "platform-1", []string{RoleAdmin}, nil)

	contextResponse := httptest.NewRecorder()
	server.ServeHTTP(contextResponse, authenticatedRequest(t, http.MethodGet, "/v2/administration/context", token))
	if contextResponse.Code != http.StatusOK {
		t.Fatalf("suspended platform administrator context returned %d: %s", contextResponse.Code, contextResponse.Body.String())
	}
	var contextPayload administrationContextResponse
	decodeTestResponse(t, contextResponse, &contextPayload)
	if contextPayload.WorkspaceAdmin || !contextPayload.PlatformAdmin || len(contextPayload.Principal.Roles) != 1 || contextPayload.Principal.Roles[0] != RoleViewer {
		t.Fatalf("suspended platform administrator received unexpected effective permissions: %+v", contextPayload)
	}
	contextEvents := sink.snapshot()
	if len(contextEvents) == 0 || !containsTestString(contextEvents[len(contextEvents)-1].AuthorizationSources, "platform_role_assignment") ||
		len(contextEvents[len(contextEvents)-1].Roles) != 1 || contextEvents[len(contextEvents)-1].Roles[0] != RoleViewer {
		t.Fatalf("platform role exception was not audited with least privilege: %+v", contextEvents)
	}

	platformWrite := httptest.NewRecorder()
	server.ServeHTTP(platformWrite, authenticatedJSONRequest(t, http.MethodPost, "/v2/platform/workspaces", `{"name":"Recovery Workspace"}`, token))
	if platformWrite.Code != http.StatusCreated {
		t.Fatalf("suspended platform administrator platform write returned %d: %s", platformWrite.Code, platformWrite.Body.String())
	}

	workspaceRead := httptest.NewRecorder()
	server.ServeHTTP(workspaceRead, authenticatedRequest(t, http.MethodGet, "/v1/agents", token))
	if workspaceRead.Code != http.StatusForbidden {
		t.Fatalf("suspended platform administrator workspace read returned %d, want 403: %s", workspaceRead.Code, workspaceRead.Body.String())
	}
}

func TestSuspendedWorkspaceMemberWithoutPlatformRoleCannotUsePlatformAPI(t *testing.T) {
	server, store := newTenantAdministrationTestServer(t, nil)
	store.memberships = map[string]managedagents.WorkspaceMembership{
		workspaceMembershipTestKey("wksp_alpha", "suspended-admin"): {
			WorkspaceID: "wksp_alpha", Subject: "suspended-admin", Role: managedagents.WorkspaceRoleAdmin, Status: "suspended",
		},
	}
	token := signedTestJWT(t, "suspended-admin", "wksp_alpha", "suspended-admin", []string{RoleAdmin}, nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, authenticatedRequest(t, http.MethodGet, "/v2/platform/workspaces", token))
	if response.Code != http.StatusForbidden {
		t.Fatalf("suspended non-platform administrator returned %d, want 403: %s", response.Code, response.Body.String())
	}
}

func TestPlatformAdminCanBindMemberOperationsToTargetWorkspace(t *testing.T) {
	server, store := newTenantAdministrationTestServer(t, map[string]bool{"platform-1": true})
	token := signedTestJWT(t, "platform-1", "wksp_home", "platform-1", []string{RoleViewer}, nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, authenticatedRequest(t, http.MethodGet, "/v2/platform/workspaces/wksp_target/members", token))
	if response.Code != http.StatusOK {
		t.Fatalf("target workspace members returned %d: %s", response.Code, response.Body.String())
	}
	if store.listedWorkspace != "wksp_target" {
		t.Fatalf("members were listed from %q instead of target workspace", store.listedWorkspace)
	}
	if store.listedScope.WorkspaceID != "wksp_target" {
		t.Fatalf("database scope was %q instead of target workspace", store.listedScope.WorkspaceID)
	}
}
