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
	defer f.Close()

	// Tighten permissions if loose.
	if info, err := f.Stat(); err == nil {
		if info.Mode().Perm()&0o077 != 0 {
			_ = os.Chmod(path, 0o600)
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
		return os.Chmod(path, 0o600)
	} else if !os.IsNotExist(err) {
		return err
	}
	// Create exclusively with restrictive umask-equivalent mode.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create auth key store: %w", err)
	}
	return f.Close()
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
	// Also write any keys not in Order (defensive).
	for name, val := range s.Keys {
		found := false
		for _, n := range s.Order {
			if n == name {
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(&b, "%s=%s\n", name, val)
		}
	}
	return atomic.WriteFileString(s.Path, b.String(), 0o600)
}

// Put validates and stores a named key.
func (s *Store) Put(name, value string) error {
	if err := names.ValidateAuthkeyName(name); err != nil {
		return err
	}
	if !names.ValidTSAuthkey(value) {
		return fmt.Errorf("TS_AUTHKEY must start with tskey-auth-")
	}
	if _, exists := s.Keys[name]; !exists {
		s.Order = append(s.Order, name)
	}
	s.Keys[name] = strings.TrimSpace(value)
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
