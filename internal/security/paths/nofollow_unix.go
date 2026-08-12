//go:build unix

package paths

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// OpenFileNoFollow opens path without following a final-component symlink.
func OpenFileNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	fd, err := unix.Open(path, flag|unix.O_NOFOLLOW|unix.O_CLOEXEC, uint32(perm.Perm()))
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

// ChmodNoFollow sets mode on path after refusing a raced symlink.
func ChmodNoFollow(path string, mode os.FileMode) error {
	f, err := OpenFileNoFollow(path, unix.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open for chmod: %w", err)
	}
	defer func() { _ = f.Close() }()
	return f.Chmod(mode)
}
