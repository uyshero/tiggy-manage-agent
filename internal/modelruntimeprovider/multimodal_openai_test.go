package modelruntimeprovider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestOpenAIRealtimeAdapterTranslatesTMAProtocol(t *testing.T) {
	providerResult := make(chan error, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			providerResult <- err
			return
		}
		defer connection.CloseNow()
		if err := writeOpenAIRealtimeEvent(r.Context(), connection, map[string]any{
			"type": "session.created", "session": map[string]any{"id": "provider-session"},
		}); err != nil {
			providerResult <- err
			return
		}
		var update map[string]any
		if err := readMultimodalTestJSON(r.Context(), connection, &update); err != nil {
			providerResult <- err
			return
		}
		if err := assertOpenAISessionUpdate(update); err != nil {
			providerResult <- err
			return
		}
		if err := writeOpenAIRealtimeEvent(r.Context(), connection, map[string]any{"type": "session.updated"}); err != nil {
			providerResult <- err
			return
		}

		var audioAppend struct {
			Type  string `json:"type"`
			Audio string `json:"audio"`
		}
		if err := readMultimodalTestJSON(r.Context(), connection, &audioAppend); err != nil || audioAppend.Type != "input_audio_buffer.append" {
			providerResult <- fmt.Errorf("unexpected audio append: %+v err=%v", audioAppend, err)
			return
		}
		audio, err := base64.StdEncoding.DecodeString(audioAppend.Audio)
		if err != nil || string(audio) != "mic" {
			providerResult <- fmt.Errorf("unexpected audio payload %q: %v", string(audio), err)
			return
		}
		var imageInput map[string]any
		if err := readMultimodalTestJSON(r.Context(), connection, &imageInput); err != nil {
			providerResult <- err
			return
		}
		if imageURL := openAITestInputContentValue(imageInput, "input_image", "image_url"); imageURL != "data:image/jpeg;base64,"+base64.StdEncoding.EncodeToString([]byte("jpeg")) {
			providerResult <- fmt.Errorf("unexpected image input %q", imageURL)
			return
		}
		var textInput map[string]any
		if err := readMultimodalTestJSON(r.Context(), connection, &textInput); err != nil {
			providerResult <- err
			return
		}
		if text := openAITestInputContentValue(textInput, "input_text", "text"); text != "hello" {
			providerResult <- fmt.Errorf("unexpected text input %q", text)
			return
		}
		for _, wantType := range []string{"input_audio_buffer.commit", "response.create"} {
			var event struct {
				Type string `json:"type"`
			}
			if err := readMultimodalTestJSON(r.Context(), connection, &event); err != nil || event.Type != wantType {
				providerResult <- fmt.Errorf("unexpected commit event %+v, want %s: %v", event, wantType, err)
				return
			}
		}
		for _, event := range []any{
			map[string]any{"type": "response.output_text.delta", "delta": "hi"},
			map[string]any{"type": "response.output_text.done", "text": "hi"},
			map[string]any{"type": "response.output_audio.delta", "delta": base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4})},
			map[string]any{"type": "response.done", "response": map[string]any{"status": "completed"}},
		} {
			if err := writeOpenAIRealtimeEvent(r.Context(), connection, event); err != nil {
				providerResult <- err
				return
			}
		}
		providerResult <- nil
	}))
	defer provider.Close()

	dialer := func(ctx context.Context, target string, headers http.Header, subprotocol string) (*websocket.Conn, *http.Response, error) {
		parsed, err := url.Parse(target)
		if err != nil {
			return nil, nil, err
		}
		if parsed.Query().Get("model") != "gpt-realtime-upstream" || parsed.Query().Get("trace") != "on" || len(parsed.Query()) != 2 {
			return nil, nil, fmt.Errorf("unexpected OpenAI target %q", target)
		}
		if headers.Get("Authorization") != "Bearer provider-secret" || subprotocol != "" {
			return nil, nil, fmt.Errorf("unexpected OpenAI handshake authorization=%q subprotocol=%q", headers.Get("Authorization"), subprotocol)
		}
		return websocket.Dial(ctx, "ws"+strings.TrimPrefix(provider.URL, "http"), nil)
	}
	result := make(chan struct {
		metrics MultimodalMetrics
		err     error
	}, 1)
	request := testOpenAIRealtimeRequest()
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{MultimodalRealtimeSubprotocol}})
		if err != nil {
			result <- struct {
				metrics MultimodalMetrics
				err     error
			}{err: err}
			return
		}
		defer connection.CloseNow()
		metrics, proxyErr := ProxyMultimodalWithDialer(r.Context(), r.Context(), connection, request, dialer)
		result <- struct {
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
	if started.Type != "session.started" || started.SessionID != request.Start.SessionID || len(started.OutputTracks) != 1 || started.OutputTracks[0].SampleRateHz != openAIRealtimeAudioSampleRate {
		t.Fatalf("unexpected OpenAI session.started: %+v", started)
	}
	writeMultimodalTestFrameFatal(t, client, MultimodalMediaFrame{Kind: MultimodalMediaAudio, TrackID: "microphone", Sequence: 1, Payload: []byte("mic")})
	assertOpenAIInputCredit(t, client, "microphone", 1, 3)
	writeMultimodalTestFrameFatal(t, client, MultimodalMediaFrame{Kind: MultimodalMediaImage, TrackID: "camera", Sequence: 1, Payload: []byte("jpeg")})
	assertOpenAIInputCredit(t, client, "camera", 1, 4)
	writeMultimodalTestJSONFatal(t, client, MultimodalEvent{Type: "input.text.append", Text: "hello"})
	writeMultimodalTestJSONFatal(t, client, MultimodalEvent{Type: "input.commit"})

	var delta MultimodalEvent
	readMultimodalTestJSONFatal(t, client, &delta)
	if delta.Type != "output.text.delta" || delta.Text != "hi" {
		t.Fatalf("unexpected text delta: %+v", delta)
	}
	var final MultimodalEvent
	readMultimodalTestJSONFatal(t, client, &final)
	if final.Type != "output.text.final" || final.Text != "hi" {
		t.Fatalf("unexpected text final: %+v", final)
	}
	frame := readMultimodalTestFrameFatal(t, client)
	if frame.TrackID != openAIRealtimeOutputTrackID || frame.Sequence != 1 || string(frame.Payload) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("unexpected OpenAI audio frame: %+v", frame)
	}
	var completed MultimodalEvent
	readMultimodalTestJSONFatal(t, client, &completed)
	if completed.Type != "session.completed" || completed.SessionID != request.Start.SessionID {
		t.Fatalf("unexpected completion event: %+v", completed)
	}

	select {
	case err := <-providerResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OpenAI provider did not complete")
	}
	select {
	case proxyResult := <-result:
		if proxyResult.err != nil {
			t.Fatal(proxyResult.err)
		}
		metrics := proxyResult.metrics
		if !metrics.Completed || metrics.InputItems != 3 || metrics.InputBytes != 7 || metrics.InputCharacters != 5 ||
			metrics.OutputItems != 2 || metrics.OutputBytes != 4 || metrics.OutputCharacters != 2 {
			t.Fatalf("unexpected OpenAI realtime metrics: %+v", metrics)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OpenAI adapter did not return")
	}
}

