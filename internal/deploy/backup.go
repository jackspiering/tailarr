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
	backupPath := filepath.Join(root, fmt.Sprintf("%s-%s", service, stamp))
	for i := 1; ; i++ {
		if _, err := os.Stat(backupPath); os.IsNotExist(err) {
			break
		}
		backupPath = filepath.Join(root, fmt.Sprintf("%s-%s-%d", service, stamp, i))
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
	return backupPath, nil
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
	prefix := service + "-"
	var matches []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
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
	defer func() { _ = out.Close() }()
	_, err = io.Copy(out, in)
	return err
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

// restoreDeploymentFromBackup renames backup back to dest when dest is missing
// or incomplete after a failed force redeploy.
func restoreDeploymentFromBackup(backupPath, dest string) error {
	if backupPath == "" {
		return fmt.Errorf("no backup to restore")
	}
	if _, err := os.Stat(backupPath); err != nil {
		return fmt.Errorf("backup missing: %w", err)
	}
	// If dest exists from a partial copy, remove it only if it has no symlinks
	// and is clearly a half-written tree under the same parent.
	if st, err := os.Lstat(dest); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: cannot restore over symlink: %s", ErrSymlink, dest)
		}
		if found, err := paths.ContainsSymlinks(dest); err != nil {
			return err
		} else if found != "" {
			return fmt.Errorf("%w: partial deploy has symlink: %s", ErrSymlink, found)
		}
		if err := os.RemoveAll(dest); err != nil {
			return fmt.Errorf("clear partial deploy for restore: %w", err)
		}
	}
	if err := os.Rename(backupPath, dest); err != nil {
		return fmt.Errorf("restore deployment from backup: %w", err)
	}
	return nil
}
