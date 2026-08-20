package deploy

import (
	"fmt"
	"io"
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
	if err := paths.EnsureDirMode(dir, "lock directory", 0o700); err != nil {
		return nil, err
	}
	_ = os.Chmod(dir, 0o700)
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
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err == nil {
			_ = tryFlock(f)
			if err := writeLockIdentity(f, pid, token); err != nil {
				releaseFlock(f)
				_ = f.Close()
				_ = os.Remove(path)
				return nil, err
			}
			return &Lock{path: path, file: f, pid: pid, token: token}, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("open lock: %w", err)
		}
		// Prefer reclaiming in place under flock so we never unlink a live
		// owner's file. Fall back to rename-away of a dead-PID lock when flock
		// is unavailable (non-Unix) or the previous owner never took a flock.
		if l, ok := tryReclaimExisting(path, pid, token); ok {
			return l, nil
		}
		// Unlink-based reclaim is only safe when flock cannot tell us a live
		// owner still holds the inode (non-Unix). On Unix, a failed flock means
		// the owner is alive; renaming the path out from under them races.
		if !flockAvailable() && tryRemoveStaleLock(path, 2*timeout) {
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("another Tailarr process holds the lock: %s", path)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func writeLockIdentity(f *os.File, pid int, token string) error {
	if _, err := f.Seek(0, 0); err != nil {
		return fmt.Errorf("seek lock: %w", err)
	}
	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("truncate lock: %w", err)
	}
	if _, err := fmt.Fprintf(f, "%d\n%s\n", pid, token); err != nil {
		return fmt.Errorf("write lock: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync lock: %w", err)
	}
	return nil
}

// tryReclaimExisting opens an existing lock, takes a non-blocking flock, and
// rewrites identity when the recorded owner is gone. Returns ok=false when the
// lock is held or flock is not available.
func tryReclaimExisting(path string, pid int, token string) (*Lock, bool) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, false
	}
	if !tryFlock(f) {
		_ = f.Close()
		return nil, false
	}
	data, err := io.ReadAll(f)
	if err != nil {
		releaseFlock(f)
		_ = f.Close()
		return nil, false
	}
	if lockOwnerAlive(data) && ownerIsTailarr(data) {
		// A live Tailarr owner still blocks reclaim; a different live process
		// means PID reuse after flock succeeded, so reclaim the lock in place.
		releaseFlock(f)
		_ = f.Close()
		return nil, false
	}
	if err := writeLockIdentity(f, pid, token); err != nil {
		releaseFlock(f)
		_ = f.Close()
		return nil, false
	}
	return &Lock{path: path, file: f, pid: pid, token: token}, true
}

func lockOwnerAlive(data []byte) bool {
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 1 {
		return false
	}
	ownerPID, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || ownerPID <= 0 {
		return false
	}
	return processAlive(ownerPID)
}

// ownerIsTailarr reports whether the recorded owner PID appears to be a
// running Tailarr process. When the PID was reused by an unrelated process
// after a crash, this returns false so the flock-verified free lock can be
// reclaimed in place.
func ownerIsTailarr(data []byte) bool {
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 1 {
		return false
	}
	ownerPID, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || ownerPID <= 0 {
		return false
	}
	if !processAlive(ownerPID) {
		return false
	}
	return processIsTailarr(ownerPID)
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
		return renameAwayLock(path, data)
	}
	if processAlive(ownerPID) {
		return false
	}
	return renameAwayLock(path, data)
}

// renameAwayLock atomically moves path aside only if its contents still match
// data, then deletes the trash name. A new lock created at path after our
// first read will not match and is left alone.
func renameAwayLock(path string, expect []byte) bool {
	cur, err := os.ReadFile(path)
	if err != nil || string(cur) != string(expect) {
		return false
	}
	trash := fmt.Sprintf("%s.reclaim-%d-%d", path, os.Getpid(), time.Now().UnixNano())
	if err := os.Rename(path, trash); err != nil {
		return false
	}
	_ = os.Remove(trash)
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
		releaseFlock(l.file)
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

// AuthkeysLockPath returns the lock file next to the authkeys store.
func AuthkeysLockPath(authkeysPath string) string {
	return authkeysPath + ".lock"
}
