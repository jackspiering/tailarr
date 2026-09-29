package ui

import (
	"fmt"
	"image/color"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/deploy"
	"github.com/jackspiering/tailarr/internal/doctor"
	"github.com/jackspiering/tailarr/internal/security/names"
	"github.com/jackspiering/tailarr/internal/security/redact"
	"github.com/jackspiering/tailarr/internal/version"
)

// Default terminal size used before the first WindowSizeMsg arrives.
const (
	defaultWidth  = 80
	defaultHeight = 24
	// sideBySideMin is the narrowest width that still fits the detail pane
	// next to the list.
	sideBySideMin = 92
)

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

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

// layout sizes the output and prompt panels; the tab body gets the rest.
func (m model) layout() (bodyH, outH int, promptLines []string) {
	w, h := m.size()
	avail := h - 2
	if m.ask != nil {
		promptLines = m.promptPanel(w)
	}
	promptH := len(promptLines)
	if avail-promptH < 3 {
		promptLines = nil
		promptH = 0
	}
	if m.busy || !m.out.empty() {
		want := max(len(m.out.lines)+2, 4)
		limit := avail * 45 / 100
		if m.busy || m.ask != nil {
			// Keep the panel steady while output streams in.
			limit = avail * 60 / 100
			want = max(want, avail*40/100)
		}
		outH = min(want, limit, avail-promptH-5)
		if outH < 3 {
			outH = 0
		}
	}
	return avail - promptH - outH, outH, promptLines
}

// pageSize is the scroll step for the output panel.
func (m model) pageSize() int {
	_, outH, _ := m.layout()
	return max((outH-2)/2, 1)
}

// maxScroll bounds scrollBack so the oldest page stays full.
func (m model) maxScroll() int {
	_, outH, _ := m.layout()
	if outH == 0 {
		return 0
	}
	return max(len(m.out.lines)-(outH-2), 0)
}

func (m model) render() string {
	w, h := m.size()
	bodyH, outH, promptLines := m.layout()
	out := []string{m.header(w)}
	out = append(out, m.body(w, bodyH)...)
	if outH > 0 {
		out = append(out, m.outputPanel(w, outH)...)
	}
	out = append(out, promptLines...)
	for len(out) < h-1 {
		out = append(out, "")
	}
	out = out[:h-1]
	out = append(out, m.footer(w))
	return strings.Join(out, "\n")
}

func (m model) spinner() string {
	return spinFrames[m.spin%len(spinFrames)]
}

func (m model) header(w int) string {
	ver := version.Version
	if ver != "" && ver[0] >= '0' && ver[0] <= '9' {
		ver = "v" + ver
	}
	brand := styleOrPlain(badgeStyle, " tailarr ")
	verText := styleOrPlain(barStyle, " "+ver+" ")
	var tabs strings.Builder
	for i, name := range tabNames {
		label := " " + name + " "
		if tab(i) == m.tab {
			if !colorEnabled() {
				label = "[" + name + "]"
			}
			tabs.WriteString(styleOrPlain(tabOnStyle, label))
		} else {
			tabs.WriteString(styleOrPlain(tabStyle, label))
		}
	}
	right := ""
	switch {
	case m.busy:
		right = styleOrPlain(busyStyle, " "+m.spinner()+" "+m.opTitle+" · "+elapsed(time.Since(m.opStart))+" ")
	case m.host != "":
		right = styleOrPlain(barStyle, " "+m.host+" ")
	}
	sep := styleOrPlain(barStyle, "  ")
	parts := []string{brand + verText + sep + tabs.String(), brand + sep + tabs.String(),
		brand + styleOrPlain(tabOnStyle, " "+tabNames[m.tab]+" ")}
	for _, left := range parts {
		gap := w - lipgloss.Width(left) - lipgloss.Width(right)
		if gap >= 1 {
			return left + styleOrPlain(barStyle, strings.Repeat(" ", gap)) + right
		}
		if gap = w - lipgloss.Width(left); gap >= 0 && left == parts[len(parts)-1] {
			return left + styleOrPlain(barStyle, strings.Repeat(" ", gap))
		}
	}
	return clip(parts[len(parts)-1], w)
}

