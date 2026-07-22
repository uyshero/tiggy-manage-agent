//go:build unix

package capability

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestGuardedArtifactExportPinsParentDirectoryDuringOpen(t *testing.T) {
	root := t.TempDir()
	guardedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	safeDir := filepath.Join(root, "safe")
	writeTestFile(t, filepath.Join(safeDir, "report.txt"), "inside artifact")
	writeTestFile(t, filepath.Join(outside, "report.txt"), "outside secret")

	request := ExportArtifactFileRequest{
		Path: filepath.Join(guardedRoot, "safe", "report.txt"), guardedRoot: guardedRoot,
	}
	var swapErr error
	result, err := exportLocalArtifactFile(t.Context(), request, func() {
		if swapErr = os.Rename(safeDir, safeDir+"-original"); swapErr != nil {
			return
		}
		swapErr = os.Symlink(outside, safeDir)
	})
	if swapErr != nil {
		t.Fatal(swapErr)
	}
	if err != nil {
		t.Fatalf("export through pinned parent: %v", err)
	}
	if string(result.Content) != "inside artifact" {
		t.Fatalf("artifact export leaked replacement content: %#v", result)
	}
	assertFileContent(t, filepath.Join(outside, "report.txt"), "outside secret")
}

func TestGuardedArtifactExportRejectsLinksAndSpecialFiles(t *testing.T) {
	root := t.TempDir()
	realTarget := filepath.Join(root, "report.txt")
	writeTestFile(t, realTarget, "artifact")
	if err := os.Symlink(realTarget, filepath.Join(root, "report-link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(root, "events.pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider, err := NewWorkspacePathGuardProvider(LocalSystemProvider{}, root)
	if err != nil {
		t.Fatal(err)
	}
	exporter := ArtifactExportProvider(provider)

	_, err = exporter.ExportArtifactFile(t.Context(), ExportArtifactFileRequest{Path: "report-link.txt"})
	assertFileReadError(t, err, "unsupported_file_type", "report-link.txt")
	_, err = exporter.ExportArtifactFile(t.Context(), ExportArtifactFileRequest{Path: "events.pipe"})
	assertFileReadError(t, err, "unsupported_file_type", "events.pipe")
}
