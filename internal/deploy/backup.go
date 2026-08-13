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
	"github.com/jackspiering/tailarr/internal/security/names"
	"github.com/jackspiering/tailarr/internal/security/paths"
)

// BackupMode selects move (redeploy) or copy (repair snapshot).
type BackupMode string

const (
	BackupMove BackupMode = "move"
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
	_ = os.Chmod(root, 0o700)

	stamp := time.Now().UTC().Format("20060102T150405Z")
	backupPath, err := backupPathFor(root, service, stamp)
	if err != nil {
		return "", err
	}

	switch mode {
	case BackupMove:
		if err := os.Rename(servicePath, backupPath); err != nil {
			return "", fmt.Errorf("move deployment to backup: %w", err)
		}
	case BackupCopy:
		if err := copyTree(servicePath, backupPath); err != nil {
			return "", fmt.Errorf("copy deployment to backup: %w", err)
		}
	default:
		return "", fmt.Errorf("unknown backup mode: %s", mode)
	}
	// Prune older backups: they accumulate unboundedly and hold plaintext
	// secrets (e.g. TS_AUTHKEY in .env). Best-effort: a prune failure must not
	// abort an operation that already moved/copied the deployment, and must
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

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: refusing to copy symlink: %s", ErrSymlink, path)
		}
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return copyFile(path, target, info.Mode().Perm())
	})
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

// RestorePersistentData copies non-compose data directories from backup into a fresh deploy.
// It intentionally skips .env (merged separately so secrets are applied via MergeEnv).
func RestorePersistentData(backupPath, servicePath string) error {
	entries, err := os.ReadDir(backupPath)
	if err != nil {
		return err
	}
	skip := map[string]bool{
		"compose.yaml": true, "compose.yml": true,
		"docker-compose.yml": true, "docker-compose.yaml": true,
		".env": true, ".tailarr.compose.yaml": true,
	}
	for _, e := range entries {
		name := e.Name()
		if skip[name] {
			continue
		}
		src := filepath.Join(backupPath, name)
		if paths.IsSymlink(src) {
			continue
		}
		dst := filepath.Join(servicePath, name)
		if e.IsDir() {
			if err := copyTree(src, dst); err != nil {
				return err
			}
			continue
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		if err := copyFile(src, dst, info.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

// restorePartialPath is the scratch directory used while swapping a failed
// force-replace dest aside. It lives under .tailarr_backups and uses a leading
// dot so it can never collide with a ValidServiceName sibling such as "web.old".
func restorePartialPath(deployPath, service string) (string, error) {
	if err := names.ValidateServiceName(service); err != nil {
		return "", err
	}
	root := filepath.Join(deployPath, config.BackupDirName)
	if err := paths.EnsureDirMode(root, "backup directory", 0o700); err != nil {
		return "", err
	}
	return filepath.Join(root, ".partial-"+service), nil
}

// restoreDeploymentFromBackup renames backup back to dest. Any existing dest
// (a partial copy from a failed force redeploy) is first renamed aside, then
// the backup is renamed into place, and only then is the partial tree removed.
// Both renames are atomic, so dest is never left missing: the backup remains
// the source of truth until the final cleanup step.
func restoreDeploymentFromBackup(deployPath, service, backupPath, dest string) error {
	if backupPath == "" {
		return fmt.Errorf("no backup to restore")
	}
	if _, err := os.Stat(backupPath); err != nil {
		return fmt.Errorf("backup missing: %w", err)
	}
	// Refuse to restore over symlinks before touching anything.
	if st, err := os.Lstat(dest); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: cannot restore over symlink: %s", ErrSymlink, dest)
		}
		if found, err := paths.ContainsSymlinks(dest); err != nil {
			return err
		} else if found != "" {
			return fmt.Errorf("%w: partial deploy has symlink: %s", ErrSymlink, found)
		}
	}
	partial, err := restorePartialPath(deployPath, service)
	if err != nil {
		return err
	}
	// Clear any leftover scratch tree from a previously interrupted restore.
	if err := os.RemoveAll(partial); err != nil {
		return fmt.Errorf("clear previous partial for restore: %w", err)
	}
	// Move the partial dest aside so the backup can take its place atomically.
	if _, err := os.Lstat(dest); err == nil {
		if err := os.Rename(dest, partial); err != nil {
			return fmt.Errorf("move partial deploy aside: %w", err)
		}
	}
	if err := os.Rename(backupPath, dest); err != nil {
		// Backup still exists; put the partial back so dest is not left missing.
		if _, err2 := os.Lstat(partial); err2 == nil {
			_ = os.Rename(partial, dest)
		}
		return fmt.Errorf("restore deployment from backup: %w", err)
	}
	if _, err := os.Lstat(partial); err == nil {
		if err := os.RemoveAll(partial); err != nil {
			return fmt.Errorf("remove partial deploy: %w", err)
		}
	}
	return nil
}
