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

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/jackspiering/tailarr/internal/authkeys"
	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/deploy"
	"github.com/jackspiering/tailarr/internal/doctor"
	"github.com/jackspiering/tailarr/internal/interrupt"
	"github.com/jackspiering/tailarr/internal/logging"
	"github.com/jackspiering/tailarr/internal/prompt"
	"github.com/jackspiering/tailarr/internal/scaletail"
	"github.com/jackspiering/tailarr/internal/security/names"
	"github.com/jackspiering/tailarr/internal/security/redact"
	"github.com/jackspiering/tailarr/internal/upgrade"
	"github.com/jackspiering/tailarr/internal/version"
)

// prog is the running Bubble Tea program, bound in Run so in-TUI prompts can
// hand the terminal back to cooked mode around direct os.Stdin reads.
var prog *tea.Program

// leaveTUI hands the terminal back to cooked mode and pauses bubbletea's stdin
// reader so prompts can read os.Stdin directly. Reenter with reenterTUI.
// Signals are not delivered to bubbletea while the terminal is released
// (bubbletea sets ignoreSignals). Run installs a process-wide SIGINT/SIGTERM
// handler that cancels in-flight work and quits only after that work finishes,
// so Apply can restore before the process exits.
func leaveTUI() {
	if prog != nil {
		_ = prog.ReleaseTerminal()
	}
}

// reenterTUI resumes bubbletea's terminal handling (raw mode, alt screen, stdin
// reader) after leaveTUI.
func reenterTUI() {
	if prog != nil {
		_ = prog.RestoreTerminal()
	}
}

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

func colorEnabled() bool { return os.Getenv("NO_COLOR") == "" }

func styleOrPlain(s lipgloss.Style, text string) string {
	if !colorEnabled() {
		return text
	}
	return s.Render(text)
}

type menuItem struct {
	label string
	desc  string
	id    string
}

type screen int

const (
	screenMain screen = iota
	screenStatus
	screenServices
	screenAuthkeys
	screenConfig
	screenMaintenance
	screenMultiSelect
	screenResult
)

type multiMode int

const (
	multiNone multiMode = iota
	multiDeploy
	multiRemove
	multiApply
	multiStop
	multiRestart
)

type model struct {
	cfg      config.Config
	log      *logging.Logger
	screen   screen
	cursor   int
	items    []menuItem
	status   string
	quitting bool
	busy     bool

	// layout state
	width  int
	height int
	scroll int
	host   string

	// multi-select state
	multi       multiMode
	multiParent screen
	opts        []string
	// selected indexes for multi
	picked map[int]bool

	rootCtx  context.Context
	opCancel context.CancelFunc
	flight   *workFlight
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
// sequences return. Apply's restore defer runs in that sequence.
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

// Run starts the interactive TUI. Lifecycle actions that need prompts leave the
// alternate screen and use stdin prompts, then return to the menu.
func Run(cfg config.Config, log *logging.Logger) error {
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()
	flight := &workFlight{}
	m := model{
		cfg:     cfg,
		log:     log,
		screen:  screenMain,
		items:   mainMenuItems(),
		picked:  map[int]bool{},
		rootCtx: rootCtx,
		flight:  flight,
	}
	if host, err := os.Hostname(); err == nil {
		m.host = host
	}
	interrupt.Set(rootCtx)
	defer interrupt.Clear()
	prompt.BindCancel(rootCtx)
	defer prompt.BindCancel(context.Background())
	p := tea.NewProgram(m)
	prog = p
	// bubbletea ignores signals while ReleaseTerminal is active. Cancel shared
	// work and quit only after sequences return so Apply restore can finish.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		drainThenQuit(rootCancel, flight, func() {
			if prog != nil {
				prog.Quit()
			}
		})
	}()
	_, err := p.Run()
	if errors.Is(err, tea.ErrInterrupted) {
		err = nil
	}
	return err
}

