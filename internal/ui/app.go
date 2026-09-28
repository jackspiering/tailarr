// Package ui implements the Bubble Tea interactive TUI.
package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/deploy"
	"github.com/jackspiering/tailarr/internal/doctor"
	"github.com/jackspiering/tailarr/internal/interrupt"
	"github.com/jackspiering/tailarr/internal/logging"
	"github.com/jackspiering/tailarr/internal/prompt"
	"github.com/jackspiering/tailarr/internal/security/redact"
)

// IsInteractive reports whether stdin/stdout support a TUI.
func IsInteractive() bool {
	if term := os.Getenv("TERM"); term == "dumb" {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil || (fi.Mode()&os.ModeCharDevice) == 0 {
		return false
	}
	fiIn, err := os.Stdin.Stat()
	if err != nil || (fiIn.Mode()&os.ModeCharDevice) == 0 {
		return false
	}
	return true
}

type tab int

const (
	tabServices tab = iota
	tabCatalog
	tabKeys
	tabSystem
	tabCount
)

var tabNames = [tabCount]string{"Services", "Catalog", "Keys", "System"}

// pollInterval is how often the Services tab refreshes container state.
const pollInterval = 4 * time.Second

// listState is the cursor, selection, and filter of one list tab. Picks are
// keyed by name, so they survive a refresh and stay visible under a filter.
type listState struct {
	cursor int
	picked map[string]bool
	filter string
}

// menuItem is one row of the action menu.
type menuItem struct {
	key   string
	label string
	desc  string
	run   func(model) (model, tea.Cmd)
}

// actionMenu is the popup that Enter opens on the Services and Keys tabs.
type actionMenu struct {
	title  string
	items  []menuItem
	cursor int
}

type model struct {
	cfg      config.Config
	log      *logging.Logger
	host     string
	width    int
	height   int
	tab      tab
	quitting bool

	lists [tabCount]listState

	status     *deploy.OverviewStats
	statusErr  string
	statusBusy bool

	catalog       []catalogItem
	catalogErr    string
	catalogLoaded bool

	keyNames   []string
	keysErr    string
	checks     []doctor.Check
	doctorBusy bool

	filtering bool
	menu      *actionMenu

	busy     bool
	opTitle  string
	opStart  time.Time
	opCancel context.CancelFunc
	note     string
	out      outLog
	// scrollBack is how many lines the output panel is scrolled up from the
	// newest line. 0 follows new output.
	scrollBack int
	spin       int
	ask        *askState

	snd     *sender
	rootCtx context.Context
	flight  *workFlight
}

type workFlight struct {
	wg sync.WaitGroup
}

func (w *workFlight) track() {
	if w == nil {
		return
	}
	w.wg.Add(1)
}

func (w *workFlight) untrack() {
	if w == nil {
		return
	}
	w.wg.Done()
}

func (w *workFlight) wait() {
	if w == nil {
		return
	}
	w.wg.Wait()
}

// drainThenQuit cancels in-flight work and invokes quit only after tracked
// operations return. Apply's restore defer runs in that operation.
func drainThenQuit(cancel context.CancelFunc, flight *workFlight, quit func()) {
	if cancel != nil {
		cancel()
	}
	if flight != nil {
		flight.wait()
	}
	if quit != nil {
		quit()
	}
}

// exitText is printed after the TUI exits, for example after an upgrade.
var exitText string

func newModel(cfg config.Config, log *logging.Logger, ctx context.Context, flight *workFlight, snd *sender) model {
	m := model{cfg: cfg, log: log, rootCtx: ctx, flight: flight, snd: snd}
	for i := range m.lists {
		m.lists[i].picked = map[string]bool{}
	}
	if host, err := os.Hostname(); err == nil {
		m.host = host
	}
	return m
}

// Run starts the interactive TUI. Prompts, compose output, and git output
// all stay inside the TUI; the terminal is never handed back mid-operation.
func Run(cfg config.Config, log *logging.Logger) error {
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()
	flight := &workFlight{}
	snd := &sender{}
	m := newModel(cfg, log, rootCtx, flight, snd)
	interrupt.Set(rootCtx)
	defer interrupt.Clear()
	prompt.BindCancel(rootCtx)
	defer prompt.BindCancel(context.Background())
	deploy.SetOutput(&lineSink{snd: snd})
	defer deploy.SetOutput(nil)

	// Tailarr handles SIGINT and SIGTERM itself: cancel shared work and quit
	// only after operations return, so Apply can restore before exit.
	p := tea.NewProgram(m, tea.WithoutSignalHandler())
	snd.bind(p.Send)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		drainThenQuit(rootCancel, flight, p.Quit)
	}()
	_, err := p.Run()
	// Quit can come while an operation runs; let it finish its cleanup.
	drainThenQuit(rootCancel, flight, nil)
	if errors.Is(err, tea.ErrInterrupted) {
		err = nil
	}
	if exitText != "" {
		fmt.Println(exitText)
	}
	return err
}

