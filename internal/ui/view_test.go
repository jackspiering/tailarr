package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/jackspiering/tailarr/internal/config"
)

func manyServices(n int) []string {
	opts := make([]string, n)
	for i := range opts {
		opts[i] = fmt.Sprintf("svc-%02d", i)
	}
	return opts
}

func multiModel(opts []string) model {
	return model{
		screen:      screenMultiSelect,
		multi:       multiDeploy,
		multiParent: screenServices,
		opts:        opts,
		items: []menuItem{
			{id: "run", label: "Run on selection", desc: "run"},
			{id: "cancel", label: "Cancel", desc: "cancel"},
		},
		picked: map[int]bool{},
	}
}

func TestRenderFitsTerminal(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	status := strings.Repeat("  [ok] check: "+strings.Repeat("x", 150)+"\n", 40)
	models := []model{
		{cfg: config.Default(), screen: screenMain, items: mainMenuItems(), host: "nas"},
		{cfg: config.Default(), screen: screenServices, items: servicesMenuItems(), status: status},
		{cfg: config.Default(), screen: screenResult, items: []menuItem{{id: "back", label: "Back"}}, status: status},
		multiModel(manyServices(120)),
	}
	sizes := [][2]int{{120, 40}, {80, 24}, {60, 20}, {40, 14}}
	for i, m := range models {
		for _, sz := range sizes {
			m.width, m.height = sz[0], sz[1]
			lines := strings.Split(m.render(), "\n")
			if len(lines) != sz[1] {
				t.Errorf("model %d at %dx%d: %d lines, want %d", i, sz[0], sz[1], len(lines), sz[1])
			}
			for n, l := range lines {
				if w := lipgloss.Width(l); w > sz[0] {
					t.Errorf("model %d at %dx%d: line %d is %d wide", i, sz[0], sz[1], n, w)
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

func TestMultiSelectWindowFollowsCursor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := multiModel(manyServices(60))
	m.width, m.height = 100, 24
	m.cursor = 45
	view := m.render()
	if !strings.Contains(view, "> 46  [ ] svc-45") {
		t.Fatalf("cursor row not visible:\n%s", view)
	}
	if strings.Contains(view, "svc-00") {
		t.Fatalf("list should scroll past the first rows:\n%s", view)
	}
	if !strings.Contains(view, "of 60") || !strings.Contains(view, "Run on selection") {
		t.Fatalf("window label or actions missing:\n%s", view)
	}
}

func TestOutputScrollsWithPageKeys(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var b strings.Builder
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&b, "line-%02d\n", i)
	}
	m := model{screen: screenResult, items: []menuItem{{id: "back", label: "Back"}}, status: b.String(), width: 80, height: 24}
	if !strings.Contains(m.render(), "line-00") {
		t.Fatal("first page should show the first line")
	}
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	m = next.(model)
	view := m.render()
	if !strings.Contains(view, "line-79") || strings.Contains(view, "line-00") {
		t.Fatalf("end should show the last page:\n%s", view)
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	m = next.(model)
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	m = next.(model)
	if m.scroll == 0 || !strings.Contains(m.render(), "pgup/pgdn") {
		t.Fatalf("pgdown should scroll: scroll=%d", m.scroll)
	}
}

func TestMeterFillsProportionally(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, tc := range []struct {
		n, total int
		want     string
	}{
		{0, 10, ".........."},
		{5, 10, "#####....."},
		{1, 100, "#........."},
		{10, 10, "##########"},
	} {
		if got := meter(tc.n, tc.total, 10); got != tc.want {
			t.Errorf("meter(%d, %d) = %q, want %q", tc.n, tc.total, got, tc.want)
		}
	}
}