// FirstRunSetup creates a default config file on first run. It is a no-op when
// the config file already exists. Prompts run before the TUI takes over the
// terminal.
func FirstRunSetup(cfg *config.Config) error {
	// If config file already exists, nothing to do.
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
	if err := config.Save(*cfg); err != nil {
		uiPrompt.Printf("Could not save config: %v\n", err)
		return fmt.Errorf("save config: %w", err)
	}
	uiPrompt.Printf("Saved config: %s\n", cfg.ConfigPath)
	return nil
}

func mainMenuItems() []menuItem {
	return []menuItem{
		{id: "status", label: "Status", desc: "Health, counts, and deployment status"},
		{id: "services", label: "Services", desc: "Deploy, apply, and control services"},
		{id: "authkeys", label: "Tailscale Authentication Keys", desc: "Manage stored Tailscale authentication keys"},
		{id: "config", label: "Configuration", desc: "View or edit Tailarr configuration"},
		{id: "maintenance", label: "Maintenance", desc: "Doctor checks and maintenance tools"},
		{id: "quit", label: "Exit", desc: "Quit Tailarr"},
	}
}

func statusMenuItems() []menuItem {
	return []menuItem{
		{id: "overview", label: "Overview", desc: "Health, counts, and managed services"},
		{id: "deployed", label: "Deployed services", desc: "Browse local deployments"},
		{id: "running", label: "Running services", desc: "Inspect running Docker containers"},
		{id: "summary", label: "Docker and config summary", desc: "Review Docker access and configuration"},
		{id: "back", label: "Back", desc: "Return to main menu"},
	}
}

func servicesMenuItems() []menuItem {
	return []menuItem{
		{id: "search", label: "Search available services", desc: "Find available ScaleTail templates"},
		{id: "refresh", label: "Refresh catalog", desc: "Clone or pull the ScaleTail templates"},
		{id: "deploy", label: "Deploy services", desc: "Create new deployments from the catalog"},
		{id: "apply", label: "Apply catalog", desc: "Sync template files, pull images, and recreate"},
		{id: "remove", label: "Remove services", desc: "Stop and remove deployments"},
		{id: "stop", label: "Stop services", desc: "Stop selected deployments"},
		{id: "restart", label: "Restart services", desc: "Restart selected deployments"},
		{id: "back", label: "Back", desc: "Return to main menu"},
	}
}

func authkeysMenuItems() []menuItem {
	return []menuItem{
		{id: "list", label: "List keys", desc: "Show stored key names (redacted)"},
		{id: "add", label: "Add key", desc: "Add a new stored auth key"},
		{id: "rename", label: "Rename key", desc: "Change a stored key name"},
		{id: "replace", label: "Replace key value", desc: "Replace a stored key value"},
		{id: "remove", label: "Remove key", desc: "Delete a stored auth key"},
		{id: "back", label: "Back", desc: "Return to main menu"},
	}
}

func configMenuItems() []menuItem {
	return []menuItem{
		{id: "view", label: "View current config", desc: "View the active configuration"},
		{id: "edit", label: "Edit config", desc: "Edit paths, repository, and logging"},
		{id: "back", label: "Back", desc: "Return to main menu"},
	}
}

