//go:build unix

package capability

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func openLocalFileForEditGuarded(request EditFileRequest, beforeOpen func()) (*os.File, error) {
	path := request.resolvedPath()
	file, err := openGuardedFileNoFollow(request.guardedRoot, path, beforeOpen)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, newFileReadError(
				"unsupported_file_type",
				"edit_file does not follow symbolic links",
				map[string]any{"path": path},
			)
		}
		return nil, err
	}
	return file, nil
}