// FirstRunSetup creates a default config file on first run. It is a no-op when
// the config file already exists. Prompts run before the TUI takes over the
// terminal.
func FirstRunSetup(cfg *config.Config) error {
	if _, err := os.Stat(cfg.ConfigPath); err == nil {
		return nil
	}
	uiPrompt := prompt.NewStd(cfg.AssumeYes)
	uiPrompt.Printf("No config found at %s.\n", cfg.ConfigPath)
	ok, err := uiPrompt.Confirm("Create one now using the current defaults?", true)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if edit, err := uiPrompt.Confirm("Edit defaults before saving?", true); err != nil {
		return err
	} else if edit {
		msg, saved := editConfigInteractive(cfg, uiPrompt)
		uiPrompt.Printf("%s\n", msg)
		if !saved {
			return fmt.Errorf("%s", msg)
		}
		return nil
	}
	// Save the defaults, not TAILARR_* overrides from this session.
	base, err := config.LoadFile(cfg.ConfigPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := config.Save(base); err != nil {
		uiPrompt.Printf("Could not save config: %v\n", err)
		return fmt.Errorf("save config: %w", err)
	}
	uiPrompt.Printf("Saved config: %s\n", cfg.ConfigPath)
	return nil
}

type (
	spinMsg time.Time
	pollMsg time.Time
)

// opResult is what an operation reports when it returns.
type opResult struct {
	lines []string
	cfg   *config.Config
	log   *logging.Logger
	quit  bool
}

type opDoneMsg struct{ res opResult }

func spinTick() tea.Cmd {
	return tea.Tick(90*time.Millisecond, func(t time.Time) tea.Msg { return spinMsg(t) })
}

func pollTick() tea.Cmd {
	return tea.Tick(pollInterval, func(t time.Time) tea.Msg { return pollMsg(t) })
}

func (m model) Init() tea.Cmd {
	return tea.Batch(loadStatus(m.cfg.DeployPath), loadCatalog(m.cfg.RepoPath), loadKeys(m.cfg.AuthkeysPath), pollTick())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case statusMsg:
		m.statusBusy = false
		m.statusErr = msg.err
		if msg.err == "" {
			st := msg.st
			m.status = &st
		}
		m.clampCursor()
		return m, nil
	case catalogMsg:
		m.catalogLoaded = true
		m.catalog, m.catalogErr = msg.items, msg.err
		m.clampCursor()
		return m, nil
	case keysMsg:
		m.keyNames, m.keysErr = msg.names, msg.err
		m.clampCursor()
		return m, nil
	case doctorMsg:
		m.doctorBusy = false
		m.checks = msg.checks
		return m, nil
	case pollMsg:
		cmds := []tea.Cmd{pollTick()}
		if m.tab == tabServices && !m.busy && !m.statusBusy {
			m.statusBusy = true
			cmds = append(cmds, loadStatus(m.cfg.DeployPath))
		}
		return m, tea.Batch(cmds...)
	case spinMsg:
		if !m.busy {
			return m, nil
		}
		m.spin++
		return m, spinTick()
	case outMsg:
		m.out.add(msg.line)
		if m.scrollBack > 0 {
			m.scrollBack++
		}
		return m, nil
	case askMsg:
		m.ask = msg.ask
		m.menu = nil
		m.filtering = false
		return m, nil
	case opDoneMsg:
		return m.finishOp(msg.res)
	case tea.PasteMsg:
		return m.paste(msg.Content), nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// paste adds pasted text to the open prompt or the filter. Only the first
// line counts: a pasted key often ends with a newline.
func (m model) paste(text string) model {
	text, _, _ = strings.Cut(strings.ReplaceAll(text, "\r", "\n"), "\n")
	text = cleanLine(text)
	switch {
	case m.ask != nil && m.ask.kind != askConfirm:
		if m.ask.fresh {
			m.ask.buf, m.ask.fresh = nil, false
		}
		m.ask.buf = append(m.ask.buf, []rune(text)...)
	case m.filtering:
		m.list().filter += text
		m.list().cursor = 0
	}
	return m
}

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.ask != nil {
		return m.handleAskKey(msg)
	}
	if key == "ctrl+c" {
		if m.busy {
			if m.opCancel != nil {
				m.opCancel()
			}
			m.note = "Canceling..."
			return m, nil
		}
		m.quitting = true
		return m, tea.Quit
	}
	if m.menu != nil {
		return m.handleMenuKey(key)
	}
	if m.filtering {
		return m.handleFilterKey(msg), nil
	}
	m.note = ""
	switch key {
	case "q":
		if m.busy {
			m.note = "Still working. Press ctrl+c to cancel."
			return m, nil
		}
		m.quitting = true
		return m, tea.Quit
	case "esc":
		switch {
		case m.list() != nil && m.list().filter != "":
			m.list().filter = ""
			m.clampCursor()
		case !m.busy && !m.out.empty():
			m.out.reset("")
			m.scrollBack = 0
		}
		return m, nil
	case "tab", "right", "l":
		return m.switchTab((m.tab + 1) % tabCount)
	case "shift+tab", "left", "h":
		return m.switchTab((m.tab + tabCount - 1) % tabCount)
	case "1", "2", "3", "4":
		return m.switchTab(tab(key[0] - '1'))
	case "up", "k":
		m.moveCursor(-1)
		return m, nil
	case "down", "j":
		m.moveCursor(1)
		return m, nil
	case "g":
		m.moveCursor(-1 << 20)
		return m, nil
	case "G":
		m.moveCursor(1 << 20)
		return m, nil
	case "pgup":
		m.scrollBack = min(m.scrollBack+m.pageSize(), m.maxScroll())
		return m, nil
	case "pgdown":
		m.scrollBack = max(m.scrollBack-m.pageSize(), 0)
		return m, nil
	case "home":
		m.scrollBack = m.maxScroll()
		return m, nil
	case "end":
		m.scrollBack = 0
		return m, nil
	}
	switch m.tab {
	case tabServices:
		return m.servicesKey(key)
	case tabCatalog:
		return m.catalogKey(key)
	case tabKeys:
		return m.keysKey(key)
	case tabSystem:
		return m.systemKey(key)
	}
	return m, nil
}

func (m model) switchTab(t tab) (tea.Model, tea.Cmd) {
	if t < 0 || t >= tabCount {
		return m, nil
	}
	m.tab = t
	m.filtering = false
	m.note = ""
	var cmds []tea.Cmd
	switch t {
	case tabSystem:
		if m.checks == nil && !m.doctorBusy {
			m.doctorBusy = true
			cmds = append(cmds, loadDoctor(m.cfg))
		}
	case tabServices:
		if !m.statusBusy && !m.busy {
			m.statusBusy = true
			cmds = append(cmds, loadStatus(m.cfg.DeployPath))
		}
	}
	return m, tea.Batch(cmds...)
}

func (m model) handleAskKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	a := m.ask
	switch key := msg.String(); key {
	case "ctrl+c":
		if m.opCancel != nil {
			m.opCancel()
		}
		a.answer(askReply{err: context.Canceled})
		m.ask = nil
		m.note = "Canceling..."
		return m, nil
	case "esc":
		a.answer(askReply{err: prompt.ErrCanceled})
		m.ask = nil
		return m, nil
	case "pgup", "pgdown", "home", "end":
		m.ask = nil
		next, cmd := m.handleKey(msg)
		nm := next.(model)
		nm.ask = a
		return nm, cmd
	}
	if a.kind == askConfirm {
		switch strings.ToLower(msg.Text) {
		case "y":
			a.answer(askReply{yes: true})
		case "n":
			a.answer(askReply{yes: false})
		default:
			if msg.String() != "enter" {
				return m, nil
			}
			a.answer(askReply{yes: a.defaultYes})
		}
		m.ask = nil
		return m, nil
	}
	switch msg.String() {
	case "enter":
		a.answer(askReply{value: string(a.buf)})
		m.ask = nil
	case "backspace":
		a.fresh = false
		if len(a.buf) > 0 {
			a.buf = a.buf[:len(a.buf)-1]
		}
	case "right", "end":
		a.fresh = false
	case "ctrl+u":
		a.fresh = false
		a.buf = a.buf[:0]
	case "ctrl+w":
		s := strings.TrimRight(string(a.buf), " ")
		if i := strings.LastIndex(s, " "); i >= 0 {
			a.buf = []rune(s[:i+1])
		} else {
			a.buf = a.buf[:0]
		}
	default:
		if t := cleanLine(msg.Text); t != "" {
			if a.fresh {
				a.buf, a.fresh = nil, false
			}
			a.buf = append(a.buf, []rune(t)...)
		}
	}
	return m, nil
}

