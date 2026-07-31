package httpapi

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

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