func elapsed(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

type hint [2]string

func (m model) hints() []hint {
	switch {
	case m.ask != nil && m.ask.kind == askConfirm:
		return []hint{{"y", "yes"}, {"n", "no"}, {"enter", "default"}, {"esc", "cancel"}, {"ctrl+c", "abort"}}
	case m.ask != nil:
		return []hint{{"enter", "submit"}, {"ctrl+u", "clear"}, {"esc", "cancel"}, {"ctrl+c", "abort"}}
	case m.menu != nil:
		return []hint{{"↑↓", "move"}, {"enter", "run"}, {"esc", "close"}}
	case m.filtering:
		return []hint{{"type", "filter"}, {"↑↓", "move"}, {"enter", "done"}, {"esc", "clear"}}
	}
	var hs []hint
	switch m.tab {
	case tabServices:
		hs = []hint{{"↑↓", "move"}, {"space", "pick"}, {"enter", "actions"}, {"r", "restart"}, {"s", "stop"},
			{"A", "apply"}, {"X", "remove"}, {"d", "deploy new"}, {"/", "filter"}}
	case tabCatalog:
		hs = []hint{{"↑↓", "move"}, {"space", "pick"}, {"enter", "deploy"}, {"/", "filter"}, {"a", "all"},
			{"n", "none"}, {"r", "refresh catalog"}}
	case tabKeys:
		hs = []hint{{"↑↓", "move"}, {"a", "add"}, {"e", "rename"}, {"p", "replace"}, {"x", "remove"}}
	case tabSystem:
		hs = []hint{{"e", "edit config"}, {"d", "run doctor"}, {"U", "upgrade"}}
	}
	if m.maxScroll() > 0 {
		hs = append([]hint{{"pgup/pgdn", "scroll"}}, hs...)
	}
	if m.busy {
		hs = append([]hint{{"ctrl+c", "cancel"}}, hs...)
	} else if !m.out.empty() {
		hs = append(hs, hint{"esc", "clear output"})
	}
	return append(hs, hint{"tab", "next tab"}, hint{"q", "quit"})
}

func (m model) footer(w int) string {
	line, used := "", 0
	if m.note != "" {
		n := clip(" "+m.note+" ", w)
		line, used = styleOrPlain(noteStyle, n), lipgloss.Width(n)
	}
	cellW := func(h hint) int { return lipgloss.Width(h[0]) + 1 + lipgloss.Width(h[1]) + 2 }
	render := func(h hint) string {
		return " " + styleOrPlain(keyStyle, h[0]) + " " + styleOrPlain(dimStyle, h[1]) + " "
	}
	// The last two hints (next tab, quit) always show when there is room;
	// others that do not fit are dropped instead of cut in half.
	hs := m.hints()
	keep := min(2, len(hs))
	reserve := 0
	for _, h := range hs[len(hs)-keep:] {
		reserve += cellW(h)
	}
	for _, h := range hs[:len(hs)-keep] {
		if used+cellW(h)+reserve > w {
			continue
		}
		line += render(h)
		used += cellW(h)
	}
	for _, h := range hs[len(hs)-keep:] {
		if used+cellW(h) > w {
			break
		}
		line += render(h)
		used += cellW(h)
	}
	return clip(line, w)
}

// body renders the current tab in exactly h lines of width w.
func (m model) body(w, h int) []string {
	if h <= 0 {
		return nil
	}
	side := w >= sideBySideMin
	listW, detailW := w, 0
	if side {
		detailW = min(max(w*2/5, 36), 52)
		listW = w - detailW
	}
	var left, right []string
	switch m.tab {
	case tabServices:
		left = m.servicesPanel(listW, h)
	case tabCatalog:
		left = m.catalogPanel(listW, h)
	case tabKeys:
		left = m.keysPanel(listW, h)
	case tabSystem:
		if side {
			half := w / 2
			left = panel("Configuration", "", m.configLines(half-4), half, h, colAccent)
			right = panel("Doctor", m.doctorLabel(), m.doctorLines(w-half-4), w-half, h, colViolet)
			return joinCols(left, right)
		}
		lines := append(m.configLines(w-4), "")
		lines = append(lines, styleOrPlain(violetStyle, "Doctor"))
		return panel("System", "", append(lines, m.doctorLines(w-4)...), w, h, colAccent)
	}
	if m.menu != nil && !side {
		menu := m.menuPanel(w, min(len(m.menu.items)+4, h))
		return append(left[:h-len(menu)], menu...)
	}
	if !side {
		return left
	}
	if m.menu != nil {
		right = m.menuPanel(detailW, h)
	} else {
		right = panel(m.detailTitle(), "", pad1(m.detailLines(detailW-4)), detailW, h, colViolet)
	}
	return joinCols(left, right)
}

func joinCols(left, right []string) []string {
	out := make([]string, len(left))
	for i := range left {
		out[i] = left[i]
		if i < len(right) {
			out[i] += right[i]
		}
	}
	return out
}

func pad1(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = " " + l
	}
	return out
}