func (m model) handleMenuKey(key string) (tea.Model, tea.Cmd) {
	mn := m.menu
	switch key {
	case "esc", "q":
		m.menu = nil
		return m, nil
	case "up", "k":
		if mn.cursor > 0 {
			mn.cursor--
		}
		return m, nil
	case "down", "j":
		if mn.cursor < len(mn.items)-1 {
			mn.cursor++
		}
		return m, nil
	case "enter":
		m.menu = nil
		return mn.items[mn.cursor].run(m)
	}
	for _, it := range mn.items {
		if it.key == key {
			m.menu = nil
			return it.run(m)
		}
	}
	return m, nil
}

func (m model) handleFilterKey(msg tea.KeyPressMsg) model {
	l := m.list()
	switch msg.String() {
	case "enter":
		m.filtering = false
	case "esc":
		m.filtering = false
		l.filter = ""
	case "backspace":
		if r := []rune(l.filter); len(r) > 0 {
			l.filter = string(r[:len(r)-1])
		}
	case "ctrl+u":
		l.filter = ""
	case "up":
		m.moveCursor(-1)
		return m
	case "down":
		m.moveCursor(1)
		return m
	default:
		if t := cleanLine(msg.Text); t != "" {
			l.filter += t
		}
	}
	m.clampCursor()
	return m
}

