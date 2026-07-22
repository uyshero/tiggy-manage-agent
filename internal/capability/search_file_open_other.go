//go:build !unix

package capability

import "os"

func openLocalFileForSearchGuarded(request SearchFileRequest, beforeOpen func()) (*os.File, error) {
	if beforeOpen != nil {
		beforeOpen()
	}
	if err := ensureGuardedMutationPath(request.Path, request.guardedRoot); err != nil {
		return nil, err
	}
	return os.Open(request.Path)
}
