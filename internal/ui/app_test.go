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
	"github.com/jackspiering/tailarr/internal/doctor"
	"github.com/jackspiering/tailarr/internal/interrupt"
	"github.com/jackspiering/tailarr/internal/logging"
	"github.com/jackspiering/tailarr/internal/prompt"
)

func runeKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

func enterKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyEnter} }

func escKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyEscape} }

func ctrlCKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl} }

func testModel(t *testing.T) model {
	t.Helper()
	cfg := config.Default()
	dir := t.TempDir()
	cfg.DeployPath = filepath.Join(dir, "stacks")
	cfg.RepoPath = filepath.Join(dir, "scaletail")
	cfg.AuthkeysPath = filepath.Join(dir, "authkeys.conf")
	m := newModel(cfg, nil, context.Background(), &workFlight{}, &sender{})
	m.width, m.height = 120, 36
	return m
}

func withServices(m model, svcs ...deploy.ServiceStatus) model {
	m.status = &deploy.Overview{Services: svcs}
	return m
}

func withCatalog(m model, names ...string) model {
	m.catalogLoaded = true
	m.catalog = nil
	for _, n := range names {
		m.catalog = append(m.catalog, catalogItem{Name: n, Image: n + "/" + n})
	}
	return m
}

func press(t *testing.T, m model, keys ...tea.KeyPressMsg) (model, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, k := range keys {
		var next tea.Model
		next, cmd = m.Update(k)
		m = next.(model)
	}
	return m, cmd
}

func TestTabsSwitchWithKeys(t *testing.T) {
	m := testModel(t)
	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.tab != tabCatalog {
		t.Fatalf("tab: got %d, want catalog", m.tab)
	}
	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if m.tab != tabServices {
		t.Fatalf("shift+tab: got %d, want services", m.tab)
	}
	m, cmd := press(t, m, runeKey('4'))
	if m.tab != tabSystem || cmd == nil || !m.doctorBusy {
		t.Fatalf("4 must open System and start doctor checks: tab=%d busy=%v", m.tab, m.doctorBusy)
	}
	m, _ = press(t, m, runeKey('3'))
	if m.tab != tabKeys {
		t.Fatalf("3: got %d, want keys", m.tab)
	}
}

func TestRefreshSecondKeyYieldsNoCommand(t *testing.T) {
	m := withCatalog(testModel(t), "web")
	m.tab = tabCatalog
	m, cmd := press(t, m, runeKey('r'))
	if cmd == nil || !m.busy {
		t.Fatal("refresh should start an operation")
	}
	if _, cmd2 := press(t, m, runeKey('r')); cmd2 != nil {
		t.Fatal("second r must not start another refresh")
	}
}

func TestUpgradeSecondKeyYieldsNoCommand(t *testing.T) {
	m := testModel(t)
	m.tab = tabSystem
	m, cmd := press(t, m, runeKey('U'))
	if cmd == nil || !m.busy {
		t.Fatal("upgrade should start an operation")
	}
	if _, cmd2 := press(t, m, runeKey('U')); cmd2 != nil {
		t.Fatal("second U must not start another upgrade")
	}
}

func TestBusyCtrlCCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := testModel(t)
	m.busy, m.opCancel = true, cancel
	m, cmd := press(t, m, ctrlCKey())
	if cmd != nil {
		t.Fatal("cancel should not start another command")
	}
	if m.quitting {
		t.Fatal("busy ctrl+c must not quit before in-flight work finishes")
	}
	if ctx.Err() == nil {
		t.Fatal("expected cancel")
	}
	if m.note != "Canceling..." {
		t.Fatalf("note = %q", m.note)
	}
}

func TestIdleCtrlCQuitsAndBusyQDoesNot(t *testing.T) {
	m := testModel(t)
	if got, _ := press(t, m, ctrlCKey()); !got.quitting {
		t.Fatal("idle ctrl+c should quit")
	}
	m.busy = true
	got, _ := press(t, m, runeKey('q'))
	if got.quitting || !strings.Contains(got.note, "ctrl+c") {
		t.Fatalf("q while busy must not quit: quitting=%v note=%q", got.quitting, got.note)
	}
}

