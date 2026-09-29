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
	printed  *[]string
}

func (u scriptUI) Confirm(string, bool) (bool, error) {
	if u.confirms != nil {
		*u.confirms++
	}
	return u.confirm, nil
}
func (u scriptUI) Line(string, string) (string, error) { return u.line, nil }
func (u scriptUI) Secret(string) (string, error)       { return u.secret, nil }
func (u scriptUI) Printf(format string, args ...any) {
	if u.printed != nil {
		*u.printed = append(*u.printed, strings.TrimSpace(strings.ReplaceAll(format, "%s", "")))
	}
}

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
	r := runBatch(cfg, nil, scriptUI{confirms: &confirms}, batchStop, []string{"web", "db"})
	if !r.canceled || r.ok+r.failed+r.skipped != 0 {
		t.Fatalf("declined batch must not run, got %+v", r)
	}
	if confirms != 1 {
		t.Fatalf("expected one summary confirm, got %d", confirms)
	}
	if got := r.summary(batchStop, []string{"web", "db"}); got != "Canceled." {
		t.Fatalf("summary = %q", got)
	}
}

func TestRunBatchLogsFailuresAndStreamsProgress(t *testing.T) {
	cfg := batchConfig(t)
	log := logging.New(cfg.LogPath, cfg.LogMaxBytes)
	var printed []string
	r := runBatch(cfg, log, scriptUI{confirm: true, printed: &printed}, batchStop, []string{"web"})
	if r.failed != 1 || r.failedNames[0] != "web" {
		t.Fatalf("expected an error for a missing deployment, got %+v", r)
	}
	data, err := os.ReadFile(cfg.LogPath)
	if err != nil || !strings.Contains(string(data), "stop web failed") {
		t.Fatalf("failure not logged: %v %q", err, data)
	}
	if len(printed) < 2 || printed[0] != "==>" || printed[1] != "error:" {
		t.Fatalf("progress and error not streamed through Printf: %q", printed)
	}
	if got := r.summary(batchStop, []string{"web"}); got != "✖ Stop failed for web · 0 of 1 ok" {
		t.Fatalf("summary = %q", got)
	}
}

func TestRunBatchSkipsRemainingAfterInterrupt(t *testing.T) {
	cfg := batchConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	interrupt.Set(ctx)
	t.Cleanup(interrupt.Clear)
	var printed []string
	r := runBatch(cfg, nil, scriptUI{confirm: true, printed: &printed}, batchRestart, []string{"web", "db"})
	if strings.Count(strings.Join(printed, "\n"), "skipped: interrupted") != 2 || r.skipped != 2 {
		t.Fatalf("expected both services skipped, got %+v %q", r, printed)
	}
	if got := r.summary(batchRestart, []string{"web", "db"}); got != "✖ Restart interrupted · 0 of 2 ok, 2 skipped" {
		t.Fatalf("summary = %q", got)
	}
}

func TestBatchSummaryOnSuccess(t *testing.T) {
	if got := (batchResult{ok: 1}).summary(batchDeploy, []string{"web"}); got != "✔ Deployed web" {
		t.Fatalf("single: %q", got)
	}
	if got := (batchResult{ok: 3}).summary(batchRestart, []string{"a", "b", "c"}); got != "✔ Restarted 3 services" {
		t.Fatalf("many: %q", got)
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

func TestAuthkeyActionUsesPickedName(t *testing.T) {
	cfg := batchConfig(t)
	if err := os.WriteFile(cfg.AuthkeysPath, []byte("home=tskey-auth-home\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := runAuthkeyAction(cfg, scriptUI{line: "office"}, "rename", "home"); got != "✔ Renamed home to office" {
		t.Fatalf("rename: %q", got)
	}
	if got := runAuthkeyAction(cfg, scriptUI{secret: "tskey-auth-new"}, "replace", "office"); got != "✔ Updated auth key office" {
		t.Fatalf("replace: %q", got)
	}
	if got := runAuthkeyAction(cfg, scriptUI{secret: "tskey-auth-x"}, "replace", "missing"); !strings.Contains(got, "not found") {
		t.Fatalf("replace missing: %q", got)
	}
	if got := runAuthkeyAction(cfg, scriptUI{confirm: true}, "remove", "office"); got != "✔ Removed auth key office" {
		t.Fatalf("remove: %q", got)
	}
	data, _ := os.ReadFile(cfg.AuthkeysPath)
	if strings.TrimSpace(string(data)) != "" {
		t.Fatalf("store not empty: %q", data)
	}
}

func TestRefreshSummaryUpToDate(t *testing.T) {
	if got := refreshSummary("Already up to date.\n"); got != "✔ Catalog is up to date." {
		t.Fatalf("got %q", got)
	}
	if got := refreshSummary("Updating a..b\n"); !strings.HasSuffix(got, "✔ Catalog refreshed.") || !strings.HasPrefix(got, "Updating") {
		t.Fatalf("got %q", got)
	}
}

func TestFilterNamesIgnoresCase(t *testing.T) {
	if got := strings.Join(filterNames([]string{"jellyfin", "plex", "immich"}, " PLE "), ","); got != "plex" {
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

func TestBatchSummaryNamesInterrupt(t *testing.T) {
	r := batchResult{failed: 1, failedNames: []string{"web"}, interrupted: true, skipped: 1}
	if got := r.summary(batchRestart, []string{"web", "db"}); got != "✖ Restart interrupted · 0 of 2 ok, 1 skipped" {
		t.Fatalf("summary = %q", got)
	}
}