// window returns the first visible row so cursor stays inside n rows.
func window(cursor, total, n int) int {
	if total <= n || n <= 0 {
		return 0
	}
	start := cursor - n/2
	return max(min(start, total-n), 0)
}

// listPanel draws a list with a summary line and column header above the
// rows. row renders item i for inner width iw.
func (m model) listPanel(title string, w, h int, summary, header string, total int, row func(i, iw int) string) []string {
	iw := w - 2
	inner := h - 2
	var lines []string
	if summary != "" {
		lines = append(lines, summary)
	}
	if header != "" && total > 0 {
		lines = append(lines, header)
	}
	visible := max(inner-len(lines), 1)
	l := m.lists[m.tab]
	start := window(l.cursor, total, visible)
	end := min(start+visible, total)
	for i := start; i < end; i++ {
		lines = append(lines, row(i, iw))
	}
	var label []string
	if l.filter != "" || m.filtering {
		f := "/ " + l.filter
		if m.filtering {
			f += "▏"
		}
		label = append(label, f)
	}
	if start > 0 || end < total {
		label = append(label, fmt.Sprintf("%d-%d of %d", start+1, end, total))
	}
	return panel(title, strings.Join(label, " · "), lines, w, h, colAccent)
}

// rowPrefix renders the cursor marker, row number, and pick mark.
func (m model) rowPrefix(i int, name string, sel bool, digits int) string {
	mark := "  "
	if sel {
		mark = "▸ "
	}
	pick := "  "
	if m.lists[m.tab].picked[name] {
		pick = "✓ "
	}
	return cell(cursorStyle, mark, sel) + cell(dimStyle, fmt.Sprintf("%*d ", digits, i+1), sel) + cell(okStyle, pick, sel)
}

// finishRow pads a rendered row to iw, keeping the selection background.
func finishRow(row string, iw int, sel bool) string {
	row = clip(row, iw)
	if gap := iw - lipgloss.Width(row); gap > 0 {
		row += cell(plainStyle, strings.Repeat(" ", gap), sel)
	}
	return row
}

func healthLook(h deploy.Health) (glyph, label string, s lipgloss.Style) {
	switch h {
	case deploy.HealthHealthy:
		return "●", "healthy", okStyle
	case deploy.HealthRunning:
		return "●", "running", okStyle
	case deploy.HealthStarting:
		return "◐", "starting", amberStyle
	case deploy.HealthUnhealthy:
		return "●", "unhealthy", errStyle
	case deploy.HealthExited:
		return "●", "exited", errStyle
	case deploy.HealthStopped:
		return "○", "stopped", dimStyle
	}
	return "?", "unknown", dimStyle
}

func (m model) serviceByName(name string) (deploy.ServiceStatus, bool) {
	if m.status != nil {
		for _, s := range m.status.Services {
			if s.Name == name {
				return s, true
			}
		}
	}
	return deploy.ServiceStatus{}, false
}

func (m model) servicesSummary() string {
	if m.status == nil {
		if m.statusErr != "" {
			return styleOrPlain(errStyle, "✖ "+m.statusErr)
		}
		return styleOrPlain(dimStyle, m.spinner()+" reading deployments...")
	}
	var healthy, starting, down, stopped, unknown int
	for _, s := range m.status.Services {
		switch s.Health {
		case deploy.HealthHealthy, deploy.HealthRunning:
			healthy++
		case deploy.HealthStarting:
			starting++
		case deploy.HealthUnhealthy, deploy.HealthExited:
			down++
		case deploy.HealthStopped:
			stopped++
		default:
			unknown++
		}
	}
	parts := []string{
		styleOrPlain(okStyle, "●") + fmt.Sprintf(" %d healthy", healthy),
		styleOrPlain(amberStyle, "◐") + fmt.Sprintf(" %d starting", starting),
		styleOrPlain(errStyle, "●") + fmt.Sprintf(" %d down", down),
		styleOrPlain(dimStyle, "○") + fmt.Sprintf(" %d stopped", stopped),
	}
	if unknown > 0 {
		parts = append(parts, styleOrPlain(dimStyle, "?")+fmt.Sprintf(" %d unknown", unknown))
	}
	s := " " + strings.Join(parts, "   ")
	if m.status.DockerErr != "" {
		s = " " + styleOrPlain(warnStyle, "▲ "+redact.Text(m.status.DockerErr))
	}
	return s
}

