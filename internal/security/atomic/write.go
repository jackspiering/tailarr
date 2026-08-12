// Package atomic provides safe atomic file writes.
package atomic

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jackspiering/tailarr/internal/security/paths"
)

// WriteFile writes data to dest atomically (temp file + rename) with the
// given mode. Parent must not be a symlink (including ancestors).
func WriteFile(dest string, data []byte, mode os.FileMode) error {
	parent := filepath.Dir(dest)
	if err := paths.RefuseSymlinkAncestry(parent); err != nil {
		return fmt.Errorf("parent directory: %w", err)
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create parent: %w", err)
	}
	if paths.IsSymlink(parent) {
		return fmt.Errorf("parent directory must not be a symlink: %s", parent)
	}
	if paths.IsSymlink(dest) {
		return fmt.Errorf("destination must not be a symlink: %s", dest)
	}

	tmp, err := os.CreateTemp(parent, ".tailarr-write.*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	cleanup = false
	// Re-apply mode after rename (some filesystems ignore temp mode).
	// Chmod via O_NOFOLLOW so a raced symlink cannot redirect the mode change.
	if err := paths.ChmodNoFollow(dest, mode); err != nil {
		return fmt.Errorf("chmod dest: %w", err)
	}
	// Best-effort durability: fsync the parent directory so the rename
	// survives power loss.
	if dir, err := os.Open(parent); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

// WriteFileString is WriteFile for string content.
func WriteFileString(dest, content string, mode os.FileMode) error {
	return WriteFile(dest, []byte(content), mode)
}
