//go:build unix

package capability

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestGuardedReadPinsParentDirectoryDuringOpen(t *testing.T) {
	root := t.TempDir()
	guardedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	safeDir := filepath.Join(root, "safe")
	if err := os.Mkdir(safeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	insideTarget := filepath.Join(safeDir, "note.txt")
	if err := os.WriteFile(insideTarget, []byte("inside content"), 0o644); err != nil {
		t.Fatal(err)
	}
	outsideTarget := filepath.Join(outside, "note.txt")
	if err := os.WriteFile(outsideTarget, []byte("outside secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	request := ReadFileRequest{
		Path: filepath.Join(guardedRoot, "safe", "note.txt"), guardedRoot: guardedRoot,
	}
	var swapErr error
	result, err := readLocalFileWithOpenHook(t.Context(), request, DefaultReadFileLimits(), func() {
		if swapErr = os.Rename(safeDir, safeDir+"-original"); swapErr != nil {
			return
		}
		swapErr = os.Symlink(outside, safeDir)
	})
	if swapErr != nil {
		t.Fatalf("swap parent directory: %v", swapErr)
	}
	if err != nil {
		t.Fatalf("read through pinned parent directory: %v", err)
	}
	if string(result.Content) != "inside content" || result.ContentSHA256 != contentSHA256([]byte("inside content")) {
		t.Fatalf("guarded read returned the wrong file: %#v", result)
	}
	assertFileContent(t, outsideTarget, "outside secret")
	assertFileContent(t, filepath.Join(safeDir+"-original", "note.txt"), "inside content")
}

func TestGuardedReadRejectsSymlinkComponents(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	realTarget := filepath.Join(realDir, "note.txt")
	if err := os.WriteFile(realTarget, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realTarget, filepath.Join(root, "target-link.txt")); err != nil {
		t.Fatal(err)
	}
	provider, err := NewWorkspacePathGuardProvider(LocalSystemProvider{}, root)
	if err != nil {
		t.Fatal(err)
	}

	_, err = provider.ReadFile(t.Context(), ReadFileRequest{Path: "alias/note.txt"})
	assertFileReadError(t, err, "workspace_path_changed", "alias/note.txt")
	_, err = provider.ReadFile(t.Context(), ReadFileRequest{Path: "target-link.txt"})
	assertFileReadError(t, err, "unsupported_file_type", "target-link.txt")
}

func TestGuardedReadRejectsNamedPipeWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	pipePath := filepath.Join(root, "events.pipe")
	if err := unix.Mkfifo(pipePath, 0o600); err != nil {
		t.Fatal(err)
	}
	provider, err := NewWorkspacePathGuardProvider(LocalSystemProvider{}, root)
	if err != nil {
		t.Fatal(err)
	}

	_, err = provider.ReadFile(t.Context(), ReadFileRequest{Path: "events.pipe"})
	assertFileReadError(t, err, "unsupported_file_type", "events.pipe")
}

func assertFileReadError(t *testing.T, err error, code, path string) {
	t.Helper()
	var readErr *FileReadError
	if !errors.As(err, &readErr) || readErr.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
	if got := readErr.Metadata["path"]; got != path {
		t.Fatalf("expected display path %q, got %#v", path, got)
	}
}
