package ui

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/interrupt"
	"github.com/jackspiering/tailarr/internal/logging"
	"github.com/jackspiering/tailarr/internal/prompt"
)

// scriptUI answers prompts from fixed values and counts confirms.
type scriptUI struct {
	confirm  bool
	line     string
	secret   string
	confirms *int
}

func (u scriptUI) Confirm(string, bool) (bool, error) {
	if u.confirms != nil {
		*u.confirms++
	}
	return u.confirm, nil
}
func (u scriptUI) Line(string, string) (string, error) { return u.line, nil }
func (u scriptUI) Secret(string) (string, error)       { return u.secret, nil }
func (u scriptUI) Printf(string, ...any)               {}

func batchConfig(t *testing.T) config.Config {
	t.Helper()
	root := t.TempDir()
	deployPath := filepath.Join(root, "stacks")
	if err := os.MkdirAll(deployPath, 0o755); err != nil {
		t.Fatal(err)
	}
	return config.Config{
		ConfigPath:   filepath.Join(root, "tailarr.conf"),
		RepoURL:      config.DefaultRepoURL,
		RepoPath:     filepath.Join(root, "scaletail"),
		DeployPath:   deployPath,
		LogPath:      filepath.Join(root, "tailarr.log"),
		AuthkeysPath: filepath.Join(root, "authkeys.conf"),
		LogMaxBytes:  config.DefaultLogMaxBytes,
	}
}

func TestRunBatchAsksBeforeStopping(t *testing.T) {
	cfg := batchConfig(t)
	confirms := 0
	out := runBatchWith(cfg, nil, scriptUI{confirms: &confirms}, multiStop, []string{"web", "db"})
	if out != "Canceled." {
		t.Fatalf("declined batch must not run, got %q", out)
	}
	if confirms != 1 {
		t.Fatalf("expected one summary confirm, got %d", confirms)
	}
}

func TestRunBatchLogsFailures(t *testing.T) {
	cfg := batchConfig(t)
	log := logging.New(cfg.LogPath, cfg.LogMaxBytes)
	out := runBatchWith(cfg, log, scriptUI{confirm: true}, multiStop, []string{"web"})
	if !strings.Contains(out, "error:") {
		t.Fatalf("expected an error for a missing deployment, got %q", out)
	}
	data, err := os.ReadFile(cfg.LogPath)
	if err != nil || !strings.Contains(string(data), "stop web failed") {
		t.Fatalf("failure not logged: %v %q", err, data)
	}
}

func TestRunBatchSkipsRemainingAfterInterrupt(t *testing.T) {
	cfg := batchConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	interrupt.Set(ctx)
	t.Cleanup(interrupt.Clear)
	out := runBatchWith(cfg, nil, scriptUI{confirm: true}, multiRestart, []string{"web", "db"})
	if strings.Count(out, "skipped: interrupted") != 2 {
		t.Fatalf("expected both services skipped, got %q", out)
	}
}

func TestSharedAuthkeyRejectsUnknownNameAndBadKey(t *testing.T) {
	cfg := batchConfig(t)
	if err := os.WriteFile(cfg.AuthkeysPath, []byte("home=tskey-auth-home\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if key, err := sharedAuthkey(cfg, scriptUI{confirm: true, line: "home"}); err != nil || key != "tskey-auth-home" {
		t.Fatalf("stored key: %q %v", key, err)
	}
	if _, err := sharedAuthkey(cfg, scriptUI{confirm: true, line: "hom"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected unknown name error, got %v", err)
	}
	if _, err := sharedAuthkey(cfg, scriptUI{confirm: true, secret: "not-a-key"}); err == nil {
		t.Fatal("expected invalid pasted key error")
	}
	if key, err := sharedAuthkey(cfg, scriptUI{confirm: true}); err != nil || key != "" {
		t.Fatalf("empty paste must fall back to per-service prompts: %q %v", key, err)
	}
}

func TestApplyFilterKeepsSelection(t *testing.T) {
	m := model{
		screen:  screenMultiSelect,
		allOpts: []string{"adguard", "immich", "jellyfin", "plex"},
		opts:    []string{"adguard", "immich", "jellyfin", "plex"},
		picked:  map[int]bool{3: true},
	}
	got := m.applyFilter("JELLY")
	if strings.Join(got.opts, ",") != "jellyfin,plex" {
		t.Fatalf("filtered opts = %v", got.opts)
	}
	if !got.picked[1] || got.picked[0] {
		t.Fatalf("selection lost: %v", got.picked)
	}
	all := got.applyFilter("")
	if len(all.opts) != 4 || !all.picked[3] {
		t.Fatalf("clearing the filter must restore all rows: %v %v", all.opts, all.picked)
	}
}

func TestMultiSelectClearKey(t *testing.T) {
	m := model{screen: screenMultiSelect, opts: []string{"a", "b"}, picked: map[int]bool{0: true, 1: true}}
	next, _ := m.Update(digitKey('n'))
	if got := next.(model); len(got.picked) != 0 {
		t.Fatalf("n must clear the selection: %v", got.picked)
	}
}

func TestSearchCatalogFilters(t *testing.T) {
	repo := t.TempDir()
	for _, name := range []string{"jellyfin", "plex", "immich"} {
		dir := filepath.Join(repo, "services", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"compose.yaml", ".env"} {
			if err := os.WriteFile(filepath.Join(dir, f), []byte("\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	out := searchCatalog(repo, "ple")
	if !strings.Contains(out, "1 of 3 services") || !strings.Contains(out, "- plex") || strings.Contains(out, "jellyfin") {
		t.Fatalf("unexpected search result: %q", out)
	}
	if out := searchCatalog(repo, "zzz"); !strings.Contains(out, "No services match") {
		t.Fatalf("expected no match, got %q", out)
	}
}

func TestRefreshSummaryUpToDate(t *testing.T) {
	if got := refreshSummary("Already up to date.\n"); got != "Catalog is up to date." {
		t.Fatalf("got %q", got)
	}
	if got := refreshSummary("Updating a..b\n"); !strings.HasPrefix(got, "Catalog refreshed.") {
		t.Fatalf("got %q", got)
	}
}

func TestEditConfigDoesNotSaveEnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TAILARR_DEPLOY_PATH", filepath.Join(dir, "env-stacks"))
	cfg := config.Default()
	cfg.ConfigPath = filepath.Join(dir, "tailarr.conf")
	if err := config.Load(&cfg); err != nil {
		t.Fatal(err)
	}
	in := strings.NewReader(strings.Repeat("\n", 5))
	ui := &prompt.Std{In: in, Out: io.Discard, Err: io.Discard}
	if text, saved := editConfigInteractive(&cfg, ui); !saved {
		t.Fatal(text)
	}
	data, err := os.ReadFile(cfg.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "env-stacks") {
		t.Fatalf("environment override was saved:\n%s", data)
	}
	if cfg.DeployPath != filepath.Join(dir, "env-stacks") {
		t.Fatalf("session must keep the environment override, got %s", cfg.DeployPath)
	}
}
