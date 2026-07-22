package capability

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	WriteModeCreate            = "create"
	WriteModeOverwrite         = "overwrite"
	WriteModeCreateOrOverwrite = "create_or_overwrite"
)

func writeLocalFileAtomic(ctx context.Context, request WriteFileRequest) (FileResult, error) {
	options, err := validateLocalFileWrite(ctx, request)
	if err != nil {
		return FileResult{}, err
	}
	if strings.TrimSpace(request.guardedRoot) != "" {
		return writeLocalFileAtomicGuarded(ctx, request, options)
	}
	return writeLocalFileAtomicPath(ctx, request, options)
}

type localFileWriteOptions struct {
	mode          string
	checksum      string
	createParents bool
}

func validateLocalFileWrite(ctx context.Context, request WriteFileRequest) (localFileWriteOptions, error) {
	if err := ctx.Err(); err != nil {
		return localFileWriteOptions{}, err
	}
	if strings.TrimSpace(request.Path) == "" {
		return localFileWriteOptions{}, newFileReadError("invalid_write_path", "write_file path is required", nil)
	}
	mode := strings.ToLower(strings.TrimSpace(request.Mode))
	if mode == "" {
		mode = WriteModeCreateOrOverwrite
	}
	if mode != WriteModeCreate && mode != WriteModeOverwrite && mode != WriteModeCreateOrOverwrite {
		return localFileWriteOptions{}, newFileReadError("invalid_write_mode", "mode must be create, overwrite, or create_or_overwrite", map[string]any{"mode": request.Mode})
	}
	if request.ExpectedAbsent && request.ExpectedRevision != "" {
		return localFileWriteOptions{}, newFileReadError("invalid_write_precondition", "expected_absent and expected_revision are mutually exclusive", nil)
	}
	checksum := contentSHA256(request.Content)
	if expected := strings.ToLower(strings.TrimSpace(request.ContentSHA256)); expected != "" && expected != checksum {
		return localFileWriteOptions{}, newFileReadError("content_checksum_mismatch", "content_sha256 does not match content", map[string]any{"expected_sha256": expected, "actual_sha256": checksum})
	}
	return localFileWriteOptions{
		mode:          mode,
		checksum:      checksum,
		createParents: request.CreateParents == nil || *request.CreateParents,
	}, nil
}

func writeLocalFileAtomicPath(ctx context.Context, request WriteFileRequest, options localFileWriteOptions) (FileResult, error) {
	if err := ensureGuardedMutationPath(request.Path, request.guardedRoot); err != nil {
		return FileResult{}, err
	}
	parent := filepath.Dir(request.Path)
	if options.createParents {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return FileResult{}, err
		}
	}
	if err := ensureGuardedMutationPath(request.Path, request.guardedRoot); err != nil {
		return FileResult{}, err
	}

	existing, statErr := os.Lstat(request.Path)
	exists := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return FileResult{}, statErr
	}
	if exists && !existing.Mode().IsRegular() {
		return FileResult{}, newFileReadError("unsupported_file_type", "write_file only supports regular files", map[string]any{"path": request.Path})
	}
	if options.mode == WriteModeCreate && exists || request.ExpectedAbsent && exists {
		return FileResult{}, newFileReadError("file_already_exists", "write precondition failed because the file already exists", map[string]any{"path": request.Path})
	}
	if options.mode == WriteModeOverwrite && !exists {
		return FileResult{}, newFileReadError("file_not_found", "overwrite mode requires an existing file", map[string]any{"path": request.Path})
	}
	if request.ExpectedRevision != "" {
		if !exists {
			return FileResult{}, staleFileRevisionError(request.Path, request.ExpectedRevision, "")
		}
		if actual := fileRevision(existing); actual != request.ExpectedRevision {
			return FileResult{}, staleFileRevisionError(request.Path, request.ExpectedRevision, actual)
		}
	}

	permission := os.FileMode(0o644)
	if exists {
		permission = existing.Mode().Perm()
	}
	temporary, err := os.CreateTemp(parent, ".tma-write-*")
	if err != nil {
		return FileResult{}, err
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(permission); err != nil {
		return FileResult{}, err
	}
	if _, err := temporary.Write(request.Content); err != nil {
		return FileResult{}, err
	}
	if err := temporary.Sync(); err != nil {
		return FileResult{}, err
	}
	if err := temporary.Close(); err != nil {
		return FileResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return FileResult{}, err
	}

	current, currentErr := os.Lstat(request.Path)
	currentExists := currentErr == nil
	if currentErr != nil && !os.IsNotExist(currentErr) {
		return FileResult{}, currentErr
	}
	if request.ExpectedAbsent && currentExists || options.mode == WriteModeCreate && currentExists {
		return FileResult{}, newFileReadError("file_already_exists", "write precondition changed before commit", map[string]any{"path": request.Path})
	}
	if options.mode == WriteModeOverwrite && !currentExists {
		return FileResult{}, newFileReadError("file_not_found", "target disappeared before overwrite commit", map[string]any{"path": request.Path})
	}
	if request.ExpectedRevision != "" {
		actual := ""
		if currentExists {
			actual = fileRevision(current)
		}
		if actual != request.ExpectedRevision {
			return FileResult{}, staleFileRevisionError(request.Path, request.ExpectedRevision, actual)
		}
	}
	if options.mode == WriteModeCreate || request.ExpectedAbsent {
		if err := ensureGuardedMutationPath(request.Path, request.guardedRoot); err != nil {
			return FileResult{}, err
		}
		if err := os.Link(temporaryPath, request.Path); err != nil {
			if os.IsExist(err) {
				return FileResult{}, newFileReadError("file_already_exists", "write precondition changed before commit", map[string]any{"path": request.Path})
			}
			return FileResult{}, fmt.Errorf("commit atomic create: %w", err)
		}
		_ = os.Remove(temporaryPath)
	} else {
		if err := ensureGuardedMutationPath(request.Path, request.guardedRoot); err != nil {
			return FileResult{}, err
		}
		if err := os.Rename(temporaryPath, request.Path); err != nil {
			return FileResult{}, fmt.Errorf("commit atomic file write: %w", err)
		}
	}
	committed = true
	if directory, err := os.Open(parent); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	info, err := os.Stat(request.Path)
	if err != nil {
		return FileResult{}, err
	}
	return completedLocalFileWrite(request, info, options.checksum), nil
}

func completedLocalFileWrite(request WriteFileRequest, info os.FileInfo, checksum string) FileResult {
	classification := classifyFile(request.Path, request.Content[:minInt(len(request.Content), 512)], !isUTF8Text(request.Content))
	result := FileResult{
		Path: request.Path, SizeBytes: info.Size(), ReturnedBytes: len(request.Content),
		NextOffsetBytes: info.Size(), EOF: true, FileRevision: fileRevision(info), Mode: "write",
		ContentSHA256: checksum,
	}
	applyFileClassification(&result, classification)
	return result
}

func isUTF8Text(content []byte) bool {
	if len(content) == 0 {
		return true
	}
	for _, value := range content {
		if value == 0 {
			return false
		}
	}
	return utf8.Valid(content)
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
