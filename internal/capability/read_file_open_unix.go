//go:build unix

package capability

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func openLocalFileForReadGuarded(request ReadFileRequest, beforeOpen func()) (*os.File, error) {
	file, err := openGuardedFileNoFollow(request.guardedRoot, request.Path, beforeOpen)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, newFileReadError(
				"unsupported_file_type",
				"read_file does not follow symbolic links",
				map[string]any{"path": request.Path},
			)
		}
		return nil, err
	}
	return file, nil
}