// list returns the list state of the current tab, or nil for System.
func (m *model) list() *listState {
	if m.tab == tabSystem {
		return nil
	}
	return &m.lists[m.tab]
}

// rows returns the names the current tab lists, after its filter. Picked
// names always stay, so a filter never hides a selection.
func (m model) rows() []string {
	var all []string
	switch m.tab {
	case tabServices:
		if m.status != nil {
			for _, s := range m.status.Services {
				all = append(all, s.Name)
			}
		}
	case tabCatalog:
		for _, it := range m.catalog {
			all = append(all, it.Name)
		}
	case tabKeys:
		all = m.keyNames
	default:
		return nil
	}
	l := m.lists[m.tab]
	if l.filter == "" {
		return all
	}
	match := map[string]bool{}
	for _, n := range filterNames(all, l.filter) {
		match[n] = true
	}
	var out []string
	for _, n := range all {
		if match[n] || l.picked[n] {
			out = append(out, n)
		}
	}
	return out
}

func (m *model) moveCursor(delta int) {
	l := m.list()
	if l == nil {
		return
	}
	l.cursor += delta
	m.clampCursor()
}

func (m *model) clampCursor() {
	for t := range m.lists {
		saved := m.tab
		m.tab = tab(t)
		n := len(m.rows())
		m.tab = saved
		l := &m.lists[t]
		if l.cursor >= n {
			l.cursor = n - 1
		}
		if l.cursor < 0 {
			l.cursor = 0
		}
	}
}

// current returns the name under the cursor, or "".
func (m model) current() string {
	rows := m.rows()
	if l := m.lists[m.tab]; l.cursor < len(rows) {
		return rows[l.cursor]
	}
	return ""
}

// targets returns the picked rows in list order, or the cursor row when
// nothing is picked.
func (m model) targets() []string {
	var out []string
	l := m.lists[m.tab]
	for _, n := range m.rows() {
		if l.picked[n] {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		if cur := m.current(); cur != "" {
			out = []string{cur}
		}
	}
	return out
}

func (m *model) togglePick(all bool) {
	l := m.list()
	if l == nil {
		return
	}
	if all {
		for _, n := range m.rows() {
			l.picked[n] = true
		}
		return
	}
	if cur := m.current(); cur != "" {
		if l.picked[cur] {
			delete(l.picked, cur)
		} else {
			l.picked[cur] = true
		}
		m.moveCursor(1)
	}
}

const busyNote = "Another action is running. Wait, or press ctrl+c to cancel it."

func (m model) servicesKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "space":
		m.togglePick(false)
		return m, nil
	case "a":
		m.togglePick(true)
		return m, nil
	case "n":
		m.lists[tabServices].picked = map[string]bool{}
		return m, nil
	case "/":
		m.filtering = true
		return m, nil
	case "d":
		return m.switchTab(tabCatalog)
	case "enter":
		if len(m.targets()) == 0 {
			return m, nil
		}
		m.menu = m.servicesMenu()
		return m, nil
	case "r":
		return m.lifecycle(multiRestart)
	case "s":
		return m.lifecycle(multiStop)
	case "A":
		return m.lifecycle(multiApply)
	case "X":
		return m.lifecycle(multiRemove)
	}
	return m, nil
}

