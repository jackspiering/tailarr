package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRefusesSymlinkedAncestor(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	l := New(filepath.Join(link, "t.log"), 100)
	if err := l.Validate(); err == nil {
		t.Fatal("expected validate error for symlinked ancestor")
	}
}

func TestRefusesSymlinkedAncestor(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(link, "logs", "t.log")
	l := New(path, 100)
	l.Event("should not be written")
	if _, err := os.Stat(path); err == nil {
		t.Fatal("log created under symlinked ancestor")
	}
}

func TestRotateOnEveryEvent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.log")
	// Tiny max so second write triggers rotation.
	l := New(path, 40)
	l.Event("first-line-long-enough-to-fill")
	// Grow past max.
	for i := 0; i < 5; i++ {
		l.Event("more data for rotation check " + strings.Repeat("x", 20))
	}
	// Either current log exists and is small, or .1 rotation exists.
	if _, err := os.Stat(path + ".1"); err != nil {
		// Rotation may have happened multiple times; ensure file exists and is bounded.
		info, err2 := os.Stat(path)
		if err2 != nil {
			t.Fatalf("log missing: %v / %v", err, err2)
		}
		if info.Size() > 200 {
			t.Fatalf("log grew without rotation: %d", info.Size())
		}
	}
}

func TestValidateReportsUnwritableLogFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes read-only files; cannot test an unwritable log")
	}
	path := filepath.Join(t.TempDir(), "tailarr.log")
	if err := os.WriteFile(path, []byte("old\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	err := New(path, 100).Validate()
	if err == nil {
		t.Fatal("Validate accepted a log file it cannot append to")
	}
	if !strings.Contains(err.Error(), "not writable") {
		t.Errorf("error does not say not writable: %v", err)
	}
}

func TestValidateAcceptsMissingLogFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tailarr.log")
	if err := New(path, 100).Validate(); err != nil {
		t.Fatalf("Validate rejected a missing log file: %v", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("Validate created the log file: %v", err)
	}
}
