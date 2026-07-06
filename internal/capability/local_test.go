package capability

import (
	"context"
	"path/filepath"
	"testing"
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
