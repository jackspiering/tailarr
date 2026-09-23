package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/deploy"
	"github.com/jackspiering/tailarr/internal/interrupt"
	"github.com/jackspiering/tailarr/internal/logging"
	"github.com/jackspiering/tailarr/internal/prompt"
)

func digitKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

func TestServicesMenuUsesApply(t *testing.T) {
	ids := map[string]bool{}
	for _, item := range servicesMenuItems() {
		ids[item.id] = true
	}
	if !ids["apply"] {
		t.Fatal("services menu must include apply")
	}
	if ids["update"] {
		t.Fatal("update is not an operator verb")
	}
	for _, item := range maintenanceMenuItems() {
		if item.id == "repair" {
			t.Fatal("repair is not an operator verb")
		}
	}
}

func TestMultiSelectNumericShortcutsMatchView(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	base := model{
		screen: screenMultiSelect,
		opts:   []string{"web"},
		items: []menuItem{
			{id: "run", label: "Run on selection", desc: "run"},
			{id: "cancel", label: "Cancel", desc: "cancel"},
		},
		picked: map[int]bool{},
	}
	view := base.render()
	for _, want := range []string{
		"1  [ ] web",
		"2  Run on selection",
		"3  Cancel",
		"1-9 select/run",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}

	next, _ := base.Update(digitKey('1'))
	selected := next.(model)
	if !selected.picked[0] {
		t.Fatal("digit 1 should toggle the first service")
	}

	actionBase := base
	actionBase.picked = map[int]bool{}
	next, _ = actionBase.Update(digitKey('2'))
	action := next.(model)
	if action.cursor != 1 || action.status != "No services selected." {
		t.Fatalf("digit 2 should activate Run on selection: cursor=%d status=%q", action.cursor, action.status)
	}
}

func enterKey() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: tea.KeyEnter}
}

func ctrlCKey() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
}

func itemCursor(items []menuItem, id string) int {
	for i, item := range items {
		if item.id == id {
			return i
		}
	}
	return 0
}

func TestResultMsgDoesNotInstallCfgWithoutPointer(t *testing.T) {
	m := model{cfg: config.Config{DeployPath: "/old/stacks"}, screen: screenMain}
	next, _ := m.Update(resultMsg{text: "Error saving: permission denied"})
	got := next.(model)
	if got.cfg.DeployPath != "/old/stacks" {
		t.Fatalf("error result installed cfg: %s", got.cfg.DeployPath)
	}
}

func TestResultMsgInstallsLogger(t *testing.T) {
	dir := t.TempDir()
	newLog := filepath.Join(dir, "new.log")
	log := logging.New(newLog, 1024)
	m := model{cfg: config.Config{LogPath: filepath.Join(dir, "old.log")}, log: logging.New(filepath.Join(dir, "old.log"), 1024)}
	next, _ := m.Update(resultMsg{
		text: "Saved config: x",
		cfg:  &config.Config{LogPath: newLog, DeployPath: "/new/stacks"},
		log:  log,
	})
	got := next.(model)
	if got.cfg.LogPath != newLog {
		t.Fatalf("cfg log path = %s", got.cfg.LogPath)
	}
	got.log.Event("deployed service web")
	data, err := os.ReadFile(newLog)
	if err != nil || !strings.Contains(string(data), "deployed service web") {
		t.Fatalf("event not in new log: %v %q", err, data)
	}
}

func TestEditConfigInteractiveDoesNotMutateOnSaveError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		ConfigPath:   filepath.Join(blocker, "tailarr.conf"),
		RepoURL:      "https://github.com/tailscale-dev/ScaleTail.git",
		RepoPath:     "/opt/tailarr/scaletail",
		DeployPath:   "/opt/docker/stacks",
		LogPath:      "/opt/tailarr/logs/tailarr.log",
		AuthkeysPath: "/opt/tailarr/authkeys.conf",
		LogMaxBytes:  config.DefaultLogMaxBytes,
	}
	original := cfg
	in := strings.NewReader(strings.Join([]string{
		"",
		"/opt/tailarr/scaletail",
		"/new/stacks",
		"/new/log",
		"/opt/tailarr/authkeys.conf",
	}, "\n") + "\n")
	ui := &prompt.Std{In: in, Out: io.Discard, Err: io.Discard}
	text, saved := editConfigInteractive(&cfg, ui)
	if saved {
		t.Fatal("expected save failure")
	}
	if cfg != original {
		t.Fatalf("caller cfg mutated on save error: %+v text=%s", cfg, text)
	}
}

