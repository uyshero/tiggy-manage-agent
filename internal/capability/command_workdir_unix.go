//go:build linux

package capability

import (
	"fmt"
	"os"
)

func prepareGuardedCommandWorkDir(guardedRoot, workDir string, childFD int) (string, *os.File, error) {
	if guardedRoot == "" {
		return workDir, nil, nil
	}
	directory, err := openGuardedDirectoryNoFollow(guardedRoot, workDir)
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("/proc/self/fd/%d", childFD), directory, nil
}
