package main

import (
	"os"
	"path/filepath"
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