func TestOpDoneInstallsConfigAndLogger(t *testing.T) {
	dir := t.TempDir()
	m := testModel(t)
	m.busy = true
	m.cfg.DeployPath = "/old/stacks"
	next, _ := m.Update(opDoneMsg{res: opResult{lines: []string{"Error saving: permission denied"}}})
	if got := next.(model); got.cfg.DeployPath != "/old/stacks" || got.busy {
		t.Fatalf("error result installed cfg or stayed busy: %s busy=%v", got.cfg.DeployPath, got.busy)
	}
	newLog := filepath.Join(dir, "new.log")
	log := logging.New(newLog, 1024)
	next, _ = m.Update(opDoneMsg{res: opResult{
		lines: []string{"Saved config: x"},
		cfg:   &config.Config{LogPath: newLog, DeployPath: filepath.Join(dir, "new")},
		log:   log,
	}})
	got := next.(model)
	if got.cfg.LogPath != newLog || got.log != log {
		t.Fatalf("cfg/log not installed: %s", got.cfg.LogPath)
	}
	got.log.Event("deployed service web")
	data, err := os.ReadFile(newLog)
	if err != nil || !strings.Contains(string(data), "deployed service web") {
		t.Fatalf("event not in new log: %v %q", err, data)
	}
	if !strings.Contains(strings.Join(got.out.lines, "\n"), "Saved config: x") {
		t.Fatalf("result not shown: %v", got.out.lines)
	}
}

func TestLifecycleSkipsUnmanagedServices(t *testing.T) {
	m := withServices(testModel(t), deploy.ServiceStatus{Name: "legacy", Health: deploy.HealthStopped})
	m, cmd := press(t, m, runeKey('r'))
	if cmd != nil || m.busy {
		t.Fatal("restart must not run on an unmanaged service")
	}
	if !strings.Contains(m.note, "not managed") {
		t.Fatalf("note = %q", m.note)
	}
	m = withServices(m, deploy.ServiceStatus{Name: "web", Managed: true})
	if m, cmd = press(t, m, runeKey('s')); cmd == nil || !m.busy {
		t.Fatal("stop should start on a managed service")
	}
}

func TestCatalogEnterSkipsDeployedServices(t *testing.T) {
	m := withCatalog(withServices(testModel(t), deploy.ServiceStatus{Name: "web", Managed: true}), "web", "api")
	m.tab = tabCatalog
	m, cmd := press(t, m, enterKey())
	if cmd != nil || !strings.Contains(m.note, "already deployed") {
		t.Fatalf("deploying a deployed service must be refused: note=%q", m.note)
	}
	m, cmd = press(t, m, runeKey('j'), enterKey())
	if cmd == nil || !m.busy || !strings.Contains(m.opTitle, "api") {
		t.Fatalf("api should deploy: busy=%v title=%q", m.busy, m.opTitle)
	}
}

func TestFilterKeepsPicks(t *testing.T) {
	m := withCatalog(testModel(t), "adguard", "immich", "jellyfin", "plex")
	m.tab = tabCatalog
	m, _ = press(t, m, runeKey('G'), tea.KeyPressMsg{Code: tea.KeySpace})
	if !m.lists[tabCatalog].picked["plex"] {
		t.Fatal("space should pick plex")
	}
	m, _ = press(t, m, runeKey('/'), runeKey('J'), runeKey('E'), runeKey('L'), enterKey())
	if got := strings.Join(m.rows(), ","); got != "jellyfin,plex" {
		t.Fatalf("filtered rows = %s", got)
	}
	if m.filtering {
		t.Fatal("enter should end filter editing")
	}
	m, _ = press(t, m, escKey())
	if len(m.rows()) != 4 || !m.lists[tabCatalog].picked["plex"] {
		t.Fatalf("esc must clear the filter and keep picks: %v", m.rows())
	}
	m, _ = press(t, m, runeKey('n'))
	if len(m.lists[tabCatalog].picked) != 0 {
		t.Fatal("n must clear the picks")
	}
}

func TestTargetsUsePicksThenCursor(t *testing.T) {
	m := withCatalog(testModel(t), "a", "b", "c")
	m.tab = tabCatalog
	m.lists[tabCatalog].cursor = 1
	if got := strings.Join(m.targets(), ","); got != "b" {
		t.Fatalf("no picks: targets = %s", got)
	}
	m.lists[tabCatalog].picked["c"] = true
	m.lists[tabCatalog].picked["a"] = true
	if got := strings.Join(m.targets(), ","); got != "a,c" {
		t.Fatalf("picks: targets = %s", got)
	}
}