func maintenanceMenuItems() []menuItem {
	return []menuItem{
		{id: "doctor", label: "Run doctor checks", desc: "Host, path, Docker, and health checks"},
		{id: "upgrade", label: "Upgrade Tailarr", desc: "Replace this binary with the latest release"},
		{id: "back", label: "Back", desc: "Return to main menu"},
	}
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case resultMsg:
		m.busy = false
		if msg.cfg != nil {
			m.cfg = *msg.cfg
		}
		if msg.log != nil {
			m.log = msg.log
		}
		m.screen = screenResult
		m.status = msg.text
		m.items = []menuItem{{id: "back", label: "Back", desc: "Return"}}
		m.cursor = 0
		m.scroll = 0
		return m, nil
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case upgradeDoneMsg:
		// The binary was replaced; leave the TUI so the new version takes over.
		m.quitting = true
		return m, tea.Quit
	case tea.KeyPressMsg:
		key := msg.String()
		if m.busy {
			if key == "ctrl+c" || key == "q" || key == "esc" {
				m.status = "Canceling..."
				if m.opCancel != nil {
					m.opCancel()
				}
				return m, nil
			}
			return m, nil
		}
		switch key {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "q", "esc":
			if m.screen == screenMultiSelect {
				if m.multiParent == screenMaintenance {
					return m.setScreen(screenMaintenance, maintenanceMenuItems()), nil
				}
				return m.setScreen(screenServices, servicesMenuItems()), nil
			}
			if m.screen == screenMain {
				m.quitting = true
				return m, tea.Quit
			}
			return m.goBack(), nil
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "pgup":
			m.scroll = max(m.scroll-m.pageSize(), 0)
		case "pgdown":
			m.scroll = min(m.scroll+m.pageSize(), m.maxScroll())
		case "home":
			m.scroll = 0
		case "end":
			m.scroll = m.maxScroll()
		case "down", "j":
			max := len(m.items) - 1
			if m.screen == screenMultiSelect {
				max = len(m.opts) + len(m.items) - 1
			}
			if m.cursor < max {
				m.cursor++
			}
		case "space":
			if m.screen == screenMultiSelect && m.cursor < len(m.opts) {
				m.picked[m.cursor] = !m.picked[m.cursor]
			}
		case "enter":
			return m.activate()
		case "a":
			if m.screen == screenMultiSelect {
				for i := range m.opts {
					m.picked[i] = true
				}
			}
		case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9":
			if msg.String() == "0" {
				if m.screen == screenMultiSelect {
					if m.multiParent == screenMaintenance {
						return m.setScreen(screenMaintenance, maintenanceMenuItems()), nil
					}
					return m.setScreen(screenServices, servicesMenuItems()), nil
				}
				if m.screen == screenMain {
					m.quitting = true
					return m, tea.Quit
				}
				return m.goBack(), nil
			}
			n := int(msg.String()[0] - '0')
			if m.screen == screenMultiSelect {
				// Digits address the same rows shown in View.
				idx := n - 1
				if idx >= 0 && idx < len(m.opts)+len(m.items) {
					m.cursor = idx
					return m.activate()
				}
				return m, nil
			}

			if n >= 1 && n <= len(m.items) {
				m.cursor = n - 1
				return m.activate()
			}
		}
	}
	return m, nil
}

type resultMsg struct {
	text string
	cfg  *config.Config
	log  *logging.Logger
}

// upgradeDoneMsg signals that the running binary was replaced and the TUI
// should exit so the new version takes over.
type upgradeDoneMsg struct{}

func (m model) goBack() model {
	m.screen = screenMain
	m.items = mainMenuItems()
	m.cursor = 0
	m.status = ""
	m.multi = multiNone
	m.picked = map[int]bool{}
	m.opts = nil
	m.scroll = 0
	return m
}

func (m model) setScreen(s screen, items []menuItem) model {
	m.screen = s
	m.items = items
	m.cursor = 0
	m.status = ""
	m.scroll = 0
	return m
}

// pageSize is the scroll step for the output panel.
func (m model) pageSize() int {
	_, h := m.size()
	return max(h/2, 1)
}