func (m model) servicesPanel(w, h int) []string {
	rows := m.rows()
	title := "Services"
	if m.status != nil {
		title = fmt.Sprintf("Services · %d deployed", len(m.status.Services))
	}
	if m.status != nil && len(m.status.Services) == 0 {
		lines := []string{"", " " + styleOrPlain(boldStyle, "No services deployed yet."), "",
			" " + styleOrPlain(dimStyle, "Press ") + styleOrPlain(keyStyle, "d") + styleOrPlain(dimStyle, " to open the catalog, pick a service"),
			" " + styleOrPlain(dimStyle, "with ") + styleOrPlain(keyStyle, "space") + styleOrPlain(dimStyle, ", and press ") +
				styleOrPlain(keyStyle, "enter") + styleOrPlain(dimStyle, " to deploy it."),
			"", " " + styleOrPlain(dimStyle, "Deploy root: ") + clipLeft(m.cfg.DeployPath, max(w-18, 8))}
		return panel(title, "", lines, w, h, colAccent)
	}
	iw := w - 2
	nameW := 10
	for _, n := range rows {
		nameW = max(nameW, lipgloss.Width(n))
	}
	digits := len(fmt.Sprint(len(rows)))
	fixed := 2 + digits + 1 + 2 + 2
	nameW = min(nameW, max(iw-fixed-22, 8), 32)
	showUp := iw >= fixed+nameW+11+8
	showDetail := iw >= fixed+nameW+11+8+12
	header := strings.Repeat(" ", fixed) + fit("NAME", nameW) + "  " + fit("HEALTH", 9)
	if showUp {
		header += "  " + fit("UP", 6)
	}
	if showDetail {
		header += "  STATUS"
	}
	cur := m.lists[tabServices].cursor
	return m.listPanel(title, w, h, m.servicesSummary(), styleOrPlain(headStyle, clip(header, iw)), len(rows), func(i, iw int) string {
		name := rows[i]
		sel := i == cur
		svc, _ := m.serviceByName(name)
		glyph, label, hs := healthLook(svc.Health)
		row := m.rowPrefix(i, name, sel, digits) + cell(hs, glyph+" ", sel)
		nameStyle := plainStyle
		if !svc.Managed {
			nameStyle = dimStyle
		}
		row += cell(nameStyle, fit(name, nameW), sel) + cell(plainStyle, "  ", sel) + cell(hs, fit(label, 9), sel)
		if showUp {
			row += cell(plainStyle, "  ", sel) + cell(dimStyle, fit(upCount(svc), 6), sel)
		}
		if showDetail {
			detail := statusDetail(svc)
			if !svc.Managed {
				detail = "not managed by Tailarr"
			}
			row += cell(plainStyle, "  ", sel) + cell(dimStyle, detail, sel)
		}
		return finishRow(row, iw, sel)
	})
}

func upCount(s deploy.ServiceStatus) string {
	if len(s.Containers) == 0 {
		return "-"
	}
	up := 0
	for _, c := range s.Containers {
		if c.State == "running" {
			up++
		}
	}
	return fmt.Sprintf("%d/%d", up, len(s.Containers))
}

// statusDetail picks the container status worth showing: a failing one
// first, then the app container.
func statusDetail(s deploy.ServiceStatus) string {
	if len(s.Containers) == 0 {
		return "no containers"
	}
	pickC := s.Containers[0]
	for _, c := range s.Containers {
		if c.Health == s.Health {
			pickC = c
			break
		}
	}
	return pickC.Status
}

func (m model) detailTitle() string {
	switch m.tab {
	case tabCatalog:
		return "Template"
	case tabKeys:
		return "Key"
	}
	return "Details"
}

