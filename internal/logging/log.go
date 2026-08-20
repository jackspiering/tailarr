// Package logging appends redacted events to the Tailarr log file.
package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jackspiering/tailarr/internal/security/paths"
	"github.com/jackspiering/tailarr/internal/security/redact"
)

// Logger writes redacted lines to a file.
type Logger struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
}

// New creates a logger for path. maxBytes triggers simple rotation to path.1.
func New(path string, maxBytes int64) *Logger {
	if maxBytes <= 0 {
		maxBytes = 5 * 1024 * 1024
	}
	return &Logger{path: path, maxBytes: maxBytes}
}

// Path returns the log file path.
func (l *Logger) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// Validate checks if the log path can be used (parent is not symlink, not a directory).
// Failures are returned so main can warn; Event remains silent by design.
func (l *Logger) Validate() error {
	if l == nil || l.path == "" {
		return fmt.Errorf("log path is empty")
	}
	dir := filepath.Dir(l.path)
	if err := paths.RefuseSymlinkAncestry(dir); err != nil {
		return err
	}
	if paths.IsSymlink(l.path) {
		return fmt.Errorf("log file must not be a symlink: %s", l.path)
	}
	return nil
}

// Event appends a timestamped, redacted message. Failures are silent.
func (l *Logger) Event(message string) {
	if l == nil || l.path == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	dir := filepath.Dir(l.path)
	// Refuse symlinked ancestors before creating anything.
	if err := paths.RefuseSymlinkAncestry(dir); err != nil {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	if paths.IsSymlink(dir) || paths.IsSymlink(l.path) {
		return
	}
	// Check size on every write so rotation keeps working for long-lived processes.
	l.rotateIfNeeded()
	f, err := paths.OpenFileNoFollow(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	line := fmt.Sprintf("[%s] %s\n", time.Now().UTC().Format(time.RFC3339), redact.Text(message))
	_, _ = f.WriteString(line)
	_ = f.Close()
}

func (l *Logger) rotateIfNeeded() {
	info, err := os.Stat(l.path)
	if err != nil || info.IsDir() {
		return
	}
	if info.Size() <= l.maxBytes {
		return
	}
	rotated := l.path + ".1"
	if paths.IsSymlink(rotated) {
		// Do not silently disable rotation; remove the symlink target check
		// failed, so just truncate in place rather than losing logs forever.
		_ = os.Remove(rotated)
		if paths.IsSymlink(rotated) {
			return
		}
	}
	_ = os.Rename(l.path, rotated)
	_ = paths.ChmodNoFollow(rotated, 0o600)
}
