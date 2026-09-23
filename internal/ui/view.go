package ui

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/security/names"
	"github.com/jackspiering/tailarr/internal/security/redact"
	"github.com/jackspiering/tailarr/internal/version"
)

// Default terminal size used before the first WindowSizeMsg arrives.
const (
	defaultWidth  = 80
	defaultHeight = 24
	// sideBySideMin is the narrowest width that still fits the details panel
	// next to the menu panel.
	sideBySideMin = 72
	// minOutputRows keeps room for the output panel below the menu.
	minOutputRows = 6
)

// Palette. Bubble Tea downsamples these to the terminal color profile.
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
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	dimStyle   = lipgloss.NewStyle().Foreground(colMuted)
	faintStyle = lipgloss.NewStyle().Foreground(colFaint)
	selStyle   = lipgloss.NewStyle().Bold(true).Foreground(colText).Background(colSelBg)
	itemStyle  = lipgloss.NewStyle().Foreground(colText)
	keyStyle   = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	errStyle   = lipgloss.NewStyle().Foreground(colErr)
	okStyle    = lipgloss.NewStyle().Foreground(colOK)
	warnStyle  = lipgloss.NewStyle().Foreground(colWarn)
	barStyle   = lipgloss.NewStyle().Foreground(colMuted).Background(colBar)
	badgeStyle = lipgloss.NewStyle().Bold(true).Foreground(colBadgeFg).Background(colAccent)
	crumbStyle = lipgloss.NewStyle().Bold(true).Foreground(colText).Background(colBar)
	busyStyle  = lipgloss.NewStyle().Bold(true).Foreground(colAmber).Background(colBar)
)

func (m model) size() (int, int) {
	w, h := m.width, m.height
	if w <= 0 {
		w = defaultWidth
	}
	if h <= 0 {
		h = defaultHeight
	}
	return w, h
}

func screenTitle(s screen) string {
	switch s {
	case screenStatus:
		return "Status"
	case screenServices:
		return "Services"
	case screenAuthkeys:
		return "Auth keys"
	case screenConfig:
		return "Configuration"
	case screenMaintenance:
		return "Maintenance"
	case screenResult:
		return "Result"
	}
	return "Main menu"
}

func multiTitle(mode multiMode) string {
	switch mode {
	case multiDeploy:
		return "Deploy"
	case multiRemove:
		return "Remove"
	case multiApply:
		return "Apply"
	case multiStop:
		return "Stop"
	case multiRestart:
		return "Restart"
	}
	return "Select"
}

func (m model) title() string {
	if m.screen == screenMultiSelect {
		return multiTitle(m.multi) + " services"
	}
	return screenTitle(m.screen)
}

func (m model) breadcrumb() string {
	switch m.screen {
	case screenMain:
		return screenTitle(screenMain)
	case screenMultiSelect:
		return screenTitle(m.multiParent) + " > " + m.title()
	}
	return screenTitle(screenMain) + " > " + m.title()
}

// frame holds the computed layout for one render.
type frame struct {
	top     []string
	outText []string
	outRows int
}

// layout splits the body between the menu panels and the output panel.
func (m model) layout() frame {
	w, h := m.size()
	body := h - 2
	var f frame
	if m.status != "" {
		f.outText = outputLines(m.status, w-4)
	}
	if m.screen == screenResult && len(f.outText) > 0 {
		// The result screen has a single Back item; give the output all rows.
		f.outRows = body
		return f
	}
	topMax, fill := body, true
	if len(f.outText) > 0 {
		topMax, fill = body-minOutputRows, false
	}
	if topMax < 3 {
		topMax = 3
	}
	f.top = m.topPanels(w, topMax, fill)
	if len(f.outText) > 0 {
		if rows := body - len(f.top); rows >= 3 {
			f.outRows = rows
		}
	}
	return f
}

// maxScroll bounds the output panel offset so the last page stays full.
func (m model) maxScroll() int {
	f := m.layout()
	if f.outRows == 0 {
		return 0
	}
	return max(len(f.outText)-(f.outRows-2), 0)
}

func (m model) render() string {
	w, h := m.size()
	f := m.layout()
	out := []string{m.header(w)}
	out = append(out, f.top...)
	scrolls := false
	if f.outRows > 0 {
		title := "Output"
		if m.screen == screenResult {
			title = "Result"
		}
		out = append(out, m.outputPanel(title, f.outText, w, f.outRows)...)
		scrolls = len(f.outText) > f.outRows-2
	}
	for len(out) < h-1 {
		out = append(out, "")
	}
	out = append(out, m.footer(w, scrolls))
	return strings.Join(out, "\n")
}