func (m model) activate() (tea.Model, tea.Cmd) {
	if m.busy {
		return m, nil
	}
	m.scroll = 0
	if m.screen == screenMultiSelect {
		// cursor indexes opts first, then action items.
		if m.cursor < len(m.opts) {
			m.picked[m.cursor] = !m.picked[m.cursor]
			return m, nil
		}
		ai := m.cursor - len(m.opts)
		if ai >= 0 && ai < len(m.items) {
			// temporarily set cursor to action index for finishMulti
			saved := m.cursor
			m.cursor = ai
			nm, cmd := m.finishMulti()
			if mm, ok := nm.(model); ok {
				mm.cursor = saved
				return mm, cmd
			}
			return nm, cmd
		}
		return m, nil
	}
	if len(m.items) == 0 {
		return m, nil
	}
	id := m.items[m.cursor].id

	switch m.screen {
	case screenMain:
		switch id {
		case "quit":
			m.quitting = true
			return m, tea.Quit
		case "status":
			return m.setScreen(screenStatus, statusMenuItems()), nil
		case "services":
			return m.setScreen(screenServices, servicesMenuItems()), nil
		case "authkeys":
			return m.setScreen(screenAuthkeys, authkeysMenuItems()), nil
		case "config":
			return m.setScreen(screenConfig, configMenuItems()), nil
		case "maintenance":
			return m.setScreen(screenMaintenance, maintenanceMenuItems()), nil
		}
	case screenStatus:
		return m.activateStatus(id)
	case screenServices:
		return m.activateServices(id)
	case screenAuthkeys:
		return m.activateAuthkeys(id)
	case screenConfig:
		return m.activateConfig(id)
	case screenMaintenance:
		return m.activateMaintenance(id)
	case screenResult:
		return m.goBack(), nil
	}
	return m, nil
}

func (m model) activateStatus(id string) (tea.Model, tea.Cmd) {
	switch id {
	case "back":
		return m.goBack(), nil
	case "overview":
		deployPath := m.cfg.DeployPath
		return m.startSequence(func() tea.Msg {
			st, err := deploy.CollectOverview(deployPath)
			if err != nil {
				return resultMsg{text: styleOrPlain(errStyle, redact.Text(err.Error()))}
			}
			return resultMsg{text: deploy.FormatOverview(st)}
		})
	case "deployed":
		deployPath := m.cfg.DeployPath
		return m.startSequence(func() tea.Msg {
			svcs, err := scaletail.ListDeployed(deployPath)
			if err != nil {
				return resultMsg{text: styleOrPlain(errStyle, redact.Text(err.Error()))}
			}
			if len(svcs) == 0 {
				return resultMsg{text: "No deployed services."}
			}
			names := make([]string, 0, len(svcs))
			for _, s := range svcs {
				names = append(names, s.Name)
			}
			health := deploy.ServiceHealthMap(names)
			var b strings.Builder
			for _, s := range svcs {
				tag := "other"
				if deploy.IsManaged(s.Dir) {
					tag = "managed"
				}
				fmt.Fprintf(&b, "  - %s\t%s\t[%s]\n", s.Name, tag, health[s.Name])
			}
			return resultMsg{text: b.String()}
		})
	case "running":
		return m.startSequence(func() tea.Msg {
			names, err := deploy.RunningServiceNames()
			if err != nil {
				return resultMsg{text: styleOrPlain(errStyle, redact.Text(err.Error()))}
			}
			if len(names) == 0 {
				return resultMsg{text: "No running ScaleTail-style containers found."}
			}
			return resultMsg{text: "  - " + strings.Join(names, "\n  - ")}
		})
	case "summary":
		cfg := m.cfg
		return m.startSequence(func() tea.Msg {
			st, err := deploy.CollectOverview(cfg.DeployPath)
			if err != nil {
				return resultMsg{text: styleOrPlain(errStyle, redact.Text(err.Error()))}
			}
			return resultMsg{text: deploy.FormatOverview(st) + "\n" + cfg.String() + "\nLog path: " + cfg.LogPath}
		})
	}
	return m, nil
}

