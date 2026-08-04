package modelruntimeprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type multimodalProxyFunc func(context.Context, context.Context, *websocket.Conn, MultimodalRequest) (MultimodalMetrics, error)

func (f multimodalProxyFunc) ProxyMultimodal(ctx, clientContext context.Context, client *websocket.Conn, request MultimodalRequest) (MultimodalMetrics, error) {
	return f(ctx, clientContext, client, request)
}

func TestHTTPMultimodalProxyRoundTripsNativeProviderAndCredits(t *testing.T) {
	providerResult := make(chan error, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{MultimodalRealtimeSubprotocol}})
		if err != nil {
			providerResult <- err
			return
		}
		defer connection.CloseNow()
		if connection.Subprotocol() != MultimodalRealtimeSubprotocol {
			providerResult <- errors.New("provider subprotocol was not negotiated")
			return
		}
		var start MultimodalSessionStart
		if err := readMultimodalTestJSON(r.Context(), connection, &start); err != nil {
			providerResult <- err
			return
		}
		if start.ProviderID != "native-provider" || start.Model != "realtime-model" || start.SessionID != "session-1" {
			providerResult <- fmt.Errorf("unexpected provider start: %+v", start)
			return
		}
		limits := MultimodalFlowLimits{MaxFrameBytes: 1024, MaxInFlightBytes: 1024, MaxInFlightFrames: 2}
		credit := MultimodalFlowCredit{Bytes: 4, Frames: 1}
		started := MultimodalSessionStarted{
			Type: "session.started", ProtocolVersion: MultimodalRealtimeProtocolVersion, SessionID: start.SessionID,
			InputFlowLimits: limits, InitialInputCredit: credit,
			OutputFlowLimits: &limits, InitialOutputCredit: &credit,
			OutputTracks: []MultimodalTrack{{
				ID: "speaker", Kind: MultimodalMediaAudio, ContentType: "audio/pcm", Codec: "pcm_s16le",
				Delivery: MultimodalDeliveryReliable, SampleRateHz: 16000, Channels: 1,
			}},
			HeartbeatMS: 15000,
		}
		if err := writeMultimodalTestJSON(r.Context(), connection, started); err != nil {
			providerResult <- err
			return
		}
		first, err := readMultimodalTestFrame(r.Context(), connection)
		if err != nil || first.Sequence != 1 || string(first.Payload) != "mic1" {
			providerResult <- fmt.Errorf("unexpected first input frame: frame=%+v err=%w", first, err)
			return
		}
		if err := writeMultimodalTestJSON(r.Context(), connection, MultimodalFlowCredit{Type: "flow.credit", Bytes: 4, Frames: 1, TrackID: "microphone", AcknowledgedSequence: 1}); err != nil {
			providerResult <- err
			return
		}
		if err := writeMultimodalTestFrame(r.Context(), connection, MultimodalMediaFrame{Kind: MultimodalMediaAudio, TrackID: "speaker", Sequence: 1, Payload: []byte("out1")}); err != nil {
			providerResult <- err
			return
		}
		var outputCredit MultimodalFlowCredit
		if err := readMultimodalTestJSON(r.Context(), connection, &outputCredit); err != nil || outputCredit.Type != "flow.credit" || outputCredit.Bytes != 4 || outputCredit.Frames != 1 {
			providerResult <- fmt.Errorf("unexpected output credit: credit=%+v err=%w", outputCredit, err)
			return
		}
		second, err := readMultimodalTestFrame(r.Context(), connection)
		if err != nil || second.Sequence != 2 || string(second.Payload) != "mic2" {
			providerResult <- fmt.Errorf("unexpected second input frame: frame=%+v err=%w", second, err)
			return
		}
		if err := writeMultimodalTestFrame(r.Context(), connection, MultimodalMediaFrame{Kind: MultimodalMediaAudio, TrackID: "speaker", Sequence: 2, Payload: []byte("out2")}); err != nil {
			providerResult <- err
			return
		}
		providerResult <- writeMultimodalEvent(r.Context(), connection, MultimodalEvent{Type: "session.completed", SessionID: start.SessionID})
	}))
	defer provider.Close()

	dialer := func(ctx context.Context, target string, headers http.Header, subprotocol string) (*websocket.Conn, *http.Response, error) {
		if target != "wss://provider.example/realtime" || headers.Get("Authorization") != "Bearer provider-secret" || subprotocol != MultimodalRealtimeSubprotocol {
			return nil, nil, fmt.Errorf("unexpected provider dial: target=%q authorization=%q subprotocol=%q", target, headers.Get("Authorization"), subprotocol)
		}
		return websocket.Dial(ctx, "ws"+strings.TrimPrefix(provider.URL, "http"), &websocket.DialOptions{Subprotocols: []string{subprotocol}})
	}
	auth := AuthConfig{Mode: AuthModeSigned, Secret: testRuntimeSigningSecret}
	runtimeHandler, err := NewHandler(HandlerConfig{
		Auth: auth,
		Multimodal: multimodalProxyFunc(func(ctx, clientContext context.Context, client *websocket.Conn, request MultimodalRequest) (MultimodalMetrics, error) {
			return ProxyMultimodalWithDialer(ctx, clientContext, client, request, dialer)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtimeServer := httptest.NewServer(runtimeHandler)
	defer runtimeServer.Close()
	executor, err := NewHTTPExecutorWithAuth(runtimeServer.URL, auth, runtimeServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	proxyResult := make(chan struct {
		metrics MultimodalMetrics
		err     error
	}, 1)
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, acceptErr := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{MultimodalRealtimeSubprotocol}})
		if acceptErr != nil {
			proxyResult <- struct {
				metrics MultimodalMetrics
				err     error
			}{err: acceptErr}
			return
		}
		defer connection.CloseNow()
		metrics, proxyErr := executor.ProxyMultimodal(r.Context(), r.Context(), connection, testMultimodalRequest())
		proxyResult <- struct {
			metrics MultimodalMetrics
			err     error
		}{metrics: metrics, err: proxyErr}
	}))
	defer platform.Close()
	client, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(platform.URL, "http"), &websocket.DialOptions{Subprotocols: []string{MultimodalRealtimeSubprotocol}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseNow()
	var started MultimodalSessionStarted
	readMultimodalTestJSONFatal(t, client, &started)
	if started.SessionID != "session-1" || client.Subprotocol() != MultimodalRealtimeSubprotocol {
		t.Fatalf("unexpected client negotiation: started=%+v subprotocol=%q", started, client.Subprotocol())
	}
	writeMultimodalTestFrameFatal(t, client, MultimodalMediaFrame{Kind: MultimodalMediaAudio, TrackID: "microphone", Sequence: 1, Payload: []byte("mic1")})
	var inputCredit MultimodalFlowCredit
	readMultimodalTestJSONFatal(t, client, &inputCredit)
	if inputCredit.Type != "flow.credit" || inputCredit.AcknowledgedSequence != 1 {
		t.Fatalf("unexpected input credit: %+v", inputCredit)
	}
	if frame := readMultimodalTestFrameFatal(t, client); frame.Sequence != 1 || string(frame.Payload) != "out1" {
		t.Fatalf("unexpected first output frame: %+v", frame)
	}
	writeMultimodalTestJSONFatal(t, client, MultimodalFlowCredit{Type: "flow.credit", Bytes: 4, Frames: 1, TrackID: "speaker", AcknowledgedSequence: 1})
	writeMultimodalTestFrameFatal(t, client, MultimodalMediaFrame{Kind: MultimodalMediaAudio, TrackID: "microphone", Sequence: 2, Payload: []byte("mic2")})
	if frame := readMultimodalTestFrameFatal(t, client); frame.Sequence != 2 || string(frame.Payload) != "out2" {
		t.Fatalf("unexpected second output frame: %+v", frame)
	}
	var completed MultimodalEvent
	readMultimodalTestJSONFatal(t, client, &completed)
	if completed.Type != "session.completed" {
		t.Fatalf("unexpected completion event: %+v", completed)
	}
	select {
	case err := <-providerResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("native provider did not complete")
	}
	select {
	case result := <-proxyResult:
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.metrics.InputItems != 2 || result.metrics.InputBytes != 8 || result.metrics.OutputItems != 2 || result.metrics.OutputBytes != 8 || !result.metrics.Completed {
			t.Fatalf("unexpected multimodal metrics: %+v", result.metrics)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("multimodal transport did not complete")
	}
}

func TestHTTPMultimodalProxyResolvesObjectRefBeforeRuntimeBoundary(t *testing.T) {
	runtimeResult := make(chan error, 1)
	runtimeHandler, err := NewHandler(HandlerConfig{
		AuthToken: "runtime-secret",
		Multimodal: multimodalProxyFunc(func(ctx, _ context.Context, client *websocket.Conn, request MultimodalRequest) (MultimodalMetrics, error) {
			if request.ResolveObjectRef != nil {
				runtimeResult <- errors.New("object ref resolver crossed the Server/Runtime boundary")
				return MultimodalMetrics{}, errors.New("unexpected resolver")
			}
			if err := writeMultimodalTestJSON(ctx, client, MultimodalSessionStarted{
				Type: "session.started", ProtocolVersion: MultimodalRealtimeProtocolVersion, SessionID: request.Start.SessionID,
			}); err != nil {
				runtimeResult <- err
				return MultimodalMetrics{}, err
			}
			frame, err := readMultimodalTestFrame(ctx, client)
			if err != nil || frame.TrackID != "camera" || frame.Sequence != 7 || string(frame.Payload) != "verified-h264" {
				err = fmt.Errorf("runtime did not receive verified media frame: frame=%+v err=%v", frame, err)
				runtimeResult <- err
				return MultimodalMetrics{}, err
			}
			err = writeMultimodalEvent(ctx, client, MultimodalEvent{Type: "session.completed", SessionID: request.Start.SessionID})
			runtimeResult <- err
			return MultimodalMetrics{Completed: err == nil}, err
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtimeServer := httptest.NewServer(runtimeHandler)
	defer runtimeServer.Close()
	executor, err := NewHTTPExecutor(runtimeServer.URL, "runtime-secret", runtimeServer.Client())
	if err != nil {
		t.Fatal(err)
	}

	request := testMultimodalRequest()
	resolverCalls := 0
	request.ResolveObjectRef = func(_ context.Context, input MultimodalObjectRefInput) (MultimodalMediaFrame, error) {
		resolverCalls++
		if input.ObjectRefID != "object-1" || input.TrackID != "camera" || input.Sequence != 7 {
			return MultimodalMediaFrame{}, fmt.Errorf("unexpected object ref input: %+v", input)
		}
		return MultimodalMediaFrame{
			Kind: MultimodalMediaVideo, TrackID: input.TrackID, Sequence: input.Sequence,
			TimestampMicros: input.TimestampMicros, Payload: []byte("verified-h264"),
		}, nil
	}
	proxyResult := make(chan struct {
		metrics MultimodalMetrics
		err     error
	}, 1)
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, acceptErr := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{MultimodalRealtimeSubprotocol}})
		if acceptErr != nil {
			proxyResult <- struct {
				metrics MultimodalMetrics
				err     error
			}{err: acceptErr}
			return
		}
		defer connection.CloseNow()
		metrics, proxyErr := executor.ProxyMultimodal(r.Context(), r.Context(), connection, request)
		proxyResult <- struct {
			metrics MultimodalMetrics
			err     error
		}{metrics: metrics, err: proxyErr}
	}))
	defer platform.Close()
	client, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(platform.URL, "http"), &websocket.DialOptions{Subprotocols: []string{MultimodalRealtimeSubprotocol}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseNow()
	var started MultimodalSessionStarted
	readMultimodalTestJSONFatal(t, client, &started)
	writeMultimodalTestJSONFatal(t, client, MultimodalObjectRefInput{
		Type: "input.object_ref", TrackID: "camera", Sequence: 7, TimestampMicros: 1000,
		ObjectRefID: "object-1", ContentType: "video/h264", SizeBytes: int64(len("verified-h264")),
	})
	var completed MultimodalEvent
	readMultimodalTestJSONFatal(t, client, &completed)
	if completed.Type != "session.completed" {
		t.Fatalf("unexpected completion event: %+v", completed)
	}
	select {
	case runtimeErr := <-runtimeResult:
		if runtimeErr != nil {
			t.Fatal(runtimeErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runtime did not receive resolved object ref")
	}
	select {
	case result := <-proxyResult:
		if result.err != nil || !result.metrics.Completed || result.metrics.InputVideoFrames != 1 || result.metrics.InputBytes != int64(len("verified-h264")) {
			t.Fatalf("unexpected resolved object ref metrics: %+v err=%v", result.metrics, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("object ref proxy did not complete")
	}
	if resolverCalls != 1 {
		t.Fatalf("expected one object ref resolution, got %d", resolverCalls)
	}
}

func TestMultimodalNativeAdapterRejectsInvalidClientTraffic(t *testing.T) {
	tests := []struct {
		name          string
		initialCredit MultimodalFlowCredit
		messages      func(*testing.T, *websocket.Conn)
		wantCode      string
	}{
		{
			name: "credit exceeded", initialCredit: MultimodalFlowCredit{Bytes: 1, Frames: 1}, wantCode: "flow_control_violation",
			messages: func(t *testing.T, connection *websocket.Conn) {
				writeMultimodalTestFrameFatal(t, connection, MultimodalMediaFrame{Kind: MultimodalMediaAudio, TrackID: "microphone", Sequence: 1, Payload: []byte("too-large")})
			},
		},
		{
			name: "duplicate sequence", initialCredit: MultimodalFlowCredit{Bytes: 16, Frames: 2}, wantCode: "media_sequence_violation",
			messages: func(t *testing.T, connection *websocket.Conn) {
				frame := MultimodalMediaFrame{Kind: MultimodalMediaAudio, TrackID: "microphone", Sequence: 1, Payload: []byte("a")}
				writeMultimodalTestFrameFatal(t, connection, frame)
				writeMultimodalTestFrameFatal(t, connection, frame)
			},
		},
		{
			name: "unresolved object ref", initialCredit: MultimodalFlowCredit{Bytes: 16, Frames: 2}, wantCode: "unresolved_object_ref",
			messages: func(t *testing.T, connection *websocket.Conn) {
				writeMultimodalTestJSONFatal(t, connection, MultimodalObjectRefInput{Type: "input.object_ref", TrackID: "camera", Sequence: 1, ObjectRefID: "object-1", ContentType: "video/h264", SizeBytes: 4})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{MultimodalRealtimeSubprotocol}})
				if err != nil {
					return
				}
				defer connection.CloseNow()
				var start MultimodalSessionStart
				if readMultimodalTestJSON(r.Context(), connection, &start) != nil {
					return
				}
				limits := MultimodalFlowLimits{MaxFrameBytes: 1024, MaxInFlightBytes: 1024, MaxInFlightFrames: 2}
				_ = writeMultimodalTestJSON(r.Context(), connection, MultimodalSessionStarted{
					Type: "session.started", ProtocolVersion: MultimodalRealtimeProtocolVersion, SessionID: start.SessionID,
					InputFlowLimits: limits, InitialInputCredit: test.initialCredit,
				})
				for {
					if _, _, err := connection.Read(r.Context()); err != nil {
						return
					}
				}
			}))
			defer provider.Close()
			dialer := func(ctx context.Context, _ string, _ http.Header, subprotocol string) (*websocket.Conn, *http.Response, error) {
				return websocket.Dial(ctx, "ws"+strings.TrimPrefix(provider.URL, "http"), &websocket.DialOptions{Subprotocols: []string{subprotocol}})
			}
			result := make(chan MultimodalMetrics, 1)
			platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{MultimodalRealtimeSubprotocol}})
				if err != nil {
					return
				}
				defer connection.CloseNow()
				metrics, _ := ProxyMultimodalWithDialer(r.Context(), r.Context(), connection, testMultimodalTextRequest(), dialer)
				result <- metrics
			}))
			defer platform.Close()
			client, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(platform.URL, "http"), &websocket.DialOptions{Subprotocols: []string{MultimodalRealtimeSubprotocol}})
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseNow()
			var started MultimodalSessionStarted
			readMultimodalTestJSONFatal(t, client, &started)
			test.messages(t, client)
			var event MultimodalEvent
			readMultimodalTestJSONFatal(t, client, &event)
			if event.Type != "error" || event.Code != test.wantCode {
				t.Fatalf("unexpected protocol error: %+v", event)
			}
			select {
			case metrics := <-result:
				if metrics.ErrorCode != test.wantCode {
					t.Fatalf("unexpected error metrics: %+v", metrics)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("invalid client session did not stop")
			}
		})
	}
}

