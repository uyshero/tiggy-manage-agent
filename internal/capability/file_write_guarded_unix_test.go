//go:build unix

package capability

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGuardedAtomicWritePinsParentDirectoryDuringCommit(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	safeDir := filepath.Join(root, "safe")
	if err := os.Mkdir(safeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(safeDir, "note.txt")
	if err := os.WriteFile(target, []byte("inside old"), 0o640); err != nil {
		t.Fatal(err)
	}
	outsideTarget := filepath.Join(outside, "note.txt")
	if err := os.WriteFile(outsideTarget, []byte("outside old"), 0o644); err != nil {
		t.Fatal(err)
	}

	request := WriteFileRequest{
		Path: target, Content: []byte("inside new"), Mode: WriteModeOverwrite, guardedRoot: root,
	}
	options, err := validateLocalFileWrite(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	var swapErr error
	result, err := writeLocalFileAtomicGuardedWithHook(t.Context(), request, options, func() {
		if swapErr = os.Rename(safeDir, safeDir+"-original"); swapErr != nil {
			return
		}
		swapErr = os.Symlink(outside, safeDir)
	})
	if swapErr != nil {
		t.Fatalf("swap parent directory: %v", swapErr)
	}
	if err != nil {
		t.Fatalf("guarded write through pinned directory: %v", err)
	}
	if result.FileRevision == "" || result.ContentSHA256 == "" {
		t.Fatalf("missing committed file metadata: %#v", result)
	}
	assertFileContent(t, outsideTarget, "outside old")
	assertFileContent(t, filepath.Join(safeDir+"-original", "note.txt"), "inside new")
	info, err := os.Stat(filepath.Join(safeDir+"-original", "note.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("expected original permissions to be preserved, got %o", info.Mode().Perm())
	}
}

func TestGuardedAtomicWriteRejectsSymlinkComponents(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	realTarget := filepath.Join(realDir, "note.txt")
	if err := os.WriteFile(realTarget, []byte("original"), 0o644); err != nil {
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

	if _, err := provider.WriteFile(t.Context(), WriteFileRequest{
		Path: "alias/note.txt", Content: []byte("via parent link"), Mode: WriteModeOverwrite,
	}); fileErrorCode(err) != "workspace_path_changed" {
		t.Fatalf("expected workspace_path_changed for symlink parent, got %v", err)
	}
	if _, err := provider.WriteFile(t.Context(), WriteFileRequest{
		Path: "target-link.txt", Content: []byte("via target link"), Mode: WriteModeOverwrite,
	}); fileErrorCode(err) != "unsupported_file_type" {
		t.Fatalf("expected unsupported_file_type for symlink target, got %v", err)
	}
	assertFileContent(t, realTarget, "original")
}

func TestGuardedAtomicWriteCreatesMissingParents(t *testing.T) {
	root := t.TempDir()
	provider, err := NewWorkspacePathGuardProvider(LocalSystemProvider{}, root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.WriteFile(t.Context(), WriteFileRequest{
		Path: "nested/deeper/note.txt", Content: []byte("created"), Mode: WriteModeCreate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FileRevision == "" {
		t.Fatalf("missing file revision: %#v", result)
	}
	assertFileContent(t, filepath.Join(root, "nested", "deeper", "note.txt"), "created")
}

func assertFileContent(t *testing.T, path, expected string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(content) != expected {
		t.Fatalf("unexpected content in %s: got %q want %q", path, content, expected)
	}
}
