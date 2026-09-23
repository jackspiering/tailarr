package upgrade

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// fakeGH puts a gh script that exits with code on PATH, or no gh at all
// when code is negative.
func fakeGH(t *testing.T, code int) {
	t.Helper()
	dir := t.TempDir()
	if code >= 0 {
		script := "#!/bin/sh\necho attestation output\nexit " + strconv.Itoa(code) + "\n"
		if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func TestCheckProvenance(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed fake gh")
	}
	fakeGH(t, 0)
	if ok, _, err := checkProvenance("asset", "o/r"); !ok || err != nil {
		t.Fatalf("verified attestation: %v %v", ok, err)
	}
	fakeGH(t, 1)
	if _, _, err := checkProvenance("asset", "o/r"); err == nil || !strings.Contains(err.Error(), "attestation check failed") {
		t.Fatalf("failed attestation must stop the upgrade, got %v", err)
	}
	fakeGH(t, ghNotLoggedIn)
	if ok, note, err := checkProvenance("asset", "o/r"); ok || err != nil || !strings.Contains(note, "gh auth login") {
		t.Fatalf("logged-out gh must skip with a note: %v %q %v", ok, note, err)
	}
	fakeGH(t, -1)
	if ok, note, err := checkProvenance("asset", "o/r"); ok || err != nil || !strings.Contains(note, "install the GitHub CLI") {
		t.Fatalf("missing gh must skip with a note: %v %q %v", ok, note, err)
	}
}