func TestHTTPMultimodalProxyRejectsInvalidSignedAuthentication(t *testing.T) {
	runtimeHandler, err := NewHandler(HandlerConfig{Auth: AuthConfig{Mode: AuthModeSigned, Secret: testRuntimeSigningSecret}})
	if err != nil {
		t.Fatal(err)
	}
	runtimeServer := httptest.NewServer(runtimeHandler)
	defer runtimeServer.Close()
	executor, err := NewHTTPExecutorWithAuth(runtimeServer.URL, AuthConfig{Mode: AuthModeSigned, Secret: "different-runtime-signing-secret-32-bytes"}, runtimeServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan MultimodalMetrics, 1)
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, acceptErr := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{MultimodalRealtimeSubprotocol}})
		if acceptErr != nil {
			return
		}
		defer connection.CloseNow()
		metrics, _ := executor.ProxyMultimodal(r.Context(), r.Context(), connection, testMultimodalRequest())
		result <- metrics
	}))
	defer platform.Close()
	client, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(platform.URL, "http"), &websocket.DialOptions{Subprotocols: []string{MultimodalRealtimeSubprotocol}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseNow()
	var event MultimodalEvent
	readMultimodalTestJSONFatal(t, client, &event)
	if event.Type != "error" || event.Code != "multimodal_runtime_connect_failed" || !event.Retryable {
		t.Fatalf("unexpected authentication error: %+v", event)
	}
	select {
	case metrics := <-result:
		if metrics.ErrorCode != "multimodal_runtime_connect_failed" {
			t.Fatalf("unexpected authentication metrics: %+v", metrics)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("authentication failure did not complete")
	}
}

func TestHTTPMultimodalProxyCancellationReachesRuntime(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	runtimeHandler, err := NewHandler(HandlerConfig{
		AuthToken: "runtime-secret",
		Multimodal: multimodalProxyFunc(func(ctx, _ context.Context, client *websocket.Conn, _ MultimodalRequest) (MultimodalMetrics, error) {
			close(started)
			_, _, readErr := client.Read(ctx)
			close(canceled)
			return MultimodalMetrics{}, readErr
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtimeServer := httptest.NewServer(runtimeHandler)
	defer runtimeServer.Close()
	executor, err := NewHTTPExecutor(runtimeServer.URL, "runtime-secret", runtimeServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, acceptErr := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{MultimodalRealtimeSubprotocol}})
		if acceptErr != nil {
			result <- acceptErr
			return
		}
		defer connection.CloseNow()
		_, proxyErr := executor.ProxyMultimodal(ctx, r.Context(), connection, testMultimodalRequest())
		result <- proxyErr
	}))
	defer platform.Close()
	client, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(platform.URL, "http"), &websocket.DialOptions{Subprotocols: []string{MultimodalRealtimeSubprotocol}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseNow()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("runtime multimodal request did not start")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("multimodal cancellation did not reach runtime")
	}
	select {
	case proxyErr := <-result:
		if !errors.Is(proxyErr, context.Canceled) {
			t.Fatalf("ProxyMultimodal() error = %v, want context canceled", proxyErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled multimodal proxy did not return")
	}
}

func TestMultimodalRouteConstraintsBoundSessionAndProviderNegotiation(t *testing.T) {
	request := testMultimodalRequest()
	if err := ValidateMultimodalRequest(request); err != nil {
		t.Fatalf("valid approved route: %v", err)
	}
	unsupportedInput := request
	unsupportedInput.Start.InputTracks = append([]MultimodalTrack(nil), request.Start.InputTracks...)
	unsupportedInput.Start.InputTracks[0].Codec = "opus"
	if err := ValidateMultimodalRequest(unsupportedInput); err == nil || !strings.Contains(err.Error(), "input track") {
		t.Fatalf("expected input format rejection, got %v", err)
	}
	unsupportedOutput := request
	unsupportedOutput.Start.OutputModalities = []string{"image"}
	if err := ValidateMultimodalRequest(unsupportedOutput); err == nil || !strings.Contains(err.Error(), "output modality") {
		t.Fatalf("expected output modality rejection, got %v", err)
	}

	limits := MultimodalFlowLimits{MaxFrameBytes: 1024, MaxInFlightBytes: 2048, MaxInFlightFrames: 2}
	credit := MultimodalFlowCredit{Bytes: 1024, Frames: 1}
	request.Route.Constraints.MaxFrameBytes = 1024
	request.Start.OutputFlowLimits = &limits
	request.Start.InitialOutputCredit = &credit
	started := MultimodalSessionStarted{
		Type: "session.started", ProtocolVersion: MultimodalRealtimeProtocolVersion, SessionID: request.Start.SessionID,
		InputFlowLimits:    MultimodalFlowLimits{MaxFrameBytes: 2048, MaxInFlightBytes: 2048, MaxInFlightFrames: 2},
		InitialInputCredit: MultimodalFlowCredit{Bytes: 2048, Frames: 1},
		OutputFlowLimits:   &limits, InitialOutputCredit: &credit,
		OutputTracks: []MultimodalTrack{{
			ID: "speaker", Kind: MultimodalMediaAudio, ContentType: "audio/pcm", Codec: "pcm_s16le",
			Delivery: MultimodalDeliveryReliable, SampleRateHz: 16000, Channels: 1,
		}},
	}
	if err := validateMultimodalNegotiation(request.Route.Constraints, started); err == nil || !strings.Contains(err.Error(), "input frame limit") {
		t.Fatalf("expected provider negotiation rejection, got %v", err)
	}
	started.InputFlowLimits = limits
	started.OutputTracks[0].Codec = "opus"
	if err := validateMultimodalNegotiation(request.Route.Constraints, started); err == nil || !strings.Contains(err.Error(), "outside the approved route") {
		t.Fatalf("expected provider output format rejection, got %v", err)
	}
}

func testMultimodalRequest() MultimodalRequest {
	return MultimodalRequest{
		Type: "session.open",
		Route: MultimodalRoute{
			ProviderID: "native-provider", ProviderType: "tma", BaseURL: "wss://provider.example/realtime", APIKey: "provider-secret", Model: "realtime-model", Protocol: MultimodalProviderProtocolTMAWebSocket,
			Constraints: MultimodalRouteConstraints{
				InputFormats: []MultimodalMediaFormat{
					{Kind: MultimodalMediaAudio, ContentType: "audio/pcm", Codec: "pcm_s16le"},
					{Kind: MultimodalMediaVideo, ContentType: "video/h264", Codec: "h264"},
				},
				OutputModalities: []string{"text", "audio"},
				OutputFormats:    []MultimodalMediaFormat{{Kind: MultimodalMediaAudio, ContentType: "audio/pcm", Codec: "pcm_s16le"}},
				MaxInputTracks:   MultimodalMaxTracks,
				MaxFrameBytes:    MultimodalMaxFrameBytes,
			},
		},
		Start: validMultimodalSessionStartWithID(),
	}
}

func testMultimodalTextRequest() MultimodalRequest {
	request := testMultimodalRequest()
	request.Start.OutputModalities = []string{"text"}
	request.Start.OutputFlowLimits = nil
	request.Start.InitialOutputCredit = nil
	return request
}

func validMultimodalSessionStartWithID() MultimodalSessionStart {
	start := validMultimodalSessionStart()
	start.ProviderID = "native-provider"
	start.Model = "realtime-model"
	start.SessionID = "session-1"
	return start
}

func writeMultimodalTestJSON(ctx context.Context, connection *websocket.Conn, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return connection.Write(ctx, websocket.MessageText, payload)
}

func writeMultimodalTestJSONFatal(t *testing.T, connection *websocket.Conn, value any) {
	t.Helper()
	if err := writeMultimodalTestJSON(t.Context(), connection, value); err != nil {
		t.Fatal(err)
	}
}

func readMultimodalTestJSON(ctx context.Context, connection *websocket.Conn, target any) error {
	messageType, payload, err := connection.Read(ctx)
	if err != nil {
		return err
	}
	if messageType != websocket.MessageText {
		return fmt.Errorf("expected text message, got %v", messageType)
	}
	return json.Unmarshal(payload, target)
}

func readMultimodalTestJSONFatal(t *testing.T, connection *websocket.Conn, target any) {
	t.Helper()
	if err := readMultimodalTestJSON(t.Context(), connection, target); err != nil {
		t.Fatal(err)
	}
}

func writeMultimodalTestFrame(ctx context.Context, connection *websocket.Conn, frame MultimodalMediaFrame) error {
	payload, err := EncodeMultimodalMediaFrame(frame)
	if err != nil {
		return err
	}
	return connection.Write(ctx, websocket.MessageBinary, payload)
}

func writeMultimodalTestFrameFatal(t *testing.T, connection *websocket.Conn, frame MultimodalMediaFrame) {
	t.Helper()
	if err := writeMultimodalTestFrame(t.Context(), connection, frame); err != nil {
		t.Fatal(err)
	}
}

func readMultimodalTestFrame(ctx context.Context, connection *websocket.Conn) (MultimodalMediaFrame, error) {
	messageType, payload, err := connection.Read(ctx)
	if err != nil {
		return MultimodalMediaFrame{}, err
	}
	if messageType != websocket.MessageBinary {
		return MultimodalMediaFrame{}, fmt.Errorf("expected binary message, got %v", messageType)
	}
	return DecodeMultimodalMediaFrame(payload)
}

func readMultimodalTestFrameFatal(t *testing.T, connection *websocket.Conn) MultimodalMediaFrame {
	t.Helper()
	frame, err := readMultimodalTestFrame(t.Context(), connection)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}