func (m model) detailLines(w int) []string {
	if w < 8 {
		return nil
	}
	switch m.tab {
	case tabServices:
		return m.serviceDetail(w)
	case tabCatalog:
		return m.catalogDetail(w)
	case tabKeys:
		return m.keyDetail(w)
	}
	return nil
}

func kv(k, v string, w int, vs lipgloss.Style) string {
	return styleOrPlain(headStyle, fit(k, 8)) + " " + styleOrPlain(vs, clipLeft(v, max(w-9, 4)))
}

func (m model) serviceDetail(w int) []string {
	name := m.current()
	svc, ok := m.serviceByName(name)
	if !ok {
		return []string{styleOrPlain(dimStyle, "Pick a service to see its containers.")}
	}
	glyph, label, hs := healthLook(svc.Health)
	managed := "managed by Tailarr"
	if !svc.Managed {
		managed = "not managed (read only)"
	}
	lines := []string{
		styleOrPlain(titleStyle, clip(name, w)),
		styleOrPlain(hs, glyph+" "+label) + styleOrPlain(dimStyle, " · "+managed),
		"",
		styleOrPlain(headStyle, "CONTAINERS"),
	}
	if len(svc.Containers) == 0 {
		lines = append(lines, styleOrPlain(dimStyle, "none (stopped or never started)"))
	}
	for _, c := range svc.Containers {
		g, _, cs := healthLook(c.Health)
		lines = append(lines, styleOrPlain(cs, g)+" "+clip(c.Name, w-2), "  "+styleOrPlain(dimStyle, clip(c.Status, w-2)))
	}
	lines = append(lines, "",
		kv("path", filepath.Join(m.cfg.DeployPath, name), w, dimStyle),
		kv("project", deploy.ProjectName(m.cfg.DeployPath, name), w, dimStyle))
	if svc.Managed {
		lines = append(lines, "", styleOrPlain(dimStyle, "enter for actions"))
	}
	return lines
}

func (m model) catalogItem(name string) (catalogItem, bool) {
	for _, it := range m.catalog {
		if it.Name == name {
			return it, true
		}
	}
	return catalogItem{}, false
}

func (m model) catalogPanel(w, h int) []string {
	rows := m.rows()
	if !m.catalogLoaded || m.catalogErr != "" || len(m.catalog) == 0 {
		lines := []string{""}
		switch {
		case !m.catalogLoaded:
			lines = append(lines, " "+styleOrPlain(dimStyle, m.spinner()+" reading catalog..."))
		default:
			if m.catalogErr != "" {
				for _, l := range wrap(m.catalogErr, w-6) {
					lines = append(lines, " "+styleOrPlain(errStyle, l))
				}
			} else {
				lines = append(lines, " "+styleOrPlain(boldStyle, "The catalog has no services."))
			}
			lines = append(lines, "", " "+styleOrPlain(dimStyle, "Press ")+styleOrPlain(keyStyle, "r")+
				styleOrPlain(dimStyle, " to clone or pull ScaleTail into"), " "+clipLeft(m.cfg.RepoPath, w-4))
		}
		return panel("Catalog", "", lines, w, h, colAccent)
	}
	deployed := 0
	for _, it := range m.catalog {
		if m.isDeployed(it.Name) {
			deployed++
		}
	}
	title := fmt.Sprintf("Catalog · %d services", len(m.catalog))
	summary := " " + styleOrPlain(dimStyle, fmt.Sprintf("%d deployed · %d picked · ScaleTail %s", deployed,
		len(m.lists[tabCatalog].picked), clipLeft(names.RedactRepoURL(m.cfg.RepoURL), max(w-40, 10))))
	if len(rows) == 0 {
		summary = " " + styleOrPlain(warnStyle, fmt.Sprintf("No services match %q.", m.lists[tabCatalog].filter))
	}
	iw := w - 2
	digits := len(fmt.Sprint(len(rows)))
	fixed := 2 + digits + 1 + 2
	nameW := 12
	for _, n := range rows {
		nameW = max(nameW, lipgloss.Width(n))
	}
	nameW = min(nameW, max(iw-fixed-14, 8), 26)
	header := strings.Repeat(" ", fixed) + fit("NAME", nameW) + "  IMAGE"
	cur := m.lists[tabCatalog].cursor
	return m.listPanel(title, w, h, summary, styleOrPlain(headStyle, clip(header, iw)), len(rows), func(i, iw int) string {
		name := rows[i]
		sel := i == cur
		it, _ := m.catalogItem(name)
		row := m.rowPrefix(i, name, sel, digits) + cell(plainStyle, fit(name, nameW), sel) + cell(plainStyle, "  ", sel)
		tag := ""
		if m.isDeployed(name) {
			tag = "✓ deployed"
		}
		imgW := iw - lipgloss.Width(row) - lipgloss.Width(tag) - 1
		if imgW > 3 {
			row += cell(dimStyle, fit(it.Image, imgW), sel)
		}
		if tag != "" {
			row += cell(plainStyle, " ", sel) + cell(okStyle, tag, sel)
		}
		return finishRow(row, iw, sel)
	})
}

