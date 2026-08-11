// Package ui implements the Bubble Tea interactive TUI.
package ui

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/doctor"
	"github.com/jackspiering/tailarr/internal/logging"
	"github.com/jackspiering/tailarr/internal/scaletail"
	"github.com/jackspiering/tailarr/internal/version"
)

// IsInteractive reports whether stdin/stdout support a TUI.
func IsInteractive() bool {
	if os.Getenv("NO_COLOR") != "" {
		// Still allow TUI; NO_COLOR only affects styling preference.
	}
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

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("44"))
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	selStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("255")).Background(lipgloss.Color("236"))
	itemStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	border     = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

type menuItem struct {
	label string
	desc  string
	id    string
}

type model struct {
	cfg      config.Config
	log      *logging.Logger
	cursor   int
	items    []menuItem
	status   string
	quitting bool
	width    int
	height   int
}

// Run starts the main menu TUI.
func Run(cfg config.Config, log *logging.Logger) error {
	m := model{
		cfg: cfg,
		log: log,
		items: []menuItem{
			{id: "list", label: "List services", desc: "Catalog ScaleTail templates"},
			{id: "deployed", label: "Deployed", desc: "Local deployments under deploy path"},
			{id: "doctor", label: "Doctor", desc: "Host readiness checks"},
			{id: "config", label: "Configuration", desc: "Show effective config"},
			{id: "authkeys", label: "Auth keys", desc: "List stored keys (redacted)"},
			{id: "logs", label: "Log path", desc: "Show Tailarr log file path"},
			{id: "quit", label: "Quit", desc: "Exit Tailarr"},
		},
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "enter", " ":
			return m.activate()
		case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9":
			// Number keys 1..n select; 0 = quit
			if msg.String() == "0" {
				m.quitting = true
				return m, tea.Quit
			}
			n := int(msg.String()[0] - '0')
			if n >= 1 && n <= len(m.items) {
				m.cursor = n - 1
				return m.activate()
			}
		}
	}
	return m, nil
}

func (m model) activate() (tea.Model, tea.Cmd) {
	item := m.items[m.cursor]
	switch item.id {
	case "quit":
		m.quitting = true
		return m, tea.Quit
	case "list":
		svcs, err := scaletail.ListAvailable(m.cfg.RepoPath)
		if err != nil {
			m.status = errStyle.Render("list: " + err.Error())
			return m, nil
		}
		if len(svcs) == 0 {
			m.status = dimStyle.Render("No valid ScaleTail services found at " + m.cfg.RepoPath)
			return m, nil
		}
		var b string
		for _, s := range svcs {
			b += "  " + s.Name + "\n"
		}
		m.status = okStyle.Render("Available services:") + "\n" + b
	case "deployed":
		svcs, err := scaletail.ListDeployed(m.cfg.DeployPath)
		if err != nil {
			m.status = errStyle.Render("deployed: " + err.Error())
			return m, nil
		}
		if len(svcs) == 0 {
			m.status = dimStyle.Render("No deployed services.")
			return m, nil
		}
		var b string
		for _, s := range svcs {
			b += "  " + s.Name + "\n"
		}
		m.status = okStyle.Render("Deployed:") + "\n" + b
	case "doctor":
		res := doctor.Run(m.cfg)
		var b string
		for _, c := range res.Checks {
			b += fmt.Sprintf("  [%s] %s: %s\n", c.Level, c.Name, c.Message)
		}
		m.status = b
	case "config":
		m.status = dimStyle.Render(m.cfg.String())
	case "authkeys":
		// Lazy import cycle avoidance: call via small helper in same package
		m.status = showAuthkeys(m.cfg.AuthkeysPath)
	case "logs":
		m.status = "Log file: " + m.cfg.LogPath
	}
	return m, nil
}

func (m model) View() string {
	if m.quitting {
		return ""
	}
	var b string
	b += titleStyle.Render(fmt.Sprintf("Tailarr %s", version.Version)) + "\n"
	b += dimStyle.Render("Deploy and manage ScaleTail services") + "\n"
	b += border.Render(repeat("-", 48)) + "\n\n"

	for i, item := range m.items {
		cursor := "  "
		line := fmt.Sprintf("%d  %s", i+1, item.label)
		if i == m.cursor {
			cursor = "> "
			b += selStyle.Render(cursor+line) + "\n"
			b += dimStyle.Render("     "+item.desc) + "\n"
		} else {
			b += itemStyle.Render(cursor+line) + "\n"
		}
	}
	b += "\n" + dimStyle.Render("arrows/jk move  enter select  q quit") + "\n"
	if m.status != "" {
		b += "\n" + border.Render(repeat("-", 48)) + "\n"
		b += m.status
	}
	return b
}

func repeat(s string, n int) string {
	if n <= 0 {
		return ""
	}
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
