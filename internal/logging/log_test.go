package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