func TestOpenAIRealtimeRouteRejectsUnsupportedMediaAndURLCredentials(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*MultimodalRequest)
		want   string
	}{
		{name: "audio sample rate", mutate: func(request *MultimodalRequest) { request.Start.InputTracks[0].SampleRateHz = 16000 }, want: "24000"},
		{name: "video input", mutate: func(request *MultimodalRequest) {
			request.Start.InputTracks[1] = MultimodalTrack{ID: "camera", Kind: MultimodalMediaVideo, ContentType: "video/h264", Codec: "h264", Delivery: MultimodalDeliveryLatest, Width: 640, Height: 480, MaxFPS: 10}
			request.Route.Constraints.InputFormats[1] = MultimodalMediaFormat{Kind: MultimodalMediaVideo, ContentType: "video/h264", Codec: "h264"}
		}, want: "unsupported"},
		{name: "URL credential", mutate: func(request *MultimodalRequest) { request.Route.BaseURL += "&api_key=secret" }, want: "credentials"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := testOpenAIRealtimeRequest()
			test.mutate(&request)
			if err := ValidateMultimodalRequest(request); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateMultimodalRequest() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestOpenAIRealtimeConnectFailureDoesNotExposeCredential(t *testing.T) {
	request := testOpenAIRealtimeRequest()
	dialer := func(context.Context, string, http.Header, string) (*websocket.Conn, *http.Response, error) {
		return nil, nil, errors.New("dial failed: provider-secret")
	}
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{MultimodalRealtimeSubprotocol}})
		if err != nil {
			return
		}
		defer connection.CloseNow()
		_, _ = ProxyMultimodalWithDialer(r.Context(), r.Context(), connection, request, dialer)
	}))
	defer platform.Close()
	client, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(platform.URL, "http"), &websocket.DialOptions{Subprotocols: []string{MultimodalRealtimeSubprotocol}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseNow()
	var event MultimodalEvent
	readMultimodalTestJSONFatal(t, client, &event)
	encoded, _ := json.Marshal(event)
	if event.Code != "multimodal_provider_connect_failed" || strings.Contains(string(encoded), "provider-secret") {
		t.Fatalf("credential leaked in public error: %s", encoded)
	}
}

