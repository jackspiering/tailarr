package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAndSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailarr.conf")
	body := `# comment
TAILARR_REPO_URL=https://example.com/ScaleTail.git
TAILARR_REPO_PATH=/tmp/st
TAILARR_DEPLOY_PATH=/tmp/deploy
TAILARR_LOG_PATH=/tmp/log
TAILARR_AUTHKEYS_PATH=/tmp/keys
TAILARR_LOG_MAX_BYTES=12345
UNKNOWN=ignore
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	// Clear env that might override.
	for _, k := range []string{
		"TAILARR_REPO_URL", "TAILARR_REPO_PATH", "TAILARR_DEPLOY_PATH",
		"TAILARR_LOG_PATH", "TAILARR_AUTHKEYS_PATH",
		"TAILARR_LOG_MAX_BYTES", "TAILARR_ASSUME_YES",
	} {
		t.Setenv(k, "")
	}

	cfg := Default()
	cfg.ConfigPath = path
	if err := Load(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.RepoURL != "https://example.com/ScaleTail.git" {
		t.Fatalf("RepoURL: %s", cfg.RepoURL)
	}
	if cfg.RepoPath != "/tmp/st" {
		t.Fatalf("RepoPath: %s", cfg.RepoPath)
	}
	if cfg.LogMaxBytes != 12345 {
		t.Fatalf("LogMaxBytes: %d", cfg.LogMaxBytes)
	}

	out := filepath.Join(dir, "out.conf")
	cfg.ConfigPath = out
	cfg.DeployPath = "/new/deploy"
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(data), "TAILARR_DEPLOY_PATH=/new/deploy") {
		t.Fatalf("saved body:\n%s", data)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode %o want 0600", info.Mode().Perm())
	}
}

func TestSaveRedactsURLCredentials(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "c.conf")
	cfg := Default()
	cfg.ConfigPath = out
	// Save rejects credential URLs via ValidateRepoURL.
	cfg.RepoURL = "https://user:token@github.com/org/repo.git"
	if err := Save(cfg); err == nil {
		t.Fatal("expected save to reject credential URL")
	}
	// Display redacts even if value is in memory.
	cfg.RepoURL = "https://user:token@github.com/org/repo.git"
	s := cfg.String()
	if contains(s, "token") {
		t.Fatalf("config String leaked credentials:\n%s", s)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.conf")
	if err := os.WriteFile(path, []byte("TAILARR_REPO_PATH=/from/file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAILARR_REPO_PATH", "/from/env")
	cfg := Default()
	cfg.ConfigPath = path
	if err := Load(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.RepoPath != "/from/env" {
		t.Fatalf("got %s", cfg.RepoPath)
	}
}

func TestMissingFileOK(t *testing.T) {
	cfg := Default()
	cfg.ConfigPath = filepath.Join(t.TempDir(), "nope.conf")
	if err := Load(&cfg); err != nil {
		t.Fatal(err)
	}
}

func TestParseIgnoresBadLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.conf")
	body := `
not-a-kv
# comment
TAILARR_REPO_URL=https://github.com/tailscale-dev/ScaleTail.git
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"TAILARR_REPO_URL"} {
		t.Setenv(k, "")
	}
	cfg := Default()
	cfg.ConfigPath = path
	if err := Load(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.RepoURL != "https://github.com/tailscale-dev/ScaleTail.git" {
		t.Fatal(cfg.RepoURL)
	}
}

func TestLoadStripsBOM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.conf")
	if err := os.WriteFile(path, []byte("\ufeffTAILARR_REPO_URL=https://github.com/tailscale-dev/ScaleTail.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAILARR_REPO_URL", "")
	cfg := Default()
	cfg.ConfigPath = path
	if err := Load(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.RepoURL != "https://github.com/tailscale-dev/ScaleTail.git" {
		t.Fatalf("BOM dropped first key: %s", cfg.RepoURL)
	}

	mid := filepath.Join(dir, "mid.conf")
	body := "TAILARR_REPO_URL=https://github.com/tailscale-dev/ScaleTail.git\n" +
		"\ufeffTAILARR_LOG_PATH=/tmp/from-bom\n"
	if err := os.WriteFile(mid, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAILARR_LOG_PATH", "")
	cfg = Default()
	cfg.ConfigPath = mid
	if err := Load(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.LogPath != "/tmp/from-bom" {
		t.Fatalf("mid-file BOM dropped key: %s", cfg.LogPath)
	}
}

func TestLoadRefusesSymlinkFile(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.conf")
	if err := os.WriteFile(real, []byte("TAILARR_REPO_PATH=/tmp/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.conf")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	cfg.ConfigPath = link
	if err := Load(&cfg); err == nil {
		t.Fatal("expected symlink refusal")
	}
}

func TestLoadRefusesSymlinkedDir(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "c.conf"), []byte("TAILARR_REPO_PATH=/tmp/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	cfg.ConfigPath = filepath.Join(link, "c.conf")
	if err := Load(&cfg); err == nil {
		t.Fatal("expected symlinked-dir refusal")
	}
}

func TestLoadRejectsInvalidFileRepoURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.conf")
	if err := os.WriteFile(path, []byte("TAILARR_REPO_URL=http://example.com/repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAILARR_REPO_URL", "")
	cfg := Default()
	cfg.ConfigPath = path
	if err := Load(&cfg); err == nil {
		t.Fatal("expected invalid file RepoURL to be rejected")
	}
}

func TestApplyEnvRejectsInvalidRepoURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.conf")
	if err := os.WriteFile(path, []byte("# empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAILARR_REPO_URL", "http://example.com/repo")
	cfg := Default()
	cfg.ConfigPath = path
	if err := Load(&cfg); err == nil {
		t.Fatal("expected invalid env RepoURL to be rejected")
	}
}

func TestLoadRejectsInvalidLogMaxBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.conf")
	if err := os.WriteFile(path, []byte("TAILARR_LOG_MAX_BYTES=garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAILARR_LOG_MAX_BYTES", "")
	cfg := Default()
	cfg.ConfigPath = path
	if err := Load(&cfg); err == nil {
		t.Fatal("expected invalid TAILARR_LOG_MAX_BYTES to be rejected")
	}
}

func TestLoadRejectsInvalidEnvLogMaxBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.conf")
	if err := os.WriteFile(path, []byte("# empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAILARR_LOG_MAX_BYTES", "-5")
	cfg := Default()
	cfg.ConfigPath = path
	if err := Load(&cfg); err == nil {
		t.Fatal("expected invalid env TAILARR_LOG_MAX_BYTES to be rejected")
	}
}
func TestLoadAcceptsWhitespaceEnvLogMaxBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.conf")
	if err := os.WriteFile(path, []byte("# empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAILARR_LOG_MAX_BYTES", " 1048576 ")
	cfg := Default()
	cfg.ConfigPath = path
	if err := Load(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.LogMaxBytes != 1048576 {
		t.Fatalf("LogMaxBytes = %d", cfg.LogMaxBytes)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		})())
}