func TestActionMenuRunsHotkey(t *testing.T) {
	m := withServices(testModel(t), deploy.ServiceStatus{Name: "web", Managed: true})
	m, _ = press(t, m, enterKey())
	if m.menu == nil || len(m.menu.items) != 4 {
		t.Fatal("enter should open the action menu")
	}
	m, _ = press(t, m, escKey())
	if m.menu != nil {
		t.Fatal("esc should close the menu")
	}
	m, _ = press(t, m, enterKey())
	m, cmd := press(t, m, runeKey('s'))
	if m.menu != nil || cmd == nil || !strings.HasPrefix(m.opTitle, "stop") {
		t.Fatalf("s in the menu should start stop: title=%q", m.opTitle)
	}
}

func startAsk(t *testing.T, fn func(ui *tuiUI) (any, error)) (model, chan tea.Msg, chan any, context.CancelFunc) {
	t.Helper()
	msgs := make(chan tea.Msg, 16)
	snd := &sender{}
	snd.bind(func(msg tea.Msg) { msgs <- msg })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ui := &tuiUI{snd: snd, ctx: ctx}
	done := make(chan any, 1)
	go func() {
		v, err := fn(ui)
		if err != nil {
			done <- err
			return
		}
		done <- v
	}()
	m := testModel(t)
	m.busy, m.opCancel = true, cancel
	select {
	case msg := <-msgs:
		next, _ := m.Update(msg)
		m = next.(model)
	case <-time.After(2 * time.Second):
		t.Fatal("prompt did not open")
	}
	if m.ask == nil {
		t.Fatal("askMsg did not open a prompt")
	}
	return m, msgs, done, cancel
}

func waitDone(t *testing.T, done chan any) any {
	t.Helper()
	select {
	case v := <-done:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("prompt did not return")
	}
	return nil
}

func TestTUIConfirmRoundTrip(t *testing.T) {
	m, _, done, _ := startAsk(t, func(ui *tuiUI) (any, error) { return ui.Confirm("Deploy web?", false) })
	if !strings.Contains(m.render(), "Deploy web?") {
		t.Fatal("prompt not rendered")
	}
	m, _ = press(t, m, runeKey('y'))
	if v := waitDone(t, done); v != true || m.ask != nil {
		t.Fatalf("confirm = %v", v)
	}
	m, _, done, _ = startAsk(t, func(ui *tuiUI) (any, error) { return ui.Confirm("Deploy web?", true) })
	press(t, m, enterKey())
	if v := waitDone(t, done); v != true {
		t.Fatalf("enter must pick the default: %v", v)
	}
}

func TestTUILineReplacesOrEditsDefault(t *testing.T) {
	m, _, done, _ := startAsk(t, func(ui *tuiUI) (any, error) { return ui.Line("Stored key name", "default") })
	press(t, m, runeKey('h'), runeKey('o'), runeKey('m'), runeKey('e'), enterKey())
	if v := waitDone(t, done); v != "home" {
		t.Fatalf("typing must replace the default: %v", v)
	}
	m, _, done, _ = startAsk(t, func(ui *tuiUI) (any, error) { return ui.Line("Path", "/opt/stacks") })
	press(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace}, runeKey('x'), enterKey())
	if v := waitDone(t, done); v != "/opt/stackx" {
		t.Fatalf("backspace must edit the default: %v", v)
	}
	m, _, done, _ = startAsk(t, func(ui *tuiUI) (any, error) { return ui.Line("Path", "/opt/stacks") })
	press(t, m, enterKey())
	if v := waitDone(t, done); v != "/opt/stacks" {
		t.Fatalf("enter must keep the default: %v", v)
	}
}

func TestTUIPromptEscAndCtrlC(t *testing.T) {
	m, _, done, _ := startAsk(t, func(ui *tuiUI) (any, error) { return ui.Line("Name", "") })
	press(t, m, escKey())
	if err, _ := waitDone(t, done).(error); !errors.Is(err, prompt.ErrCanceled) {
		t.Fatalf("esc must cancel the prompt: %v", err)
	}
	m, _, done, _ = startAsk(t, func(ui *tuiUI) (any, error) { return ui.Secret("TS_AUTHKEY") })
	m, _ = press(t, m, ctrlCKey())
	if err, _ := waitDone(t, done).(error); !errors.Is(err, context.Canceled) {
		t.Fatalf("ctrl+c must cancel the operation: %v", err)
	}
	if m.ask != nil || m.note != "Canceling..." {
		t.Fatalf("ctrl+c must close the prompt: note=%q", m.note)
	}
}

func TestTUIPromptUnblocksOnCancel(t *testing.T) {
	_, _, done, cancel := startAsk(t, func(ui *tuiUI) (any, error) { return ui.Line("Name", "") })
	cancel()
	if err, _ := waitDone(t, done).(error); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context must unblock the prompt: %v", err)
	}
}

