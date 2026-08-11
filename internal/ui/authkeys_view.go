package ui

import (
	"github.com/jackspiering/tailarr/internal/authkeys"
)

func showAuthkeys(path string) string {
	s, err := authkeys.Load(path)
	if err != nil {
		return errStyle.Render(err.Error())
	}
	lines := s.RedactedList()
	if len(lines) == 0 {
		return dimStyle.Render("No stored auth keys at " + path)
	}
	var b string
	b += okStyle.Render("Stored auth keys:") + "\n"
	for _, line := range lines {
		b += "  " + line + "\n"
	}
	return b
}
