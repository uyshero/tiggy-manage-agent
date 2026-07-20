package capability

import (
	"context"
	"errors"
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

func TestWorkspacePathGuardProviderSearchFileKeepsReadBoundary(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.log"), []byte("first\nfind me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.log"), []byte("find me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside-link")); err != nil {
		t.Fatal(err)
	}
	provider, err := NewWorkspacePathGuardProvider(LocalSystemProvider{}, root)
	if err != nil {
		t.Fatal(err)
	}

	result, err := provider.SearchFile(t.Context(), SearchFileRequest{Path: "app.log", Query: "find me"})
	if err != nil {
		t.Fatalf("search workspace file: %v", err)
	}
	if len(result.Matches) != 1 || result.Matches[0].LineNumber != 2 {
		t.Fatalf("unexpected search result: %#v", result)
	}
	if _, err := provider.SearchFile(t.Context(), SearchFileRequest{Path: "../secret.log", Query: "find me"}); err == nil {
		t.Fatal("expected parent path search to be denied")
	}
	if _, err := provider.SearchFile(t.Context(), SearchFileRequest{Path: "outside-link/secret.log", Query: "find me"}); err == nil {
		t.Fatal("expected symlink escape search to be denied")
	}
}

func TestWorkspacePathGuardProviderFindAndSearchFilesStayInsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main\n// needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.go"), []byte("// needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside-link")); err != nil {
		t.Fatal(err)
	}
	provider, err := NewWorkspacePathGuardProvider(LocalSystemProvider{}, root)
	if err != nil {
		t.Fatal(err)
	}
	found, err := provider.FindFiles(t.Context(), FindFilesRequest{Pattern: "**/*.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Files) != 1 || found.Files[0].Path != "src/main.go" {
		t.Fatalf("unexpected guarded discovery: %#v", found)
	}
	searched, err := provider.SearchFiles(t.Context(), SearchFilesRequest{Query: "needle", Paths: []string{"**/*.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(searched.Matches) != 1 || searched.Matches[0].Path != "src/main.go" {
		t.Fatalf("unexpected guarded search: %#v", searched)
	}
	if _, err := provider.FindFiles(t.Context(), FindFilesRequest{Root: "../", Pattern: "**/*"}); err == nil {
		t.Fatal("expected escaped discovery root to be denied")
	}
	if _, err := provider.SearchFiles(t.Context(), SearchFilesRequest{Root: "outside-link", Query: "needle", Paths: []string{"**/*"}}); err == nil {
		t.Fatal("expected symlink search root to be denied")
	}
}

func TestWorkspacePathGuardProviderRemapsStructuredSearchErrorPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.log"), []byte("value\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	provider, err := NewWorkspacePathGuardProvider(LocalSystemProvider{}, root)
	if err != nil {
		t.Fatal(err)
	}

	_, err = provider.SearchFile(t.Context(), SearchFileRequest{
		Path: "app.log", Query: "value", FileRevision: "stat-v1:stale",
	})
	var readErr *FileReadError
	if !errors.As(err, &readErr) || readErr.Code != "stale_file_revision" {
		t.Fatalf("expected stale_file_revision, got %v", err)
	}
	if got := readErr.Metadata["path"]; got != "app.log" {
		t.Fatalf("expected display path in structured error, got %#v", got)
	}
	if strings.Contains(readErr.Error(), root) {
		t.Fatalf("structured error leaked workspace root: %v", readErr)
	}
}

func TestWorkspacePathGuardProviderDeniesSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "outside-link")); err != nil {
		t.Fatalf("create escape symlink: %v", err)
	}
	provider, err := NewWorkspacePathGuardProvider(LocalSystemProvider{}, root)
	if err != nil {
		t.Fatalf("new workspace path guard provider: %v", err)
	}

	if _, err := provider.WriteFile(context.Background(), WriteFileRequest{Path: "outside-link/secret.txt", Content: []byte("nope")}); err == nil {
		t.Fatal("expected symlink escape write to be denied")
	}
	if _, err := os.Stat(filepath.Join(outside, "secret.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected no file outside workspace, err=%v", err)
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
