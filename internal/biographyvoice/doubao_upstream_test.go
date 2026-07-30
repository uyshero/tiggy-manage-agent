package biographyvoice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type fakeDoubaoRead struct {
	messageType websocket.MessageType
	payload     []byte
	err         error
}

type fakeDoubaoConnection struct {
	reads   chan fakeDoubaoRead
	mu      sync.Mutex
	writes  [][]byte
	onWrite func([]byte)
}

func newFakeDoubaoConnection() *fakeDoubaoConnection {
	return &fakeDoubaoConnection{reads: make(chan fakeDoubaoRead, 16)}
}

func (connection *fakeDoubaoConnection) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	select {
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	case result := <-connection.reads:
		return result.messageType, result.payload, result.err
	}
}

func (connection *fakeDoubaoConnection) Write(_ context.Context, messageType websocket.MessageType, payload []byte) error {
	if messageType != websocket.MessageBinary {
		return errors.New("expected binary upstream frame")
	}
	copyOfPayload := append([]byte(nil), payload...)
	connection.mu.Lock()
	connection.writes = append(connection.writes, copyOfPayload)
	onWrite := connection.onWrite
	connection.mu.Unlock()
	if onWrite != nil {
		onWrite(copyOfPayload)
	}
	return nil
}

func (connection *fakeDoubaoConnection) Close(websocket.StatusCode, string) error { return nil }

func (connection *fakeDoubaoConnection) writtenFrames(t *testing.T) []doubaoFrame {
	t.Helper()
	connection.mu.Lock()
	defer connection.mu.Unlock()
	frames := make([]doubaoFrame, 0, len(connection.writes))
	for _, payload := range connection.writes {
		frame, err := parseDoubaoFrame(payload)
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, frame)
	}
	return frames
}