func TestEditConfigInteractiveSuccessRebindsLogPath(t *testing.T) {
	dir := t.TempDir()
	newLog := filepath.Join(dir, "new.log")
	cfg := config.Config{
		ConfigPath:   filepath.Join(dir, "tailarr.conf"),
		RepoURL:      "https://github.com/tailscale-dev/ScaleTail.git",
		RepoPath:     "/opt/tailarr/scaletail",
		DeployPath:   "/opt/docker/stacks",
		LogPath:      filepath.Join(dir, "old.log"),
		AuthkeysPath: "/opt/tailarr/authkeys.conf",
		LogMaxBytes:  config.DefaultLogMaxBytes,
	}
	in := strings.NewReader(strings.Join([]string{
		"",
		"/opt/tailarr/scaletail",
		"/opt/docker/stacks",
		newLog,
		"/opt/tailarr/authkeys.conf",
	}, "\n") + "\n")
	ui := &prompt.Std{In: in, Out: io.Discard, Err: io.Discard}
	text, saved := editConfigInteractive(&cfg, ui)
	if !saved {
		t.Fatal(text)
	}
	if cfg.LogPath != newLog {
		t.Fatalf("LogPath = %s", cfg.LogPath)
	}
	log := logging.New(cfg.LogPath, cfg.LogMaxBytes)
	if err := log.Validate(); err != nil {
		t.Fatal(err)
	}
	log.Event("deployed service web")
	data, err := os.ReadFile(newLog)
	if err != nil || !strings.Contains(string(data), "deployed service web") {
		t.Fatalf("event missing from new log: %v %q", err, data)
	}
}

func TestFirstRunSetupDoesNotKeepFailedEdit(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.ConfigPath = filepath.Join(dir, "missing.conf")
	cfg.AssumeYes = true
	original := cfg.RepoURL
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = old
		_ = r.Close()
	})
	go func() {
		_, _ = fmt.Fprintln(w, "https://github.com/example/other.git")
		_ = w.Close()
	}()
	err = FirstRunSetup(&cfg)
	if err == nil {
		t.Fatal("expected failed edit")
	}
	if cfg.RepoURL != original {
		t.Fatalf("first-run cfg mutated: %s", cfg.RepoURL)
	}
}

func TestRefreshSecondEnterYieldsNoCommand(t *testing.T) {
	items := servicesMenuItems()
	m := model{screen: screenServices, items: items, cursor: itemCursor(items, "refresh"), picked: map[int]bool{}}
	next, cmd := m.Update(enterKey())
	if cmd == nil {
		t.Fatal("expected refresh command")
	}
	got := next.(model)
	if !got.busy {
		t.Fatal("refresh should set busy")
	}
	_, cmd2 := got.Update(enterKey())
	if cmd2 != nil {
		t.Fatal("second enter must not start another refresh")
	}
}

func TestUpgradeSecondEnterYieldsNoCommand(t *testing.T) {
	items := maintenanceMenuItems()
	m := model{screen: screenMaintenance, items: items, cursor: itemCursor(items, "upgrade"), picked: map[int]bool{}}
	next, cmd := m.Update(enterKey())
	if cmd == nil {
		t.Fatal("expected upgrade command")
	}
	got := next.(model)
	if !got.busy {
		t.Fatal("upgrade should set busy")
	}
	_, cmd2 := got.Update(enterKey())
	if cmd2 != nil {
		t.Fatal("second enter must not start another upgrade")
	}
}

func TestFinishMultiSetsBusy(t *testing.T) {
	m := model{
		screen: screenMultiSelect,
		multi:  multiApply,
		opts:   []string{"web"},
		picked: map[int]bool{0: true},
		items: []menuItem{
			{id: "run", label: "Run on selection", desc: "run"},
			{id: "cancel", label: "Cancel", desc: "cancel"},
		},
	}
	next, cmd := m.Update(digitKey('2'))
	if cmd == nil {
		t.Fatal("expected batch command")
	}
	got := next.(model)
	if !got.busy {
		t.Fatal("finishMulti should set busy")
	}
	_, cmd2 := got.Update(digitKey('2'))
	if cmd2 != nil {
		t.Fatal("busy batch must ignore a second run")
	}
}

func TestBusyCtrlCCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := model{busy: true, opCancel: cancel, screen: screenMain, items: mainMenuItems(), picked: map[int]bool{}}
	next, cmd := m.Update(ctrlCKey())
	got := next.(model)
	if cmd != nil {
		t.Fatal("cancel should not start another command")
	}
	if got.quitting {
		t.Fatal("busy ctrl+c must not quit before in-flight work finishes")
	}
	if ctx.Err() == nil {
		t.Fatal("expected cancel")
	}
	if got.status != "Canceling..." {
		t.Fatalf("status = %q", got.status)
	}
}

func TestDoctorAndStatusReturnCommands(t *testing.T) {
	items := maintenanceMenuItems()
	m := model{screen: screenMaintenance, items: items, cursor: itemCursor(items, "doctor"), picked: map[int]bool{}}
	start := time.Now()
	next, cmd := m.Update(enterKey())
	if time.Since(start) > time.Second {
		t.Fatal("doctor Update blocked")
	}
	if cmd == nil || !next.(model).busy {
		t.Fatal("doctor should return a command and set busy")
	}
	statusItems := statusMenuItems()
	sm := model{screen: screenStatus, items: statusItems, cursor: itemCursor(statusItems, "overview"), picked: map[int]bool{}}
	start = time.Now()
	snext, scmd := sm.Update(enterKey())
	if time.Since(start) > time.Second {
		t.Fatal("status Update blocked")
	}
	if scmd == nil || !snext.(model).busy {
		t.Fatal("status overview should return a command and set busy")
	}
}

func TestSignalShutdownWaitsForApplyRestore(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	tpl := filepath.Join(repo, "services", "web")
	if err := os.MkdirAll(tpl, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tpl, "compose.yaml"), []byte("services:\n  app:\n    image: new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tpl, ".env"), []byte("TS_AUTHKEY=tskey-auth-from-template\nHOSTNAME=t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(deployRoot, "web")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	orig := "services:\n  app:\n    image: original\n"
	if err := os.WriteFile(filepath.Join(dest, "compose.yaml"), []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, ".env"), []byte("TS_AUTHKEY=tskey-auth-from-template\nHOSTNAME=old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, ".tailarr.compose.yaml"), []byte("services:\n  app:\n    labels:\n      tailarr.managed: \"true\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	started := filepath.Join(t.TempDir(), "started")
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \"$*\" in\n*version*) exit 0 ;;\n*--services*) echo app; exit 0 ;;\n*pull*|*up*) echo started > " + fmt.Sprintf("%q", started) + "; exec sleep 30 ;;\n*) exit 0 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	rootCtx, rootCancel := context.WithCancel(context.Background())
	interrupt.Set(rootCtx)
	t.Cleanup(func() {
		rootCancel()
		interrupt.Clear()
	})
	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	flight := &workFlight{}
	applied := make(chan struct{})
	errCh := make(chan error, 1)
	flight.track()
	go func() {
		defer flight.untrack()
		mgr := &deploy.Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
		err := mgr.Apply("web", deploy.DeployOpts{})
		close(applied)
		errCh <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(started); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(started); err != nil {
		t.Fatal("compose did not start")
	}
	quit := make(chan struct{})
	sawSignal := make(chan struct{})
	go func() {
		<-sigCtx.Done()
		close(sawSignal)
		drainThenQuit(rootCancel, flight, func() {
			select {
			case <-applied:
			default:
				t.Errorf("quit before apply returned")
			}
			close(quit)
		})
	}()
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sawSignal:
	case <-time.After(2 * time.Second):
		t.Fatal("SIGINT was not delivered to the shutdown handler")
	}
	select {
	case <-quit:
	case <-time.After(8 * time.Second):
		t.Fatal("shutdown did not finish")
	}
	data, err := os.ReadFile(filepath.Join(dest, "compose.yaml"))
	if err != nil || string(data) != orig {
		t.Fatalf("restore incomplete when shutdown finished: %v %q", err, data)
	}
	select {
	case err := <-errCh:
		if err == nil || !errors.Is(err, deploy.ErrInterrupted) {
			t.Fatalf("expected ErrInterrupted, got %v", err)
		}
	default:
		t.Fatal("apply had not returned")
	}
}
