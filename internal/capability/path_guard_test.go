package capability

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspacePathGuardProviderReadWriteWithinRoot(t *testing.T) {
	root := t.TempDir()
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}
	provider, err := NewWorkspacePathGuardProvider(LocalSystemProvider{}, root)
	if err != nil {
		t.Fatalf("new workspace path guard provider: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatalf("make notes dir: %v", err)
	}

	if _, err := provider.WriteFile(context.Background(), WriteFileRequest{Path: "notes/out.txt", Content: []byte("hello")}); err != nil {
		t.Fatalf("write file: %v", err)
	}
	result, err := provider.ReadFile(context.Background(), ReadFileRequest{Path: "notes/out.txt"})
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(result.Content) != "hello" {
		t.Fatalf("unexpected content: %q", string(result.Content))
	}
	if !strings.HasPrefix(result.Path, resolvedRoot) {
		t.Fatalf("expected resolved path inside root, got %q", result.Path)
	}
}

func TestWorkspacePathGuardProviderDeniesPathEscape(t *testing.T) {
	root := t.TempDir()
	provider, err := NewWorkspacePathGuardProvider(LocalSystemProvider{}, root)
	if err != nil {
		t.Fatalf("new workspace path guard provider: %v", err)
	}

	if _, err := provider.ReadFile(context.Background(), ReadFileRequest{Path: "../outside.txt"}); err == nil {
		t.Fatal("expected parent path read to be denied")
	}
	if _, err := provider.WriteFile(context.Background(), WriteFileRequest{Path: filepath.Join(filepath.Dir(root), "outside.txt"), Content: []byte("nope")}); err == nil {
		t.Fatal("expected absolute outside write to be denied")
	}
}

func TestWorkspacePathGuardProviderRunsCommandFromWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "marker.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	provider, err := NewWorkspacePathGuardProvider(LocalSystemProvider{}, root)
	if err != nil {
		t.Fatalf("new workspace path guard provider: %v", err)
	}

	result, err := provider.RunCommand(context.Background(), RunCommandRequest{
		Command: "sh",
		Args:    []string{"-c", "pwd && cat marker.txt"},
	})
	if err != nil {
		t.Fatalf("run command: %v", err)
	}
	if result.ExitCode != 0 || !strings.Contains(result.Stdout, root) || !strings.Contains(result.Stdout, "ok") {
		t.Fatalf("unexpected command result: %#v", result)
	}

	_, err = provider.RunCommand(context.Background(), RunCommandRequest{
		Command: "sh",
		Args:    []string{"-c", "pwd"},
		WorkDir: "../",
	})
	if err == nil {
		t.Fatal("expected escaped work_dir to be denied")
	}
}

func TestWorkspacePathGuardProviderEditFileWithinRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	provider, err := NewWorkspacePathGuardProvider(LocalSystemProvider{}, root)
	if err != nil {
		t.Fatalf("new workspace path guard provider: %v", err)
	}

	result, err := provider.EditFile(context.Background(), EditFileRequest{
		Path:      "file.txt",
		OldString: "world",
		NewString: "sandbox",
	})
	if err != nil {
		t.Fatalf("edit file: %v", err)
	}
	if !result.Success || result.Replacements != 1 {
		t.Fatalf("unexpected edit result: %#v", result)
	}
	content, err := os.ReadFile(filepath.Join(root, "file.txt"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(content) != "hello sandbox\n" {
		t.Fatalf("unexpected content: %q", string(content))
	}
}
