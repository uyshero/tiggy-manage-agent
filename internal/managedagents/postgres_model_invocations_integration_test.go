package managedagents

import (
	"strings"
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
	realtime, err := store.RecordModelInvocationContext(ctx, RecordModelInvocationInput{
		WorkspaceID: workspaceID, PrincipalID: "service_identity:svc_biography", ServiceIdentityID: "svc_biography", AuthType: "service_credential", RequestID: "req_realtime",
		Capability: ModelInvocationCapabilityMultimodalRealtime, ProviderID: "realtime", ProviderType: "tma", Model: "native",
		Status: ModelInvocationStatusCompleted, InputItems: 8, OutputItems: 5, InputBytes: 4096, OutputBytes: 2048,
		InputAudioMillis: 800, OutputAudioMillis: 400, InputVideoFrames: 4, OutputVideoFrames: 3,
		InputVideoDropped: 2, OutputVideoDropped: 1, InputVideoMillis: 120, OutputVideoMillis: 80,
		StartedAt: startedAt.Add(2 * time.Second), CompletedAt: startedAt.Add(2100 * time.Millisecond), LatencyMillis: 100,
	})
	if err != nil || realtime.Capability != ModelInvocationCapabilityMultimodalRealtime || realtime.InputVideoFrames != 4 || realtime.OutputVideoDropped != 1 {
		t.Fatalf("record realtime model invocation: record=%+v err=%v", realtime, err)
	}
	report, err := store.ListModelInvocationsContext(ctx, ListModelInvocationsInput{
		WorkspaceID: workspaceID, ServiceIdentityID: "svc_knowledge", Status: ModelInvocationStatusCompleted, Limit: 10,
	})
	if err != nil || len(report.Records) != 1 || report.Records[0].ID != completed.ID ||
		report.Summary.RecordCount != 1 || report.Summary.CompletedCount != 1 || report.Summary.InputTokens != 12 || report.Records[0].ServiceIdentityID != "svc_knowledge" {
		t.Fatalf("filter model invocations: report=%+v err=%v", report, err)
	}
	realtimeReport, err := store.ListModelInvocationsContext(ctx, ListModelInvocationsInput{
		WorkspaceID: workspaceID, Capability: ModelInvocationCapabilityMultimodalRealtime, Limit: 10,
	})
	if err != nil || len(realtimeReport.Records) != 1 || realtimeReport.Records[0].ID != realtime.ID ||
		realtimeReport.Summary.InputVideoFrames != 4 || realtimeReport.Summary.OutputVideoFrames != 3 ||
		realtimeReport.Summary.InputVideoDropped != 2 || realtimeReport.Summary.OutputVideoMillis != 80 {
		t.Fatalf("filter realtime model invocations: report=%+v err=%v", realtimeReport, err)
	}
}

func TestPostgresModelInvocationQuotaIsAtomicAcrossConcurrentRequests(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	tests := []struct {
		name       string
		capability string
	}{
		{name: "generate", capability: ModelInvocationCapabilityGenerate},
		{name: "multimodal realtime", capability: ModelInvocationCapabilityMultimodalRealtime},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workspaceID := createPostgresIntegrationWorkspace(t, store, "model-invocation-quota-"+strings.ReplaceAll(test.name, " ", "-"))
			ctx, err := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: workspaceID})
			if err != nil {
				t.Fatal(err)
			}
			window := time.Now().UTC().Truncate(time.Minute)
			input := ReserveModelInvocationQuotaInput{
				WorkspaceID: workspaceID, PrincipalID: "service_identity:svc_knowledge", ServiceIdentityID: "svc_knowledge",
				Capability: test.capability, ProviderID: "fake", Model: "fake-demo",
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
		})
	}
}
