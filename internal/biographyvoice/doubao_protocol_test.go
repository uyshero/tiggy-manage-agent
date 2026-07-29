package biographyvoice

import (
	"bytes"
	"testing"
)

func TestDoubaoProtocolRoundTripsEventAndGzipPayload(t *testing.T) {
	original := doubaoFrame{
		MessageType: doubaoMessageFullServer, Flags: doubaoFlagWithEvent,
		Serialization: doubaoSerializationJSON, Compression: doubaoCompressionGzip,
		HasEvent: true, Event: doubaoEventSessionStarted, EventID: "session-1",
		Payload: []byte(`{"status_code":20000000}`),
	}
	encoded, err := buildDoubaoFrame(original)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseDoubaoFrame(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Event != original.Event || parsed.EventID != original.EventID || !bytes.Equal(parsed.Payload, original.Payload) {
		t.Fatalf("unexpected parsed frame: %+v", parsed)
	}
}

func TestDoubaoProtocolBuildsFinalASRAudioWithoutSequenceField(t *testing.T) {
	encoded, err := buildDoubaoASRAudio(nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != 8 || encoded[1]&0x0F != doubaoFlagLastNoSequence {
		t.Fatalf("unexpected final audio frame: %x", encoded)
	}
}

func TestDoubaoProtocolCarriesSessionIDOnStartSession(t *testing.T) {
	encoded, err := buildDoubaoTTSSessionEvent("session-1", doubaoEventStartSession, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseDoubaoFrame(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Event != doubaoEventStartSession || parsed.EventID != "session-1" {
		t.Fatalf("unexpected session frame: %+v", parsed)
	}
}

func TestDoubaoProtocolRejectsTruncatedPayload(t *testing.T) {
	_, err := parseDoubaoFrame([]byte{0x11, 0x90, 0x10, 0x00, 0x00, 0x00, 0x00, 0x10})
	if err == nil {
		t.Fatal("expected truncated payload error")
	}
}
