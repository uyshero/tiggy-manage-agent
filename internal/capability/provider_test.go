package capability

import (
	"context"
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
