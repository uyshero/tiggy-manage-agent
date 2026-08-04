package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	modelruntime "tiggy-manage-agent/internal/modelruntimeprovider"
)

func TestMultimodalInvocationRecorderPersistsAggregateMetricsOnce(t *testing.T) {
	store := newTestStore()
	server := &Server{store: store}
	request := multimodalInvocationTestRequest()
	recorder := server.newMultimodalInvocationRecorder(request, multimodalInvocationTestRoute(), time.Now().UTC().Add(-time.Second))

	recorder.Finish(modelruntime.MultimodalMetrics{
		InputItems: 8, OutputItems: 5, InputBytes: 4096, OutputBytes: 2048,
		InputCharacters: 12, OutputCharacters: 24, InputAudioMillis: 800, OutputAudioMillis: 400,
		InputVideoFrames: 4, OutputVideoFrames: 3, InputVideoDropped: 2, OutputVideoDropped: 1,
		InputVideoMillis: 120, OutputVideoMillis: 80, Completed: true,
	}, nil)
	recorder.Finish(modelruntime.MultimodalMetrics{ErrorCode: "should_not_record_twice"}, errors.New("late close"))

	if len(store.modelInvocations) != 1 {
		t.Fatalf("expected exactly one invocation, got %+v", store.modelInvocations)
	}
	invocation := store.modelInvocations[0]
	if invocation.Capability != managedagents.ModelInvocationCapabilityMultimodalRealtime || invocation.Status != managedagents.ModelInvocationStatusCompleted ||
		invocation.WorkspaceID != "workspace-1" || invocation.PrincipalID != "user-1" || invocation.ServiceIdentityID != "app-1" ||
		invocation.InputItems != 8 || invocation.OutputItems != 5 || invocation.InputBytes != 4096 || invocation.OutputBytes != 2048 ||
		invocation.InputAudioMillis != 800 || invocation.OutputAudioMillis != 400 || invocation.InputVideoFrames != 4 ||
		invocation.OutputVideoFrames != 3 || invocation.InputVideoDropped != 2 || invocation.OutputVideoDropped != 1 ||
		invocation.InputVideoMillis != 120 || invocation.OutputVideoMillis != 80 {
		t.Fatalf("unexpected multimodal invocation: %+v", invocation)
	}
}

func TestMultimodalInvocationRecorderNormalizesTerminalOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		metrics    modelruntime.MultimodalMetrics
		proxyErr   error
		wantStatus string
		wantCode   string
	}{
		{name: "canceled", metrics: modelruntime.MultimodalMetrics{Canceled: true}, wantStatus: managedagents.ModelInvocationStatusCanceled},
		{name: "context canceled", proxyErr: context.Canceled, wantStatus: managedagents.ModelInvocationStatusCanceled},
		{name: "known protocol error", metrics: modelruntime.MultimodalMetrics{ErrorCode: "flow_control_violation"}, wantStatus: managedagents.ModelInvocationStatusFailed, wantCode: "flow_control_violation"},
		{name: "provider backpressure", metrics: modelruntime.MultimodalMetrics{ErrorCode: "backpressure_timeout"}, wantStatus: managedagents.ModelInvocationStatusFailed, wantCode: "backpressure_timeout"},
		{name: "provider disconnected", metrics: modelruntime.MultimodalMetrics{ErrorCode: "multimodal_provider_disconnected"}, wantStatus: managedagents.ModelInvocationStatusFailed, wantCode: "multimodal_provider_disconnected"},
		{name: "unknown provider error", metrics: modelruntime.MultimodalMetrics{ErrorCode: "provider-secret-class"}, wantStatus: managedagents.ModelInvocationStatusFailed, wantCode: "multimodal_provider_error"},
		{name: "transport error", proxyErr: errors.New("socket closed"), wantStatus: managedagents.ModelInvocationStatusFailed, wantCode: "multimodal_transport_error"},
		{name: "incomplete", wantStatus: managedagents.ModelInvocationStatusFailed, wantCode: "multimodal_session_incomplete"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, code := multimodalInvocationOutcome(test.metrics, test.proxyErr)
			if status != test.wantStatus || code != test.wantCode {
				t.Fatalf("unexpected outcome status=%q code=%q", status, code)
			}
		})
	}
}

func TestMultimodalInvocationUsesSessionAdmissionAndStableErrorCode(t *testing.T) {
	policy := DefaultModelRuntimePolicy()
	policy.SpeechGlobalConcurrency = 1
	server := &Server{store: newTestStore(), modelRuntimeAdmission: newModelRuntimeAdmission(policy)}
	first := server.newMultimodalInvocationRecorder(multimodalInvocationTestRequest(), multimodalInvocationTestRoute(), time.Now().UTC())
	second := server.newMultimodalInvocationRecorder(multimodalInvocationTestRequest(), multimodalInvocationTestRoute(), time.Now().UTC())

	release, err := first.Admit(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err = second.Admit(t.Context()); err == nil {
		t.Fatal("expected multimodal session capacity rejection")
	}
	if code := modelRuntimeAdmissionErrorCode(modelRuntimeFamilyMultimodal, err); code != "multimodal_capacity_exceeded" {
		t.Fatalf("unexpected multimodal admission code %q", code)
	}
	second.RejectAdmission(err)
	store := server.store.(*testStore)
	if len(store.modelInvocations) != 1 || store.modelInvocations[0].ErrorCode != "multimodal_capacity_exceeded" {
		t.Fatalf("multimodal admission rejection was not audited: %+v", store.modelInvocations)
	}
}

func multimodalInvocationTestRequest() *http.Request {
	request := httptest.NewRequest("GET", "/v2/model-runtime/realtime", nil)
	request.Header.Set(requestIDHeader, "req-realtime-1")
	return request.WithContext(context.WithValue(request.Context(), principalContextKey{}, Principal{
		Subject: "user-1", WorkspaceID: "workspace-1", OwnerID: "user-1", ServiceIdentityID: "app-1", AuthType: AuthTypeDelegated,
	}))
}

func multimodalInvocationTestRoute() resolvedMultimodalRealtimeRoute {
	return resolvedMultimodalRealtimeRoute{
		Provider: managedagents.LLMProvider{ID: "realtime", ProviderType: "tma"},
		Model:    managedagents.LLMModel{ProviderID: "realtime", Model: "native", CapabilityType: managedagents.LLMModelCapabilityMultimodalRealtime},
	}
}