func TestOpenAIRealtimeAdapterBoundsSlowClientWithOutputCredit(t *testing.T) {
	serverConnection := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err == nil {
			serverConnection <- connection
		}
	}))
	defer server.Close()
	client, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseNow()
	providerSide := <-serverConnection
	defer providerSide.CloseNow()

	limits := MultimodalFlowLimits{MaxFrameBytes: 1024, MaxInFlightBytes: 1024, MaxInFlightFrames: 1}
	window, err := NewMultimodalCreditWindow(limits, MultimodalFlowCredit{Bytes: 1, Frames: 1})
	if err != nil {
		t.Fatal(err)
	}
	metrics := newMultimodalMetrics(nil)
	metrics.setOutputTracks([]MultimodalTrack{{
		ID: openAIRealtimeOutputTrackID, Kind: MultimodalMediaAudio, ContentType: "audio/pcm", Codec: "pcm_s16le",
		Delivery: MultimodalDeliveryReliable, SampleRateHz: 24000, Channels: 1,
	}})
	state := &openAIRealtimeState{sessionID: "session-1", outputWindow: window, metrics: &metrics}
	delta, _ := json.Marshal(map[string]any{
		"type": "response.output_audio.delta", "delta": base64.StdEncoding.EncodeToString([]byte{1}),
	})
	if done, err := state.processProviderMessage(t.Context(), providerSide, websocket.MessageText, delta); done || err != nil {
		t.Fatalf("first audio delta failed: done=%t err=%v", done, err)
	}
	if frame := readMultimodalTestFrameFatal(t, client); len(frame.Payload) != 1 || frame.Sequence != 1 {
		t.Fatalf("unexpected first bounded output frame: %+v", frame)
	}
	done, err := state.processProviderMessage(t.Context(), providerSide, websocket.MessageText, delta)
	if !done || !errors.Is(err, ErrMultimodalFlowControl) {
		t.Fatalf("second audio delta should exceed client credit: done=%t err=%v", done, err)
	}
	var event MultimodalEvent
	readMultimodalTestJSONFatal(t, client, &event)
	if event.Type != "error" || event.Code != "flow_control_violation" || metrics.OutputBytes != 1 {
		t.Fatalf("unexpected slow-client result event=%+v metrics=%+v", event, metrics)
	}
}

func TestOpenAIRealtimeAdapterTimesOutSlowProviderWrite(t *testing.T) {
	serverConnection := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err == nil {
			serverConnection <- connection
		}
	}))
	defer server.Close()
	client, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseNow()
	adapterSide := <-serverConnection
	defer adapterSide.CloseNow()

	metrics := newMultimodalMetrics(nil)
	state := &openAIRealtimeState{
		metrics: &metrics, providerTimeout: 100 * time.Millisecond,
		providerWriter: func(ctx context.Context, _ *websocket.Conn, _ any) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	payload, _ := json.Marshal(MultimodalEvent{Type: "input.text.append", Text: "blocked"})
	startedAt := time.Now()
	done, err := state.processClientMessage(t.Context(), t.Context(), adapterSide, nil, websocket.MessageText, payload)
	if !done || !errors.Is(err, errOpenAIRealtimeBackpressureTimeout) {
		t.Fatalf("slow provider should time out: done=%t err=%v", done, err)
	}
	if elapsed := time.Since(startedAt); elapsed < 75*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("unexpected provider write timeout duration %s", elapsed)
	}
	var event MultimodalEvent
	readMultimodalTestJSONFatal(t, client, &event)
	if event.Type != "error" || event.Code != "backpressure_timeout" || !event.Retryable || metrics.InputItems != 0 {
		t.Fatalf("unexpected slow-provider result event=%+v metrics=%+v", event, metrics)
	}
}