func (m model) servicesMenu() *actionMenu {
	run := func(mode multiMode) func(model) (model, tea.Cmd) {
		return func(m model) (model, tea.Cmd) { return m.lifecycle(mode) }
	}
	t := m.targets()
	title := t[0]
	if len(t) > 1 {
		title = fmt.Sprintf("%d services", len(t))
	}
	return &actionMenu{title: title, items: []menuItem{
		{key: "r", label: "Restart", desc: "Stop, then start again with compose up", run: run(multiRestart)},
		{key: "s", label: "Stop", desc: "Stop the containers and keep all files", run: run(multiStop)},
		{key: "A", label: "Apply catalog", desc: "Copy the latest template files, pull images, and recreate", run: run(multiApply)},
		{key: "X", label: "Remove", desc: "Back up, take down, and delete the deployment", run: run(multiRemove)},
	}}
}

// lifecycle runs mode on the managed targets of the Services tab.
func (m model) lifecycle(mode multiMode) (model, tea.Cmd) {
	if m.busy {
		m.note = busyNote
		return m, nil
	}
	managed := map[string]bool{}
	if m.status != nil {
		for _, s := range m.status.Services {
			managed[s.Name] = s.Managed
		}
	}
	var names, skipped []string
	for _, n := range m.targets() {
		if managed[n] {
			names = append(names, n)
		} else {
			skipped = append(skipped, n)
		}
	}
	if len(names) == 0 {
		if len(skipped) > 0 {
			m.note = skipped[0] + " is not managed by Tailarr (no " + ".tailarr.compose.yaml marker)."
		}
		return m, nil
	}
	m.lists[tabServices].picked = map[string]bool{}
	return m.batch(mode, names)
}

func (m model) batch(mode multiMode, names []string) (model, tea.Cmd) {
	cfg, log := m.cfg, m.log
	title := strings.ToLower(multiTitle(mode)) + " " + summarizeNames(names, 3)
	return m.startOp(title, func(ui prompt.UI) opResult {
		r := runBatchWith(cfg, log, ui, mode, names)
		return opResult{lines: []string{r.summary(mode, names)}}
	})
}

func (m model) catalogKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "space":
		m.togglePick(false)
		return m, nil
	case "a":
		m.togglePick(true)
		return m, nil
	case "n":
		m.lists[tabCatalog].picked = map[string]bool{}
		return m, nil
	case "/":
		m.filtering = true
		return m, nil
	case "r":
		if m.busy {
			m.note = busyNote
			return m, nil
		}
		cfg := m.cfg
		return m.startOp("refresh catalog", func(prompt.UI) opResult {
			return opResult{lines: strings.Split(runCatalogRefresh(cfg), "\n")}
		})
	case "enter":
		if m.busy {
			m.note = busyNote
			return m, nil
		}
		var names, deployed []string
		for _, n := range m.targets() {
			if m.isDeployed(n) {
				deployed = append(deployed, n)
			} else {
				names = append(names, n)
			}
		}
		if len(names) == 0 {
			if len(deployed) > 0 {
				m.note = deployed[0] + " is already deployed. Use Apply on the Services tab."
			}
			return m, nil
		}
		m.lists[tabCatalog].picked = map[string]bool{}
		return m.batch(multiDeploy, names)
	}
	return m, nil
}

func (m model) isDeployed(name string) bool {
	if m.status == nil {
		return false
	}
	for _, s := range m.status.Services {
		if s.Name == name {
			return true
		}
	}
	return false
}

func (m model) keysKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "a":
		return m.keyAction("add", "")
	case "e":
		return m.keyAction("rename", m.current())
	case "p":
		return m.keyAction("replace", m.current())
	case "x":
		return m.keyAction("remove", m.current())
	case "enter":
		cur := m.current()
		if cur == "" {
			return m.keyAction("add", "")
		}
		act := func(action string) func(model) (model, tea.Cmd) {
			return func(m model) (model, tea.Cmd) { return m.keyAction(action, cur) }
		}
		m.menu = &actionMenu{title: cur, items: []menuItem{
			{key: "e", label: "Rename", desc: "Give the key a new name; the value stays", run: act("rename")},
			{key: "p", label: "Replace value", desc: "Paste a new TS_AUTHKEY for this name", run: act("replace")},
			{key: "x", label: "Remove", desc: "Delete the key from the store", run: act("remove")},
			{key: "a", label: "Add new key", desc: "Store another named key", run: func(m model) (model, tea.Cmd) { return m.keyAction("add", "") }},
		}}
		return m, nil
	}
	return m, nil
}

