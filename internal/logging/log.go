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
	checked  bool
}

// New creates a logger for path. maxBytes triggers simple rotation to path.1.
func New(path string, maxBytes int64) *Logger {
	if maxBytes <= 0 {
		maxBytes = 5 * 1024 * 1024
	}
	return &Logger{path: path, maxBytes: maxBytes}
}

// Event appends a timestamped, redacted message. Failures are silent.
func (l *Logger) Event(message string) {
	if l == nil || l.path == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	dir := filepath.Dir(l.path)
	_ = os.MkdirAll(dir, 0o755)
	if paths.IsSymlink(dir) || paths.IsSymlink(l.path) {
		return
	}
	if !l.checked {
		l.checked = true
		l.rotateIfNeeded()
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	line := fmt.Sprintf("[%s] %s\n", time.Now().UTC().Format(time.RFC3339), redact.Text(message))
	_, _ = f.WriteString(line)
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
		return
	}
	_ = os.Rename(l.path, rotated)
	_ = os.Chmod(rotated, 0o600)
}
