package ui

import (
	"os"

	"charm.land/lipgloss/v2"
)

// Palette. Bubble Tea downsamples these to the terminal color profile.
// Body text keeps the terminal's own foreground, so the TUI reads on light
// and dark backgrounds; colText is only used on the dark bars.
var (
	colAccent  = lipgloss.Color("#4FD1C5")
	colViolet  = lipgloss.Color("#9F7AEA")
	colAmber   = lipgloss.Color("#F6AD55")
	colText    = lipgloss.Color("#E2E8F0")
	colMuted   = lipgloss.Color("#8A94A6")
	colFaint   = lipgloss.Color("#4A5568")
	colOK      = lipgloss.Color("#68D391")
	colWarn    = lipgloss.Color("#F6E05E")
	colErr     = lipgloss.Color("#FC8181")
	colBar     = lipgloss.Color("#1F2533")
	colSelBg   = lipgloss.Color("#2D3748")
	colBadgeFg = lipgloss.Color("#10141C")
)

var (
	plainStyle   = lipgloss.NewStyle()
	boldStyle    = lipgloss.NewStyle().Bold(true)
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	violetStyle  = lipgloss.NewStyle().Bold(true).Foreground(colViolet)
	dimStyle     = lipgloss.NewStyle().Foreground(colMuted)
	headStyle    = lipgloss.NewStyle().Bold(true).Foreground(colFaint)
	keyStyle     = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	errStyle     = lipgloss.NewStyle().Foreground(colErr)
	okStyle      = lipgloss.NewStyle().Foreground(colOK)
	warnStyle    = lipgloss.NewStyle().Foreground(colWarn)
	amberStyle   = lipgloss.NewStyle().Foreground(colAmber)
	barStyle     = lipgloss.NewStyle().Foreground(colMuted).Background(colBar)
	badgeStyle   = lipgloss.NewStyle().Bold(true).Foreground(colBadgeFg).Background(colAccent)
	tabStyle     = lipgloss.NewStyle().Foreground(colMuted).Background(colBar)
	tabOnStyle   = lipgloss.NewStyle().Bold(true).Foreground(colAccent).Background(colSelBg)
	busyStyle    = lipgloss.NewStyle().Bold(true).Foreground(colAmber).Background(colBar)
	noteStyle    = lipgloss.NewStyle().Bold(true).Foreground(colAmber)
	cursorStyle  = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	selTextStyle = lipgloss.NewStyle().Bold(true).Foreground(colText)
)

func colorEnabled() bool { return os.Getenv("NO_COLOR") == "" }

func styleOrPlain(s lipgloss.Style, text string) string {
	if !colorEnabled() {
		return text
	}
	return s.Render(text)
}

// cell renders text in s, on the selection background when sel is set.
func cell(s lipgloss.Style, text string, sel bool) string {
	if text == "" {
		return ""
	}
	if sel {
		if _, unset := s.GetForeground().(lipgloss.NoColor); unset {
			s = s.Inherit(selTextStyle)
		}
		s = s.Background(colSelBg)
	}
	return styleOrPlain(s, text)
}
