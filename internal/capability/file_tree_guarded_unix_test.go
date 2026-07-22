//go:build unix

package capability

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestGuardedDiscoveryPinsRootDirectory(t *testing.T) {
	root := t.TempDir()
	guardedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	safeDir := filepath.Join(root, "safe")
	writeTestFile(t, filepath.Join(safeDir, "inside.go"), "package inside\n")
	writeTestFile(t, filepath.Join(outside, "outside.go"), "package outside\n")

	var swapErr error
	candidates, _, _, err := discoverLocalFiles(
		t.Context(), filepath.Join(guardedRoot, "safe"), []string{"**/*.go"}, nil, false,
		hardFindFilesScanned, 10, "", guardedRoot, func() {
			if swapErr = os.Rename(safeDir, safeDir+"-original"); swapErr != nil {
				return
			}
			swapErr = os.Symlink(outside, safeDir)
		},
	)
	if swapErr != nil {
		t.Fatalf("swap discovery root: %v", swapErr)
	}
	if err != nil {
		t.Fatalf("discover through pinned root: %v", err)
	}
	if len(candidates) != 1 || candidates[0].relative != "inside.go" {
		t.Fatalf("guarded discovery traversed the replacement directory: %#v", candidates)
	}
}

func TestGuardedFindRejectsRootChangedBeforeCandidateOpen(t *testing.T) {
	_, guardedRoot, safeDir, outside := guardedTreeSwapFixture(t)
	request := FindFilesRequest{
		Root: filepath.Join(guardedRoot, "safe"), Pattern: "inside.go", guardedRoot: guardedRoot,
	}
	var swapErr error
	_, err := findLocalFilesWithDiscoveryHook(t.Context(), request, func() {
		swapErr = swapTreeRoot(safeDir, outside)
	})
	if swapErr != nil {
		t.Fatal(swapErr)
	}
	assertFileReadErrorCode(t, err, "workspace_path_changed")
	assertFileContent(t, filepath.Join(outside, "outside.go"), "package outside\n// needle\n")
}

func TestGuardedSearchFilesRejectsRootChangedBeforeCandidateOpen(t *testing.T) {
	_, guardedRoot, safeDir, outside := guardedTreeSwapFixture(t)
	request := SearchFilesRequest{
		Root: filepath.Join(guardedRoot, "safe"), Query: "needle", Paths: []string{"inside.go"}, guardedRoot: guardedRoot,
	}
	var swapErr error
	_, err := searchLocalFilesWithDiscoveryHook(t.Context(), request, func() {
		swapErr = swapTreeRoot(safeDir, outside)
	})
	if swapErr != nil {
		t.Fatal(swapErr)
	}
	assertFileReadErrorCode(t, err, "workspace_path_changed")
	assertFileContent(t, filepath.Join(outside, "outside.go"), "package outside\n// needle\n")
}

func TestGuardedTreeOperationsSkipLinksAndSpecialFiles(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	writeTestFile(t, filepath.Join(realDir, "note.txt"), "needle\n")
	if err := os.Symlink(realDir, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(root, "events.pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider, err := NewWorkspacePathGuardProvider(LocalSystemProvider{}, root)
	if err != nil {
		t.Fatal(err)
	}

	found, err := provider.FindFiles(t.Context(), FindFilesRequest{Pattern: "**/*"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Files) != 1 || found.Files[0].Path != "real/note.txt" {
		t.Fatalf("unexpected guarded discovery result: %#v", found)
	}
	searched, err := provider.SearchFiles(t.Context(), SearchFilesRequest{
		Query: "needle", Paths: []string{"**/*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(searched.Matches) != 1 || searched.Matches[0].Path != "real/note.txt" {
		t.Fatalf("unexpected guarded tree search result: %#v", searched)
	}
	_, err = provider.FindFiles(t.Context(), FindFilesRequest{Root: "alias", Pattern: "**/*"})
	assertFileReadError(t, err, "workspace_path_changed", "alias")
}

func guardedTreeSwapFixture(t *testing.T) (root, guardedRoot, safeDir, outside string) {
	t.Helper()
	root = t.TempDir()
	var err error
	guardedRoot, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	outside = t.TempDir()
	safeDir = filepath.Join(root, "safe")
	writeTestFile(t, filepath.Join(safeDir, "inside.go"), "package inside\n// needle\n")
	writeTestFile(t, filepath.Join(outside, "outside.go"), "package outside\n// needle\n")
	return root, guardedRoot, safeDir, outside
}

func swapTreeRoot(safeDir, outside string) error {
	if err := os.Rename(safeDir, safeDir+"-original"); err != nil {
		return err
	}
	return os.Symlink(outside, safeDir)
}

func assertFileReadErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var readErr *FileReadError
	if !errors.As(err, &readErr) || readErr.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}