func (m model) catalogDetail(w int) []string {
	it, ok := m.catalogItem(m.current())
	if !ok {
		return nil
	}
	lines := []string{styleOrPlain(titleStyle, clip(it.Name, w)), ""}
	if it.Summary != "" {
		lines = append(lines, wrap(it.Summary, w)...)
		lines = append(lines, "")
	}
	state := styleOrPlain(dimStyle, "not deployed")
	if m.isDeployed(it.Name) {
		state = styleOrPlain(okStyle, "✓ deployed")
	}
	asks := "nothing"
	if len(it.Prompts) > 0 {
		asks = strings.Join(it.Prompts, ", ")
	}
	lines = append(lines,
		kv("image", orDash(it.Image), w, plainStyle),
		kv("port", orDash(it.Port), w, plainStyle),
		kv("asks", "", w, plainStyle)+styleOrPlain(amberStyle, clip(asks, max(w-9, 4))),
		styleOrPlain(headStyle, fit("state", 8))+" "+state,
		"",
		styleOrPlain(dimStyle, "space picks · enter deploys"),
	)
	return lines
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (m model) keysPanel(w, h int) []string {
	rows := m.rows()
	title := fmt.Sprintf("Auth keys · %d stored", len(m.keyNames))
	if m.keysErr != "" || len(rows) == 0 {
		lines := []string{""}
		if m.keysErr != "" {
			for _, l := range wrap(m.keysErr, w-6) {
				lines = append(lines, " "+styleOrPlain(errStyle, l))
			}
		} else {
			lines = append(lines, " "+styleOrPlain(boldStyle, "No stored auth keys."), "",
				" "+styleOrPlain(dimStyle, "Press ")+styleOrPlain(keyStyle, "a")+styleOrPlain(dimStyle, " to store a named TS_AUTHKEY."),
				" "+styleOrPlain(dimStyle, "Deploy offers stored keys by name."))
		}
		return panel(title, "", lines, w, h, colAccent)
	}
	digits := len(fmt.Sprint(len(rows)))
	cur := m.lists[tabKeys].cursor
	return m.listPanel(title, w, h, "", "", len(rows), func(i, iw int) string {
		sel := i == cur
		row := m.rowPrefix(i, rows[i], sel, digits) + cell(plainStyle, fit(rows[i], 24), sel) + cell(dimStyle, "  "+redact.Redacted, sel)
		return finishRow(row, iw, sel)
	})
}

func (m model) keyDetail(w int) []string {
	lines := []string{}
	if cur := m.current(); cur != "" {
		lines = append(lines, styleOrPlain(titleStyle, clip(cur, w)), styleOrPlain(dimStyle, "value "+redact.Redacted), "")
	}
	lines = append(lines, wrap("Deploy fills TS_AUTHKEY from a stored key when you type its name. Values are never shown.", w)...)
	return append(lines, "", kv("store", m.cfg.AuthkeysPath, w, dimStyle), kv("mode", "600", w, dimStyle))
}

func (m model) configLines(w int) []string {
	rows := [][2]string{
		{"config", m.cfg.ConfigPath},
		{"catalog", names.RedactRepoURL(m.cfg.RepoURL)},
		{"repo", m.cfg.RepoPath},
		{"deploy", m.cfg.DeployPath},
		{"keys", m.cfg.AuthkeysPath},
		{"log", m.cfg.LogPath},
		{"log max", formatSize(m.cfg.LogMaxBytes)},
		{"version", version.Version},
	}
	if m.host != "" {
		rows = append(rows, [2]string{"host", m.host})
	}
	lines := []string{""}
	for _, r := range rows {
		lines = append(lines, " "+kv(r[0], redact.Text(r[1]), w, plainStyle))
	}
	if env := config.EnvOverrides(); len(env) > 0 {
		lines = append(lines, "", " "+styleOrPlain(amberStyle, "environment overrides:"))
		for _, k := range env {
			lines = append(lines, "  "+styleOrPlain(dimStyle, k))
		}
	}
	return lines
}

func formatSize(n int64) string {
	if n%(1<<20) == 0 {
		return fmt.Sprintf("%d MiB", n>>20)
	}
	return fmt.Sprintf("%d bytes", n)
}

func (m model) doctorLabel() string {
	if m.doctorBusy {
		return m.spinner() + " checking"
	}
	if m.checks == nil {
		return ""
	}
	fails, warns := 0, 0
	for _, c := range m.checks {
		switch c.Level {
		case doctor.Fail:
			fails++
		case doctor.Warn:
			warns++
		}
	}
	return fmt.Sprintf("%d fail · %d warn", fails, warns)
}

func (m model) doctorLines(w int) []string {
	if m.checks == nil {
		return []string{"", " " + styleOrPlain(dimStyle, m.spinner()+" running checks...")}
	}
	lines := []string{""}
	for _, c := range m.checks {
		if c.Name == "paths" {
			// The Configuration panel already lists every path.
			continue
		}
		g, s := "•", dimStyle
		switch c.Level {
		case doctor.OK:
			g, s = "✔", okStyle
		case doctor.Warn:
			g, s = "▲", warnStyle
		case doctor.Fail:
			g, s = "✖", errStyle
		}
		// A passing check fits on one line; a problem wraps so the full
		// message stays readable.
		msgW := max(w-20, 10)
		msg := []string{clipLeft(redact.Text(c.Message), msgW)}
		if c.Level == doctor.Warn || c.Level == doctor.Fail {
			msg = wrap(redact.Text(c.Message), msgW)
		}
		if len(msg) == 0 {
			msg = []string{""}
		}
		lines = append(lines, " "+styleOrPlain(s, g)+" "+styleOrPlain(boldStyle, fit(c.Name, 16))+" "+msg[0])
		for _, l := range msg[1:] {
			lines = append(lines, strings.Repeat(" ", 20)+l)
		}
	}
	return lines
}

func (m model) menuPanel(w, h int) []string {
	mn := m.menu
	lines := []string{""}
	for i, it := range mn.items {
		sel := i == mn.cursor
		mark := "  "
		if sel {
			mark = "▸ "
		}
		row := cell(cursorStyle, " "+mark, sel) + cell(keyStyle, fit(it.key, 2), sel) + cell(plainStyle, it.label, sel)
		lines = append(lines, finishRow(row, w-2, sel))
		if sel && h >= len(mn.items)*2+4 {
			for _, l := range wrap(it.desc, w-10) {
				lines = append(lines, "      "+styleOrPlain(dimStyle, l))
			}
		}
	}
	return panel("Actions · "+mn.title, "esc closes", lines, w, h, colAmber)
}

func (m model) outputPanel(w, h int) []string {
	inner := h - 2
	lines := m.out.lines
	end := max(len(lines)-m.scrollBack, 0)
	start := max(end-inner, 0)
	var body []string
	for _, l := range lines[start:end] {
		for _, part := range wrapStyled(highlight(l), w-3) {
			body = append(body, " "+part)
		}
	}
	if len(body) > inner {
		body = body[len(body)-inner:]
	}
	if len(lines) == 0 && m.busy {
		body = []string{" " + styleOrPlain(dimStyle, "working...")}
	}
	title := "Output"
	if m.out.title != "" {
		title += " · " + m.out.title
	}
	if m.busy {
		title = m.spinner() + " " + title
	}
	label := ""
	if start > 0 || end < len(lines) {
		label = fmt.Sprintf("%d-%d of %d", start+1, end, len(lines))
	}
	return panel(title, label, body, w, h, colAmber)
}

func (m model) promptPanel(w int) []string {
	a := m.ask
	iw := w - 4
	var lines []string
	for _, l := range wrap(a.label, iw) {
		lines = append(lines, " "+styleOrPlain(boldStyle, l))
	}
	label := "enter submits · esc cancels"
	switch a.kind {
	case askConfirm:
		yes, no := "y yes", "n no"
		if a.defaultYes {
			yes = "Y yes"
		} else {
			no = "N no"
		}
		lines = append(lines, " "+styleOrPlain(keyStyle, yes)+"   "+styleOrPlain(keyStyle, no))
		label = "enter picks the capital letter"
	case askSecret:
		dots := strings.Repeat("•", min(len(a.buf), max(iw-4, 1)))
		lines = append(lines, " "+styleOrPlain(cursorStyle, "› ")+dots+styleOrPlain(cursorStyle, "▏")+
			styleOrPlain(dimStyle, "  hidden"))
	default:
		val := string(a.buf)
		if lipgloss.Width(val) > iw-4 {
			r := []rune(val)
			val = "…" + string(r[len(r)-(iw-5):])
		}
		if a.fresh {
			val = cell(plainStyle, val, true)
			label = "typing replaces · backspace edits · enter keeps"
		}
		lines = append(lines, " "+styleOrPlain(cursorStyle, "› ")+val+styleOrPlain(cursorStyle, "▏"))
	}
	title := "Input"
	if m.opTitle != "" {
		title = "Input · " + m.opTitle
	}
	return panel(title, label, lines, w, len(lines)+2, colAccent)
}

// highlight colors result tokens and compose progress states.
func highlight(line string) string {
	if !colorEnabled() {
		return line
	}
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(line, "==> "):
		return titleStyle.Render(line)
	case trimmed == "ok", strings.HasPrefix(trimmed, "✔"):
		return okStyle.Render(line)
	case strings.HasPrefix(trimmed, "error:"), strings.HasPrefix(trimmed, "Error"), strings.HasPrefix(trimmed, "✖"):
		return errStyle.Render(line)
	case strings.HasPrefix(trimmed, "skipped"), trimmed == "Canceled.", strings.HasPrefix(trimmed, "Warning"),
		strings.HasPrefix(trimmed, "Note:"), strings.HasPrefix(trimmed, "ignoring "):
		return warnStyle.Render(line)
	}
	if progressKey(line) != "" {
		f := strings.Fields(line)
		n := 2
		if len(f[0]) == 12 {
			n = 1
		}
		state := strings.Join(f[n:], " ")
		if state == "" {
			return line
		}
		return dimStyle.Render(strings.Join(f[:n], " ")) + " " + progressStyle(state).Render(state)
	}
	return line
}

