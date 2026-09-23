package deploy

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/interrupt"
	"github.com/jackspiering/tailarr/internal/security/names"
	"github.com/jackspiering/tailarr/internal/security/paths"
)

// BackupMode selects how Backup snapshots a deployment. Production always copies.
type BackupMode string

const (
	BackupCopy BackupMode = "copy"
)

// Backup creates a timestamped backup of servicePath under deployPath/.tailarr_backups.
func Backup(deployPath, service, servicePath string, mode BackupMode) (string, error) {
	if err := names.ValidateServiceName(service); err != nil {
		return "", err
	}
	if _, err := os.Stat(servicePath); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	root := filepath.Join(deployPath, config.BackupDirName)
	if err := paths.EnsureDirMode(root, "backup directory", 0o700); err != nil {
		return "", err
	}

	stamp := time.Now().UTC().Format("20060102T150405Z")
	backupPath, err := backupPathFor(root, service, stamp)
	if err != nil {
		return "", err
	}

	if mode != BackupCopy {
		return "", fmt.Errorf("unknown backup mode: %s", mode)
	}
	if err := copyTree(servicePath, backupPath); err != nil {
		_ = os.RemoveAll(backupPath)
		return "", fmt.Errorf("copy deployment to backup: %w", err)
	}
	// Prune older backups: they accumulate unboundedly and hold plaintext
	// secrets (e.g. TS_AUTHKEY in .env). Best-effort: a prune failure must not
	// abort an operation that already copied the deployment, and must
	// never lose the backup just created (which is always the newest).
	_ = pruneBackups(root, service, 2)
	return backupPath, nil
}

// backupPathFor returns a free "<service>-<stamp>" directory path under root,
// appending a numeric suffix on same-second collisions. Stat errors other than
// IsNotExist abort instead of looping forever.
func backupPathFor(root, service, stamp string) (string, error) {
	base := filepath.Join(root, fmt.Sprintf("%s-%s", service, stamp))
	for i := 1; ; i++ {
		if _, err := os.Lstat(base); os.IsNotExist(err) {
			return base, nil
		} else if err != nil {
			return "", fmt.Errorf("inspect backup path: %w", err)
		}
		base = filepath.Join(root, fmt.Sprintf("%s-%s-%d", service, stamp, i))
	}
}

// pruneBackups removes all but the newest keep backups for service under root.
// Entries are matched by the "<service>-<stamp>" name prefix used by Backup
// (collision-suffixed names such as "<stamp>-1" count as backups too).
func pruneBackups(root, service string, keep int) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var matches []string
	for _, e := range entries {
		if !e.IsDir() || !isServiceBackupName(service, e.Name()) {
			continue
		}
		p := filepath.Join(root, e.Name())
		if paths.IsSymlink(p) {
			continue
		}
		matches = append(matches, p)
	}
	if len(matches) <= keep {
		return nil
	}
	// Timestamped names sort chronologically, so the last keep are newest.
	sort.Strings(matches)
	for _, p := range matches[:len(matches)-keep] {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	return nil
}

// LatestBackup returns the newest backup directory for service, or "".
func LatestBackup(deployPath, service string) (string, error) {
	if err := names.ValidateServiceName(service); err != nil {
		return "", err
	}
	root := filepath.Join(deployPath, config.BackupDirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var matches []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if !isServiceBackupName(service, name) {
			continue
		}
		p := filepath.Join(root, name)
		if paths.IsSymlink(p) {
			continue
		}
		matches = append(matches, p)
	}
	if len(matches) == 0 {
		return "", nil
	}
	sort.Strings(matches)
	return matches[len(matches)-1], nil
}

// copyTree copies the deployment at src to dst for a backup. Directories and
// regular files keep their mode, mtime, and (when running as root) owner, so
// a restored copy stays usable by containers that run as another uid.
// Sockets, FIFOs, and devices are skipped: they are runtime artifacts, and
// opening a FIFO blocks until a writer appears. The operator's interrupt
// stops a long copy between files.
func copyTree(src, dst string) error {
	ctx := interrupt.Context()
	type dirEntry struct {
		path string
		info os.FileInfo
	}
	var dirs []dirEntry
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("%w: backup stopped: %v", ErrInterrupted, cerr)
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			return fmt.Errorf("%w: refusing to copy symlink: %s", ErrSymlink, path)
		case info.IsDir():
			// Owner-only while copying; the real mode is set once the
			// contents are in place, so a read-only source dir still copies.
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			dirs = append(dirs, dirEntry{target, info})
			return nil
		case info.Mode().IsRegular():
			return copyRegular(path, target, info)
		default:
			return nil
		}
	})
	if err != nil {
		return err
	}
	// Children first, so a parent's mtime is not bumped after it is set.
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := applyMeta(dirs[i].path, dirs[i].info); err != nil {
			return err
		}
	}
	return nil
}

// copyRegular copies the regular file src to dst. An existing dst is
// truncated in place, so its inode (and a single-file bind mount of it)
// survives. Mode, mtime, and owner follow info.
func copyRegular(src, dst string, info os.FileInfo) error {
	in, err := paths.OpenFileNoFollow(src, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := paths.OpenFileNoFollow(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return applyMeta(dst, info)
}

// applyMeta sets mode, owner, and mtime on path from info. Chmod runs after
// Chown because changing the owner clears setuid and setgid bits.
func applyMeta(path string, info os.FileInfo) error {
	if err := preserveOwner(path, info); err != nil {
		return fmt.Errorf("preserve owner of %s: %w", path, err)
	}
	if err := os.Chmod(path, info.Mode()&(os.ModePerm|os.ModeSetuid|os.ModeSetgid|os.ModeSticky)); err != nil {
		return err
	}
	return os.Chtimes(path, info.ModTime(), info.ModTime())
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// isServiceBackupName reports whether name is a Backup() directory for service.
// Names are "<service>-<YYYYMMDDTHHMMSSZ>" with an optional "-<n>" collision suffix.
// A hyphenated service such as "web-ui" must not match the prefix of "web".
func isServiceBackupName(service, name string) bool {
	prefix := service + "-"
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	rest := name[len(prefix):]
	stamp, suf, ok := strings.Cut(rest, "-")
	if !ok {
		return isBackupStamp(rest)
	}
	if !isBackupStamp(stamp) || suf == "" {
		return false
	}
	for _, c := range suf {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func isBackupStamp(s string) bool {
	// time.RFC3339 compact UTC: 20060102T150405Z
	if len(s) != 16 || s[8] != 'T' || s[15] != 'Z' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if i == 8 || i == 15 {
			continue
		}
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
