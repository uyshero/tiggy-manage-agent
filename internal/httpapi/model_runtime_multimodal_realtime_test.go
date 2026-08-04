package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"tiggy-manage-agent/internal/managedagents"
	modelruntime "tiggy-manage-agent/internal/modelruntimeprovider"
	"tiggy-manage-agent/internal/objectstore"
)

type testMultimodalRuntimeFunc func(context.Context, context.Context, *websocket.Conn, modelruntime.MultimodalRequest) (modelruntime.MultimodalMetrics, error)

func (f testMultimodalRuntimeFunc) ProxyMultimodal(ctx context.Context, clientContext context.Context, client *websocket.Conn, request modelruntime.MultimodalRequest) (modelruntime.MultimodalMetrics, error) {
	return f(ctx, clientContext, client, request)
}

func TestMultimodalRealtimeServiceIdentityScope(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/v2/model-runtime/multimodal/realtime", nil)
	scope, mapped := serviceIdentityScopeForRequest(request)
	if !mapped || scope != managedagents.ServiceScopeModelRealtime {
		t.Fatalf("unexpected multimodal service scope mapped=%v scope=%q", mapped, scope)
	}
}

func TestServeMultimodalRealtimeRunsGovernedLifecycle(t *testing.T) {
	store := newTestStore()
	store.providers["realtime"] = managedagents.LLMProvider{
		ID: "realtime", ProviderType: "tma", BaseURL: "wss://provider.example/realtime",
		APIKeyEnv: "TEST_MULTIMODAL_KEY", Enabled: true,
	}
	store.models[llmModelKey("realtime", "native")] = testMultimodalCatalogModel()
	t.Setenv("TEST_MULTIMODAL_KEY", "provider-secret")

	runtimeCalled := make(chan modelruntime.MultimodalRequest, 1)
	runtime := testMultimodalRuntimeFunc(func(_ context.Context, clientContext context.Context, client *websocket.Conn, request modelruntime.MultimodalRequest) (modelruntime.MultimodalMetrics, error) {
		runtimeCalled <- request
		started, _ := json.Marshal(modelruntime.MultimodalSessionStarted{
			Type: "session.started", ProtocolVersion: modelruntime.MultimodalRealtimeProtocolVersion,
			SessionID: request.Start.SessionID,
		})
		if err := client.Write(clientContext, websocket.MessageText, started); err != nil {
			return modelruntime.MultimodalMetrics{}, err
		}
		completed, _ := json.Marshal(modelruntime.MultimodalEvent{Type: "session.completed", SessionID: request.Start.SessionID})
		if err := client.Write(clientContext, websocket.MessageText, completed); err != nil {
			return modelruntime.MultimodalMetrics{}, err
		}
		return modelruntime.MultimodalMetrics{InputItems: 2, InputBytes: 128, Completed: true}, nil
	})
	server := &Server{
		store: store, modelRuntimeAdmission: newModelRuntimeAdmission(DefaultModelRuntimePolicy()),
		modelRuntimeExecutor: modelruntime.LocalExecutor{}, multimodalRuntime: runtime,
	}
	handlerFinished := make(chan struct{})
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.serveMultimodalRealtime(w, r)
		close(handlerFinished)
	}))
	defer platform.Close()

	connection, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(platform.URL, "http"), &websocket.DialOptions{
		Subprotocols: []string{modelruntime.MultimodalRealtimeSubprotocol},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	start := testCatalogMultimodalStart()
	start.InputTracks = start.InputTracks[:1]
	start.OutputModalities = []string{"text"}
	writeMultimodalHTTPTestJSON(t, connection, start)
	var started modelruntime.MultimodalSessionStarted
	readMultimodalHTTPTestJSON(t, connection, &started)
	var completed modelruntime.MultimodalEvent
	readMultimodalHTTPTestJSON(t, connection, &completed)
	if started.Type != "session.started" || completed.Type != "session.completed" {
		t.Fatalf("unexpected public events: started=%+v completed=%+v", started, completed)
	}
	_ = connection.Close(websocket.StatusNormalClosure, "test complete")
	select {
	case <-handlerFinished:
	case <-time.After(2 * time.Second):
		t.Fatal("multimodal public handler did not finish")
	}

	select {
	case request := <-runtimeCalled:
		if request.Route.APIKey != "provider-secret" || request.ResolveObjectRef == nil || request.Type != "session.open" {
			t.Fatalf("runtime request did not receive governed route: %+v", request)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("multimodal runtime was not called")
	}
	if len(store.modelInvocations) != 1 || store.modelInvocations[0].Capability != managedagents.ModelInvocationCapabilityMultimodalRealtime ||
		store.modelInvocations[0].Status != managedagents.ModelInvocationStatusCompleted || store.modelInvocations[0].InputBytes != 128 {
		t.Fatalf("unexpected public multimodal invocation: %+v", store.modelInvocations)
	}
}

func TestServeMultimodalRealtimeAuditsAdmissionRejection(t *testing.T) {
	store := newTestStore()
	store.providers["realtime"] = managedagents.LLMProvider{
		ID: "realtime", ProviderType: "tma", BaseURL: "wss://provider.example/realtime", Enabled: true,
	}
	store.models[llmModelKey("realtime", "native")] = testMultimodalCatalogModel()
	policy := DefaultModelRuntimePolicy()
	policy.SpeechGlobalConcurrency = 1
	server := &Server{store: store, modelRuntimeAdmission: newModelRuntimeAdmission(policy)}
	holder := server.newMultimodalInvocationRecorder(
		httptest.NewRequest(http.MethodGet, "/v2/model-runtime/multimodal/realtime", nil),
		resolvedMultimodalRealtimeRoute{Provider: store.providers["realtime"], Model: store.models[llmModelKey("realtime", "native")]},
		time.Now().UTC(),
	)
	release, err := holder.Admit(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	handlerFinished := make(chan struct{})
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.serveMultimodalRealtime(w, r)
		close(handlerFinished)
	}))
	defer platform.Close()
	connection, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(platform.URL, "http"), &websocket.DialOptions{
		Subprotocols: []string{modelruntime.MultimodalRealtimeSubprotocol},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	start := testCatalogMultimodalStart()
	start.InputTracks = start.InputTracks[:1]
	start.OutputModalities = []string{"text"}
	writeMultimodalHTTPTestJSON(t, connection, start)
	var event modelruntime.MultimodalEvent
	readMultimodalHTTPTestJSON(t, connection, &event)
	if event.Type != "error" || event.Code != "multimodal_capacity_exceeded" || !event.Retryable || event.RetryAfterSeconds < 1 {
		t.Fatalf("unexpected multimodal admission event: %+v", event)
	}
	select {
	case <-handlerFinished:
	case <-time.After(2 * time.Second):
		t.Fatal("rejected multimodal public handler did not finish")
	}
	if len(store.modelInvocations) != 1 || store.modelInvocations[0].Status != managedagents.ModelInvocationStatusFailed ||
		store.modelInvocations[0].ErrorCode != "multimodal_capacity_exceeded" {
		t.Fatalf("multimodal admission rejection was not audited: %+v", store.modelInvocations)
	}
}