// progressVerbs are compose states that are still in progress.
var progressVerbs = []string{"pulling", "waiting", "creating", "starting", "stopping", "removing", "recreate",
	"downloading", "extracting", "verifying", "restarting", "killing", "building"}

func progressStyle(state string) lipgloss.Style {
	s := strings.ToLower(state)
	switch {
	case strings.Contains(s, "error"), strings.Contains(s, "unhealthy"), strings.Contains(s, "failed"):
		return errStyle
	case s == "pull complete", s == "download complete":
		return okStyle
	}
	for _, v := range progressVerbs {
		if strings.HasPrefix(s, v) {
			return amberStyle
		}
	}
	return okStyle
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
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}

// fit truncates plain text to w cells with an ellipsis, then pads it to w.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) > w {
		r := []rune(s)
		for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
			r = r[:len(r)-1]
		}
		s = string(r) + "…"
	}
	return pad(s, w)
}

// clipLeft keeps the end of a long plain path, where the useful part is.
func clipLeft(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	return "…" + string(r[len(r)-w+1:])
}

// pad right-fills s with spaces to w display cells.
func pad(s string, w int) string {
	if gap := w - lipgloss.Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

// wrap breaks s into lines no wider than w on word boundaries. A word longer
// than w is split.
func wrap(s string, w int) []string {
	if w <= 0 {
		return nil
	}
	var lines []string
	cur := ""
	for _, word := range strings.Fields(s) {
		for lipgloss.Width(word) > w {
			if cur != "" {
				lines = append(lines, cur)
				cur = ""
			}
			r := []rune(word)
			lines = append(lines, string(r[:w]))
			word = string(r[w:])
		}
		switch {
		case cur == "":
			cur = word
		case lipgloss.Width(cur)+1+lipgloss.Width(word) <= w:
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

// wrapStyled wraps a styled line to w cells.
func wrapStyled(s string, w int) []string {
	if w <= 0 || lipgloss.Width(s) <= w {
		return []string{s}
	}
	return strings.Split(lipgloss.Wrap(s, w, " /=-"), "\n")
}
