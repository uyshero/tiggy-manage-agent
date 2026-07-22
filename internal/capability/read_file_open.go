package capability

import (
	"os"
	"strings"
)

func openLocalFileForRead(request ReadFileRequest, beforeOpen func()) (*os.File, error) {
	if strings.TrimSpace(request.guardedRoot) != "" {
		return openLocalFileForReadGuarded(request, beforeOpen)
	}
	return os.Open(request.Path)
}