func testMultimodalCatalogModel() managedagents.LLMModel {
	return managedagents.LLMModel{
		ProviderID: "realtime", Model: "native", CapabilityType: managedagents.LLMModelCapabilityMultimodalRealtime,
		Capabilities: managedagents.LLMModelCapabilities{
			Protocol: managedagents.LLMMultimodalRealtimeProtocolTMAWebSocket,
			Realtime: &managedagents.LLMRealtimeCapabilities{
				InputFormats: []managedagents.LLMRealtimeMediaFormat{
					{Kind: "audio", ContentType: "audio/pcm", Codec: "pcm_s16le"},
					{Kind: "video", ContentType: "video/h264", Codec: "h264"},
				},
				OutputModalities: []string{"text", "audio"},
				OutputFormats: []managedagents.LLMRealtimeMediaFormat{
					{Kind: "audio", ContentType: "audio/pcm", Codec: "pcm_s16le"},
				},
				MaxInputTracks: 2, MaxFrameBytes: 1024,
			},
		},
	}
}

func writeMultimodalHTTPTestJSON(t *testing.T, connection *websocket.Conn, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Write(t.Context(), websocket.MessageText, payload); err != nil {
		t.Fatal(err)
	}
}

func readMultimodalHTTPTestJSON(t *testing.T, connection *websocket.Conn, target any) {
	t.Helper()
	messageType, payload, err := connection.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.MessageText || json.Unmarshal(payload, target) != nil {
		t.Fatalf("unexpected multimodal message type=%d payload=%q", messageType, payload)
	}
}