func (m model) header(w int) string {
	ver := version.Version
	if ver != "" && ver[0] >= '0' && ver[0] <= '9' {
		ver = "v" + ver
	}
	left := styleOrPlain(badgeStyle, " Tailarr ") +
		styleOrPlain(barStyle, " "+ver+"  ") +
		styleOrPlain(crumbStyle, m.breadcrumb())
	right := ""
	if m.busy {
		right = styleOrPlain(busyStyle, "* working ")
	} else if m.host != "" {
		right = styleOrPlain(barStyle, "host "+m.host+" ")
	}
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		right = ""
		gap = w - lipgloss.Width(left)
	}
	if gap < 0 {
		return clip(left, w)
	}
	return left + styleOrPlain(barStyle, strings.Repeat(" ", gap)) + right
}

func (m model) footer(w int, scrolls bool) string {
	back := "back"
	if m.screen == screenMain {
		back = "quit"
	}
	hints := [][2]string{{"up/down", "move"}, {"enter", "select"}}
	switch m.screen {
	case screenResult:
		hints = nil
	case screenMultiSelect:
		hints = append(hints, [2]string{"space", "toggle"}, [2]string{"a", "all"}, [2]string{"1-9", "select/run"})
	default:
		hints = append(hints, [2]string{"1-9", "jump"})
	}
	if scrolls {
		hints = append(hints, [2]string{"pgup/pgdn", "scroll"})
	}
	if m.screen == screenResult {
		hints = append(hints, [2]string{"enter/q", back})
	} else {
		hints = append(hints, [2]string{"q", back})
	}
	// Drop hints that do not fit instead of cutting one in half. The last
	// hint (back or quit) always stays.
	line, used := "", 1
	for i, h := range hints {
		cell := len(h[0]) + 1 + len(h[1]) + 2
		if used+cell > w && i < len(hints)-1 {
			continue
		}
		line += " " + styleOrPlain(keyStyle, h[0]) + " " + styleOrPlain(dimStyle, h[1]) + " "
		used += cell
	}
	return clip(line, w)
}

// topPanels renders the menu panel and, when wide enough, the details panel
// next to it. It returns at most maxRows lines.
func (m model) topPanels(w, maxRows int, fill bool) []string {
	side := w >= sideBySideMin
	menuW := w
	if side {
		menuW = w * 11 / 20
	}
	detailW := w - menuW

	var details []string
	if side {
		for _, l := range m.detailLines(detailW - 4) {
			details = append(details, " "+l)
		}
	}
	menuRows := m.menuRowCount(!side)
	rows := menuRows
	if len(details) > rows && fill {
		rows = len(details)
	}
	if fill || rows+2 > maxRows {
		rows = maxRows - 2
	}
	menu, label := m.menuLines(menuW-2, rows, !side)
	left := panel(m.title(), label, menu, menuW, rows+2, colAccent)
	if !side {
		return left
	}
	dTitle := "Details"
	if m.screen == screenMultiSelect {
		dTitle = "Selection"
	}
	right := panel(dTitle, "", details, detailW, rows+2, colViolet)
	joined := make([]string, len(left))
	for i := range left {
		joined[i] = left[i] + right[i]
	}
	return joined
}

func (m model) menuRowCount(inlineDesc bool) int {
	if m.screen == screenMultiSelect {
		return len(m.opts) + 1 + len(m.items)
	}
	n := len(m.items)
	if inlineDesc && n > 0 {
		n++
	}
	return n
}

// menuLines renders the rows of the menu panel within rows lines of inner
// width w. The label reports the visible window when the list is clipped.
func (m model) menuLines(w, rows int, inlineDesc bool) ([]string, string) {
	if m.screen == screenMultiSelect {
		return m.multiLines(w, rows)
	}
	var lines []string
	for i, item := range m.items {
		lines = append(lines, menuRow(fmt.Sprint(i+1), item.label, i == m.cursor, w))
		if inlineDesc && i == m.cursor {
			lines = append(lines, styleOrPlain(dimStyle, "      "+item.desc))
		}
	}
	return lines, ""
}