func TestDoubaoASRStreamSendsHeadersAudioAndFinalResult(t *testing.T) {
	connection := newFakeDoubaoConnection()
	var target string
	var headers http.Header
	dialer := func(_ context.Context, gotTarget string, gotHeaders http.Header) (doubaoConnection, error) {
		target = gotTarget
		headers = gotHeaders.Clone()
		return connection, nil
	}
	events := make(chan doubaoUpstreamEvent, 8)
	config := testDoubaoConfig()
	stream, err := openDoubaoASR(t.Context(), config, "app-session", dialer, events)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	if target != config.DoubaoASRURL || headers.Get("X-Api-Key") != config.DoubaoAPIKey ||
		headers.Get("X-Api-Resource-Id") != config.DoubaoASRResourceID || headers.Get("X-Api-Connect-Id") == "" {
		t.Fatalf("unexpected ASR dial target or headers: target=%q headers=%v", target, headers)
	}
	if err := stream.SendAudio(t.Context(), []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if err := stream.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	frames := connection.writtenFrames(t)
	if len(frames) != 3 || frames[0].MessageType != doubaoMessageFullClient ||
		frames[1].MessageType != doubaoMessageAudioClient || !bytes.Equal(frames[1].Payload, []byte{1, 2, 3, 4}) ||
		frames[2].Flags != doubaoFlagLastNoSequence {
		t.Fatalf("unexpected ASR frames: %+v", frames)
	}

	connection.reads <- fakeDoubaoRead{messageType: websocket.MessageBinary, payload: mustDoubaoFrame(t, doubaoFrame{
		MessageType: doubaoMessageFullServer, Flags: doubaoFlagLastWithSequence, Serialization: doubaoSerializationJSON,
		HasSequence: true, Sequence: 2,
		Payload: []byte(`{"code":20000000,"result":{"text":"我十九岁去了上海","utterances":[{"definite":true}]}}`),
	})}
	event := waitDoubaoEvent(t, events)
	if event.Type != ServerASRFinal || event.Text != "我十九岁去了上海" || event.StreamID != stream.id {
		t.Fatalf("unexpected ASR event: %+v", event)
	}
}

func TestDoubaoASRDefiniteUtteranceRemainsPartialWithoutLastFrame(t *testing.T) {
	result, final, err := decodeDoubaoASRResult(doubaoFrame{
		MessageType: doubaoMessageFullServer, Flags: doubaoFlagPositiveSequence,
		Payload: []byte(`{"code":20000000,"result":{"text":"我十九岁去了上海","utterances":[{"definite":true}]}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if final || result != "我十九岁去了上海" {
		t.Fatalf("stable utterance was treated as final: result=%q final=%v", result, final)
	}
}

func TestDoubaoASRCommittedCloseUsesLatestPartialAsFinal(t *testing.T) {
	connection := newFakeDoubaoConnection()
	events := make(chan doubaoUpstreamEvent, 8)
	stream, err := openDoubaoASR(t.Context(), testDoubaoConfig(), "app-session", func(context.Context, string, http.Header) (doubaoConnection, error) {
		return connection, nil
	}, events)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	connection.reads <- fakeDoubaoRead{messageType: websocket.MessageBinary, payload: mustDoubaoFrame(t, doubaoFrame{
		MessageType: doubaoMessageFullServer, Flags: doubaoFlagPositiveSequence, Serialization: doubaoSerializationJSON,
		HasSequence: true, Sequence: 1, Payload: []byte(`{"code":20000000,"result":{"text":"我十九岁去了上海"}}`),
	})}
	partial := waitDoubaoEvent(t, events)
	if partial.Type != ServerASRPartial || partial.Text != "我十九岁去了上海" {
		t.Fatalf("unexpected ASR partial: %+v", partial)
	}
	if err := stream.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	connection.reads <- fakeDoubaoRead{err: errors.New("upstream closed after commit")}

	final := waitDoubaoEvent(t, events)
	if final.Type != ServerASRFinal || final.Text != partial.Text || final.Err != nil {
		t.Fatalf("unexpected ASR fallback final: %+v", final)
	}
}

func TestDoubaoTTSStreamSendsExpressionAudioAndCancel(t *testing.T) {
	connection := newFakeDoubaoConnection()
	connection.onWrite = func(payload []byte) {
		frame, err := parseDoubaoFrame(payload)
		if err != nil {
			return
		}
		responseEvent := int32(0)
		switch frame.Event {
		case doubaoEventStartConnection:
			responseEvent = doubaoEventConnectionStarted
		case doubaoEventStartSession:
			responseEvent = doubaoEventSessionStarted
		case doubaoEventCancelSession:
			responseEvent = doubaoEventSessionCanceled
		}
		if responseEvent != 0 {
			connection.reads <- fakeDoubaoRead{messageType: websocket.MessageBinary, payload: mustDoubaoFrame(t, doubaoFrame{
				MessageType: doubaoMessageFullServer, Flags: doubaoFlagWithEvent,
				Serialization: doubaoSerializationJSON, HasEvent: true, Event: responseEvent,
				EventID: frame.EventID, Payload: []byte(`{}`),
			})}
		}
	}

	var headers http.Header
	dialer := func(_ context.Context, _ string, gotHeaders http.Header) (doubaoConnection, error) {
		headers = gotHeaders.Clone()
		return connection, nil
	}
	events := make(chan doubaoUpstreamEvent, 8)
	config := testDoubaoConfig()
	stream, err := openDoubaoTTS(t.Context(), config, "app-session", "那是第一次离开家吗？", "温和、关切，略带好奇", dialer, events)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if headers.Get("X-Api-Key") != config.DoubaoAPIKey || headers.Get("X-Api-Resource-Id") != config.DoubaoTTSResourceID || headers.Get("X-Api-Connect-Id") == "" {
		t.Fatalf("unexpected TTS headers: %v", headers)
	}
	started := waitDoubaoEvent(t, events)
	if started.Type != ServerTTSStarted || started.StreamID != stream.id {
		t.Fatalf("unexpected TTS start event: %+v", started)
	}

	frames := connection.writtenFrames(t)
	startSession := frameWithEvent(t, frames, doubaoEventStartSession)
	if startSession.EventID == "" {
		t.Fatal("TTS start session did not carry a session ID")
	}
	var request struct {
		ReqParams struct {
			Model        string   `json:"model"`
			Speaker      string   `json:"speaker"`
			SectionID    string   `json:"section_id"`
			ContextTexts []string `json:"context_texts"`
		} `json:"req_params"`
	}
	if err := json.Unmarshal(startSession.Payload, &request); err != nil {
		t.Fatal(err)
	}
	if request.ReqParams.Model != config.DoubaoTTSModel || request.ReqParams.Speaker != config.DoubaoTTSSpeaker ||
		request.ReqParams.SectionID != "app-session" || len(request.ReqParams.ContextTexts) != 1 ||
		request.ReqParams.ContextTexts[0] != "温和、关切，略带好奇" {
		t.Fatalf("unexpected TTS request: %+v", request)
	}

	connection.reads <- fakeDoubaoRead{messageType: websocket.MessageBinary, payload: mustDoubaoFrame(t, doubaoFrame{
		MessageType: doubaoMessageAudioServer, Flags: doubaoFlagWithEvent,
		HasEvent: true, Event: doubaoEventTTSResponse, EventID: startSession.EventID, Payload: []byte{7, 8, 9},
	})}
	audio := waitDoubaoEvent(t, events)
	if !bytes.Equal(audio.Audio, []byte{7, 8, 9}) || audio.StreamID != stream.id {
		t.Fatalf("unexpected TTS audio event: %+v", audio)
	}
	if err := stream.Cancel(t.Context()); err != nil {
		t.Fatal(err)
	}
	canceled := waitDoubaoEvent(t, events)
	if canceled.Type != ServerTTSCanceled || canceled.StreamID != stream.id {
		t.Fatalf("unexpected TTS cancel event: %+v", canceled)
	}
	cancelFrame := frameWithEvent(t, connection.writtenFrames(t), doubaoEventCancelSession)
	if cancelFrame.EventID != startSession.EventID {
		t.Fatalf("cancel used a different session ID: start=%q cancel=%q", startSession.EventID, cancelFrame.EventID)
	}
}

func TestDoubaoTTSStreamAcceptsMultipleTextChunksBeforeFinish(t *testing.T) {
	connection := newFakeDoubaoConnection()
	connection.onWrite = func(payload []byte) {
		frame, err := parseDoubaoFrame(payload)
		if err != nil {
			return
		}
		responseEvent := int32(0)
		switch frame.Event {
		case doubaoEventStartConnection:
			responseEvent = doubaoEventConnectionStarted
		case doubaoEventStartSession:
			responseEvent = doubaoEventSessionStarted
		}
		if responseEvent != 0 {
			connection.reads <- fakeDoubaoRead{messageType: websocket.MessageBinary, payload: mustDoubaoFrame(t, doubaoFrame{
				MessageType: doubaoMessageFullServer, Flags: doubaoFlagWithEvent,
				Serialization: doubaoSerializationJSON, HasEvent: true, Event: responseEvent,
				EventID: frame.EventID, Payload: []byte(`{}`),
			})}
		}
	}

	events := make(chan doubaoUpstreamEvent, 8)
	stream, err := openDoubaoTTSSession(t.Context(), testDoubaoConfig(), "app-session", "温和、自然", func(context.Context, string, http.Header) (doubaoConnection, error) {
		return connection, nil
	}, events)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	_ = waitDoubaoEvent(t, events)

	if err := stream.SendText(t.Context(), "这段经历很有分量。"); err != nil {
		t.Fatal(err)
	}
	if err := stream.SendText(t.Context(), "当时您最先想到的画面是什么？"); err != nil {
		t.Fatal(err)
	}
	if err := stream.Finish(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := stream.Finish(t.Context()); err != nil {
		t.Fatal(err)
	}

	frames := connection.writtenFrames(t)
	var taskTexts []string
	finishCount := 0
	for _, frame := range frames {
		switch frame.Event {
		case doubaoEventTaskRequest:
			var request struct {
				ReqParams struct {
					Text string `json:"text"`
				} `json:"req_params"`
			}
			if err := json.Unmarshal(frame.Payload, &request); err != nil {
				t.Fatal(err)
			}
			taskTexts = append(taskTexts, request.ReqParams.Text)
		case doubaoEventFinishSession:
			finishCount++
		}
	}
	if want := []string{"这段经历很有分量。", "当时您最先想到的画面是什么？"}; len(taskTexts) != len(want) || taskTexts[0] != want[0] || taskTexts[1] != want[1] {
		t.Fatalf("unexpected TTS task chunks: %#v", taskTexts)
	}
	if finishCount != 1 {
		t.Fatalf("FinishSession count = %d, want 1", finishCount)
	}
	if err := stream.SendText(t.Context(), "不应再发送"); err == nil {
		t.Fatal("SendText succeeded after Finish")
	}
}

func TestProviderErrorRedactsSharedAPIKey(t *testing.T) {
	server := &Server{config: Config{DoubaoAPIKey: "shared-secret-value"}}
	err := server.safeProviderError(errors.New("upstream rejected shared-secret-value"))
	if strings.Contains(err.Error(), "shared-secret-value") || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("provider error was not redacted: %v", err)
	}
}

func TestDefaultDoubaoDialerIncludesBoundedHandshakeDetails(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Tt-Logid", "log-123")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"resource not authorized"}`))
	}))
	defer upstream.Close()

	_, err := defaultDoubaoDialer(t.Context(), "ws"+strings.TrimPrefix(upstream.URL, "http"), nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") || !strings.Contains(err.Error(), "logid=log-123") ||
		!strings.Contains(err.Error(), "resource not authorized") {
		t.Fatalf("unexpected handshake error: %v", err)
	}
}

func testDoubaoConfig() Config {
	return Config{
		HTTPAddr: ":0", Provider: ProviderDoubao, AllowedOrigins: []string{"127.0.0.1"},
		DoubaoAPIKey: "shared-secret", DoubaoASRURL: "wss://speech.example/asr", DoubaoASRResourceID: "asr-resource",
		DoubaoTTSURL: "wss://speech.example/tts", DoubaoTTSResourceID: "tts-resource",
		DoubaoTTSModel: "custom-model", DoubaoTTSSpeaker: "zh_female_example",
	}
}

func mustDoubaoFrame(t *testing.T, frame doubaoFrame) []byte {
	t.Helper()
	payload, err := buildDoubaoFrame(frame)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func waitDoubaoEvent(t *testing.T, events <-chan doubaoUpstreamEvent) doubaoUpstreamEvent {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Doubao event")
		return doubaoUpstreamEvent{}
	}
}

func frameWithEvent(t *testing.T, frames []doubaoFrame, event int32) doubaoFrame {
	t.Helper()
	for _, frame := range frames {
		if frame.Event == event {
			return frame
		}
	}
	t.Fatalf("event %d not found in frames", event)
	return doubaoFrame{}
}
