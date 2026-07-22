//go:build unix && !linux

package capability

import "os"

func prepareGuardedCommandWorkDir(guardedRoot, workDir string, _ int) (string, *os.File, error) {
	if err := ensureGuardedMutationPath(workDir, guardedRoot); err != nil {
		return "", nil, err
	}
	return workDir, nil, nil
}
