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

func TestLoadMissing(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Keys) != 0 {
		t.Fatal("expected empty")
	}
}
