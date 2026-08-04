package modelruntimeprovider

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestMultimodalSessionStartValidatesTracksAndOutputs(t *testing.T) {
	start := validMultimodalSessionStart()
	if err := start.Validate(); err != nil {
		t.Fatalf("valid session start: %v", err)
	}

	duplicate := start
	duplicate.InputTracks = append(append([]MultimodalTrack(nil), start.InputTracks...), start.InputTracks[0])
	if err := duplicate.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate track rejection, got %v", err)
	}

	unreliableAudio := start
	unreliableAudio.InputTracks = append([]MultimodalTrack(nil), start.InputTracks...)
	unreliableAudio.InputTracks[0].Delivery = MultimodalDeliveryLatest
	if err := unreliableAudio.Validate(); err == nil || !strings.Contains(err.Error(), "reliable") {
		t.Fatalf("expected audio delivery rejection, got %v", err)
	}

	wrongVersion := start
	wrongVersion.ProtocolVersion = "tma.multimodal.realtime.v2"
	if err := wrongVersion.Validate(); !errors.Is(err, ErrMultimodalUnsupportedProtocol) {
		t.Fatalf("expected protocol rejection, got %v", err)
	}

	textOnly := start
	textOnly.OutputModalities = []string{"text"}
	textOnly.OutputFlowLimits = nil
	textOnly.InitialOutputCredit = nil
	if err := textOnly.Validate(); err != nil {
		t.Fatalf("valid text-only output: %v", err)
	}
	encoded, err := json.Marshal(textOnly)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "output_flow_limits") || strings.Contains(string(encoded), "initial_output_credit") {
		t.Fatalf("text-only start must omit output media credit: %s", encoded)
	}

	missingOutputCredit := start
	missingOutputCredit.InitialOutputCredit = nil
	if err := missingOutputCredit.Validate(); err == nil || !strings.Contains(err.Error(), "initial_output_credit") {
		t.Fatalf("expected binary output credit rejection, got %v", err)
	}
}

func TestMultimodalObjectRefRequiresDeclaredNonAudioTrack(t *testing.T) {
	start := validMultimodalSessionStart()
	valid := MultimodalObjectRefInput{
		Type: "input.object_ref", TrackID: "camera", Sequence: 1, TimestampMicros: 1000,
		ObjectRefID: "obj_1", ContentType: "video/h264", SizeBytes: 1024,
	}
	if err := valid.Validate(start); err != nil {
		t.Fatalf("valid object ref: %v", err)
	}
	valid.TrackID = "microphone"
	valid.ContentType = "audio/pcm"
	if err := valid.Validate(start); err == nil || !strings.Contains(err.Error(), "not supported for audio") {
		t.Fatalf("expected audio object ref rejection, got %v", err)
	}
}

func TestMultimodalMediaFrameRoundTrip(t *testing.T) {
	want := MultimodalMediaFrame{
		Kind: MultimodalMediaVideo, Flags: MultimodalMediaFlagKeyFrame | MultimodalMediaFlagDiscontinuity,
		Sequence: 42, TimestampMicros: 1_500_000, TrackID: "camera", Payload: []byte("encoded-frame"),
	}
	encoded, err := EncodeMultimodalMediaFrame(want)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != MultimodalMediaHeaderBytes+len(want.TrackID)+len(want.Payload) || string(encoded[:4]) != "TMAM" || binary.BigEndian.Uint64(encoded[8:16]) != want.Sequence {
		t.Fatalf("unexpected wire frame: %x", encoded)
	}
	got, err := DecodeMultimodalMediaFrame(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != want.Kind || got.Flags != want.Flags || got.Sequence != want.Sequence || got.TimestampMicros != want.TimestampMicros || got.TrackID != want.TrackID || string(got.Payload) != string(want.Payload) {
		t.Fatalf("round trip mismatch: got=%+v want=%+v", got, want)
	}
	encoded[len(encoded)-1] = 'X'
	if string(got.Payload) != "encoded-frame" {
		t.Fatal("decoded payload must not alias the input buffer")
	}
}

func TestMultimodalMediaFrameRejectsMalformedInput(t *testing.T) {
	valid, err := EncodeMultimodalMediaFrame(MultimodalMediaFrame{
		Kind: MultimodalMediaImage, Sequence: 1, TimestampMicros: 0, TrackID: "still", Payload: []byte("jpeg"),
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := [][]byte{
		valid[:10],
		append([]byte("FAIL"), valid[4:]...),
		func() []byte { value := append([]byte(nil), valid...); value[4] = 2; return value }(),
		func() []byte { value := append([]byte(nil), valid...); value[5] = 99; return value }(),
		func() []byte { value := append([]byte(nil), valid...); value[27] = 1; return value }(),
	}
	for _, encoded := range tests {
		if _, err := DecodeMultimodalMediaFrame(encoded); !errors.Is(err, ErrMultimodalInvalidFrame) {
			t.Fatalf("expected invalid frame for %x, got %v", encoded, err)
		}
	}
}

func TestMultimodalCreditWindowEnforcesCreditsAndSequence(t *testing.T) {
	limits := MultimodalFlowLimits{MaxFrameBytes: 1024, MaxInFlightBytes: 2048, MaxInFlightFrames: 2}
	window, err := NewMultimodalCreditWindow(limits, MultimodalFlowCredit{Bytes: 1500, Frames: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := window.Reserve("camera", 10, 1000); err != nil {
		t.Fatal(err)
	}
	if snapshot := window.Snapshot(); snapshot.Bytes != 500 || snapshot.Frames != 1 {
		t.Fatalf("unexpected credit after reserve: %+v", snapshot)
	}
	if err := window.Reserve("camera", 11, 600); !errors.Is(err, ErrMultimodalFlowControl) {
		t.Fatalf("expected byte credit rejection, got %v", err)
	}
	if err := window.Grant(MultimodalFlowCredit{Bytes: 1000, Frames: 1, TrackID: "camera", AcknowledgedSequence: 10}); err != nil {
		t.Fatal(err)
	}
	if err := window.Reserve("camera", 11, 600); err != nil {
		t.Fatal(err)
	}
	if err := window.Reserve("camera", 11, 1); !errors.Is(err, ErrMultimodalSequence) {
		t.Fatalf("expected sequence rejection, got %v", err)
	}
	if err := window.Grant(MultimodalFlowCredit{Bytes: 2000, Frames: 2}); !errors.Is(err, ErrMultimodalFlowControl) {
		t.Fatalf("expected credit overflow rejection, got %v", err)
	}
}

func validMultimodalSessionStart() MultimodalSessionStart {
	outputLimits := DefaultMultimodalFlowLimits()
	outputCredit := MultimodalFlowCredit{Bytes: 4 << 20, Frames: 2}
	return MultimodalSessionStart{
		Type: "session.start", ProtocolVersion: MultimodalRealtimeProtocolVersion,
		ProviderID: "provider", Model: "realtime-model", OutputModalities: []string{"text", "audio"},
		OutputFlowLimits:    &outputLimits,
		InitialOutputCredit: &outputCredit,
		InputTracks: []MultimodalTrack{
			{ID: "microphone", Kind: MultimodalMediaAudio, ContentType: "audio/pcm", Codec: "pcm_s16le", Delivery: MultimodalDeliveryReliable, SampleRateHz: 16000, Channels: 1},
			{ID: "camera", Kind: MultimodalMediaVideo, ContentType: "video/h264", Codec: "h264", Delivery: MultimodalDeliveryLatest, Width: 1280, Height: 720, MaxFPS: 30},
		},
	}
}
