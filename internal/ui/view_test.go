package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/jackspiering/tailarr/internal/deploy"
	"github.com/jackspiering/tailarr/internal/doctor"
)

func manyNames(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("svc-%02d", i)
	}
	return out
}

func sampleServices() []deploy.ServiceStatus {
	return []deploy.ServiceStatus{
		{Name: "legacy-app", Health: deploy.HealthStopped},
		{Name: "test-web", Managed: true, Health: deploy.HealthHealthy, Containers: []deploy.Container{
			{Name: "app-TEST_web", State: "running", Status: "Up 3 minutes (healthy)", Health: deploy.HealthHealthy},
			{Name: "tailscale-TEST_web", State: "running", Status: "Up 3 minutes (healthy)", Health: deploy.HealthHealthy},
		}},
		{Name: "test-worker", Managed: true, Health: deploy.HealthStarting, Containers: []deploy.Container{
			{Name: "app-TEST_worker", State: "running", Status: "Up 5 seconds (health: starting)", Health: deploy.HealthStarting},
		}},
	}
}

func TestRenderFitsTerminal(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	long := strings.Repeat("x", 150)
	base := withCatalog(withServices(testModel(t), sampleServices()...), manyNames(120)...)
	base.keyNames = []string{"home", "office"}
	base.checks = []doctor.Check{{Level: doctor.Warn, Name: "tun", Message: long}, {Level: doctor.OK, Name: "git", Message: "git found"}}

	var states []model
	for tb := tab(0); tb < tabCount; tb++ {
		m := base
		m.tab = tb
		states = append(states, m)
	}
	busy := base
	busy.busy, busy.opTitle = true, "deploy "+long
	for i := 0; i < 40; i++ {
		busy.out.add(fmt.Sprintf("  [ok] line %d %s", i, long))
	}
	states = append(states, busy)
	asking := busy
	asking.ask = &askState{kind: askLine, label: "Question " + long, buf: []rune(long), fresh: true}
	states = append(states, asking)
	menu := base
	menu.menu = menu.servicesMenu()
	states = append(states, menu)
	empty := testModel(t)
	empty.status = &deploy.Overview{}
	empty.catalogLoaded = true
	states = append(states, empty)

	sizes := [][2]int{{160, 48}, {120, 40}, {80, 24}, {60, 20}, {40, 14}}
	for i, m := range states {
		for _, sz := range sizes {
			m.width, m.height = sz[0], sz[1]
			lines := strings.Split(m.render(), "\n")
			if len(lines) != sz[1] {
				t.Errorf("state %d at %dx%d: %d lines, want %d", i, sz[0], sz[1], len(lines), sz[1])
			}
			for n, l := range lines {
				if w := lipgloss.Width(l); w > sz[0] {
					t.Errorf("state %d at %dx%d: line %d is %d wide", i, sz[0], sz[1], n, w)
				}
			}
		}
	}
}

func TestWindowSizeMsgSetsLayout(t *testing.T) {
	next, _ := model{}.Update(tea.WindowSizeMsg{Width: 132, Height: 50})
	m := next.(model)
	if w, h := m.size(); w != 132 || h != 50 {
		t.Fatalf("size = %dx%d, want 132x50", w, h)
	}
}

func TestServicesTableShowsHealth(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := withServices(testModel(t), sampleServices()...)
	m.lists[tabServices].cursor = 1
	view := m.render()
	for _, want := range []string{
		"Services · 3 deployed",
		"● 1 healthy", "◐ 1 starting", "○ 1 stopped",
		"▸ 2   ● test-web", "healthy", "2/2",
		"not managed by Tailarr",
		"app-TEST_web", "tailscale-TEST_web",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}

func TestEmptyServicesShowsNextStep(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := withServices(testModel(t))
	view := m.render()
	if !strings.Contains(view, "No services deployed yet.") || !strings.Contains(view, "open the catalog") {
		t.Fatalf("empty state missing:\n%s", view)
	}
}

func TestCatalogWindowFollowsCursor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := withCatalog(testModel(t), manyNames(60)...)
	m.tab = tabCatalog
	m.width, m.height = 100, 24
	m.lists[tabCatalog].cursor = 45
	view := m.render()
	if !strings.Contains(view, "▸ 46   svc-45") {
		t.Fatalf("cursor row not visible:\n%s", view)
	}
	if strings.Contains(view, "svc-00") {
		t.Fatalf("list should scroll past the first rows:\n%s", view)
	}
	if !strings.Contains(view, "of 60") {
		t.Fatalf("window label missing:\n%s", view)
	}
}

func TestCatalogDetailShowsTemplateFacts(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := withCatalog(testModel(t), "jellyfin")
	m.catalog[0] = catalogItem{Name: "jellyfin", Image: "jellyfin/jellyfin", Port: "8096",
		Prompts: []string{"TS_AUTHKEY"}, Summary: "Jellyfin is a media server."}
	m.tab = tabCatalog
	view := m.render()
	for _, want := range []string{"Jellyfin is a media server.", "jellyfin/jellyfin", "8096", "TS_AUTHKEY", "not deployed"} {
		if !strings.Contains(view, want) {
			t.Errorf("detail missing %q:\n%s", want, view)
		}
	}
}