func (m model) multiLines(w, rows int) ([]string, string) {
	visible := rows - 1 - len(m.items)
	if visible < 1 {
		visible = 1
	}
	start := 0
	if len(m.opts) > visible {
		if m.cursor < len(m.opts) {
			start = m.cursor - visible/2
		} else {
			start = len(m.opts) - visible
		}
		if start < 0 {
			start = 0
		}
		if start > len(m.opts)-visible {
			start = len(m.opts) - visible
		}
	}
	end := start + visible
	if end > len(m.opts) {
		end = len(m.opts)
	}
	digits := len(fmt.Sprint(len(m.opts) + len(m.items)))
	var lines []string
	for i := start; i < end; i++ {
		mark := "[ ]"
		if m.picked[i] {
			mark = "[x]"
		}
		lines = append(lines, checkRow(fmt.Sprintf("%*d", digits, i+1), mark, m.opts[i], i == m.cursor, w))
	}
	lines = append(lines, styleOrPlain(faintStyle, " "+strings.Repeat("-", max(w-2, 0))))
	for i, item := range m.items {
		idx := len(m.opts) + i
		lines = append(lines, menuRow(fmt.Sprintf("%*d", digits, idx+1), item.label, idx == m.cursor, w))
	}
	label := ""
	if start > 0 || end < len(m.opts) {
		label = fmt.Sprintf("%d-%d of %d", start+1, end, len(m.opts))
	}
	return lines, label
}

func menuRow(n, label string, selected bool, w int) string {
	text := n + "  " + label
	if selected {
		return styleOrPlain(selStyle, pad(clip(" > "+text, w), w))
	}
	return styleOrPlain(itemStyle, "   "+text)
}

func checkRow(n, mark, name string, selected bool, w int) string {
	text := n + "  " + mark + " " + name
	if selected {
		return styleOrPlain(selStyle, pad(clip(" > "+text, w), w))
	}
	if mark == "[x]" {
		return "   " + styleOrPlain(itemStyle, n+"  ") + styleOrPlain(okStyle, mark) + " " + styleOrPlain(itemStyle, name)
	}
	return styleOrPlain(itemStyle, "   "+text)
}

// detailLines builds the details panel body for inner width w.
func (m model) detailLines(w int) []string {
	if w < 8 {
		return nil
	}
	if m.screen == screenMultiSelect {
		return m.selectionLines(w)
	}
	var lines []string
	if m.cursor >= 0 && m.cursor < len(m.items) {
		item := m.items[m.cursor]
		lines = append(lines, styleOrPlain(titleStyle, item.label))
		for _, l := range wrap(item.desc, w) {
			lines = append(lines, styleOrPlain(dimStyle, l))
		}
		lines = append(lines, "")
	}
	return append(lines, contextLines(m.cfg, w)...)
}

func (m model) selectionLines(w int) []string {
	n := 0
	for i := range m.opts {
		if m.picked[i] {
			n++
		}
	}
	lines := []string{
		styleOrPlain(titleStyle, m.title()),
		fmt.Sprintf("%s %s", styleOrPlain(dimStyle, "picked"), styleOrPlain(itemStyle, fmt.Sprintf("%d of %d", n, len(m.opts)))),
		meter(n, len(m.opts), w),
		"",
	}
	if m.cursor < len(m.opts) {
		lines = append(lines, styleOrPlain(dimStyle, "cursor"), styleOrPlain(itemStyle, m.opts[m.cursor]))
	} else if i := m.cursor - len(m.opts); i >= 0 && i < len(m.items) {
		for _, l := range wrap(m.items[i].desc, w) {
			lines = append(lines, styleOrPlain(dimStyle, l))
		}
	}
	return lines
}

// contextLines lists the active paths so operators see where actions land.
func contextLines(cfg config.Config, w int) []string {
	rows := [][2]string{
		{"catalog", names.RedactRepoURL(cfg.RepoURL)},
		{"repo", cfg.RepoPath},
		{"deploy", cfg.DeployPath},
		{"keys", cfg.AuthkeysPath},
		{"log", cfg.LogPath},
	}
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		if r[1] == "" {
			continue
		}
		val := clip(redact.Text(r[1]), max(w-9, 1))
		lines = append(lines, styleOrPlain(dimStyle, fmt.Sprintf("%-8s ", r[0]))+styleOrPlain(itemStyle, val))
	}
	return lines
}

// meter draws a gradient bar filled to n of total across w cells.
func meter(n, total, w int) string {
	if w < 1 {
		return ""
	}
	filled := 0
	if total > 0 {
		filled = n * w / total
		if n > 0 && filled == 0 {
			filled = 1
		}
	}
	if !colorEnabled() {
		return strings.Repeat("#", filled) + strings.Repeat(".", w-filled)
	}
	grad := lipgloss.Blend1D(w, colAccent, colViolet)
	var b strings.Builder
	for i := 0; i < w; i++ {
		if i < filled {
			b.WriteString(lipgloss.NewStyle().Foreground(grad[i]).Render("■"))
		} else {
			b.WriteString(faintStyle.Render("■"))
		}
	}
	return b.String()
}