func TestResolveMultimodalRealtimeRouteEnforcesCatalogCapabilities(t *testing.T) {
	store := newTestStore()
	store.providers["realtime"] = managedagents.LLMProvider{ID: "realtime", ProviderType: "tma", BaseURL: "wss://provider.example/realtime", Enabled: true}
	store.models[llmModelKey("realtime", "native")] = managedagents.LLMModel{
		ProviderID: "realtime", Model: "native", CapabilityType: managedagents.LLMModelCapabilityMultimodalRealtime,
		Capabilities: managedagents.LLMModelCapabilities{
			Protocol: managedagents.LLMMultimodalRealtimeProtocolTMAWebSocket,
			Realtime: &managedagents.LLMRealtimeCapabilities{
				InputFormats: []managedagents.LLMRealtimeMediaFormat{
					{Kind: "audio", ContentType: "audio/pcm", Codec: "pcm_s16le"},
					{Kind: "video", ContentType: "video/h264", Codec: "h264"},
				},
				OutputModalities: []string{"text", "audio"},
				OutputFormats:    []managedagents.LLMRealtimeMediaFormat{{Kind: "audio", ContentType: "audio/pcm", Codec: "pcm_s16le"}},
				MaxInputTracks:   2,
				MaxFrameBytes:    1024,
			},
		},
	}
	server := &Server{store: store}
	start := testCatalogMultimodalStart()
	resolved, err := server.resolveMultimodalRealtimeRoute(start)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Route.Protocol != modelruntime.MultimodalProviderProtocolTMAWebSocket || resolved.Route.Constraints.MaxFrameBytes != 1024 || len(resolved.Route.Constraints.InputFormats) != 2 {
		t.Fatalf("unexpected approved route: %+v", resolved.Route)
	}

	unsupportedCodec := start
	unsupportedCodec.InputTracks = append([]modelruntime.MultimodalTrack(nil), start.InputTracks...)
	unsupportedCodec.InputTracks[0].Codec = "opus"
	if _, err := server.resolveMultimodalRealtimeRoute(unsupportedCodec); err == nil || !strings.Contains(err.Error(), "input track") {
		t.Fatalf("expected catalog codec rejection, got %v", err)
	}
	unsupportedOutput := start
	unsupportedOutput.OutputModalities = []string{"image"}
	unsupportedOutput.OutputFlowLimits = start.OutputFlowLimits
	unsupportedOutput.InitialOutputCredit = start.InitialOutputCredit
	if _, err := server.resolveMultimodalRealtimeRoute(unsupportedOutput); err == nil || !strings.Contains(err.Error(), "output modality") {
		t.Fatalf("expected catalog output rejection, got %v", err)
	}
}

