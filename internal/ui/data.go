package ui

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/jackspiering/tailarr/internal/authkeys"
	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/deploy"
	"github.com/jackspiering/tailarr/internal/doctor"
	"github.com/jackspiering/tailarr/internal/scaletail"
	"github.com/jackspiering/tailarr/internal/security/paths"
	"github.com/jackspiering/tailarr/internal/security/redact"
)

// statusMsg carries a fresh deployment and container snapshot.
type statusMsg struct {
	st  deploy.OverviewStats
	err string
}

// catalogItem is one catalog service with the facts the detail pane shows.
type catalogItem struct {
	Name    string
	Image   string
	Port    string
	Prompts []string
	Summary string
}

type catalogMsg struct {
	items []catalogItem
	err   string
}

type keysMsg struct {
	names []string
	err   string
}

type doctorMsg struct{ checks []doctor.Check }

func loadStatus(deployPath string) tea.Cmd {
	return func() tea.Msg {
		st, err := deploy.CollectOverview(deployPath)
		if err != nil {
			return statusMsg{err: redact.Text(err.Error())}
		}
		return statusMsg{st: st}
	}
}

func loadCatalog(repoPath string) tea.Cmd {
	return func() tea.Msg {
		svcs, err := scaletail.ListAvailable(repoPath)
		if err != nil {
			return catalogMsg{err: redact.Text(err.Error())}
		}
		items := make([]catalogItem, 0, len(svcs))
		for _, s := range svcs {
			items = append(items, describeService(s))
		}
		return catalogMsg{items: items}
	}
}

func loadKeys(path string) tea.Cmd {
	return func() tea.Msg {
		s, err := authkeys.Load(path)
		if err != nil {
			return keysMsg{err: redact.Text(err.Error())}
		}
		return keysMsg{names: s.Names()}
	}
}

func loadDoctor(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		return doctorMsg{checks: doctor.Run(cfg).Checks}
	}
}

// describeService reads the template .env and README of s. Values are
// display hints only; nothing here is written or executed.
func describeService(s scaletail.Service) catalogItem {
	it := catalogItem{Name: s.Name}
	env, keys, err := parseEnvKeys(s.EnvFile)
	if err == nil {
		it.Image = displayValue(env["IMAGE_URL"])
		it.Port = displayValue(env["SERVICEPORT"])
		it.Prompts = scaletail.PlaceholderKeys(env, keys)
	}
	it.Summary = readmeSummary(filepath.Join(s.Dir, "README.md"))
	return it
}

func parseEnvKeys(path string) (scaletail.EnvMap, []string, error) {
	f, err := paths.OpenFileNoFollow(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()
	return scaletail.ParseEnv(io.LimitReader(f, 1<<20))
}

// displayValue drops quotes and an inline " # comment" from a raw .env value.
func displayValue(v string) string {
	if i := strings.Index(v, " #"); i >= 0 && !strings.HasPrefix(v, "'") && !strings.HasPrefix(v, `"`) {
		v = v[:i]
	}
	return strings.Trim(strings.TrimSpace(v), `"'`)
}

var (
	mdLinkRE  = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	mdEmphRE  = regexp.MustCompile("[*_`]{1,3}")
	sentEndRE = regexp.MustCompile(`[.!?](\s|$)`)
)

// readmeSummary returns the first sentence of the first paragraph under the
// second heading of a ScaleTail README ("## <Service>"), which describes the
// app itself. It falls back to the first paragraph after the title.
func readmeSummary(path string) string {
	f, err := paths.OpenFileNoFollow(path, os.O_RDONLY, 0)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	type para struct {
		section int
		text    string
	}
	var paras []para
	var cur []string
	section := 0
	flush := func() {
		if len(cur) > 0 {
			paras = append(paras, para{section, strings.Join(cur, " ")})
			cur = nil
		}
	}
	sc := bufio.NewScanner(io.LimitReader(f, 64<<10))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "#"):
			flush()
			section++
		case isProse(line):
			cur = append(cur, line)
		default:
			flush()
		}
	}
	flush()
	pick := func(sec int) string {
		for _, p := range paras {
			if p.section == sec {
				return p.text
			}
		}
		return ""
	}
	text := pick(2)
	if text == "" {
		text = pick(1)
	}
	text = mdEmphRE.ReplaceAllString(mdLinkRE.ReplaceAllString(text, "$1"), "")
	if loc := sentEndRE.FindStringIndex(text); loc != nil {
		text = text[:loc[0]+1]
	}
	return strings.TrimSpace(text)
}

// isProse reports whether a README line is paragraph text rather than a
// list, table, quote, code fence, image, or HTML.
func isProse(line string) bool {
	if line == "" {
		return false
	}
	for _, p := range []string{"<", "- ", "* ", "|", ">", "```", "![", "[!"} {
		if strings.HasPrefix(line, p) {
			return false
		}
	}
	return true
}
