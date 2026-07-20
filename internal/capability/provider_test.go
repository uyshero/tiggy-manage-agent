package capability

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestNewRequestMetaSetsProtocolVersion(t *testing.T) {
	deadline := time.Date(2026, 7, 6, 8, 0, 0, 0, time.UTC)

	meta := NewRequestMeta("sesn_000001", "turn_000001", &deadline)

	if meta.ProtocolVersion != ProtocolVersion {
		t.Fatalf("expected protocol version %q, got %q", ProtocolVersion, meta.ProtocolVersion)
	}
	if meta.SessionID != "sesn_000001" {
		t.Fatalf("expected session id, got %q", meta.SessionID)
	}
	if meta.TurnID != "turn_000001" {
		t.Fatalf("expected turn id, got %q", meta.TurnID)
	}
	if meta.Deadline == nil || !meta.Deadline.Equal(deadline) {
		t.Fatalf("expected deadline %s, got %#v", deadline, meta.Deadline)
	}
}

var _ Provider = fakeProvider{}

type fakeProvider struct{}

func (fakeProvider) RunCommand(context.Context, RunCommandRequest) (CommandResult, error) {
	return CommandResult{}, nil
}

func (fakeProvider) ExecuteCode(context.Context, ExecuteCodeRequest) (CommandResult, error) {
	return CommandResult{}, nil
}

func (fakeProvider) ReadFile(context.Context, ReadFileRequest) (FileResult, error) {
	return FileResult{}, nil
}

func (fakeProvider) WriteFile(context.Context, WriteFileRequest) (FileResult, error) {
	return FileResult{}, nil
}

func (fakeProvider) EditFile(context.Context, EditFileRequest) (EditFileResult, error) {
	return EditFileResult{}, nil
}

func TestRunCommandRequestJSONAcceptsPlainTextStdin(t *testing.T) {
	var request RunCommandRequest
	if err := json.Unmarshal([]byte(`{"command":"sh","stdin":"echo hello\n"}`), &request); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if request.Command != "sh" {
		t.Fatalf("expected command to be preserved, got %q", request.Command)
	}
	if string(request.Stdin) != "echo hello\n" {
		t.Fatalf("expected plain stdin, got %q", string(request.Stdin))
	}
}

func TestRunCommandRequestJSONPreservesExecutionLimits(t *testing.T) {
	original := RunCommandRequest{
		Command:        "sh",
		Args:           []string{"-c", "printf ok"},
		TimeoutMS:      2500,
		MaxOutputBytes: 32768,
	}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded RunCommandRequest
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.TimeoutMS != original.TimeoutMS || decoded.MaxOutputBytes != original.MaxOutputBytes {
		t.Fatalf("execution limits were not preserved: %#v", decoded)
	}
}

func TestWriteFileRequestJSONAcceptsPlainTextContent(t *testing.T) {
	var request WriteFileRequest
	if err := json.Unmarshal([]byte(`{"path":"script.sh","content":"#!/bin/sh\necho hello\n"}`), &request); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if request.Path != "script.sh" {
		t.Fatalf("expected path to be preserved, got %q", request.Path)
	}
	if string(request.Content) != "#!/bin/sh\necho hello\n" {
		t.Fatalf("expected plain content, got %q", string(request.Content))
	}
}

func TestWriteFileRequestJSONRoundTripsBinaryContent(t *testing.T) {
	original := WriteFileRequest{
		Path:    "artifact.bin",
		Content: []byte{0xff, 0x00, 0x01, 'A'},
	}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if bytes.Contains(encoded, []byte(`"content":"`)) {
		t.Fatalf("expected binary payload to use content_base64, got %s", string(encoded))
	}
	if !bytes.Contains(encoded, []byte(`"content_base64":"`)) {
		t.Fatalf("expected binary payload to include content_base64, got %s", string(encoded))
	}
	var decoded WriteFileRequest
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("round-trip request: %v", err)
	}
	if !bytes.Equal(decoded.Content, original.Content) {
		t.Fatalf("expected binary content to survive round-trip, got %#v", decoded.Content)
	}
}