func TestResolveMultimodalRealtimeRouteBuildsOpenAIAdapterRoute(t *testing.T) {
	store := newTestStore()
	store.providers["openai"] = managedagents.LLMProvider{
		ID: "openai", ProviderType: "openai", BaseURL: "wss://api.openai.com/v1/realtime", Enabled: true,
	}
	store.models[llmModelKey("openai", "realtime")] = managedagents.LLMModel{
		ProviderID: "openai", Model: "realtime", CapabilityType: managedagents.LLMModelCapabilityMultimodalRealtime,
		Capabilities: managedagents.LLMModelCapabilities{
			Protocol: managedagents.LLMMultimodalRealtimeProtocolOpenAI, UpstreamModel: "gpt-realtime",
			Realtime: &managedagents.LLMRealtimeCapabilities{
				InputFormats: []managedagents.LLMRealtimeMediaFormat{
					{Kind: "audio", ContentType: "audio/pcm", Codec: "pcm_s16le"},
					{Kind: "image", ContentType: "image/jpeg", Codec: "jpeg"},
				},
				OutputModalities: []string{"text", "audio"},
				OutputFormats:    []managedagents.LLMRealtimeMediaFormat{{Kind: "audio", ContentType: "audio/pcm", Codec: "pcm_s16le"}},
				MaxInputTracks:   2, MaxFrameBytes: 1024,
			},
		},
	}
	limits := modelruntime.MultimodalFlowLimits{MaxFrameBytes: 1024, MaxInFlightBytes: 2048, MaxInFlightFrames: 2}
	credit := modelruntime.MultimodalFlowCredit{Bytes: 1024, Frames: 1}
	start := modelruntime.MultimodalSessionStart{
		Type: "session.start", ProtocolVersion: modelruntime.MultimodalRealtimeProtocolVersion,
		ProviderID: "openai", Model: "realtime", SessionID: "session-1",
		InputTracks: []modelruntime.MultimodalTrack{
			{ID: "microphone", Kind: "audio", ContentType: "audio/pcm", Codec: "pcm_s16le", Delivery: "reliable", SampleRateHz: 24000, Channels: 1},
			{ID: "camera", Kind: "image", ContentType: "image/jpeg", Codec: "jpeg", Delivery: "reliable", Width: 1280, Height: 720},
		},
		OutputModalities: []string{"text", "audio"}, OutputFlowLimits: &limits, InitialOutputCredit: &credit,
	}
	server := &Server{store: store}
	resolved, err := server.resolveMultimodalRealtimeRoute(start)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Route.Protocol != modelruntime.MultimodalProviderProtocolOpenAI || resolved.Route.Model != "realtime" ||
		resolved.Route.UpstreamModel != "gpt-realtime" || resolved.Route.BaseURL != "wss://api.openai.com/v1/realtime" {
		t.Fatalf("unexpected OpenAI adapter route: %+v", resolved.Route)
	}
}

func testCatalogMultimodalStart() modelruntime.MultimodalSessionStart {
	limits := modelruntime.MultimodalFlowLimits{MaxFrameBytes: 1024, MaxInFlightBytes: 2048, MaxInFlightFrames: 2}
	credit := modelruntime.MultimodalFlowCredit{Bytes: 1024, Frames: 1}
	return modelruntime.MultimodalSessionStart{
		Type: "session.start", ProtocolVersion: modelruntime.MultimodalRealtimeProtocolVersion,
		ProviderID: "realtime", Model: "native", SessionID: "session-1",
		InputTracks: []modelruntime.MultimodalTrack{
			{ID: "microphone", Kind: "audio", ContentType: "audio/pcm", Codec: "pcm_s16le", Delivery: "reliable", SampleRateHz: 16000, Channels: 1},
			{ID: "camera", Kind: "video", ContentType: "video/h264", Codec: "h264", Delivery: "latest", Width: 1280, Height: 720, MaxFPS: 30},
		},
		OutputModalities: []string{"text", "audio"}, OutputFlowLimits: &limits, InitialOutputCredit: &credit,
	}
}

func TestResolveMultimodalObjectRefReturnsVerifiedFrame(t *testing.T) {
	server, scope, route, start, input, client := newMultimodalObjectRefFixture(t, managedagents.ObjectVisibilityWorkspace)

	frame, err := server.resolveMultimodalObjectRef(t.Context(), scope, route, start, input)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Kind != modelruntime.MultimodalMediaVideo || frame.TrackID != input.TrackID || frame.Sequence != input.Sequence ||
		frame.TimestampMicros != input.TimestampMicros || !bytes.Equal(frame.Payload, client.content) {
		t.Fatalf("unexpected resolved frame: %+v", frame)
	}
	if client.calls != 1 || client.lastGet.Bucket != "realtime-private" || client.lastGet.Key != "sessions/session-1/frame.h264" {
		t.Fatalf("unexpected object read: calls=%d input=%+v", client.calls, client.lastGet)
	}
}

func TestResolveMultimodalObjectRefAllowsOptionalClientChecksum(t *testing.T) {
	server, scope, route, start, input, _ := newMultimodalObjectRefFixture(t, managedagents.ObjectVisibilityWorkspace)
	input.ChecksumSHA256 = ""

	if _, err := server.resolveMultimodalObjectRef(t.Context(), scope, route, start, input); err != nil {
		t.Fatalf("expected persisted and actual checksum verification without a client checksum, got %v", err)
	}
}

