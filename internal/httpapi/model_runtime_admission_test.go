package httpapi

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
)

type quotaPolicyAdmissionTestStore struct {
	*testStore
	policies map[string]managedagents.ModelRuntimeQuotaPolicy
}

type sharedQuotaAdmissionTestStore struct {
	*quotaPolicyAdmissionTestStore
	mu                sync.Mutex
	workspaceRequests int
	identityRequests  int
}

func (s *sharedQuotaAdmissionTestStore) ReserveModelInvocationQuotaContext(_ context.Context, input managedagents.ReserveModelInvocationQuotaInput) (managedagents.ModelInvocationQuotaReservation, error) {
	if _, err := managedagents.NormalizeReserveModelInvocationQuotaInput(input); err != nil {
		return managedagents.ModelInvocationQuotaReservation{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if input.WorkspaceLimit > 0 && s.workspaceRequests >= input.WorkspaceLimit {
		return managedagents.ModelInvocationQuotaReservation{Allowed: false, ExceededScope: "workspace", Limit: input.WorkspaceLimit, Current: s.workspaceRequests}, nil
	}
	if input.IdentityLimit > 0 && s.identityRequests >= input.IdentityLimit {
		return managedagents.ModelInvocationQuotaReservation{Allowed: false, ExceededScope: "identity", Limit: input.IdentityLimit, Current: s.identityRequests}, nil
	}
	s.workspaceRequests++
	s.identityRequests++
	return managedagents.ModelInvocationQuotaReservation{Allowed: true}, nil
}

func (s *quotaPolicyAdmissionTestStore) GetModelRuntimeQuotaPolicyContext(_ context.Context, _, scope, appID string) (managedagents.ModelRuntimeQuotaPolicy, error) {
	policy, ok := s.policies[scope+"/"+appID]
	if !ok {
		return managedagents.ModelRuntimeQuotaPolicy{}, managedagents.ErrNotFound
	}
	return policy, nil
}

func (s *quotaPolicyAdmissionTestStore) ListModelRuntimeQuotaPoliciesContext(context.Context, string, bool) ([]managedagents.ModelRuntimeQuotaPolicy, error) {
	return nil, nil
}

func (s *quotaPolicyAdmissionTestStore) UpsertModelRuntimeQuotaPolicyContext(context.Context, managedagents.UpsertModelRuntimeQuotaPolicyInput) (managedagents.ModelRuntimeQuotaPolicy, error) {
	return managedagents.ModelRuntimeQuotaPolicy{}, nil
}

func (s *quotaPolicyAdmissionTestStore) ArchiveModelRuntimeQuotaPolicyContext(context.Context, managedagents.ArchiveModelRuntimeQuotaPolicyInput) (managedagents.ModelRuntimeQuotaPolicy, error) {
	return managedagents.ModelRuntimeQuotaPolicy{}, nil
}

func (s *quotaPolicyAdmissionTestStore) GetModelRuntimeQuotaUsageContext(context.Context, string, string, time.Time, time.Time) (managedagents.ModelRuntimeQuotaUsage, error) {
	return managedagents.ModelRuntimeQuotaUsage{}, nil
}

func quotaTestInt(value int) *int { return &value }

func TestModelRuntimeAdmissionResolvesWorkspaceAndApplicationPolicies(t *testing.T) {
	store := &quotaPolicyAdmissionTestStore{testStore: newTestStore(), policies: map[string]managedagents.ModelRuntimeQuotaPolicy{
		"workspace/": {
			Status: managedagents.ModelRuntimeQuotaPolicyStatusActive,
			Config: managedagents.ModelRuntimeQuotaPolicyConfig{
				ModelWorkspaceRequestsPerMinute: quotaTestInt(7), ModelIdentityRequestsPerMinute: quotaTestInt(5),
			},
		},
		"application/app-one": {
			Status: managedagents.ModelRuntimeQuotaPolicyStatusActive,
			Config: managedagents.ModelRuntimeQuotaPolicyConfig{
				ModelWorkspaceRequestsPerMinute: quotaTestInt(99), ModelIdentityRequestsPerMinute: quotaTestInt(2),
			},
		},
	}}
	server := &Server{store: store, modelRuntimeAdmission: newModelRuntimeAdmission(ModelRuntimePolicy{
		ModelWorkspaceRequestsPerMinute: 600, ModelIdentityRequestsPerMinute: 120,
	})}
	workspaceLimit, identityLimit, err := server.effectiveModelRuntimeQuotaLimits(t.Context(), modelRuntimeAdmissionRequest{
		Family: modelRuntimeFamilyModel, WorkspaceID: "wksp", ServiceIdentityID: "app-one",
	})
	if err != nil || workspaceLimit != 7 || identityLimit != 2 {
		t.Fatalf("application effective limits = %d/%d, err=%v", workspaceLimit, identityLimit, err)
	}
	workspaceLimit, identityLimit, err = server.effectiveModelRuntimeQuotaLimits(t.Context(), modelRuntimeAdmissionRequest{
		Family: modelRuntimeFamilyModel, WorkspaceID: "wksp", ServiceIdentityID: "app-two",
	})
	if err != nil || workspaceLimit != 7 || identityLimit != 5 {
		t.Fatalf("workspace defaults for second app = %d/%d, err=%v", workspaceLimit, identityLimit, err)
	}
}

func TestModelRuntimeAdmissionEnforcesConcurrencyAndReleasesExactlyOnce(t *testing.T) {
	admission := newModelRuntimeAdmission(ModelRuntimePolicy{ModelGlobalConcurrency: 2})
	request := modelRuntimeAdmissionRequest{
		Family: modelRuntimeFamilyModel, WorkspaceID: "wksp", PrincipalID: "user", Capability: "generate", ProviderID: "fake", Model: "fake-demo",
	}
	var admitted atomic.Int64
	var rejected atomic.Int64
	start := make(chan struct{})
	releases := make(chan func(), 8)
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			release, err := admission.acquireCapacity(request)
			if err != nil {
				var limitError *modelRuntimeAdmissionError
				if !errors.As(err, &limitError) || limitError.scope != "global" {
					t.Errorf("unexpected admission error: %v", err)
				}
				rejected.Add(1)
				return
			}
			admitted.Add(1)
			releases <- release
		}()
	}
	close(start)
	group.Wait()
	if admitted.Load() != 2 || rejected.Load() != 6 {
		t.Fatalf("unexpected admission counts: admitted=%d rejected=%d", admitted.Load(), rejected.Load())
	}
	close(releases)
	for release := range releases {
		release()
		release()
	}
	if _, err := admission.acquireCapacity(request); err != nil {
		t.Fatalf("capacity was not released: %v", err)
	}
}

