// Package paths provides path containment and symlink safety helpers.
package paths

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// IsSymlink reports whether path exists and is a symbolic link.
func IsSymlink(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeSymlink != 0
}

// RefuseSymlink returns an error if path is a symlink.
func RefuseSymlink(path, label string) error {
	if IsSymlink(path) {
		return fmt.Errorf("%s must not be a symlink: %s", label, path)
	}
	return nil
}

// AbsExistingDir returns the physical absolute path of an existing directory.
func AbsExistingDir(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("directory does not exist: %s", path)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("path must not be a symlink: %s", path)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// Within reports whether path is strictly inside root (after resolving
// the parent of path and the root). Both must exist as directories for the
// parent resolution; path itself may not exist yet.
func Within(path, root string) (bool, error) {
	rootAbs, err := AbsExistingDir(root)
	if err != nil {
		return false, err
	}
	parent := filepath.Dir(path)
	parentInfo, err := os.Stat(parent)
	if err != nil || !parentInfo.IsDir() {
		return false, fmt.Errorf("parent directory does not exist: %s", parent)
	}
	parentAbs, err := filepath.EvalSymlinks(parent)
	if err != nil {
		parentAbs, err = filepath.Abs(parent)
		if err != nil {
			return false, err
		}
	}
	base := filepath.Base(path)
	joined := filepath.Join(parentAbs, base)
	// Normalize for comparison.
	joined = filepath.Clean(joined)
	rootAbs = filepath.Clean(rootAbs)
	sep := string(os.PathSeparator)
	if joined == rootAbs {
		return false, nil // path is the root itself, not within
	}
	return strings.HasPrefix(joined, rootAbs+sep), nil
}

// JoinUnder joins name under root after validating the service name is a
// single path segment and the result stays within root.
func JoinUnder(root, name string) (string, error) {
	if name == "" || strings.Contains(name, string(os.PathSeparator)) || name == "." || name == ".." {
		return "", fmt.Errorf("invalid path segment: %q", name)
	}
	if strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid path segment: %q", name)
	}
	rootAbs, err := AbsExistingDir(root)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(rootAbs, name)
	ok, err := Within(candidate, rootAbs)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("path escaped root: %s", candidate)
	}
	if IsSymlink(candidate) {
		return "", fmt.Errorf("refusing to operate on symlink: %s", candidate)
	}
	return candidate, nil
}

// ContainsSymlinks walks root (non-symlink) and returns the first symlink found.
// Empty string means no symlinks.
func ContainsSymlinks(root string) (string, error) {
	if err := RefuseSymlink(root, "path"); err != nil {
		return root, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", root)
	}
	var found string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return found, nil
}

// RefuseSymlinkAncestry walks path and every existing ancestor; returns an
// error if any component is a symlink. Missing leaf path is OK (creation
// target); missing intermediate parents are also OK until the first existing
// ancestor is checked.
func RefuseSymlinkAncestry(path string) error {
	if path == "" {
		return fmt.Errorf("path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	// Walk from abs up to root; check each existing component with Lstat.
	cur := abs
	for {
		info, err := os.Lstat(cur)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("path component must not be a symlink: %s", cur)
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return nil
}

// EnsureDir creates path if missing; refuses symlinks (including ancestors)
// and non-directories.
func EnsureDir(path, label string) error {
	if IsSymlink(path) {
		return fmt.Errorf("%s must not be a symlink: %s", label, path)
	}
	// Refuse symlinked parents that would redirect MkdirAll.
	parent := filepath.Dir(path)
	if parent != path {
		if err := RefuseSymlinkAncestry(parent); err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
	}
	info, err := os.Lstat(path)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s exists but is not a directory: %s", label, path)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	return os.MkdirAll(path, 0o755)
}

// EnsureDirMode creates path with the given mode if missing.
func EnsureDirMode(path, label string, mode os.FileMode) error {
	if err := EnsureDir(path, label); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}
