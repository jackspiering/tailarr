// Package authkeys stores named Tailscale auth keys (mode 600).
package authkeys

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackspiering/tailarr/internal/security/atomic"
	"github.com/jackspiering/tailarr/internal/security/names"
	"github.com/jackspiering/tailarr/internal/security/paths"
	"github.com/jackspiering/tailarr/internal/security/redact"
)

// Store is an in-memory view of the auth key file.
type Store struct {
	Path string
	// Keys maps name -> value (secret). Never print values.
	Keys map[string]string
	// Order preserves file order for stable listing.
	Order []string
}

// Load reads the authkeys file. Missing file yields an empty store.
func Load(path string) (*Store, error) {
	s := &Store{Path: path, Keys: make(map[string]string)}
	if path == "" {
		return s, nil
	}
	if paths.IsSymlink(path) {
		return nil, fmt.Errorf("auth key store must not be a symlink: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	// Tighten permissions if loose. Fail closed if chmod errors (e.g. read-only FS).
	if info, err := f.Stat(); err == nil {
		if info.Mode().Perm()&0o077 != 0 {
			if err := f.Chmod(0o600); err != nil {
				return nil, fmt.Errorf("tighten authkeys permissions: %w", err)
			}
		}
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if !names.ValidAuthkeyName(name) {
			continue
		}
		if _, exists := s.Keys[name]; !exists {
			s.Order = append(s.Order, name)
		}
		s.Keys[name] = value
	}
	return s, sc.Err()
}

// Ensure creates the file with mode 600 if missing.
func Ensure(path string) error {
	if paths.IsSymlink(path) {
		return fmt.Errorf("auth key store must not be a symlink: %s", path)
	}
	parent := filepath.Dir(path)
	if err := paths.EnsureDir(parent, "auth key directory"); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		// Tighten permissions via descriptor with O_NOFOLLOW where available,
		// so a raced symlink swap cannot be chmod'd instead.
		f, err := paths.OpenFileNoFollow(path, os.O_RDWR, 0)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		return f.Chmod(0o600)
	} else if !os.IsNotExist(err) {
		return err
	}
	// Create exclusively with restrictive umask-equivalent mode.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create auth key store: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// Best-effort durability: fsync the parent directory so the new file's
	// directory entry survives power loss.
	if dir, err := os.Open(parent); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

// Save writes all keys atomically with mode 600.
func (s *Store) Save() error {
	if err := Ensure(s.Path); err != nil {
		return err
	}
	var b strings.Builder
	for _, name := range s.Order {
		val, ok := s.Keys[name]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "%s=%s\n", name, val)
	}
	return atomic.WriteFileString(s.Path, b.String(), 0o600)
}

// Put validates and stores a named key.
// Values must be single-line tskey-auth-* material (no CR/LF injection).
func (s *Store) Put(name, value string) error {
	if err := names.ValidateAuthkeyName(name); err != nil {
		return err
	}
	value = strings.TrimSpace(value)
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("TS_AUTHKEY must be a single line")
	}
	if !names.ValidTSAuthkey(value) {
		return fmt.Errorf("TS_AUTHKEY must start with tskey-auth-")
	}
	if _, exists := s.Keys[name]; !exists {
		s.Order = append(s.Order, name)
	}
	s.Keys[name] = value
	return nil
}

// Remove deletes a named key.
func (s *Store) Remove(name string) error {
	if _, ok := s.Keys[name]; !ok {
		return fmt.Errorf("auth key not found: %s", name)
	}
	delete(s.Keys, name)
	out := s.Order[:0]
	for _, n := range s.Order {
		if n != name {
			out = append(out, n)
		}
	}
	s.Order = out
	return nil
}

// Rename changes a stored key's label while keeping the secret value.
func (s *Store) Rename(oldName, newName string) error {
	if err := names.ValidateAuthkeyName(newName); err != nil {
		return err
	}
	val, ok := s.Keys[oldName]
	if !ok {
		return fmt.Errorf("auth key not found: %s", oldName)
	}
	if oldName == newName {
		return nil
	}
	if _, exists := s.Keys[newName]; exists {
		return fmt.Errorf("auth key already exists: %s", newName)
	}
	if err := s.Remove(oldName); err != nil {
		return err
	}
	return s.Put(newName, val)
}

// Names returns ordered key names.
func (s *Store) Names() []string {
	return append([]string(nil), s.Order...)
}

// RedactedList returns "name [redacted]" lines for UI/CLI.
func (s *Store) RedactedList() []string {
	lines := make([]string, 0, len(s.Order))
	for _, name := range s.Order {
		lines = append(lines, fmt.Sprintf("%s (%s)", name, redact.Preview(s.Keys[name])))
	}
	return lines
}