func TestModelRuntimeAdmissionLocalQuotaSeparatesWorkspaceIdentityAndRoute(t *testing.T) {
	admission := newModelRuntimeAdmission(ModelRuntimePolicy{
		ModelWorkspaceRequestsPerMinute: 3,
		ModelIdentityRequestsPerMinute:  1,
	})
	now := time.Date(2026, 7, 31, 12, 0, 30, 0, time.UTC)
	request := modelRuntimeAdmissionRequest{
		Family: modelRuntimeFamilyModel, WorkspaceID: "wksp", PrincipalID: "user-1", Capability: "generate", ProviderID: "fake", Model: "fake-demo",
	}
	if err := admission.reserveLocalQuota(request, now); err != nil {
		t.Fatal(err)
	}
	if err := admission.reserveLocalQuota(request, now); err == nil {
		t.Fatal("expected identity quota rejection")
	} else {
		var limitError *modelRuntimeAdmissionError
		if !errors.As(err, &limitError) || limitError.scope != "identity" || limitError.retryAfterSeconds() != 30 {
			t.Fatalf("unexpected identity quota error: %#v", err)
		}
	}
	request.PrincipalID = "user-2"
	if err := admission.reserveLocalQuota(request, now); err != nil {
		t.Fatalf("second identity should have an independent quota: %v", err)
	}
	request.Model = "other-model"
	if err := admission.reserveLocalQuota(request, now); err != nil {
		t.Fatalf("another route should have an independent quota: %v", err)
	}
	request.Model = "fake-demo"
	if err := admission.reserveLocalQuota(request, now.Add(time.Minute)); err != nil {
		t.Fatalf("new minute should reset the quota: %v", err)
	}
}

func TestMultimodalAdmissionSharesAtomicQuotaAcrossServerReplicas(t *testing.T) {
	store := &sharedQuotaAdmissionTestStore{quotaPolicyAdmissionTestStore: &quotaPolicyAdmissionTestStore{
		testStore: newTestStore(), policies: map[string]managedagents.ModelRuntimeQuotaPolicy{},
	}}
	policy := DefaultModelRuntimePolicy()
	policy.SpeechGlobalConcurrency = 100
	policy.SpeechWorkspaceConcurrency = 100
	policy.SpeechIdentityConcurrency = 100
	policy.SpeechRouteConcurrency = 100
	policy.SpeechWorkspaceSessionsPerMinute = 3
	policy.SpeechIdentitySessionsPerMinute = 3
	servers := []*Server{
		{store: store, modelRuntimeAdmission: newModelRuntimeAdmission(policy)},
		{store: store, modelRuntimeAdmission: newModelRuntimeAdmission(policy)},
	}
	request := modelRuntimeAdmissionRequest{
		Family: modelRuntimeFamilyMultimodal, WorkspaceID: "wksp", PrincipalID: "service_identity:app",
		ServiceIdentityID: "app", Capability: managedagents.ModelInvocationCapabilityMultimodalRealtime,
		ProviderID: "openai", Model: "realtime",
	}
	start := make(chan struct{})
	var allowed atomic.Int64
	var rejected atomic.Int64
	var group sync.WaitGroup
	for index := range 12 {
		group.Add(1)
		go func(server *Server) {
			defer group.Done()
			<-start
			release, err := server.admitModelRuntime(t.Context(), request)
			if err == nil {
				allowed.Add(1)
				release()
				return
			}
			var admissionError *modelRuntimeAdmissionError
			if !errors.As(err, &admissionError) || admissionError.kind != "quota" {
				t.Errorf("unexpected replica admission error: %v", err)
				return
			}
			rejected.Add(1)
		}(servers[index%len(servers)])
	}
	close(start)
	group.Wait()
	if allowed.Load() != 3 || rejected.Load() != 9 || store.workspaceRequests != 3 || store.identityRequests != 3 {
		t.Fatalf("unexpected shared quota counts allowed=%d rejected=%d workspace=%d identity=%d",
			allowed.Load(), rejected.Load(), store.workspaceRequests, store.identityRequests)
	}
}