func TestOpenAIRealtimeAdapterNormalizesProviderDisconnect(t *testing.T) {
	providerResult := make(chan error, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			providerResult <- err
			return
		}
		defer connection.CloseNow()
		if err := writeOpenAIRealtimeEvent(r.Context(), connection, map[string]any{
			"type": "session.created", "session": map[string]any{"id": "provider-session"},
		}); err != nil {
			providerResult <- err
			return
		}
		var update map[string]any
		if err := readMultimodalTestJSON(r.Context(), connection, &update); err != nil {
			providerResult <- err
			return
		}
		if err := writeOpenAIRealtimeEvent(r.Context(), connection, map[string]any{"type": "session.updated"}); err != nil {
			providerResult <- err
			return
		}
		providerResult <- connection.Close(websocket.StatusInternalError, "private-provider-reason")
	}))
	defer provider.Close()
	dialer := func(ctx context.Context, _ string, _ http.Header, _ string) (*websocket.Conn, *http.Response, error) {
		return websocket.Dial(ctx, "ws"+strings.TrimPrefix(provider.URL, "http"), nil)
	}
	result := make(chan struct {
		metrics MultimodalMetrics
		err     error
	}, 1)
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{MultimodalRealtimeSubprotocol}})
		if err != nil {
			return
		}
		defer connection.CloseNow()
		metrics, proxyErr := ProxyMultimodalWithDialer(r.Context(), r.Context(), connection, testOpenAIRealtimeRequest(), dialer)
		result <- struct {
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
	var event MultimodalEvent
	readMultimodalTestJSONFatal(t, client, &event)
	if event.Type != "error" || event.Code != "multimodal_provider_disconnected" || !event.Retryable || strings.Contains(event.Message, "private-provider-reason") {
		t.Fatalf("unexpected normalized disconnect event: %+v", event)
	}
	select {
	case proxyResult := <-result:
		if proxyResult.metrics.ErrorCode != "multimodal_provider_disconnected" || proxyResult.err == nil || strings.Contains(proxyResult.err.Error(), "private-provider-reason") {
			t.Fatalf("unexpected normalized disconnect result: metrics=%+v err=%v", proxyResult.metrics, proxyResult.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OpenAI disconnect did not terminate the adapter")
	}
	select {
	case providerErr := <-providerResult:
		if providerErr != nil && websocket.CloseStatus(providerErr) != websocket.StatusNormalClosure {
			t.Logf("provider close completed with %v", providerErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider disconnect did not finish")
	}
}

func TestOpenAIRealtimeAdapterPropagatesClientDisconnect(t *testing.T) {
	providerDisconnected := make(chan error, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			providerDisconnected <- err
			return
		}
		defer connection.CloseNow()
		if err := writeOpenAIRealtimeEvent(r.Context(), connection, map[string]any{
			"type": "session.created", "session": map[string]any{"id": "provider-session"},
		}); err != nil {
			providerDisconnected <- err
			return
		}
		var update map[string]any
		if err := readMultimodalTestJSON(r.Context(), connection, &update); err != nil {
			providerDisconnected <- err
			return
		}
		if err := writeOpenAIRealtimeEvent(r.Context(), connection, map[string]any{"type": "session.updated"}); err != nil {
			providerDisconnected <- err
			return
		}
		_, _, err = connection.Read(r.Context())
		providerDisconnected <- err
	}))
	defer provider.Close()
	dialer := func(ctx context.Context, _ string, _ http.Header, _ string) (*websocket.Conn, *http.Response, error) {
		return websocket.Dial(ctx, "ws"+strings.TrimPrefix(provider.URL, "http"), nil)
	}
	proxyResult := make(chan error, 1)
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{MultimodalRealtimeSubprotocol}})
		if err != nil {
			proxyResult <- err
			return
		}
		defer connection.CloseNow()
		_, proxyErr := ProxyMultimodalWithDialer(r.Context(), r.Context(), connection, testOpenAIRealtimeRequest(), dialer)
		proxyResult <- proxyErr
	}))
	defer platform.Close()
	client, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(platform.URL, "http"), &websocket.DialOptions{Subprotocols: []string{MultimodalRealtimeSubprotocol}})
	if err != nil {
		t.Fatal(err)
	}
	var started MultimodalSessionStarted
	readMultimodalTestJSONFatal(t, client, &started)
	if err := client.Close(websocket.StatusNormalClosure, "client complete"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-proxyResult:
		if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
			t.Fatalf("unexpected client disconnect result: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client disconnect did not terminate the adapter")
	}
	select {
	case err := <-providerDisconnected:
		if err == nil {
			t.Fatal("provider connection remained open after client disconnect")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client disconnect was not propagated to the provider")
	}
}

