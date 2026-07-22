//go:build !unix

package capability

import "os"

func openLocalFileForEditGuarded(request EditFileRequest, beforeOpen func()) (*os.File, error) {
	if beforeOpen != nil {
		beforeOpen()
	}
	if err := ensureGuardedMutationPath(request.resolvedPath(), request.guardedRoot); err != nil {
		return nil, err
	}
	return os.Open(request.resolvedPath())
}