func (m model) keyAction(action, name string) (model, tea.Cmd) {
	if m.busy {
		m.note = busyNote
		return m, nil
	}
	if action != "add" && name == "" {
		return m, nil
	}
	cfg := m.cfg
	title := action + " key"
	if name != "" {
		title += " " + name
	}
	return m.startOp(title, func(ui prompt.UI) opResult {
		return opResult{lines: []string{runAuthkeyAction(cfg, ui, action, name)}}
	})
}

func (m model) systemKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "d":
		if m.doctorBusy {
			return m, nil
		}
		m.doctorBusy = true
		return m, loadDoctor(m.cfg)
	case "e":
		if m.busy {
			m.note = busyNote
			return m, nil
		}
		cfg := m.cfg
		return m.startOp("edit config", func(ui prompt.UI) opResult {
			next := cfg
			text, saved := editConfigInteractive(&next, ui)
			res := opResult{lines: []string{text}}
			if !saved {
				return res
			}
			res.cfg = &next
			if next.LogPath != cfg.LogPath || next.LogMaxBytes != cfg.LogMaxBytes {
				log := logging.New(next.LogPath, next.LogMaxBytes)
				if err := log.Validate(); err != nil {
					res.lines = append(res.lines, "Warning: log path: "+redact.Text(err.Error()))
				}
				res.log = log
			}
			return res
		})
	case "U":
		if m.busy {
			m.note = busyNote
			return m, nil
		}
		log, snd := m.log, m.snd
		return m.startOp("upgrade tailarr", func(ui prompt.UI) opResult {
			text, replaced := runUpgradeAction(log, ui, redact.Writer(&lineSink{snd: snd}))
			return opResult{lines: []string{text}, quit: replaced}
		})
	}
	return m, nil
}

// startOp runs fn off the event loop. fn prompts through the TUI and its
// Printf lines, compose output, and result stream into the output panel.
// Ctrl+C cancels the context that compose, git, and prompts all watch.
func (m model) startOp(title string, fn func(ui prompt.UI) opResult) (model, tea.Cmd) {
	if m.busy {
		return m, nil
	}
	parent := m.rootCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	m.busy = true
	m.opCancel = cancel
	m.opTitle = title
	m.opStart = time.Now()
	m.note = ""
	m.menu = nil
	m.filtering = false
	m.out.reset(title)
	m.scrollBack = 0
	interrupt.Set(ctx)
	prompt.BindCancel(ctx)
	ui := &tuiUI{snd: m.snd, ctx: ctx, assumeYes: m.cfg.AssumeYes}
	flight := m.flight
	flight.track()
	return m, tea.Batch(func() tea.Msg {
		defer flight.untrack()
		defer cancel()
		defer prompt.BindCancel(parent)
		defer interrupt.Set(parent)
		return opDoneMsg{res: fn(ui)}
	}, spinTick())
}

func (m model) finishOp(res opResult) (tea.Model, tea.Cmd) {
	elapsed := time.Since(m.opStart)
	m.busy = false
	m.ask = nil
	m.opCancel = nil
	m.note = ""
	for _, l := range res.lines {
		if strings.TrimSpace(l) != "" {
			m.out.add(l)
		}
	}
	if elapsed >= time.Second {
		m.out.title = fmt.Sprintf("%s · %s", m.opTitle, elapsed.Round(time.Second))
	}
	if res.cfg != nil {
		m.cfg = *res.cfg
	}
	if res.log != nil {
		m.log = res.log
	}
	if res.quit {
		exitText = strings.Join(res.lines, "\n")
		m.quitting = true
		return m, tea.Quit
	}
	m.statusBusy = true
	return m, tea.Batch(loadStatus(m.cfg.DeployPath), loadCatalog(m.cfg.RepoPath), loadKeys(m.cfg.AuthkeysPath))
}

func (m model) View() tea.View {
	if m.quitting {
		return tea.View{}
	}
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "tailarr · " + tabNames[m.tab]
	// No v.ProgressBar: it uses OSC 9;4, which iTerm2 and kitty show as a
	// desktop notification. The header spinner shows progress instead.
	return v
}