func (m model) activateServices(id string) (tea.Model, tea.Cmd) {
	switch id {
	case "back":
		return m.goBack(), nil
	case "search":
		svcs, err := scaletail.ListAvailable(m.cfg.RepoPath)
		if err != nil {
			m.status = styleOrPlain(errStyle, redact.Text(err.Error()))
			return m, nil
		}
		if len(svcs) == 0 {
			m.status = "No valid ScaleTail services found."
			return m, nil
		}
		var b strings.Builder
		for _, s := range svcs {
			fmt.Fprintf(&b, "  - %s\n", s.Name)
		}
		m.status = b.String()
		return m, nil
	case "refresh":
		cfg := m.cfg
		return m.startSequence(func() tea.Msg {
			leaveTUI()
			defer reenterTUI()
			return resultMsg{text: runCatalogRefresh(cfg)}
		})
	case "deploy":
		return m.beginMulti(multiDeploy)
	case "apply":
		return m.beginMulti(multiApply)
	case "remove":
		return m.beginMulti(multiRemove)
	case "stop":
		return m.beginMulti(multiStop)
	case "restart":
		return m.beginMulti(multiRestart)
	}
	return m, nil
}

func (m model) activateAuthkeys(id string) (tea.Model, tea.Cmd) {
	ui := prompt.NewStd(m.cfg.AssumeYes)
	switch id {
	case "back":
		return m.goBack(), nil
	case "list":
		s, err := authkeys.Load(m.cfg.AuthkeysPath)
		if err != nil {
			m.status = styleOrPlain(errStyle, redact.Text(err.Error()))
			return m, nil
		}
		lines := s.RedactedList()
		if len(lines) == 0 {
			m.status = "No stored auth keys."
		} else {
			m.status = "  - " + strings.Join(lines, "\n  - ")
		}
		return m, nil
	case "add", "rename", "replace", "remove":
		// Leave the TUI for interactive prompts. ReleaseTerminal/RestoreTerminal
		// (replacing the alt-screen exit/enter commands) also restore raw mode and
		// pause bubbletea's stdin reader so prompt reads don't race the TUI readLoop.
		cfg := m.cfg
		action := id
		return m.startSequence(func() tea.Msg {
			leaveTUI()
			defer reenterTUI()
			text := runAuthkeyAction(cfg, ui, action)
			return resultMsg{text: text}
		})
	}
	return m, nil
}

func runAuthkeyAction(cfg config.Config, ui *prompt.Std, action string) string {
	// Serialize read-modify-write with a lock next to the store.
	lock, err := deploy.AcquireLock(deploy.AuthkeysLockPath(cfg.AuthkeysPath), deploy.DefaultLockTimeout)
	if err != nil {
		return "Error: authkeys lock: " + err.Error()
	}
	defer func() { _ = lock.Release() }()

	s, err := authkeys.Load(cfg.AuthkeysPath)
	if err != nil {
		return "Error: " + err.Error()
	}
	switch action {
	case "add":
		name, err := ui.Line("New key name", "")
		if err != nil || name == "" {
			return "Canceled."
		}
		val, err := ui.Secret("TS_AUTHKEY")
		if err != nil {
			return "Error: " + err.Error()
		}
		if err := s.Put(name, val); err != nil {
			return "Error: " + err.Error()
		}
		if err := s.Save(); err != nil {
			return "Error: " + err.Error()
		}
		return "Stored auth key: " + name
	case "rename":
		if len(s.Order) == 0 {
			return "No stored keys."
		}
		ui.Printf("Keys: %s\n", strings.Join(s.Order, ", "))
		old, err := ui.Line("Key to rename", "")
		if err != nil || old == "" {
			return "Canceled."
		}
		nw, err := ui.Line("New name", "")
		if err != nil || nw == "" {
			return "Canceled."
		}
		if err := s.Rename(old, nw); err != nil {
			return "Error: " + err.Error()
		}
		if err := s.Save(); err != nil {
			return "Error: " + err.Error()
		}
		return "Renamed to: " + nw
	case "replace":
		if len(s.Order) == 0 {
			return "No stored keys."
		}
		ui.Printf("Keys: %s\n", strings.Join(s.Order, ", "))
		name, err := ui.Line("Key to replace", "")
		if err != nil || name == "" {
			return "Canceled."
		}
		val, err := ui.Secret("TS_AUTHKEY")
		if err != nil {
			return "Error: " + err.Error()
		}
		if err := s.Put(name, val); err != nil {
			return "Error: " + err.Error()
		}
		if err := s.Save(); err != nil {
			return "Error: " + err.Error()
		}
		return "Updated auth key: " + name
	case "remove":
		if len(s.Order) == 0 {
			return "No stored keys."
		}
		ui.Printf("Keys: %s\n", strings.Join(s.Order, ", "))
		name, err := ui.Line("Key to remove", "")
		if err != nil || name == "" {
			return "Canceled."
		}
		ok, err := ui.Confirm("Remove stored auth key "+name+"?", false)
		if err != nil || !ok {
			return "Canceled."
		}
		if err := s.Remove(name); err != nil {
			return "Error: " + err.Error()
		}
		if err := s.Save(); err != nil {
			return "Error: " + err.Error()
		}
		return "Removed auth key: " + name
	}
	return ""
}

