package tma

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

func TestMultimodalRealtimeSDKAppliesCreditAndLatestDrop(t *testing.T) {
	serverResult := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/model-runtime/multimodal/realtime" || r.Header.Get("Authorization") != "Bearer realtime-token" {
			serverResult <- fmt.Errorf("unexpected handshake path=%q auth=%q", r.URL.Path, r.Header.Get("Authorization"))
			return
		}
		connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{MultimodalRealtimeProtocol}})
		if err != nil {
			serverResult <- err
			return
		}
		defer connection.CloseNow()
		var start multimodalSessionStartWire
		if err := readSDKMultimodalJSON(r.Context(), connection, &start); err != nil {
			serverResult <- err
			return
		}
		if start.ProtocolVersion != MultimodalRealtimeProtocol || start.ProviderID != "native" || start.Model != "realtime" || len(start.InputTracks) != 1 {
			serverResult <- fmt.Errorf("unexpected session start: %+v", start)
			return
		}
		limits := MultimodalFlowLimits{MaxFrameBytes: 4, MaxInFlightBytes: 4, MaxInFlightFrames: 1}
		credit := MultimodalFlowCredit{Bytes: 4, Frames: 1}
		if err := writeSDKMultimodalJSON(r.Context(), connection, MultimodalSessionStarted{
			Type: "session.started", ProtocolVersion: MultimodalRealtimeProtocol, SessionID: "session-1",
			InputFlowLimits: limits, InitialInputCredit: credit,
			OutputFlowLimits: &limits, InitialOutputCredit: &credit,
			OutputTracks: []MultimodalTrack{{
				ID: "speaker", Kind: MultimodalMediaAudio, ContentType: "audio/pcm", Codec: "pcm_s16le",
				Delivery: MultimodalDeliveryReliable, SampleRateHz: 16000, Channels: 1,
			}},
		}); err != nil {
			serverResult <- err
			return
		}
		first, err := readSDKMultimodalFrame(r.Context(), connection)
		if err != nil || first.Sequence != 1 || string(first.Payload) != "one1" {
			serverResult <- fmt.Errorf("unexpected first frame: %+v err=%v", first, err)
			return
		}
		if err := writeSDKMultimodalJSON(r.Context(), connection, MultimodalFlowCredit{
			Type: "flow.credit", Bytes: 4, Frames: 1, TrackID: "camera", AcknowledgedSequence: 1,
		}); err != nil {
			serverResult <- err
			return
		}
		third, err := readSDKMultimodalFrame(r.Context(), connection)
		if err != nil || third.Sequence != 3 || third.Flags&MultimodalMediaFlagDiscontinuity == 0 || string(third.Payload) != "tri3" {
			serverResult <- fmt.Errorf("latest drop was not signaled: %+v err=%v", third, err)
			return
		}
		if err := writeSDKMultimodalFrame(r.Context(), connection, MultimodalMediaFrame{
			Kind: MultimodalMediaAudio, TrackID: "speaker", Sequence: 1, Payload: []byte("out1"),
		}); err != nil {
			serverResult <- err
			return
		}
		var outputCredit MultimodalFlowCredit
		if err := readSDKMultimodalJSON(r.Context(), connection, &outputCredit); err != nil || outputCredit.Type != "flow.credit" || outputCredit.AcknowledgedSequence != 1 || outputCredit.Bytes != 4 {
			serverResult <- fmt.Errorf("unexpected output credit: %+v err=%v", outputCredit, err)
			return
		}
		serverResult <- writeSDKMultimodalJSON(r.Context(), connection, MultimodalRealtimeEvent{Type: "session.completed", SessionID: "session-1"})
	}))
	defer server.Close()

	client, err := NewClient(server.URL, WithBearerToken("realtime-token"))
	if err != nil {
		t.Fatal(err)
	}
	realtime, err := client.ModelRuntime.DialMultimodalRealtime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer realtime.CloseNow()
	limits := MultimodalFlowLimits{MaxFrameBytes: 4, MaxInFlightBytes: 4, MaxInFlightFrames: 1}
	credit := MultimodalFlowCredit{Bytes: 4, Frames: 1}
	started, err := realtime.Start(t.Context(), MultimodalSessionStart{
		ProviderID: "native", Model: "realtime", SessionID: "session-1",
		InputTracks: []MultimodalTrack{{
			ID: "camera", Kind: MultimodalMediaVideo, ContentType: "video/h264", Codec: "h264",
			Delivery: MultimodalDeliveryLatest, Width: 640, Height: 480, MaxFPS: 30,
		}},
		OutputModalities: []string{"audio"}, OutputFlowLimits: &limits, InitialOutputCredit: &credit,
	})
	if err != nil || started.SessionID != "session-1" {
		t.Fatalf("unexpected session start: %+v err=%v", started, err)
	}
	sent, err := realtime.SendMedia(t.Context(), MultimodalMediaFrame{
		Kind: MultimodalMediaVideo, TrackID: "camera", Sequence: 1, Payload: []byte("one1"),
	})
	if err != nil || !sent {
		t.Fatalf("first media frame was not sent: sent=%v err=%v", sent, err)
	}
	sent, err = realtime.SendMedia(t.Context(), MultimodalMediaFrame{
		Kind: MultimodalMediaVideo, TrackID: "camera", Sequence: 2, Payload: []byte("two2"),
	})
	if err != nil || sent {
		t.Fatalf("latest frame should be dropped without credit: sent=%v err=%v", sent, err)
	}
	event, err := realtime.Read(t.Context())
	if err != nil || event.Credit == nil || event.Credit.AcknowledgedSequence != 1 {
		t.Fatalf("unexpected input credit event: %+v err=%v", event, err)
	}
	sent, err = realtime.SendMedia(t.Context(), MultimodalMediaFrame{
		Kind: MultimodalMediaVideo, TrackID: "camera", Sequence: 3, Payload: []byte("tri3"),
	})
	if err != nil || !sent {
		t.Fatalf("third media frame was not sent: sent=%v err=%v", sent, err)
	}
	event, err = realtime.Read(t.Context())
	if err != nil || event.Media == nil || event.Media.TrackID != "speaker" || string(event.Media.Payload) != "out1" {
		t.Fatalf("unexpected output media: %+v err=%v", event, err)
	}
	if err := realtime.GrantOutputCredit(t.Context(), *event.Media); err != nil {
		t.Fatal(err)
	}
	event, err = realtime.Read(t.Context())
	if err != nil || event.Type != "session.completed" {
		t.Fatalf("unexpected completion: %+v err=%v", event, err)
	}
	select {
	case err := <-serverResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("multimodal SDK server did not complete")
	}
}