func TestResolveMultimodalObjectRefEnforcesSessionVisibility(t *testing.T) {
	server, scope, route, start, input, _ := newMultimodalObjectRefFixture(t, managedagents.ObjectVisibilitySession)

	if _, err := server.resolveMultimodalObjectRef(t.Context(), scope, route, start, input); !errors.Is(err, managedagents.ErrForbidden) {
		t.Fatalf("expected unlinked session object rejection, got %v", err)
	}
	server.store.(*testStore).sessionArtifacts[start.SessionID] = []managedagents.SessionArtifact{{
		ID: "artifact-1", WorkspaceID: scope.WorkspaceID, SessionID: start.SessionID, ObjectRefID: input.ObjectRefID,
	}}
	if _, err := server.resolveMultimodalObjectRef(t.Context(), scope, route, start, input); err != nil {
		t.Fatalf("expected linked session object to resolve, got %v", err)
	}
}

func TestResolveMultimodalObjectRefEnforcesAccessScope(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*managedagents.AccessScope, *modelruntime.MultimodalSessionStart)
	}{
		{name: "workspace", mutate: func(scope *managedagents.AccessScope, _ *modelruntime.MultimodalSessionStart) {
			scope.WorkspaceID = "workspace-other"
		}},
		{name: "owner", mutate: func(scope *managedagents.AccessScope, _ *modelruntime.MultimodalSessionStart) {
			scope.OwnerID = "owner-other"
		}},
		{name: "missing session", mutate: func(_ *managedagents.AccessScope, start *modelruntime.MultimodalSessionStart) {
			start.SessionID = "session-other"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, scope, route, start, input, client := newMultimodalObjectRefFixture(t, managedagents.ObjectVisibilityWorkspace)
			test.mutate(&scope, &start)
			if _, err := server.resolveMultimodalObjectRef(t.Context(), scope, route, start, input); !errors.Is(err, managedagents.ErrForbidden) {
				t.Fatalf("expected access rejection, got %v", err)
			}
			if client.calls != 0 {
				t.Fatalf("object storage must not be read before authorization, calls=%d", client.calls)
			}
		})
	}
}

func TestResolveMultimodalObjectRefRequiresSessionAndObject(t *testing.T) {
	t.Run("session id", func(t *testing.T) {
		server, scope, route, start, input, client := newMultimodalObjectRefFixture(t, managedagents.ObjectVisibilityWorkspace)
		start.SessionID = ""
		if _, err := server.resolveMultimodalObjectRef(t.Context(), scope, route, start, input); !errors.Is(err, managedagents.ErrInvalid) {
			t.Fatalf("expected session requirement, got %v", err)
		}
		if client.calls != 0 {
			t.Fatalf("object storage must not be read without a session, calls=%d", client.calls)
		}
	})
	t.Run("object ref", func(t *testing.T) {
		server, scope, route, start, input, client := newMultimodalObjectRefFixture(t, managedagents.ObjectVisibilityWorkspace)
		input.ObjectRefID = "object-missing"
		if _, err := server.resolveMultimodalObjectRef(t.Context(), scope, route, start, input); !errors.Is(err, managedagents.ErrForbidden) || strings.Contains(err.Error(), input.ObjectRefID) {
			t.Fatalf("expected non-enumerable object rejection, got %v", err)
		}
		if client.calls != 0 {
			t.Fatalf("object storage must not be read for a missing object, calls=%d", client.calls)
		}
	})
}

