//go:build unix

package capability

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestGuardedEditPinsParentDirectoryDuringOpen(t *testing.T) {
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
	if err := os.WriteFile(insideTarget, []byte("inside old"), 0o644); err != nil {
		t.Fatal(err)
	}
	outsideTarget := filepath.Join(outside, "note.txt")
	if err := os.WriteFile(outsideTarget, []byte("outside untouched"), 0o644); err != nil {
		t.Fatal(err)
	}

	request := EditFileRequest{
		Path: filepath.Join(guardedRoot, "safe", "note.txt"), OldString: "old", NewString: "new", guardedRoot: guardedRoot,
	}
	var swapErr error
	result := editLocalFileContextWithOpenHook(t.Context(), request, func() {
		if swapErr = os.Rename(safeDir, safeDir+"-original"); swapErr != nil {
			return
		}
		swapErr = os.Symlink(outside, safeDir)
	})
	if swapErr != nil {
		t.Fatalf("swap parent directory: %v", swapErr)
	}
	if result.Success || result.Code != "workspace_path_changed" {
		t.Fatalf("expected write phase to reject the changed public path after a pinned read, got %#v", result)
	}
	assertFileContent(t, outsideTarget, "outside untouched")
	assertFileContent(t, filepath.Join(safeDir+"-original", "note.txt"), "inside old")
}

func TestGuardedEditRejectsSymlinkComponents(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	realTarget := filepath.Join(realDir, "note.txt")
	if err := os.WriteFile(realTarget, []byte("old value"), 0o644); err != nil {
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

	result, err := provider.EditFile(t.Context(), EditFileRequest{
		Path: "alias/note.txt", OldString: "old", NewString: "new",
	})
	if err != nil || result.Code != "workspace_path_changed" || result.Success {
		t.Fatalf("expected symlink parent rejection, result=%#v err=%v", result, err)
	}
	result, err = provider.EditFile(t.Context(), EditFileRequest{
		Path: "target-link.txt", OldString: "old", NewString: "new",
	})
	if err != nil || result.Code != "unsupported_file_type" || result.Success {
		t.Fatalf("expected symlink target rejection, result=%#v err=%v", result, err)
	}
	assertFileContent(t, realTarget, "old value")
}

func TestGuardedEditRejectsNamedPipeWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	pipePath := filepath.Join(root, "events.pipe")
	if err := unix.Mkfifo(pipePath, 0o600); err != nil {
		t.Fatal(err)
	}
	provider, err := NewWorkspacePathGuardProvider(LocalSystemProvider{}, root)
	if err != nil {
		t.Fatal(err)
	}

	result, err := provider.EditFile(t.Context(), EditFileRequest{
		Path: "events.pipe", OldString: "old", NewString: "new",
	})
	if err != nil || result.Code != "unsupported_file_type" || result.Success {
		t.Fatalf("expected named pipe rejection, result=%#v err=%v", result, err)
	}
}