func (m model) outputPanel(title string, lines []string, w, rows int) []string {
	inner := rows - 2
	maxScroll := len(lines) - inner
	if maxScroll < 0 {
		maxScroll = 0
	}
	start := m.scroll
	if start > maxScroll {
		start = maxScroll
	}
	if start < 0 {
		start = 0
	}
	end := start + inner
	if end > len(lines) {
		end = len(lines)
	}
	label := ""
	if maxScroll > 0 {
		label = fmt.Sprintf("%d-%d of %d", start+1, end, len(lines))
	}
	return panel(title, label, lines[start:end], w, rows, colAmber)
}

// outputLines splits status text into display lines no wider than w and
// highlights status tokens.
func outputLines(text string, w int) []string {
	text = strings.TrimRight(strings.ReplaceAll(text, "\t", "  "), "\n")
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		l = highlight(l)
		if w > 0 && lipgloss.Width(l) > w {
			l = lipgloss.Wrap(l, w, " /=")
		}
		for _, part := range strings.Split(l, "\n") {
			lines = append(lines, " "+part)
		}
	}
	return lines
}

var tokenStyles = []struct {
	token string
	style *lipgloss.Style
}{
	{"[ok]", &okStyle},
	{"[healthy]", &okStyle},
	{"[running/no-healthcheck]", &okStyle},
	{"[warn]", &warnStyle},
	{"[starting]", &warnStyle},
	{"[unknown]", &warnStyle},
	{"[fail]", &errStyle},
	{"[unhealthy]", &errStyle},
	{"[exited]", &errStyle},
	{"[stopped]", &dimStyle},
	{"[info]", &dimStyle},
}

func highlight(line string) string {
	if !colorEnabled() {
		return line
	}
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(line, "==> "):
		return titleStyle.Render(line)
	case trimmed == "ok":
		return okStyle.Render(line)
	case strings.HasPrefix(trimmed, "error:"), strings.HasPrefix(trimmed, "Error"):
		return errStyle.Render(line)
	}
	for _, t := range tokenStyles {
		if strings.Contains(line, t.token) {
			line = strings.ReplaceAll(line, t.token, t.style.Render(t.token))
		}
	}
	return line
}

// panel draws a rounded box of width w and height h with the title set into
// the top border and an optional label in the bottom border.
func panel(title, label string, body []string, w, h int, c color.Color) []string {
	if w < 4 || h < 2 {
		return nil
	}
	edge := lipgloss.NewStyle().Foreground(c)
	head := lipgloss.NewStyle().Bold(true).Foreground(c)
	inner := w - 2

	topText := clip(" "+title+" ", max(inner-2, 0))
	top := styleOrPlain(edge, "╭─") + styleOrPlain(head, topText) +
		styleOrPlain(edge, strings.Repeat("─", max(inner-1-lipgloss.Width(topText), 0))+"╮")

	bottomFill := inner
	bottomText := ""
	if label != "" {
		bottomText = clip(" "+label+" ", max(inner-2, 0))
		bottomFill = inner - 1 - lipgloss.Width(bottomText)
	}
	bottom := styleOrPlain(edge, "╰"+strings.Repeat("─", max(bottomFill, 0)))
	if bottomText != "" {
		bottom += styleOrPlain(dimStyle, bottomText) + styleOrPlain(edge, "─")
	}
	bottom += styleOrPlain(edge, "╯")

	lines := make([]string, 0, h)
	lines = append(lines, top)
	side := styleOrPlain(edge, "│")
	for i := 0; i < h-2; i++ {
		row := ""
		if i < len(body) {
			row = body[i]
		}
		lines = append(lines, side+pad(clip(row, inner), inner)+side)
	}
	return append(lines, bottom)
}

// clip truncates s to w display cells, keeping ANSI styling intact.
func clip(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}

// pad right-fills s with spaces to w display cells.
func pad(s string, w int) string {
	if gap := w - lipgloss.Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

// wrap breaks s into lines no wider than w on word boundaries.
func wrap(s string, w int) []string {
	var lines []string
	cur := ""
	for _, word := range strings.Fields(s) {
		switch {
		case cur == "":
			cur = word
		case len(cur)+1+len(word) <= w:
			cur += " " + word
		default:
			lines = append(lines, cur)
			cur = word
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}