func (m model) activateConfig(id string) (tea.Model, tea.Cmd) {
	switch id {
	case "back":
		return m.goBack(), nil
	case "view":
		m.status = m.cfg.String()
		return m, nil
	case "edit":
		// Leave the TUI for interactive prompts. ReleaseTerminal/RestoreTerminal
		// (replacing the alt-screen exit/enter commands) also restore raw mode and
		// pause bubbletea's stdin reader so prompt reads don't race the TUI readLoop.
		return m.startSequence(func() tea.Msg {
			leaveTUI()
			defer reenterTUI()
			cfg := m.cfg
			oldLog := m.cfg.LogPath
			oldMax := m.cfg.LogMaxBytes
			ui := prompt.NewStd(false)
			text, saved := editConfigInteractive(&cfg, ui)
			if !saved {
				return resultMsg{text: text}
			}
			msg := resultMsg{text: text, cfg: &cfg}
			if cfg.LogPath != oldLog || cfg.LogMaxBytes != oldMax {
				log := logging.New(cfg.LogPath, cfg.LogMaxBytes)
				if err := log.Validate(); err != nil {
					msg.text += "\nWarning: log path: " + redact.Text(err.Error())
				}
				msg.log = log
			}
			return msg
		})
	}
	return m, nil
}

func editConfigInteractive(cfg *config.Config, ui *prompt.Std) (string, bool) {
	next := *cfg
	var err error
	var raw string
	if raw, err = ui.Line("TAILARR_REPO_URL", next.RepoURL); err != nil {
		return redact.Text(err.Error()), false
	}
	raw = strings.TrimSpace(raw)
	if raw != "" {
		if err := names.ValidateRepoURL(raw); err != nil {
			return "Error saving: " + redact.Text(err.Error()), false
		}
	}
	next.RepoURL = raw
	if next.RepoPath, err = ui.Line("TAILARR_REPO_PATH", next.RepoPath); err != nil {
		return redact.Text(err.Error()), false
	}
	if next.DeployPath, err = ui.Line("TAILARR_DEPLOY_PATH", next.DeployPath); err != nil {
		return redact.Text(err.Error()), false
	}
	if next.LogPath, err = ui.Line("TAILARR_LOG_PATH", next.LogPath); err != nil {
		return redact.Text(err.Error()), false
	}
	if next.AuthkeysPath, err = ui.Line("TAILARR_AUTHKEYS_PATH", next.AuthkeysPath); err != nil {
		return redact.Text(err.Error()), false
	}
	if err := config.Save(next); err != nil {
		return "Error saving: " + redact.Text(err.Error()), false
	}
	*cfg = next
	return "Saved config: " + next.ConfigPath, true
}

