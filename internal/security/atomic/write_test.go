package atomic

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jackspiering/tailarr/internal/security/paths"
)

func TestWriteFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.conf")
	content := "KEY=value\n"
	if err := WriteFileString(dest, content, 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Fatalf("got %q", data)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
}

func TestWriteFileMode600(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dest := filepath.Join(dir, "secrets")
	if err := WriteFileString(dest, "k=v\n", 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o want 600", info.Mode().Perm())
	}
}

func TestWriteFileOverwrites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dest := filepath.Join(dir, "f")
	if err := WriteFileString(dest, "old\n", 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileString(dest, "new\n", 0o644); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(dest)
	if string(data) != "new\n" {
		t.Fatalf("got %q", data)
	}
}

func TestWriteFileKeepsOwnerAsRoot(t *testing.T) {
	// This test replaces package vars, so it must not run in parallel.
	prevEUID, prevChown := geteuid, fchown
	t.Cleanup(func() { geteuid, fchown = prevEUID, prevChown })
	geteuid = func() int { return 0 }
	calls := 0
	gotUID, gotGID := -1, -1
	fchown = func(_ *os.File, uid, gid int) error {
		calls++
		gotUID, gotGID = uid, gid
		return nil
	}

	dir := t.TempDir()
	dest := filepath.Join(dir, "out.conf")
	if err := WriteFileString(dest, "new\n", 0o600); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("fchown called %d times for a new file", calls)
	}

	if err := os.WriteFile(dest, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantUID, wantGID, err := paths.Owner(dest)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFileString(dest, "newer\n", 0o600); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || gotUID != wantUID || gotGID != wantGID {
		t.Fatalf("fchown got uid %d gid %d x%d, want %d %d x1", gotUID, gotGID, calls, wantUID, wantGID)
	}
}
