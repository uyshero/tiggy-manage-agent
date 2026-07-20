package capability

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalSystemProviderRunCommandWithStdin(t *testing.T) {
	provider := LocalSystemProvider{}

	result, err := provider.RunCommand(context.Background(), RunCommandRequest{
		Command: "sh",
		Args:    []string{"-c", "cat"},
		Stdin:   []byte("hello local provider"),
	})
	if err != nil {
		t.Fatalf("run command: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
	if result.Stdout != "hello local provider" {
		t.Fatalf("expected stdout from stdin, got %q", result.Stdout)
	}
}

func TestLocalSystemProviderRunCommandReturnsExitCode(t *testing.T) {
	provider := LocalSystemProvider{}

	result, err := provider.RunCommand(context.Background(), RunCommandRequest{
		Command: "sh",
		Args:    []string{"-c", "echo failed >&2; exit 7"},
	})
	if err != nil {
		t.Fatalf("run command: %v", err)
	}
	if result.ExitCode != 7 {
		t.Fatalf("expected exit code 7, got %d", result.ExitCode)
	}
	if result.Stderr != "failed\n" {
		t.Fatalf("expected stderr, got %q", result.Stderr)
	}
}

func TestLocalSystemProviderRunCommandBoundsStdoutAndStderr(t *testing.T) {
	provider := LocalSystemProvider{}
	result, err := provider.RunCommand(t.Context(), RunCommandRequest{
		Command:        "sh",
		Args:           []string{"-c", `i=0; while [ "$i" -lt 4096 ]; do printf x; printf y >&2; i=$((i + 1)); done`},
		MaxOutputBytes: 1024,
	})
	if err != nil {
		t.Fatalf("run command: %v", err)
	}
	if result.Stdout != strings.Repeat("x", 1024) || result.Stderr != strings.Repeat("y", 1024) {
		t.Fatalf("unexpected captured output lengths stdout=%d stderr=%d", len(result.Stdout), len(result.Stderr))
	}
	if result.StdoutBytes != 4096 || result.StderrBytes != 4096 || result.StdoutCapturedBytes != 1024 || result.StderrCapturedBytes != 1024 {
		t.Fatalf("unexpected output byte metadata: %#v", result)
	}
	if !result.StdoutTruncated || !result.StderrTruncated {
		t.Fatalf("expected both output streams to be truncated: %#v", result)
	}
}

func TestLocalSystemProviderRunCommandAppliesRequestTimeout(t *testing.T) {
	provider := LocalSystemProvider{}
	startedAt := time.Now()
	result, err := provider.RunCommand(t.Context(), RunCommandRequest{
		Command:   "sh",
		Args:      []string{"-c", "sleep 30"},
		TimeoutMS: 100,
	})
	if err != nil {
		t.Fatalf("run command: %v", err)
	}
	if !result.TimedOut || result.Status != "timeout" || result.ExitCode == 0 {
		t.Fatalf("expected structured timeout result: %#v", result)
	}
	if elapsed := time.Since(startedAt); elapsed > 3*time.Second {
		t.Fatalf("timed out command took too long: %s", elapsed)
	}
}

func TestLocalSystemProviderRunCommandRejectsInvalidLimits(t *testing.T) {
	provider := LocalSystemProvider{}
	for _, request := range []RunCommandRequest{
		{Command: "true", TimeoutMS: 99},
		{Command: "true", TimeoutMS: int(MaxRunCommandTimeout.Milliseconds()) + 1},
		{Command: "true", MaxOutputBytes: 1023},
		{Command: "true", MaxOutputBytes: MaxCommandOutputBytes + 1},
	} {
		if _, err := provider.RunCommand(t.Context(), request); err == nil {
			t.Fatalf("expected invalid limits to fail: %#v", request)
		}
	}
}

func TestLocalSystemProviderReadWriteFile(t *testing.T) {
	provider := LocalSystemProvider{}
	path := filepath.Join(t.TempDir(), "note.txt")

	if _, err := provider.WriteFile(context.Background(), WriteFileRequest{
		Path:    path,
		Content: []byte("hello file"),
	}); err != nil {
		t.Fatalf("write file: %v", err)
	}

	result, err := provider.ReadFile(context.Background(), ReadFileRequest{Path: path})
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(result.Content) != "hello file" {
		t.Fatalf("expected file content, got %q", string(result.Content))
	}
}

func TestLocalSystemProviderWriteFileCreatesParentDirs(t *testing.T) {
	provider := LocalSystemProvider{}
	path := filepath.Join(t.TempDir(), "nested", "reports", "note.txt")

	if _, err := provider.WriteFile(context.Background(), WriteFileRequest{
		Path:    path,
		Content: []byte("hello nested file"),
	}); err != nil {
		t.Fatalf("write nested file: %v", err)
	}

	result, err := provider.ReadFile(context.Background(), ReadFileRequest{Path: path})
	if err != nil {
		t.Fatalf("read nested file: %v", err)
	}
	if string(result.Content) != "hello nested file" {
		t.Fatalf("expected nested file content, got %q", string(result.Content))
	}
}

func TestLocalSystemProviderExecuteShellCode(t *testing.T) {
	provider := LocalSystemProvider{}

	result, err := provider.ExecuteCode(context.Background(), ExecuteCodeRequest{
		Language: "sh",
		Code:     "printf shell-code",
	})
	if err != nil {
		t.Fatalf("execute code: %v", err)
	}
	if result.Stdout != "shell-code" {
		t.Fatalf("expected shell code output, got %q", result.Stdout)
	}
}
