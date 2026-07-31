package managedagents

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPostgresModelInvocationsRecordAndFilterWithoutSession(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	workspaceID := createPostgresIntegrationWorkspace(t, store, "model-invocations")
	ctx, err := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: workspaceID})
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC().Add(-time.Second)
	completed, err := store.RecordModelInvocationContext(ctx, RecordModelInvocationInput{
		WorkspaceID: workspaceID, PrincipalID: "service_identity:svc_knowledge", ServiceIdentityID: "svc_knowledge", AuthType: "service_credential", RequestID: "req_completed",
		Capability: ModelInvocationCapabilityEmbedding, ProviderID: "fake", ProviderType: "fake", Model: "embedding-test",
		Status: ModelInvocationStatusCompleted, InputTokens: 12, TotalTokens: 12, InputItems: 2, OutputItems: 2,
		StartedAt: startedAt, CompletedAt: startedAt.Add(25 * time.Millisecond), LatencyMillis: 25,
	})
	if err != nil || completed.ID == "" || completed.WorkspaceID != workspaceID || completed.InputTokens != 12 {
		t.Fatalf("record completed model invocation: record=%+v err=%v", completed, err)
	}
	failed, err := store.RecordModelInvocationContext(ctx, RecordModelInvocationInput{
		WorkspaceID: workspaceID, PrincipalID: "service_identity:svc_knowledge", ServiceIdentityID: "svc_knowledge", AuthType: "service_credential", RequestID: "req_failed",
		Capability: ModelInvocationCapabilityRerank, ProviderID: "fake", ProviderType: "fake", Model: "rerank-test",
		Status: ModelInvocationStatusFailed, ErrorCode: "provider_rate_limit",
		StartedAt: startedAt.Add(time.Second), CompletedAt: startedAt.Add(1100 * time.Millisecond), LatencyMillis: 100,
	})
	if err != nil || failed.Status != ModelInvocationStatusFailed || failed.ErrorCode != "provider_rate_limit" {
		t.Fatalf("record failed model invocation: record=%+v err=%v", failed, err)
	}
	report, err := store.ListModelInvocationsContext(ctx, ListModelInvocationsInput{
		WorkspaceID: workspaceID, ServiceIdentityID: "svc_knowledge", Status: ModelInvocationStatusCompleted, Limit: 10,
	})
	if err != nil || len(report.Records) != 1 || report.Records[0].ID != completed.ID ||
		report.Summary.RecordCount != 1 || report.Summary.CompletedCount != 1 || report.Summary.InputTokens != 12 || report.Records[0].ServiceIdentityID != "svc_knowledge" {
		t.Fatalf("filter model invocations: report=%+v err=%v", report, err)
	}
}

func TestPostgresModelInvocationQuotaIsAtomicAcrossConcurrentRequests(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	workspaceID := createPostgresIntegrationWorkspace(t, store, "model-invocation-quota")
	ctx, err := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: workspaceID})
	if err != nil {
		t.Fatal(err)
	}
	window := time.Now().UTC().Truncate(time.Minute)
	input := ReserveModelInvocationQuotaInput{
		WorkspaceID: workspaceID, PrincipalID: "service_identity:svc_knowledge", ServiceIdentityID: "svc_knowledge",
		Capability: ModelInvocationCapabilityGenerate, ProviderID: "fake", Model: "fake-demo",
		WindowStartedAt: window, WorkspaceLimit: 3, IdentityLimit: 3,
	}
	var allowed atomic.Int64
	var group sync.WaitGroup
	for range 12 {
		group.Add(1)
		go func() {
			defer group.Done()
			reservation, reserveErr := store.ReserveModelInvocationQuotaContext(ctx, input)
			if reserveErr != nil {
				t.Errorf("reserve quota: %v", reserveErr)
				return
			}
			if reservation.Allowed {
				allowed.Add(1)
			}
		}()
	}
	group.Wait()
	if allowed.Load() != 3 {
		t.Fatalf("expected exactly 3 reservations, got %d", allowed.Load())
	}

	input.WindowStartedAt = window.Add(time.Minute)
	reservation, err := store.ReserveModelInvocationQuotaContext(ctx, input)
	if err != nil || !reservation.Allowed {
		t.Fatalf("new quota window should reset the bucket: reservation=%+v err=%v", reservation, err)
	}
}