func testOpenAIRealtimeRequest() MultimodalRequest {
	limits := MultimodalFlowLimits{MaxFrameBytes: 1024, MaxInFlightBytes: 4096, MaxInFlightFrames: 4}
	credit := MultimodalFlowCredit{Bytes: 4096, Frames: 4}
	return MultimodalRequest{
		Type: "session.open",
		Route: MultimodalRoute{
			ProviderID: "openai", ProviderType: "openai", BaseURL: "wss://api.openai.com/v1/realtime?trace=on",
			APIKey: "provider-secret", Model: "catalog-model", UpstreamModel: "gpt-realtime-upstream", Protocol: MultimodalProviderProtocolOpenAI,
			Constraints: MultimodalRouteConstraints{
				InputFormats: []MultimodalMediaFormat{
					{Kind: MultimodalMediaAudio, ContentType: "audio/pcm", Codec: "pcm_s16le"},
					{Kind: MultimodalMediaImage, ContentType: "image/jpeg", Codec: "jpeg"},
				},
				OutputModalities: []string{"text", "audio"},
				OutputFormats:    []MultimodalMediaFormat{{Kind: MultimodalMediaAudio, ContentType: "audio/pcm", Codec: "pcm_s16le"}},
				MaxInputTracks:   2, MaxFrameBytes: 1024,
			},
		},
		Start: MultimodalSessionStart{
			Type: "session.start", ProtocolVersion: MultimodalRealtimeProtocolVersion,
			ProviderID: "openai", Model: "catalog-model", SessionID: "public-session",
			InputTracks: []MultimodalTrack{
				{ID: "microphone", Kind: MultimodalMediaAudio, ContentType: "audio/pcm", Codec: "pcm_s16le", Delivery: MultimodalDeliveryReliable, SampleRateHz: 24000, Channels: 1},
				{ID: "camera", Kind: MultimodalMediaImage, ContentType: "image/jpeg", Codec: "jpeg", Delivery: MultimodalDeliveryReliable, Width: 640, Height: 480},
			},
			OutputModalities: []string{"text", "audio"}, OutputFlowLimits: &limits, InitialOutputCredit: &credit,
		},
	}
}

func assertOpenAISessionUpdate(update map[string]any) error {
	if update["type"] != "session.update" {
		return fmt.Errorf("unexpected session update type: %+v", update)
	}
	session, ok := update["session"].(map[string]any)
	if !ok || session["type"] != "realtime" {
		return fmt.Errorf("unexpected session configuration: %+v", update)
	}
	modalities, ok := session["output_modalities"].([]any)
	if !ok || len(modalities) != 1 || modalities[0] != "audio" {
		return fmt.Errorf("unexpected output modalities: %+v", session["output_modalities"])
	}
	audio, ok := session["audio"].(map[string]any)
	if !ok || audio["input"] == nil || audio["output"] == nil {
		return fmt.Errorf("unexpected audio configuration: %+v", session["audio"])
	}
	return nil
}

func openAITestInputContentValue(event map[string]any, contentType, field string) string {
	item, _ := event["item"].(map[string]any)
	content, _ := item["content"].([]any)
	if event["type"] != "conversation.item.create" || len(content) != 1 {
		return ""
	}
	part, _ := content[0].(map[string]any)
	if part["type"] != contentType {
		return ""
	}
	value, _ := part[field].(string)
	return value
}

func assertOpenAIInputCredit(t *testing.T, connection *websocket.Conn, trackID string, sequence uint64, size int64) {
	t.Helper()
	var credit MultimodalFlowCredit
	readMultimodalTestJSONFatal(t, connection, &credit)
	if credit.Type != "flow.credit" || credit.TrackID != trackID || credit.AcknowledgedSequence != sequence || credit.Bytes != size || credit.Frames != 1 {
		t.Fatalf("unexpected OpenAI input credit: %+v", credit)
	}
}
