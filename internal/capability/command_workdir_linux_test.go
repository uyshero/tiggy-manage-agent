//go:build linux

package capability

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestGuardedCommandPinsWorkDirBeforeStart(t *testing.T) {
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
	result, err := runLocalCommand(t.Context(), request, func() {
		swapErr = swapTreeRoot(safeDir, outside)
	})
	if swapErr != nil {
		t.Fatal(swapErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != "inside" {
		t.Fatalf("command used replacement work_dir: %#v", result)
	}
	assertFileContent(t, filepath.Join(outside, "marker.txt"), "outside")
}
