package ui

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/jackspiering/tailarr/internal/prompt"
	"github.com/jackspiering/tailarr/internal/security/redact"
)

// sender delivers messages to the running program from work goroutines.
// Run binds fn to the program's Send; tests bind it to a channel.
type sender struct {
	mu sync.Mutex
	fn func(tea.Msg)
}

func (s *sender) send(msg tea.Msg) {
	if s == nil {
		return
	}
	s.mu.Lock()
	fn := s.fn
	s.mu.Unlock()
	if fn != nil {
		fn(msg)
	}
}

func (s *sender) bind(fn func(tea.Msg)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fn = fn
}

type askKind int

const (
	askConfirm askKind = iota
	askLine
	askSecret
)

// askState is one open prompt. The work goroutine waits on reply; the model
// answers from key presses. buf holds the typed text, a secret included, and
// is cleared once answered. It is never rendered for a secret.
type askState struct {
	kind       askKind
	label      string
	def        string
	defaultYes bool
	buf        []rune
	// fresh marks a prefilled default that the first typed key replaces,
	// like selected text. Backspace, right, or end edit it instead.
	fresh bool
	reply chan askReply
}

type askReply struct {
	yes   bool
	value string
	err   error
}

// askMsg opens a prompt in the TUI.
type askMsg struct{ ask *askState }

// outMsg appends one line to the output panel.
type outMsg struct{ line string }

// answer sends r to the waiting goroutine once and wipes the input buffer.
func (a *askState) answer(r askReply) {
	for i := range a.buf {
		a.buf[i] = 0
	}
	a.buf = nil
	select {
	case a.reply <- r:
	default:
	}
}

// tuiUI implements prompt.UI inside the TUI. Each prompt opens in the prompt
// panel instead of handing the terminal back, and Printf lines go to the
// output panel. ctx is the operation context: canceling it (Ctrl+C) unblocks
// a waiting prompt.
type tuiUI struct {
	snd       *sender
	ctx       context.Context
	assumeYes bool
}

var _ prompt.UI = (*tuiUI)(nil)

func (u *tuiUI) ask(a *askState) (askReply, error) {
	if err := u.ctx.Err(); err != nil {
		return askReply{}, err
	}
	a.reply = make(chan askReply, 1)
	u.snd.send(askMsg{ask: a})
	select {
	case r := <-a.reply:
		return r, r.err
	case <-u.ctx.Done():
		return askReply{}, u.ctx.Err()
	}
}

// Confirm implements prompt.UI.
func (u *tuiUI) Confirm(question string, defaultYes bool) (bool, error) {
	if u.assumeYes && defaultYes {
		u.Printf("%s (auto-yes)\n", question)
		return true, nil
	}
	r, err := u.ask(&askState{kind: askConfirm, label: question, defaultYes: defaultYes})
	if err != nil {
		return false, err
	}
	return r.yes, nil
}

// Line implements prompt.UI. The default is prefilled and selected, and an
// empty answer returns it, as in prompt.Std.
func (u *tuiUI) Line(label, defaultVal string) (string, error) {
	r, err := u.ask(&askState{kind: askLine, label: label, def: defaultVal, buf: []rune(defaultVal), fresh: defaultVal != ""})
	if err != nil {
		return "", err
	}
	if v := strings.TrimSpace(r.value); v != "" {
		return v, nil
	}
	return defaultVal, nil
}

// Secret implements prompt.UI. The value is never echoed.
func (u *tuiUI) Secret(label string) (string, error) {
	r, err := u.ask(&askState{kind: askSecret, label: label})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(r.value), nil
}

// Printf implements prompt.UI. Lines are redacted before they reach the
// screen.
func (u *tuiUI) Printf(format string, args ...any) {
	text := strings.TrimRight(fmt.Sprintf(format, args...), "\n")
	for _, line := range strings.Split(text, "\n") {
		u.snd.send(outMsg{line: redact.Text(line)})
	}
}

// ansiRE matches CSI and OSC escape sequences in subprocess output.
var ansiRE = regexp.MustCompile(`\x1b(\[[0-9;?]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\))`)

// cleanLine strips escape sequences and control characters so subprocess
// output cannot move the cursor or restyle the TUI.
func cleanLine(s string) string {
	s = ansiRE.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case r < 0x20 || r == 0x7f:
			return -1
		}
		return r
	}, s)
}

// lineSink is an io.Writer that sends each complete line to the output panel.
// Callers wrap it in redact.Writer; carriage returns end a line too, so a
// progress bar that redraws in place becomes separate updates.
type lineSink struct {
	mu  sync.Mutex
	snd *sender
	buf []byte
}

func (l *lineSink) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, p...)
	for {
		i := strings.IndexAny(string(l.buf), "\r\n")
		if i < 0 {
			break
		}
		if line := cleanLine(string(l.buf[:i])); strings.TrimSpace(line) != "" {
			l.snd.send(outMsg{line: line})
		}
		l.buf = l.buf[i+1:]
	}
	if len(l.buf) > 8192 {
		l.snd.send(outMsg{line: cleanLine(string(l.buf))})
		l.buf = nil
	}
	return len(p), nil
}
