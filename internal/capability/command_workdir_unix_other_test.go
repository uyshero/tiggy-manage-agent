//go:build unix && !linux

package capability

import (
	"path/filepath"
	"testing"
)

func TestGuardedCommandRejectsWorkDirSwapBeforeStart(t *testing.T) {
	root := t.TempDir()
	guardedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	safeDir := filepath.Join(root, "safe")
	writeTestFile(t, filepath.Join(safeDir, "marker.txt"), "inside")
	writeTestFile(t, filepath.Join(outside, "marker.txt"), "outside")
	request := RunCommandRequest{
		Command: "sh", Args: []string{"-c", "cat marker.txt"},
		WorkDir: filepath.Join(guardedRoot, "safe"), guardedRoot: guardedRoot,
	}

	var swapErr error
	_, err = runLocalCommand(t.Context(), request, func() {
		swapErr = swapTreeRoot(safeDir, outside)
	})
	if swapErr != nil {
		t.Fatal(swapErr)
	}
	assertFileReadErrorCode(t, err, "workspace_path_changed")
	assertFileContent(t, filepath.Join(outside, "marker.txt"), "outside")
}
