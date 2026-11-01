//go:build unix

package paths

import (
	"fmt"
	"os"
	"syscall"

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
	f, err := OpenFileNoFollow(path, unix.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("open for chmod: %w", err)
	}
	defer func() { _ = f.Close() }()
	return f.Chmod(mode)
}

// lchown and geteuid are replaced by tests.
var (
	lchown  = os.Lchown
	geteuid = os.Geteuid
)

// Owner returns the owner of path without following a symlink.
func Owner(path string) (uid, gid int, err error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, 0, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, fmt.Errorf("no owner information for %s", path)
	}
	return int(st.Uid), int(st.Gid), nil
}

// ChownLike gives path the owner of ref when Tailarr runs as root. For any
// other user it does nothing, because only root can change ownership.
func ChownLike(path, ref string) error {
	if geteuid() != 0 {
		return nil
	}
	uid, gid, err := Owner(ref)
	if err != nil {
		return err
	}
	return lchown(path, uid, gid)
}