func (m model) activateMaintenance(id string) (tea.Model, tea.Cmd) {
	switch id {
	case "back":
		return m.goBack(), nil
	case "doctor":
		cfg := m.cfg
		return m.startSequence(func() tea.Msg {
			res := doctor.Run(cfg)
			var b strings.Builder
			for _, c := range res.Checks {
				fmt.Fprintf(&b, "  [%s] %s: %s\n", c.Level, c.Name, c.Message)
			}
			return resultMsg{text: b.String()}
		})
	case "upgrade":
		// Leave the TUI: the running binary may be replaced, and prompts/download
		// progress need the normal terminal. ReleaseTerminal/RestoreTerminal
		// (replacing the alt-screen exit/enter commands) also restore raw mode and
		// pause bubbletea's stdin reader so prompt reads don't race the TUI readLoop.
		cfg := m.cfg
		log := m.log
		return m.startSequence(func() tea.Msg {
			leaveTUI()
			text, replaced := runUpgradeAction(cfg, log)
			if replaced {
				// Binary replaced: print the result, then quit. Do not restore the
				// terminal after quit - the program's shutdown restores state and
				// the new version takes over the TTY.
				_, _ = fmt.Fprintln(os.Stdout, text)
				return upgradeDoneMsg{}
			}
			defer reenterTUI()
			return resultMsg{text: text}
		})
	}
	return m, nil
}

func runUpgradeAction(cfg config.Config, log *logging.Logger) (string, bool) {
	ui := prompt.NewStd(cfg.AssumeYes)
	opts := upgrade.Options{Current: version.Version, Out: os.Stdout}
	latest, err := upgrade.Latest(opts)
	if err != nil {
		return "Error: " + err.Error(), false
	}
	if upgrade.Comparable(version.Version, latest) && upgrade.Compare(version.Version, latest) >= 0 {
		return fmt.Sprintf("Already up to date (%s)", version.Version), false
	}
	question := fmt.Sprintf("Upgrade Tailarr %s to %s?", version.Version, latest)
	if !upgrade.Comparable(version.Version, latest) {
		question = fmt.Sprintf("Installed %s is not SemVer; install %s anyway?", version.Version, latest)
	}
	ok, err := ui.Confirm(question, true)
	if err != nil || !ok {
		return "Canceled.", false
	}
	tag, err := upgrade.Upgrade(opts)
	if err != nil {
		return "Error: " + err.Error(), false
	}
	if log != nil {
		log.Event("tailarr upgraded to " + tag)
	}
	return fmt.Sprintf("Upgraded Tailarr to %s", tag), true
}

func (m model) beginMulti(mode multiMode) (tea.Model, tea.Cmd) {
	var names []string
	var err error
	switch mode {
	case multiDeploy:
		svcs, e := scaletail.ListAvailable(m.cfg.RepoPath)
		err = e
		for _, s := range svcs {
			names = append(names, s.Name)
		}
	default:
		svcs, e := scaletail.ListDeployed(m.cfg.DeployPath)
		err = e
		for _, s := range svcs {
			if deploy.IsManaged(s.Dir) {
				names = append(names, s.Name)
			}
		}
	}
	if err != nil {
		m.status = styleOrPlain(errStyle, redact.Text(err.Error()))
		return m, nil
	}
	if len(names) == 0 {
		m.status = "No services available for this action."
		return m, nil
	}
	m.multi = mode
	m.multiParent = m.screen
	m.opts = names
	m.picked = map[int]bool{}
	m.screen = screenMultiSelect
	m.items = []menuItem{
		{id: "run", label: "Run on selection", desc: "space toggles, a selects all, enter runs"},
		{id: "cancel", label: "Cancel", desc: "Return without changes"},
	}
	m.cursor = 0
	m.status = ""
	return m, nil
}

