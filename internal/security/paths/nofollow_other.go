//go:build !unix

package paths

import (
	"fmt"
	"os"
)

// OpenFileNoFollow falls back to a symlink check plus OpenFile.
// On !unix this is check-then-act and cannot prevent a TOCTOU symlink swap
// between the check and the open. Unix builds use O_NOFOLLOW atomically.
func OpenFileNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	if IsSymlink(path) {
		return nil, fmt.Errorf("refusing to open symlink: %s", path)
	}
	return os.OpenFile(path, flag, perm)
}

// ChmodNoFollow falls back to a symlink check plus Chmod.
// On !unix this is check-then-act and cannot prevent a TOCTOU symlink swap
// between the check and the chmod. Unix builds use O_NOFOLLOW atomically.
func ChmodNoFollow(path string, mode os.FileMode) error {
	if IsSymlink(path) {
		return fmt.Errorf("refusing to chmod symlink: %s", path)
	}
	return os.Chmod(path, mode)
}