func TestResolveMultimodalObjectRefValidatesTrackAndFrameLimits(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*modelruntime.MultimodalRoute, *modelruntime.MultimodalSessionStart, *modelruntime.MultimodalObjectRefInput)
	}{
		{name: "undeclared track", mutate: func(_ *modelruntime.MultimodalRoute, _ *modelruntime.MultimodalSessionStart, input *modelruntime.MultimodalObjectRefInput) {
			input.TrackID = "screen"
		}},
		{name: "audio object ref", mutate: func(_ *modelruntime.MultimodalRoute, _ *modelruntime.MultimodalSessionStart, input *modelruntime.MultimodalObjectRefInput) {
			input.TrackID, input.ContentType = "microphone", "audio/pcm"
		}},
		{name: "catalog frame limit", mutate: func(route *modelruntime.MultimodalRoute, _ *modelruntime.MultimodalSessionStart, _ *modelruntime.MultimodalObjectRefInput) {
			route.Constraints.MaxFrameBytes = 4
		}},
		{name: "protocol frame limit", mutate: func(route *modelruntime.MultimodalRoute, _ *modelruntime.MultimodalSessionStart, input *modelruntime.MultimodalObjectRefInput) {
			route.Constraints.MaxFrameBytes = modelruntime.MultimodalMaxFrameBytes
			input.SizeBytes = modelruntime.MultimodalMaxFrameBytes + 1
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, scope, route, start, input, client := newMultimodalObjectRefFixture(t, managedagents.ObjectVisibilityWorkspace)
			test.mutate(&route, &start, &input)
			if _, err := server.resolveMultimodalObjectRef(t.Context(), scope, route, start, input); !errors.Is(err, managedagents.ErrInvalid) {
				t.Fatalf("expected invalid input rejection, got %v", err)
			}
			if client.calls != 0 {
				t.Fatalf("object storage must not be read for invalid input, calls=%d", client.calls)
			}
		})
	}
}

func TestResolveMultimodalObjectRefValidatesPersistedMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*managedagents.ObjectRef, *modelruntime.MultimodalObjectRefInput)
	}{
		{name: "database size", mutate: func(ref *managedagents.ObjectRef, _ *modelruntime.MultimodalObjectRefInput) { ref.SizeBytes++ }},
		{name: "database content type", mutate: func(ref *managedagents.ObjectRef, _ *modelruntime.MultimodalObjectRefInput) {
			ref.ContentType = "image/jpeg"
		}},
		{name: "missing database checksum", mutate: func(ref *managedagents.ObjectRef, _ *modelruntime.MultimodalObjectRefInput) { ref.ChecksumSHA256 = "" }},
		{name: "invalid database checksum", mutate: func(ref *managedagents.ObjectRef, _ *modelruntime.MultimodalObjectRefInput) {
			ref.ChecksumSHA256 = "not-a-checksum"
		}},
		{name: "declared checksum", mutate: func(_ *managedagents.ObjectRef, input *modelruntime.MultimodalObjectRefInput) {
			input.ChecksumSHA256 = strings.Repeat("0", 64)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, scope, route, start, input, client := newMultimodalObjectRefFixture(t, managedagents.ObjectVisibilityWorkspace)
			store := server.store.(*testStore)
			ref := store.objectRefs[input.ObjectRefID]
			test.mutate(&ref, &input)
			store.objectRefs[ref.ID] = ref
			if _, err := server.resolveMultimodalObjectRef(t.Context(), scope, route, start, input); !errors.Is(err, managedagents.ErrInvalid) {
				t.Fatalf("expected metadata rejection, got %v", err)
			}
			if client.calls != 0 {
				t.Fatalf("object storage must not be read after metadata rejection, calls=%d", client.calls)
			}
		})
	}
}

func TestResolveMultimodalObjectRefValidatesStoredObject(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*multimodalObjectRefTestClient)
	}{
		{name: "actual size", mutate: func(client *multimodalObjectRefTestClient) { client.content = append(client.content, 'x') }},
		{name: "storage size", mutate: func(client *multimodalObjectRefTestClient) { client.sizeBytes++ }},
		{name: "storage content type", mutate: func(client *multimodalObjectRefTestClient) { client.contentType = "image/jpeg" }},
		{name: "missing storage content type", mutate: func(client *multimodalObjectRefTestClient) { client.contentType = "" }},
		{name: "storage checksum", mutate: func(client *multimodalObjectRefTestClient) { client.checksumSHA256 = strings.Repeat("0", 64) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, scope, route, start, input, client := newMultimodalObjectRefFixture(t, managedagents.ObjectVisibilityWorkspace)
			test.mutate(client)
			if _, err := server.resolveMultimodalObjectRef(t.Context(), scope, route, start, input); !errors.Is(err, managedagents.ErrInvalid) {
				t.Fatalf("expected stored object rejection, got %v", err)
			}
		})
	}
}

func TestResolveMultimodalObjectRefHidesStorageCoordinates(t *testing.T) {
	server, scope, route, start, input, client := newMultimodalObjectRefFixture(t, managedagents.ObjectVisibilityWorkspace)
	client.getErr = fmt.Errorf("provider failed for bucket realtime-private key sessions/session-1/frame.h264")

	_, err := server.resolveMultimodalObjectRef(t.Context(), scope, route, start, input)
	if err == nil || strings.Contains(err.Error(), "realtime-private") || strings.Contains(err.Error(), "frame.h264") {
		t.Fatalf("expected redacted object storage error, got %v", err)
	}
}

