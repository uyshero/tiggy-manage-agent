package capability

import (
	"os"
	"strings"
)

func openLocalFileForEdit(request EditFileRequest, beforeOpen func()) (*os.File, error) {
	if strings.TrimSpace(request.guardedRoot) != "" {
		return openLocalFileForEditGuarded(request, beforeOpen)
	}
	return os.Open(request.resolvedPath())
}
