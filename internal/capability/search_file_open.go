package capability

import (
	"os"
	"strings"
)

func openLocalFileForSearch(request SearchFileRequest, beforeOpen func()) (*os.File, error) {
	if strings.TrimSpace(request.guardedRoot) != "" {
		return openLocalFileForSearchGuarded(request, beforeOpen)
	}
	return os.Open(request.Path)
}