func TestMultimodalMediaFrameCodecRejectsMalformedFrames(t *testing.T) {
	frame := MultimodalMediaFrame{
		Kind: MultimodalMediaVideo, Flags: MultimodalMediaFlagKeyFrame, TrackID: "camera",
		Sequence: 42, TimestampMicros: 1234, Payload: []byte("h264"),
	}
	encoded, err := EncodeMultimodalMediaFrame(frame)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeMultimodalMediaFrame(encoded)
	if err != nil || decoded.Kind != frame.Kind || decoded.Flags != frame.Flags || decoded.Sequence != frame.Sequence ||
		decoded.TimestampMicros != frame.TimestampMicros || decoded.TrackID != frame.TrackID || string(decoded.Payload) != "h264" {
		t.Fatalf("unexpected frame round trip: %+v err=%v", decoded, err)
	}
	if wire := fmt.Sprintf("%x", encoded); wire != "544d414d01030001000000000000002a00000000000004d20006000063616d65726168323634" {
		t.Fatalf("SDK frame does not match the v1 wire fixture: %s", wire)
	}
	encoded[0] = 'X'
	if _, err := DecodeMultimodalMediaFrame(encoded); !errors.Is(err, ErrMultimodalInvalidFrame) {
		t.Fatalf("malformed frame error=%v", err)
	}
}

func TestMultimodalReliableInputWaitsForCredit(t *testing.T) {
	client := &MultimodalRealtimeClient{
		started: true,
		inputTracks: map[string]MultimodalTrack{
			"microphone": {ID: "microphone", Kind: MultimodalMediaAudio, Delivery: MultimodalDeliveryReliable},
		},
		inputLimits:   MultimodalFlowLimits{MaxFrameBytes: 4, MaxInFlightBytes: 4, MaxInFlightFrames: 1},
		inputSequence: map[string]uint64{}, droppedLatest: map[string]bool{}, creditChanged: make(chan struct{}),
	}
	result := make(chan error, 1)
	go func() {
		reserved, err := client.reserveInput(t.Context(), "microphone", 1, 4)
		if err == nil && !reserved {
			err = errors.New("reliable frame was dropped")
		}
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("reliable frame did not wait for credit: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	if err := client.applyInputCredit(MultimodalFlowCredit{Bytes: 4, Frames: 1}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("reliable frame did not resume after credit")
	}
}

func writeSDKMultimodalJSON(ctx context.Context, connection *websocket.Conn, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return connection.Write(ctx, websocket.MessageText, payload)
}

func readSDKMultimodalJSON(ctx context.Context, connection *websocket.Conn, target any) error {
	messageType, payload, err := connection.Read(ctx)
	if err != nil {
		return err
	}
	if messageType != websocket.MessageText {
		return fmt.Errorf("expected text message, got %v", messageType)
	}
	return json.Unmarshal(payload, target)
}

func writeSDKMultimodalFrame(ctx context.Context, connection *websocket.Conn, frame MultimodalMediaFrame) error {
	payload, err := EncodeMultimodalMediaFrame(frame)
	if err != nil {
		return err
	}
	return connection.Write(ctx, websocket.MessageBinary, payload)
}

func readSDKMultimodalFrame(ctx context.Context, connection *websocket.Conn) (MultimodalMediaFrame, error) {
	messageType, payload, err := connection.Read(ctx)
	if err != nil {
		return MultimodalMediaFrame{}, err
	}
	if messageType != websocket.MessageBinary {
		return MultimodalMediaFrame{}, fmt.Errorf("expected binary message, got %v payload=%q", messageType, strings.TrimSpace(string(payload)))
	}
	return DecodeMultimodalMediaFrame(payload)
}