func TestSecretIsNeverRendered(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m, _, done, _ := startAsk(t, func(ui *tuiUI) (any, error) { return ui.Secret("TS_AUTHKEY") })
	next, _ := m.Update(tea.PasteMsg{Content: "tskey-auth-SECRET123\n"})
	m = next.(model)
	view := m.render()
	if strings.Contains(view, "SECRET123") || strings.Contains(view, "tskey-auth") {
		t.Fatalf("secret rendered:\n%s", view)
	}
	if !strings.Contains(view, "••••") {
		t.Fatalf("secret input shows no progress:\n%s", view)
	}
	press(t, m, enterKey())
	if v := waitDone(t, done); v != "tskey-auth-SECRET123" {
		t.Fatalf("pasted secret = %v", v)
	}
	if m.ask != nil && len(m.ask.buf) != 0 {
		t.Fatal("answered prompt kept the secret in its buffer")
	}
}

func TestTUIPrintfRedacts(t *testing.T) {
	msgs := make(chan tea.Msg, 4)
	snd := &sender{}
	snd.bind(func(msg tea.Msg) { msgs <- msg })
	ui := &tuiUI{snd: snd, ctx: context.Background()}
	ui.Printf("TS_AUTHKEY=tskey-auth-LEAK\nsecond line\n")
	first := (<-msgs).(outMsg).line
	second := (<-msgs).(outMsg).line
	if strings.Contains(first, "LEAK") || second != "second line" {
		t.Fatalf("printf lines: %q %q", first, second)
	}
}

func TestTUIConfirmAssumeYes(t *testing.T) {
	msgs := make(chan tea.Msg, 4)
	snd := &sender{}
	snd.bind(func(msg tea.Msg) { msgs <- msg })
	ui := &tuiUI{snd: snd, ctx: context.Background(), assumeYes: true}
	if ok, err := ui.Confirm("Store this key?", true); !ok || err != nil {
		t.Fatalf("assume-yes confirm = %v %v", ok, err)
	}
	if line := (<-msgs).(outMsg).line; !strings.Contains(line, "auto-yes") {
		t.Fatalf("auto answer not shown: %q", line)
	}
}

func TestLineSinkSplitsAndCleans(t *testing.T) {
	msgs := make(chan tea.Msg, 8)
	snd := &sender{}
	snd.bind(func(msg tea.Msg) { msgs <- msg })
	sink := &lineSink{snd: snd}
	_, _ = sink.Write([]byte("\x1b[32m Container app-web Started\x1b[0m\r\n pulling"))
	_, _ = sink.Write([]byte(" 50%\rdone\n"))
	var got []string
	for len(msgs) > 0 {
		got = append(got, (<-msgs).(outMsg).line)
	}
	want := []string{" Container app-web Started", " pulling 50%", "done"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("sink lines = %q, want %q", got, want)
	}
}

func TestPasteUsesFirstLineInFilter(t *testing.T) {
	m := withCatalog(testModel(t), "jellyfin", "plex")
	m.tab = tabCatalog
	m.filtering = true
	next, _ := m.Update(tea.PasteMsg{Content: "jelly\nplex"})
	if got := next.(model).lists[tabCatalog].filter; got != "jelly" {
		t.Fatalf("filter = %q", got)
	}
}

func TestUpdateStartsNoBlockingWork(t *testing.T) {
	m := testModel(t)
	start := time.Now()
	next, cmd := m.Update(runeKey('4'))
	if time.Since(start) > time.Second {
		t.Fatal("tab switch blocked")
	}
	if cmd == nil || !next.(model).doctorBusy {
		t.Fatal("doctor checks should run as a command")
	}
	next, _ = next.(model).Update(doctorMsg{checks: []doctor.Check{{Level: doctor.OK, Name: "git", Message: "ok"}}})
	if got := next.(model); got.doctorBusy || len(got.checks) != 1 {
		t.Fatal("doctor result not installed")
	}
}

func TestPollRefreshesOnlyWhenIdle(t *testing.T) {
	m := testModel(t)
	next, cmd := m.Update(pollMsg(time.Now()))
	if cmd == nil || !next.(model).statusBusy {
		t.Fatal("idle poll should refresh status")
	}
	m.busy = true
	next, _ = m.Update(pollMsg(time.Now()))
	if next.(model).statusBusy {
		t.Fatal("poll must not refresh while an operation runs")
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
	case <-time.After(15 * time.Second):
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
