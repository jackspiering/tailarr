package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jackspiering/tailarr/internal/security/names"
	"github.com/jackspiering/tailarr/internal/security/paths"
)

// Lock is a simple exclusive file lock using O_CREATE|O_EXCL.
// Not as strong as flock across all platforms, but works for single-host ops
// without CGO. Stale locks older than Timeout may be removed.
type Lock struct {
	path string
	file *os.File
}

// DefaultLockTimeout is how long to wait for a lock.
const DefaultLockTimeout = 30 * time.Second

// AcquireLock creates an exclusive lock file at path.
func AcquireLock(path string, timeout time.Duration) (*Lock, error) {
	if timeout <= 0 {
		timeout = DefaultLockTimeout
	}
	dir := filepath.Dir(path)
	if err := paths.EnsureDir(dir, "lock directory"); err != nil {
		return nil, err
	}
	if paths.IsSymlink(dir) || paths.IsSymlink(path) {
		return nil, fmt.Errorf("lock path must not be a symlink: %s", path)
	}

	deadline := time.Now().Add(timeout)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
			return &Lock{path: path, file: f}, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("open lock: %w", err)
		}
		// Stale lock: if older than 2x timeout, remove.
		if info, statErr := os.Stat(path); statErr == nil {
			if time.Since(info.ModTime()) > 2*timeout {
				_ = os.Remove(path)
				continue
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("another Tailarr process holds the lock: %s", path)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Release removes the lock file.
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	if l.file != nil {
		_ = l.file.Close()
	}
	return os.Remove(l.path)
}

// ServiceLockPath returns the lock file for a service under deploy parent.
func ServiceLockPath(deployPath, service string) (string, error) {
	if err := names.ValidateServiceName(service); err != nil {
		return "", err
	}
	parent := filepath.Dir(deployPath)
	return filepath.Join(parent, ".tailarr_locks", service+".lock"), nil
}

// RepoLockPath returns the lock file next to the repo path.
func RepoLockPath(repoPath string) string {
	return repoPath + ".lock"
}
