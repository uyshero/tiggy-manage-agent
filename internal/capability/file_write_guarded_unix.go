//go:build unix

package capability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func writeLocalFileAtomicGuarded(ctx context.Context, request WriteFileRequest, options localFileWriteOptions) (FileResult, error) {
	return writeLocalFileAtomicGuardedWithHook(ctx, request, options, nil)
}

func writeLocalFileAtomicGuardedWithHook(
	ctx context.Context,
	request WriteFileRequest,
	options localFileWriteOptions,
	beforeCommit func(),
) (FileResult, error) {
	parentFD, targetName, err := openGuardedParent(request.guardedRoot, request.Path, options.createParents)
	if err != nil {
		return FileResult{}, err
	}
	defer unix.Close(parentFD)

	existing, exists, err := guardedTargetInfo(parentFD, targetName, request.Path)
	if err != nil {
		return FileResult{}, err
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
	temporary, temporaryName, err := createGuardedTemporary(parentFD)
	if err != nil {
		return FileResult{}, err
	}
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = unix.Unlinkat(parentFD, temporaryName, 0)
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
	if beforeCommit != nil {
		beforeCommit()
	}

	current, currentExists, err := guardedTargetInfo(parentFD, targetName, request.Path)
	if err != nil {
		return FileResult{}, err
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
		if err := unix.Linkat(parentFD, temporaryName, parentFD, targetName, 0); err != nil {
			if errors.Is(err, unix.EEXIST) {
				return FileResult{}, newFileReadError("file_already_exists", "write precondition changed before commit", map[string]any{"path": request.Path})
			}
			return FileResult{}, fmt.Errorf("commit guarded atomic create: %w", err)
		}
		_ = unix.Unlinkat(parentFD, temporaryName, 0)
	} else if err := unix.Renameat(parentFD, temporaryName, parentFD, targetName); err != nil {
		return FileResult{}, fmt.Errorf("commit guarded atomic file write: %w", err)
	}
	committed = true
	_ = unix.Fsync(parentFD)

	info, exists, err := guardedTargetInfo(parentFD, targetName, request.Path)
	if err != nil {
		return FileResult{}, err
	}
	if !exists {
		return FileResult{}, fmt.Errorf("stat guarded committed file: %w", os.ErrNotExist)
	}
	return completedLocalFileWrite(request, info, options.checksum), nil
}

func openGuardedParent(root, target string, createParents bool) (int, string, error) {
	cleanRoot := filepath.Clean(root)
	cleanTarget := filepath.Clean(target)
	relative, err := filepath.Rel(cleanRoot, cleanTarget)
	if err != nil || relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return -1, "", guardedPathChangedError(target)
	}
	components := strings.Split(relative, string(os.PathSeparator))
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			return -1, "", guardedPathChangedError(target)
		}
	}

	currentFD, err := unix.Open(cleanRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, "", guardedTraversalError(target, err)
	}
	for _, component := range components[:len(components)-1] {
		nextFD, openErr := unix.Openat(currentFD, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(openErr, unix.ENOENT) && createParents {
			mkdirErr := unix.Mkdirat(currentFD, component, 0o755)
			if mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				unix.Close(currentFD)
				return -1, "", mkdirErr
			}
			nextFD, openErr = unix.Openat(currentFD, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		}
		if openErr != nil {
			unix.Close(currentFD)
			return -1, "", guardedTraversalError(target, openErr)
		}
		unix.Close(currentFD)
		currentFD = nextFD
	}
	return currentFD, components[len(components)-1], nil
}

func guardedTargetInfo(parentFD int, name, path string) (os.FileInfo, bool, error) {
	fd, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, false, nil
	}
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, false, newFileReadError("unsupported_file_type", "write_file does not follow symbolic links", map[string]any{"path": path})
		}
		return nil, false, err
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		unix.Close(fd)
		return nil, false, fmt.Errorf("open guarded target %q", path)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, newFileReadError("unsupported_file_type", "write_file only supports regular files", map[string]any{"path": path})
	}
	return info, true, nil
}

func createGuardedTemporary(parentFD int) (*os.File, string, error) {
	for attempt := 0; attempt < 100; attempt++ {
		var random [12]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, "", err
		}
		name := ".tma-write-" + hex.EncodeToString(random[:])
		fd, err := unix.Openat(parentFD, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		file := os.NewFile(uintptr(fd), name)
		if file == nil {
			unix.Close(fd)
			return nil, "", fmt.Errorf("create guarded temporary file")
		}
		return file, name, nil
	}
	return nil, "", fmt.Errorf("create guarded temporary file: too many name collisions")
}

func guardedTraversalError(path string, err error) error {
	if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
		return guardedPathChangedError(path)
	}
	return err
}

func guardedPathChangedError(path string) error {
	return newFileReadError(
		"workspace_path_changed",
		"target path changed after workspace authorization; resolve the path and retry",
		map[string]any{"path": path},
	)
}
