package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func digitKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

func TestServicesMenuUsesApply(t *testing.T) {
	ids := map[string]bool{}
	for _, item := range servicesMenuItems() {
		ids[item.id] = true
	}
	if !ids["apply"] {
		t.Fatal("services menu must include apply")
	}
	if ids["update"] {
		t.Fatal("update is not an operator verb")
	}
	for _, item := range maintenanceMenuItems() {
		if item.id == "repair" {
			t.Fatal("repair is not an operator verb")
		}
	}
}

func TestMultiSelectNumericShortcutsMatchView(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	base := model{
		screen: screenMultiSelect,
		opts:   []string{"web"},
		items: []menuItem{
			{id: "run", label: "Run on selection", desc: "run"},
			{id: "cancel", label: "Cancel", desc: "cancel"},
		},
		picked: map[int]bool{},
	}
	view := base.render()
	for _, want := range []string{
		"1  [ ] web",
		"2  Run on selection",
		"3  Cancel",
		"digits 1-9 select/run",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}

	next, _ := base.Update(digitKey('1'))
	selected := next.(model)
	if !selected.picked[0] {
		t.Fatal("digit 1 should toggle the first service")
	}

	actionBase := base
	actionBase.picked = map[int]bool{}
	next, _ = actionBase.Update(digitKey('2'))
	action := next.(model)
	if action.cursor != 1 || action.status != "No services selected." {
		t.Fatalf("digit 2 should activate Run on selection: cursor=%d status=%q", action.cursor, action.status)
	}
}
