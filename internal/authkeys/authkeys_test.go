package authkeys

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPutSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "authkeys.conf")
	s := &Store{Path: path, Keys: make(map[string]string)}
	if err := s.Put("prod", "tskey-auth-ABCDEF"); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}

	s2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Keys["prod"] != "tskey-auth-ABCDEF" {
		t.Fatalf("got %q", s2.Keys["prod"])
	}
	list := s2.RedactedList()
	if len(list) != 1 || list[0] != "prod ([redacted])" {
		t.Fatalf("list: %v", list)
	}
}

func TestRejectBadKey(t *testing.T) {
	s := &Store{Keys: make(map[string]string)}
	if err := s.Put("x", "not-a-key"); err == nil {
		t.Fatal("expected error")
	}
	if err := s.Put("../x", "tskey-auth-x"); err == nil {
		t.Fatal("expected bad name error")
	}
}

func TestRemove(t *testing.T) {
	s := &Store{Keys: map[string]string{"a": "tskey-auth-a"}, Order: []string{"a"}}
	if err := s.Remove("a"); err != nil {
		t.Fatal(err)
	}
	if len(s.Keys) != 0 {
		t.Fatal("not empty")
	}
}

func TestRename(t *testing.T) {
	s := &Store{Keys: map[string]string{"old": "tskey-auth-OLD"}, Order: []string{"old"}}
	if err := s.Rename("old", "new"); err != nil {
		t.Fatal(err)
	}
	if s.Keys["new"] != "tskey-auth-OLD" {
		t.Fatal(s.Keys)
	}
	if _, ok := s.Keys["old"]; ok {
		t.Fatal("old still present")
	}
}

func TestLoadMissing(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Keys) != 0 {
		t.Fatal("expected empty")
	}
}

func TestLoadRefusesSymlinkFile(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.conf")
	if err := os.WriteFile(real, []byte("prod=tskey-auth-SECRET\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.conf")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	s, err := Load(link)
	if err == nil {
		t.Fatal("expected symlink file refusal")
	}
	if s != nil && s.Keys["prod"] != "" {
		t.Fatalf("must not read keys through symlink file: %v", s.Keys)
	}
	info, err := os.Lstat(real)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("must not chmod symlink target, mode %o", info.Mode().Perm())
	}
}

func TestLoadRefusesSymlinkParent(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(real, "authkeys.conf")
	if err := os.WriteFile(path, []byte("prod=tskey-auth-SECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	s, err := Load(filepath.Join(link, "authkeys.conf"))
	if err == nil {
		t.Fatal("expected symlink parent refusal")
	}
	if s != nil && s.Keys["prod"] != "" {
		t.Fatalf("must not read keys through symlink parent: %v", s.Keys)
	}
}

func TestLoadTightensLoosePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "authkeys.conf")
	if err := os.WriteFile(path, []byte("prod=tskey-auth-ABCDEF\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Keys["prod"] != "tskey-auth-ABCDEF" {
		t.Fatalf("got %q", s.Keys["prod"])
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
}
