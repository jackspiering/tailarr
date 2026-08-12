package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/security/names"
	"github.com/jackspiering/tailarr/internal/security/paths"
)

// Lock is a simple exclusive file lock using O_CREATE|O_EXCL.
// Not as strong as flock across all platforms, but works for single-host ops
// without CGO. Ownership is bound to a PID+token so Release only removes our
// own lock, and stale cleanup only removes locks whose owner process is gone.
type Lock struct {
	path  string
	file  *os.File
	pid   int
	token string
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
	if err := paths.RefuseSymlinkAncestry(dir); err != nil {
		return nil, fmt.Errorf("lock directory: %w", err)
	}
	if paths.IsSymlink(path) {
		return nil, fmt.Errorf("%w: lock path: %s", ErrSymlink, path)
	}

	deadline := time.Now().Add(timeout)
	pid := os.Getpid()
	token := fmt.Sprintf("%d-%d", pid, time.Now().UnixNano())

	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			if _, werr := fmt.Fprintf(f, "%d\n%s\n", pid, token); werr != nil {
				_ = f.Close()
				_ = os.Remove(path)
				return nil, fmt.Errorf("write lock: %w", werr)
			}
			if serr := f.Sync(); serr != nil {
				_ = f.Close()
				_ = os.Remove(path)
				return nil, fmt.Errorf("sync lock: %w", serr)
			}
			return &Lock{path: path, file: f, pid: pid, token: token}, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("open lock: %w", err)
		}
		// Stale lock: reclaim if owner process is gone (regardless of age),
		// or if content is corrupt and the lock is old.
		if tryRemoveStaleLock(path, 2*timeout) {
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("another Tailarr process holds the lock: %s", path)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// tryRemoveStaleLock removes path when the recorded owner PID is not running,
// regardless of lock age, or when the content is unparseable and the lock is
// older than maxAge (treat as corrupt). A live owner is never evicted, even an
// old one: the authkeys lock is held across long interactive prompts. Returns
// true if the lock was removed.
func tryRemoveStaleLock(path string, maxAge time.Duration) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 1 {
		return false
	}
	ownerPID, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || ownerPID <= 0 {
		// Unparseable owner: reclaim only once the lock is old enough that a
		// writer mid-creation is not clobbered.
		info, err := os.Stat(path)
		if err != nil {
			return false
		}
		if time.Since(info.ModTime()) <= maxAge {
			return false
		}
		_ = os.Remove(path)
		return true
	}
	if processAlive(ownerPID) {
		return false
	}
	_ = os.Remove(path)
	return true
}

// processAlive reports whether pid appears to be a running process (best effort).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, Signal(0) checks existence without killing (see process_unix.go).
	return processAliveSignal(p)
}

// Release removes the lock file only if we still own it.
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	data, err := os.ReadFile(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		// Not our format; leave it alone to avoid stealing another process's lock.
		return nil
	}
	ownerPID, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || ownerPID != l.pid {
		return nil
	}
	if strings.TrimSpace(lines[1]) != l.token {
		return nil
	}
	return os.Remove(l.path)
}

// ServiceLockPath returns the lock file under deployPath/.tailarr_locks.
func ServiceLockPath(deployPath, service string) (string, error) {
	if err := names.ValidateServiceName(service); err != nil {
		return "", err
	}
	return filepath.Join(deployPath, config.LockDirName, service+".lock"), nil
}

// RepoLockPath returns the lock file next to the repo path.
func RepoLockPath(repoPath string) string {
	return repoPath + ".lock"
}
