//go:build unix

package capability

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"

	"golang.org/x/sys/unix"
)

func discoverLocalFilesGuarded(
	ctx context.Context,
	guardedRoot, absoluteRoot string,
	patterns, excludes []string,
	includeHidden bool,
	maxScanned, maxMatches int,
	afterPath string,
	afterRootOpen func(),
) ([]fileCandidate, int, bool, error) {
	rootDirectory, err := openGuardedDirectoryNoFollow(guardedRoot, absoluteRoot)
	if err != nil {
		return nil, 0, false, err
	}
	defer rootDirectory.Close()
	if afterRootOpen != nil {
		afterRootOpen()
	}

	candidates := make([]fileCandidate, 0, minInt(maxMatches, defaultFindFilesMaxResults))
	scanned := 0
	truncated := false
	stop := errors.New("guarded file discovery complete")
	var walk func(*os.File, string) error
	walk = func(directory *os.File, relativeDirectory string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := directory.ReadDir(-1)
		if err != nil {
			return err
		}
		sort.Slice(entries, func(left, right int) bool { return entries[left].Name() < entries[right].Name() })
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			name := entry.Name()
			relative := path.Join(relativeDirectory, name)
			var stat unix.Stat_t
			if err := unix.Fstatat(int(directory.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return err
			}
			fileType := uint32(stat.Mode) & unix.S_IFMT
			if fileType == unix.S_IFLNK {
				continue
			}
			if fileType == unix.S_IFDIR {
				if (!includeHidden && pathHasHiddenComponent(relative)) || matchesAnyGlob(excludes, relative) {
					continue
				}
				child, err := openGuardedChild(directory, name, true, filepath.Join(absoluteRoot, filepath.FromSlash(relative)))
				if err != nil {
					return err
				}
				err = walk(child, relative)
				_ = child.Close()
				if err != nil {
					return err
				}
				continue
			}

			scanned++
			if scanned > maxScanned {
				truncated = true
				return stop
			}
			if !includeHidden && pathHasHiddenComponent(relative) || matchesAnyGlob(excludes, relative) || !matchesAnyGlob(patterns, relative) || relative <= afterPath {
				continue
			}
			if fileType != unix.S_IFREG {
				continue
			}
			absolute := filepath.Join(absoluteRoot, filepath.FromSlash(relative))
			file, err := openGuardedChild(directory, name, false, absolute)
			if err != nil {
				return err
			}
			info, err := file.Stat()
			_ = file.Close()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return guardedPathChangedError(absolute)
			}
			candidates = append(candidates, fileCandidate{
				absolute: absolute, relative: relative, info: info, guardedRoot: guardedRoot,
			})
			if len(candidates) >= maxMatches {
				truncated = true
				return stop
			}
		}
		return nil
	}

	err = walk(rootDirectory, "")
	if err != nil && !errors.Is(err, stop) {
		return nil, scanned, truncated, err
	}
	sort.Slice(candidates, func(left, right int) bool { return candidates[left].relative < candidates[right].relative })
	return candidates, scanned, truncated, nil
}

func openGuardedDirectoryNoFollow(guardedRoot, target string) (*os.File, error) {
	cleanRoot := filepath.Clean(guardedRoot)
	cleanTarget := filepath.Clean(target)
	if cleanTarget == cleanRoot {
		fd, err := unix.Open(cleanRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, guardedTraversalError(target, err)
		}
		return guardedOSFile(fd, filepath.Base(cleanTarget), target)
	}
	parentFD, name, err := openGuardedParent(cleanRoot, cleanTarget, false)
	if err != nil {
		return nil, err
	}
	defer unix.Close(parentFD)
	var stat unix.Stat_t
	if err := unix.Fstatat(parentFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, err
	}
	fileType := uint32(stat.Mode) & unix.S_IFMT
	if fileType == unix.S_IFLNK {
		return nil, guardedPathChangedError(target)
	}
	if fileType != unix.S_IFDIR {
		return nil, newFileReadError("unsupported_file_type", "file discovery root must be a directory", map[string]any{"root": target})
	}
	fd, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ENOENT) {
			return nil, guardedPathChangedError(target)
		}
		return nil, err
	}
	return guardedOSFile(fd, name, target)
}

func openGuardedChild(parent *os.File, name string, directory bool, path string) (*os.File, error) {
	flags := unix.O_RDONLY | unix.O_NONBLOCK | unix.O_NOFOLLOW | unix.O_CLOEXEC
	if directory {
		flags |= unix.O_DIRECTORY
	}
	fd, err := unix.Openat(int(parent.Fd()), name, flags, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ENOENT) {
			return nil, guardedPathChangedError(path)
		}
		return nil, err
	}
	return guardedOSFile(fd, name, path)
}

func guardedOSFile(fd int, name, path string) (*os.File, error) {
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		unix.Close(fd)
		return nil, fmt.Errorf("open guarded path %q", path)
	}
	return file, nil
}