func TestOutputFollowsNewLinesAndScrolls(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := testModel(t)
	m.width, m.height = 80, 24
	for i := 0; i < 80; i++ {
		m.out.add(fmt.Sprintf("line-%02d", i))
	}
	if view := m.render(); !strings.Contains(view, "line-79") || strings.Contains(view, "line-00") {
		t.Fatalf("output must follow the newest line:\n%s", view)
	}
	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyHome})
	view := m.render()
	if !strings.Contains(view, "line-00") || strings.Contains(view, "line-79") {
		t.Fatalf("home should show the first page:\n%s", view)
	}
	top := m.scrollBack
	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.scrollBack >= top || !strings.Contains(m.render(), "pgup/pgdn") {
		t.Fatalf("pgdown should scroll toward new lines: %d -> %d", top, m.scrollBack)
	}
	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnd}, escKey())
	if !m.out.empty() {
		t.Fatal("esc should clear finished output")
	}
}

func TestOutLogCoalescesProgress(t *testing.T) {
	var o outLog
	for _, l := range []string{
		"==> web",
		" Image busybox Pulling",
		" a1b2c3d4e5f6 Downloading 1MB",
		" a1b2c3d4e5f6 Downloading 5MB",
		" a1b2c3d4e5f6 Pull complete",
		" Image busybox Pulled",
		" Container app-web Started",
	} {
		o.add(l)
	}
	want := []string{"==> web", " Image busybox Pulled", " a1b2c3d4e5f6 Pull complete", " Container app-web Started"}
	if strings.Join(o.lines, "|") != strings.Join(want, "|") {
		t.Fatalf("lines = %q", o.lines)
	}
}

func TestFooterKeepsQuitHint(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := withServices(testModel(t), sampleServices()...)
	for _, w := range []int{60, 80, 120} {
		if f := m.footer(w); !strings.Contains(f, "q quit") || lipgloss.Width(f) > w {
			t.Errorf("footer at %d: %q", w, f)
		}
	}
}

func TestHeaderShowsTabsAndBusyOperation(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := testModel(t)
	m.tab = tabCatalog
	h := m.header(120)
	if !strings.Contains(h, "[Catalog]") || !strings.Contains(h, "Services") {
		t.Fatalf("header tabs: %q", h)
	}
	m.busy, m.opTitle = true, "deploy web"
	if h := m.header(120); !strings.Contains(h, "deploy web") {
		t.Fatalf("busy header: %q", h)
	}
}

func TestHealthLookLabels(t *testing.T) {
	for h, want := range map[deploy.Health]string{
		deploy.HealthHealthy:   "healthy",
		deploy.HealthRunning:   "running",
		deploy.HealthStarting:  "starting",
		deploy.HealthUnhealthy: "unhealthy",
		deploy.HealthExited:    "exited",
		deploy.HealthStopped:   "stopped",
		deploy.HealthUnknown:   "unknown",
	} {
		if _, got, _ := healthLook(h); got != want {
			t.Errorf("healthLook(%s) = %s, want %s", h, got, want)
		}
	}
}

func TestReadmeSummaryUsesServiceSection(t *testing.T) {
	dir := t.TempDir()
	readme := "# Jellyfin with Tailscale Sidecar Configuration\n\n" +
		"This Docker Compose configuration sets up Jellyfin.\n\n" +
		"## Jellyfin\n\n" +
		"[Jellyfin](https://jellyfin.org) is an **open-source** media server. It streams.\n\n" +
		"## Configuration Overview\n\nMore text.\n"
	path := filepath.Join(dir, "README.md")
	if err := os.WriteFile(path, []byte(readme), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readmeSummary(path); got != "Jellyfin is an open-source media server." {
		t.Fatalf("summary = %q", got)
	}
	if err := os.WriteFile(path, []byte("# Title\n\nOnly an intro here. More.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readmeSummary(path); got != "Only an intro here." {
		t.Fatalf("fallback summary = %q", got)
	}
}

func TestDisplayValueStripsComments(t *testing.T) {
	for in, want := range map[string]string{
		"Europe/Amsterdam # See: tz list": "Europe/Amsterdam",
		`"quoted value"`:                  "quoted value",
		"lscr.io/linuxserver/app":         "lscr.io/linuxserver/app",
	} {
		if got := displayValue(in); got != want {
			t.Errorf("displayValue(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanLineDropsEscapes(t *testing.T) {
	if got := cleanLine("\x1b[31mred\x1b[0m\x1b]0;title\x07\ttab\x00"); got != "red tab" {
		t.Fatalf("cleanLine = %q", got)
	}
}

func TestProgressStyleSeparatesStates(t *testing.T) {
	for _, st := range []string{"Pulling", "Waiting", "Starting", "Downloading 5MB", "Extracting 2MB"} {
		if progressStyle(st).GetForeground() != amberStyle.GetForeground() {
			t.Errorf("%s should read as in progress", st)
		}
	}
	for _, st := range []string{"Running", "Started", "Healthy", "Pulled", "Pull complete", "Removed"} {
		if progressStyle(st).GetForeground() != okStyle.GetForeground() {
			t.Errorf("%s should read as done", st)
		}
	}
	if progressStyle("Error while Stopping").GetForeground() != errStyle.GetForeground() {
		t.Error("an error state should read as failed")
	}
}