func TestResolveMultimodalObjectRefReportsUnconfiguredStorage(t *testing.T) {
	server, scope, route, start, input, _ := newMultimodalObjectRefFixture(t, managedagents.ObjectVisibilityWorkspace)
	server.objectStore = objectstore.NewNoopClient(objectstore.Config{})

	if _, err := server.resolveMultimodalObjectRef(t.Context(), scope, route, start, input); !errors.Is(err, objectstore.ErrNotConfigured) {
		t.Fatalf("expected object storage configuration error, got %v", err)
	}
}

func TestResolveMultimodalObjectRefRedactsMissingStoredObject(t *testing.T) {
	server, scope, route, start, input, client := newMultimodalObjectRefFixture(t, managedagents.ObjectVisibilityWorkspace)
	client.getErr = objectstore.ErrNotFound

	if _, err := server.resolveMultimodalObjectRef(t.Context(), scope, route, start, input); err == nil ||
		errors.Is(err, objectstore.ErrNotFound) || strings.Contains(err.Error(), input.ObjectRefID) {
		t.Fatalf("expected redacted missing stored object error, got %v", err)
	}
}

func newMultimodalObjectRefFixture(t *testing.T, visibility string) (*Server, managedagents.AccessScope, modelruntime.MultimodalRoute, modelruntime.MultimodalSessionStart, modelruntime.MultimodalObjectRefInput, *multimodalObjectRefTestClient) {
	t.Helper()
	payload := []byte("h264-frame")
	digest := sha256.Sum256(payload)
	checksum := fmt.Sprintf("%x", digest)
	store := newTestStore()
	store.sessions["session-1"] = managedagents.Session{ID: "session-1", WorkspaceID: "workspace-1", OwnerID: "owner-1"}
	store.objectRefs["object-1"] = managedagents.ObjectRef{
		ID: "object-1", WorkspaceID: "workspace-1", StorageProvider: objectstore.ProviderS3,
		Bucket: "realtime-private", ObjectKey: "sessions/session-1/frame.h264", ObjectVersion: "version-1",
		ContentType: "video/h264", SizeBytes: int64(len(payload)), ChecksumSHA256: checksum, Visibility: visibility,
	}
	client := &multimodalObjectRefTestClient{
		content: payload, contentType: "video/h264", sizeBytes: int64(len(payload)), checksumSHA256: checksum,
	}
	start := testCatalogMultimodalStart()
	start.SessionID = "session-1"
	input := modelruntime.MultimodalObjectRefInput{
		Type: "input.object_ref", TrackID: "camera", Sequence: 7, TimestampMicros: 1234,
		ObjectRefID: "object-1", ContentType: "video/h264", SizeBytes: int64(len(payload)), ChecksumSHA256: checksum,
	}
	route := modelruntime.MultimodalRoute{Constraints: modelruntime.MultimodalRouteConstraints{MaxFrameBytes: 1024}}
	return &Server{store: store, objectStore: client}, managedagents.AccessScope{WorkspaceID: "workspace-1", OwnerID: "owner-1"}, route, start, input, client
}

type multimodalObjectRefTestClient struct {
	objectstore.NoopClient
	content        []byte
	contentType    string
	sizeBytes      int64
	checksumSHA256 string
	getErr         error
	calls          int
	lastGet        objectstore.GetObjectInput
}

func (client *multimodalObjectRefTestClient) GetObject(_ context.Context, input objectstore.GetObjectInput) (objectstore.GetObjectResult, error) {
	client.calls++
	client.lastGet = input
	if client.getErr != nil {
		return objectstore.GetObjectResult{}, client.getErr
	}
	return objectstore.GetObjectResult{
		Bucket: input.Bucket, Key: input.Key, Version: input.Version,
		Body: io.NopCloser(bytes.NewReader(client.content)), ContentType: client.contentType,
		SizeBytes: client.sizeBytes, ChecksumSHA256: client.checksumSHA256,
	}, nil
}