func (m model) finishMulti() (tea.Model, tea.Cmd) {
	id := m.items[m.cursor].id
	if id == "cancel" {
		if m.multiParent == screenMaintenance {
			return m.setScreen(screenMaintenance, maintenanceMenuItems()), nil
		}
		return m.setScreen(screenServices, servicesMenuItems()), nil
	}
	var selected []string
	for i, name := range m.opts {
		if m.picked[i] {
			selected = append(selected, name)
		}
	}
	if len(selected) == 0 {
		m.status = "No services selected."
		return m, nil
	}
	mode := m.multi
	cfg := m.cfg
	log := m.log
	return m.startSequence(func() tea.Msg {
		// ReleaseTerminal/RestoreTerminal (replacing the alt-screen exit/enter
		// commands) also restore raw mode and pause bubbletea's stdin reader so
		// prompt reads don't race the TUI readLoop.
		leaveTUI()
		defer reenterTUI()
		text := runBatch(cfg, log, mode, selected)
		return resultMsg{text: text}
	})
}

func (m model) startSequence(fn func() tea.Msg) (tea.Model, tea.Cmd) {
	if m.busy {
		return m, nil
	}
	parent := m.rootCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	m.opCancel = cancel
	m.busy = true
	interrupt.Set(ctx)
	prompt.BindCancel(ctx)
	flight := m.flight
	if flight != nil {
		flight.track()
	}
	return m, tea.Sequence(func() tea.Msg {
		defer cancel()
		defer prompt.BindCancel(parent)
		defer interrupt.Set(parent)
		if flight != nil {
			defer flight.untrack()
		}
		return fn()
	})
}

func runCatalogRefresh(cfg config.Config) string {
	lock, err := deploy.AcquireLock(deploy.RepoLockPath(cfg.RepoPath), deploy.DefaultLockTimeout)
	if err != nil {
		return styleOrPlain(errStyle, "repo lock: "+redact.Text(err.Error()))
	}
	defer func() { _ = lock.Release() }()
	msg, err := scaletail.Refresh(cfg.RepoURL, cfg.RepoPath)
	if err != nil {
		return styleOrPlain(errStyle, redact.Text(err.Error()))
	}
	if strings.HasPrefix(msg, "Using local") {
		return msg
	}
	if msg == "" {
		return "Catalog is up to date."
	}
	return "Catalog refreshed.\n" + msg
}

func runBatch(cfg config.Config, log *logging.Logger, mode multiMode, services []string) string {
	ui := prompt.NewStd(cfg.AssumeYes)
	mgr := &deploy.Manager{Cfg: &cfg, Log: log, UI: ui}
	var b strings.Builder
	var sharedKey string
	if mode == multiDeploy && len(services) > 1 {
		if ok, _ := ui.Confirm("Use one reusable Tailscale auth key for all selected services?", true); ok {
			// Resolve once interactively via a dummy merge path is complex; prompt once.
			s, err := authkeys.Load(cfg.AuthkeysPath)
			if err == nil && len(s.Order) > 0 {
				ui.Printf("Stored keys: %s\n", strings.Join(s.Order, ", "))
				name, _ := ui.Line("Auth key name (empty to paste)", "")
				if name != "" {
					sharedKey = s.Keys[name]
				}
			}
			if sharedKey == "" {
				val, err := ui.Secret("TS_AUTHKEY for all services")
				if err == nil && val != "" {
					sharedKey = val
				}
			}
		}
	}
	for _, svc := range services {
		fmt.Fprintf(&b, "==> %s\n", svc)
		var err error
		switch mode {
		case multiDeploy:
			err = mgr.DeployWith(svc, deploy.DeployOpts{ReusableAuthKey: sharedKey})
		case multiApply:
			err = mgr.Apply(svc, deploy.DeployOpts{ReusableAuthKey: sharedKey})
		case multiRemove:
			err = mgr.RemoveWith(svc, deploy.DeployOpts{})
		case multiStop:
			err = mgr.Stop(svc)
		case multiRestart:
			err = mgr.Restart(svc)
		}
		if err != nil {
			fmt.Fprintf(&b, "  error: %s\n", redact.Text(err.Error()))
		} else {
			fmt.Fprintf(&b, "  ok\n")
		}
	}
	return b.String()
}

func (m model) View() tea.View {
	if m.quitting {
		return tea.View{}
	}
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}
