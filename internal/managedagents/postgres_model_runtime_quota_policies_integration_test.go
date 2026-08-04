package managedagents

import (
	"errors"
	"testing"
	"time"
)

func TestPostgresModelRuntimeQuotaPolicyLifecycleAndUsage(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	workspaceID := createPostgresIntegrationWorkspace(t, store, "runtime-quota-policy")
	ctx, err := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: workspaceID})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := store.CreateServiceIdentity(ctx, CreateServiceIdentityInput{
		WorkspaceID: workspaceID, Kind: ServiceIdentityKindApplication, Name: "quota-app",
		Role: WorkspaceRoleMember, Scopes: []string{ServiceScopeModelGenerate}, CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatal(err)
	}

	workspacePolicy, err := store.UpsertModelRuntimeQuotaPolicyContext(ctx, UpsertModelRuntimeQuotaPolicyInput{
		WorkspaceID: workspaceID, Scope: ModelRuntimeQuotaScopeWorkspace, Plan: "team", UpdatedBy: "admin",
		Config: ModelRuntimeQuotaPolicyConfig{
			ModelWorkspaceRequestsPerMinute: intPointer(500), ModelIdentityRequestsPerMinute: intPointer(100),
			MonthlyModelRequestBudget: int64Pointer(1000), AlertThresholdPercent: intPointer(80),
		},
	})
	if err != nil || workspacePolicy.Revision != 1 {
		t.Fatalf("create workspace policy: policy=%+v err=%v", workspacePolicy, err)
	}
	applicationPolicy, err := store.UpsertModelRuntimeQuotaPolicyContext(ctx, UpsertModelRuntimeQuotaPolicyInput{
		WorkspaceID: workspaceID, Scope: ModelRuntimeQuotaScopeApplication, AppID: identity.ID,
		Plan: "application-pro", UpdatedBy: "admin",
		Config: ModelRuntimeQuotaPolicyConfig{
			ModelIdentityRequestsPerMinute: intPointer(20), MonthlyModelRequestBudget: int64Pointer(100),
		},
	})
	if err != nil || applicationPolicy.Revision != 1 {
		t.Fatalf("create application policy: policy=%+v err=%v", applicationPolicy, err)
	}
	if _, err := store.UpsertModelRuntimeQuotaPolicyContext(ctx, UpsertModelRuntimeQuotaPolicyInput{
		WorkspaceID: workspaceID, Scope: ModelRuntimeQuotaScopeApplication, AppID: identity.ID,
		Plan: "stale", UpdatedBy: "admin", ExpectedRevision: 99,
		Config: ModelRuntimeQuotaPolicyConfig{ModelIdentityRequestsPerMinute: intPointer(10)},
	}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("expected stale revision conflict, got %v", err)
	}

	effective := EffectiveModelRuntimeQuota(ModelRuntimeQuotaLimits{}, workspaceID, identity.ID, &workspacePolicy, &applicationPolicy)
	if effective.Limits.ModelWorkspaceRequestsPerMinute != 500 || effective.Limits.ModelIdentityRequestsPerMinute != 20 || effective.MonthlyModelRequestBudget != 100 {
		t.Fatalf("unexpected effective policy: %+v", effective)
	}

	now := time.Now().UTC()
	_, err = store.RecordModelInvocationContext(ctx, RecordModelInvocationInput{
		WorkspaceID: workspaceID, PrincipalID: "service_identity:" + identity.ID, ServiceIdentityID: identity.ID,
		AuthType: "service_credential", RequestID: "quota-usage-completed", Capability: ModelInvocationCapabilityGenerate,
		ProviderID: "fake", ProviderType: "fake", Model: "test-model", Status: ModelInvocationStatusCompleted,
		StartedAt: now, CompletedAt: now.Add(time.Millisecond),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.RecordModelInvocationContext(ctx, RecordModelInvocationInput{
		WorkspaceID: workspaceID, PrincipalID: "service_identity:" + identity.ID, ServiceIdentityID: identity.ID,
		AuthType: "service_credential", RequestID: "quota-usage-rejected", Capability: ModelInvocationCapabilityGenerate,
		ProviderID: "fake", ProviderType: "fake", Model: "test-model", Status: ModelInvocationStatusFailed,
		ErrorCode: "model_quota_exceeded", StartedAt: now, CompletedAt: now.Add(time.Millisecond),
	})
	if err != nil {
		t.Fatal(err)
	}
	usage, err := store.GetModelRuntimeQuotaUsageContext(ctx, workspaceID, identity.ID, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil || usage.ModelRequests != 1 || usage.SpeechSessions != 0 {
		t.Fatalf("unexpected quota usage: usage=%+v err=%v", usage, err)
	}

	archived, err := store.ArchiveModelRuntimeQuotaPolicyContext(ctx, ArchiveModelRuntimeQuotaPolicyInput{
		WorkspaceID: workspaceID, Scope: ModelRuntimeQuotaScopeApplication, AppID: identity.ID,
		ExpectedRevision: applicationPolicy.Revision, ArchivedBy: "admin",
	})
	if err != nil || archived.Status != ModelRuntimeQuotaPolicyStatusArchived || archived.Revision != 2 {
		t.Fatalf("archive application policy: policy=%+v err=%v", archived, err)
	}
	items, err := store.ListModelRuntimeQuotaPoliciesContext(ctx, workspaceID, false)
	if err != nil || len(items) != 1 || items[0].ID != workspacePolicy.ID {
		t.Fatalf("active policy list: items=%+v err=%v", items, err)
	}
	items, err = store.ListModelRuntimeQuotaPoliciesContext(ctx, workspaceID, true)
	if err != nil || len(items) != 2 {
		t.Fatalf("archived policy list: items=%+v err=%v", items, err)
	}
}

func TestPostgresModelRuntimeQuotaPolicyWorkspaceScopeIsolation(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	alpha := createPostgresIntegrationWorkspace(t, store, "runtime-quota-alpha")
	beta := createPostgresIntegrationWorkspace(t, store, "runtime-quota-beta")
	alphaCtx, _ := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: alpha})
	policy, err := store.UpsertModelRuntimeQuotaPolicyContext(alphaCtx, UpsertModelRuntimeQuotaPolicyInput{
		WorkspaceID: alpha, Scope: ModelRuntimeQuotaScopeWorkspace, Plan: "alpha", UpdatedBy: "admin",
		Config: ModelRuntimeQuotaPolicyConfig{ModelWorkspaceRequestsPerMinute: intPointer(10)},
	})
	if err != nil {
		t.Fatal(err)
	}
	betaCtx, _ := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: beta})
	if _, err := store.GetModelRuntimeQuotaPolicyContext(betaCtx, alpha, ModelRuntimeQuotaScopeWorkspace, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-workspace context should be forbidden, policy=%s err=%v", policy.ID, err)
	}
}
