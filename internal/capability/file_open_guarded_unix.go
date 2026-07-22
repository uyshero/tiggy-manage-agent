//go:build unix

package capability

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openGuardedFileNoFollow(root, path string, beforeOpen func()) (*os.File, error) {
	parentFD, targetName, err := openGuardedParent(root, path, false)
	if err != nil {
		return nil, err
	}
	defer unix.Close(parentFD)
	if beforeOpen != nil {
		beforeOpen()
	}

	fd, err := unix.Openat(parentFD, targetName, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), targetName)
	if file == nil {
		unix.Close(fd)
		return nil, fmt.Errorf("open guarded file %q", path)
	}
	return file, nil
}
