package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	modelruntime "tiggy-manage-agent/internal/modelruntimeprovider"
)

type multimodalInvocationRecorder struct {
	server     *Server
	request    *http.Request
	invocation managedagents.RecordModelInvocationInput
	once       sync.Once
}

func (s *Server) newMultimodalInvocationRecorder(r *http.Request, route resolvedMultimodalRealtimeRoute, startedAt time.Time) *multimodalInvocationRecorder {
	return &multimodalInvocationRecorder{
		server: s, request: r,
		invocation: s.newModelInvocationInput(r, route.Provider, route.Model, managedagents.ModelInvocationCapabilityMultimodalRealtime, startedAt),
	}
}

func (recorder *multimodalInvocationRecorder) Input() managedagents.RecordModelInvocationInput {
	if recorder == nil {
		return managedagents.RecordModelInvocationInput{}
	}
	return recorder.invocation
}

func (recorder *multimodalInvocationRecorder) Admit(ctx context.Context) (func(), error) {
	if recorder == nil || recorder.server == nil {
		return nil, errors.New("multimodal invocation recorder is unavailable")
	}
	return recorder.server.admitModelRuntime(ctx, modelRuntimeAdmissionRequestFromInvocation(modelRuntimeFamilyMultimodal, recorder.invocation))
}

func (recorder *multimodalInvocationRecorder) RejectAdmission(err error) {
	if recorder == nil {
		return
	}
	recorder.finish(modelruntime.MultimodalMetrics{}, managedagents.ModelInvocationStatusFailed,
		modelRuntimeAdmissionErrorCode(modelRuntimeFamilyMultimodal, err))
}

func (recorder *multimodalInvocationRecorder) Finish(metrics modelruntime.MultimodalMetrics, proxyErr error) {
	if recorder == nil || recorder.server == nil || recorder.request == nil {
		return
	}
	status, errorCode := multimodalInvocationOutcome(metrics, proxyErr)
	recorder.finish(metrics, status, errorCode)
}

func (recorder *multimodalInvocationRecorder) finish(metrics modelruntime.MultimodalMetrics, status, errorCode string) {
	if recorder == nil || recorder.server == nil || recorder.request == nil {
		return
	}
	recorder.once.Do(func() {
		invocation := recorder.invocation
		invocation.InputItems = metrics.InputItems
		invocation.OutputItems = metrics.OutputItems
		invocation.InputBytes = metrics.InputBytes
		invocation.OutputBytes = metrics.OutputBytes
		invocation.InputCharacters = metrics.InputCharacters
		invocation.OutputCharacters = metrics.OutputCharacters
		invocation.InputAudioMillis = metrics.InputAudioMillis
		invocation.OutputAudioMillis = metrics.OutputAudioMillis
		invocation.InputVideoFrames = metrics.InputVideoFrames
		invocation.OutputVideoFrames = metrics.OutputVideoFrames
		invocation.InputVideoDropped = metrics.InputVideoDropped
		invocation.OutputVideoDropped = metrics.OutputVideoDropped
		invocation.InputVideoMillis = metrics.InputVideoMillis
		invocation.OutputVideoMillis = metrics.OutputVideoMillis

		completeModelInvocation(&invocation, status, errorCode)
		recorder.server.recordModelInvocation(recorder.request, invocation)
	})
}

func multimodalInvocationOutcome(metrics modelruntime.MultimodalMetrics, proxyErr error) (string, string) {
	if code := stableMultimodalInvocationErrorCode(metrics.ErrorCode); code != "" {
		return managedagents.ModelInvocationStatusFailed, code
	}
	if errors.Is(proxyErr, context.Canceled) || metrics.Canceled {
		return managedagents.ModelInvocationStatusCanceled, ""
	}
	if proxyErr != nil {
		return managedagents.ModelInvocationStatusFailed, "multimodal_transport_error"
	}
	if metrics.Completed {
		return managedagents.ModelInvocationStatusCompleted, ""
	}
	return managedagents.ModelInvocationStatusFailed, "multimodal_session_incomplete"
}

func stableMultimodalInvocationErrorCode(code string) string {
	switch strings.TrimSpace(code) {
	case "":
		return ""
	case "invalid_multimodal_route", "multimodal_protocol_negotiation_failed", "multimodal_provider_capability_violation",
		"multimodal_provider_connect_failed", "multimodal_provider_handshake_failed", "multimodal_runtime_connect_failed",
		"multimodal_provider_disconnected", "backpressure_timeout",
		"invalid_session_start", "flow_control_violation", "media_sequence_violation", "invalid_control_event",
		"invalid_media_frame", "invalid_media_track", "invalid_provider_event", "unresolved_object_ref", "object_ref_rejected",
		"multimodal_provider_unconfigured", "multimodal_provider_unavailable", "multimodal_session_duration_exceeded",
		"unsupported_control_event", "unsupported_provider_event":
		return strings.TrimSpace(code)
	case "multimodal_runtime_error":
		return "multimodal_runtime_error"
	default:
		return "multimodal_provider_error"
	}
}
