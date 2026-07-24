package capability

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

const FileReferenceScheme = "fileref"

type FileReference struct {
	Scope string
	Path  string
}

// ParseFileReference parses portable file references while leaving legacy
// plain paths untouched. The canonical form is fileref://<scope>/<path>.
func ParseFileReference(value string) (FileReference, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" || !strings.Contains(value, "://") {
		return FileReference{}, false, nil
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return FileReference{}, true, fmt.Errorf("invalid file reference: %w", err)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return FileReference{}, true, fmt.Errorf("file reference must not contain user info, query, or fragment")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "file":
		if parsed.Host != "" && parsed.Host != "localhost" {
			return FileReference{}, true, fmt.Errorf("file URI host must be empty or localhost")
		}
		if !strings.HasPrefix(parsed.Path, "/") {
			return FileReference{}, true, fmt.Errorf("file URI path must be absolute")
		}
		return FileReference{Scope: "absolute", Path: path.Clean(parsed.Path)}, true, nil
	case FileReferenceScheme:
		scope := strings.ToLower(strings.TrimSpace(parsed.Host))
		if scope != "workspace" && scope != "data" && scope != "tmp" && scope != "artifact" {
			return FileReference{}, true, fmt.Errorf("unsupported file reference scope %q", scope)
		}
		cleaned := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(parsed.Path, "/")), "/")
		if cleaned == "." {
			cleaned = ""
		}
		if scope == "artifact" && cleaned == "" {
			return FileReference{}, true, fmt.Errorf("artifact file reference requires an artifact id")
		}
		return FileReference{Scope: scope, Path: cleaned}, true, nil
	default:
		return FileReference{}, true, fmt.Errorf("unsupported file reference scheme %q", parsed.Scheme)
	}
}

func PortableFileReferencePath(value string) (string, bool, error) {
	reference, recognized, err := ParseFileReference(value)
	if err != nil || !recognized {
		return value, recognized, err
	}
	switch reference.Scope {
	case "absolute":
		return reference.Path, true, nil
	case "workspace":
		return portableFileReferenceRoot("/workspace", reference.Path), true, nil
	case "data":
		return portableFileReferenceRoot("/mnt/data", reference.Path), true, nil
	case "tmp":
		return portableFileReferenceRoot("/tmp", reference.Path), true, nil
	case "artifact":
		return value, true, nil
	default:
		return value, true, fmt.Errorf("unsupported file reference scope %q", reference.Scope)
	}
}

func portableFileReferenceRoot(root string, relative string) string {
	if relative == "" {
		return root
	}
	return path.Join(root, relative)
}
