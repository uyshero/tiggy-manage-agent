//go:build unix

package capability

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestGuardedSearchPinsParentDirectoryDuringOpen(t *testing.T) {
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
	insideTarget := filepath.Join(safeDir, "app.log")
	if err := os.WriteFile(insideTarget, []byte("inside needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outsideTarget := filepath.Join(outside, "app.log")
	if err := os.WriteFile(outsideTarget, []byte("outside needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	request := SearchFileRequest{
		Path: filepath.Join(guardedRoot, "safe", "app.log"), Query: "needle", guardedRoot: guardedRoot,
	}
	var swapErr error
	result, err := searchLocalFileWithOpenHook(t.Context(), request, func() {
		if swapErr = os.Rename(safeDir, safeDir+"-original"); swapErr != nil {
			return
		}
		swapErr = os.Symlink(outside, safeDir)
	})
	if swapErr != nil {
		t.Fatalf("swap parent directory: %v", swapErr)
	}
	if err != nil {
		t.Fatalf("search through pinned parent directory: %v", err)
	}
	if len(result.Matches) != 1 || result.Matches[0].Line != "inside needle" {
		t.Fatalf("guarded search returned the wrong file: %#v", result)
	}
	assertFileContent(t, outsideTarget, "outside needle\n")
	assertFileContent(t, filepath.Join(safeDir+"-original", "app.log"), "inside needle\n")
}

func TestGuardedSearchRejectsSymlinkComponents(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	realTarget := filepath.Join(realDir, "app.log")
	if err := os.WriteFile(realTarget, []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realTarget, filepath.Join(root, "target-link.log")); err != nil {
		t.Fatal(err)
	}
	provider, err := NewWorkspacePathGuardProvider(LocalSystemProvider{}, root)
	if err != nil {
		t.Fatal(err)
	}

	_, err = provider.SearchFile(t.Context(), SearchFileRequest{Path: "alias/app.log", Query: "needle"})
	assertFileReadError(t, err, "workspace_path_changed", "alias/app.log")
	_, err = provider.SearchFile(t.Context(), SearchFileRequest{Path: "target-link.log", Query: "needle"})
	assertFileReadError(t, err, "unsupported_file_type", "target-link.log")
}

func TestGuardedSearchRejectsNamedPipeWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	pipePath := filepath.Join(root, "events.pipe")
	if err := unix.Mkfifo(pipePath, 0o600); err != nil {
		t.Fatal(err)
	}
	provider, err := NewWorkspacePathGuardProvider(LocalSystemProvider{}, root)
	if err != nil {
		t.Fatal(err)
	}

	_, err = provider.SearchFile(t.Context(), SearchFileRequest{Path: "events.pipe", Query: "needle"})
	assertFileReadError(t, err, "unsupported_file_type", "events.pipe")
}
