package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/logging"
)

func TestReopenLoggerValidatesSymlinkedAncestor(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(dir, "old.log")
	log := logging.New(oldPath, 1024)
	cfg := config.Config{
		LogPath:     filepath.Join(link, "tailarr.log"),
		LogMaxBytes: 1024,
	}
	next, err := reopenLogger(log, cfg)
	if err == nil {
		t.Fatal("expected validate error for symlinked log ancestor")
	}
	if next == nil || next.Path() != cfg.LogPath {
		t.Fatalf("replacement logger path = %v", next)
	}
	next.Event("should not be written")
	if _, statErr := os.Stat(cfg.LogPath); statErr == nil {
		t.Fatal("event wrote through a symlinked ancestor")
	}
}

func TestReopenLoggerKeepsLoggerWhenPathUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailarr.log")
	log := logging.New(path, 1024)
	cfg := config.Config{LogPath: path, LogMaxBytes: 1024}
	next, err := reopenLogger(log, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if next != log {
		t.Fatal("unchanged log path should keep the logger")
	}
}

// TestMainWithoutTTYIgnoresConfig re-runs the test binary as the program.
func TestMainWithoutTTYIgnoresConfig(t *testing.T) {
	if os.Getenv("TAILARR_TEST_RUN_MAIN") == "1" {
		main()
		return
	}
	unreadable := filepath.Join(t.TempDir(), "tailarr.conf")
	if err := os.WriteFile(unreadable, []byte("TAILARR_REPO_PATH=relative/path\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainWithoutTTYIgnoresConfig$")
	// stdin is /dev/null, so there is no terminal. The config has an invalid
	// (relative) path, which would fail config.Load if it ran first.
	cmd.Env = append(os.Environ(), "TAILARR_TEST_RUN_MAIN=1", "TAILARR_CONFIG_PATH="+unreadable)
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("expected exit status 1, got %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Tailarr is interactive; run inside a terminal.") {
		t.Fatalf("expected the interactive message, got:\n%s", out)
	}
	if strings.Contains(string(out), "load config") {
		t.Fatalf("config was loaded before the terminal check:\n%s", out)
	}
}
