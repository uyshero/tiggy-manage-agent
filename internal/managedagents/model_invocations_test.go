package managedagents

import (
	"errors"
	"testing"
	"time"
)

func TestNormalizeModelInvocationAcceptsMultimodalRealtimeMetrics(t *testing.T) {
	now := time.Now().UTC()
	input := RecordModelInvocationInput{
		WorkspaceID: "workspace-1", PrincipalID: "app-1", RequestID: "request-1",
		Capability: ModelInvocationCapabilityMultimodalRealtime, ProviderID: "realtime", Model: "native",
		Status: ModelInvocationStatusCompleted, InputVideoFrames: 4, OutputVideoFrames: 3,
		InputVideoDropped: 2, OutputVideoDropped: 1, InputVideoMillis: 120, OutputVideoMillis: 80,
		StartedAt: now, CompletedAt: now.Add(time.Second),
	}
	if _, err := NormalizeRecordModelInvocationInput(input); err != nil {
		t.Fatal(err)
	}
	input.InputVideoDropped = -1
	if _, err := NormalizeRecordModelInvocationInput(input); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected negative realtime metric rejection, got %v", err)
	}
}

func TestNormalizeQuotaAcceptsMultimodalRealtimeCapability(t *testing.T) {
	_, err := NormalizeReserveModelInvocationQuotaInput(ReserveModelInvocationQuotaInput{
		WorkspaceID: "workspace-1", PrincipalID: "app-1", Capability: ModelInvocationCapabilityMultimodalRealtime,
		ProviderID: "realtime", Model: "native", WindowStartedAt: time.Now(), WorkspaceLimit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
}
