// Package prompt provides interactive confirmation and value prompts for CLI/TUI.
package prompt

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// UI is the interactive surface used by deploy and lifecycle operations.
type UI interface {
	// Confirm asks a yes/no question. defaultYes controls empty-answer behavior.
	// When AssumeYes is true, default-yes prompts return true without reading.
	// Default-no prompts still require an answer.
	Confirm(question string, defaultYes bool) (bool, error)
	// Line reads a single line (echo on). Empty input returns defaultVal.
	Line(label, defaultVal string) (string, error)
	// Secret reads a single line with echo disabled when stdin is a TTY. It fails
	// when stdin is not a TTY so secrets are never echoed back to the terminal.
	Secret(label string) (string, error)
	// Printf writes operator-facing messages.
	Printf(format string, args ...any)
}

// Std is a terminal-backed UI.
type Std struct {
	In        io.Reader
	Out       io.Writer
	Err       io.Writer
	AssumeYes bool

	br   *bufio.Reader
	brIn io.Reader
}

// NewStd builds a UI on os.Stdin/Stdout/Stderr.
func NewStd(assumeYes bool) *Std {
	return &Std{In: os.Stdin, Out: os.Stdout, Err: os.Stderr, AssumeYes: assumeYes}
}

// Confirm implements UI.
func (s *Std) Confirm(question string, defaultYes bool) (bool, error) {
	suffix := "[Y/n]"
	if !defaultYes {
		suffix = "[y/N]"
	}
	if s.AssumeYes && defaultYes {
		s.Printf("%s %s: (auto-yes)\n", question, suffix)
		return true, nil
	}
	if !s.isTerminal() {
		return false, fmt.Errorf("stdin is not a terminal: %s", question)
	}
	s.Printf("%s %s: ", question, suffix)
	line, err := s.readLine()
	if err != nil {
		return false, err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultYes, nil
	}
	switch strings.ToLower(line) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// Line implements UI.
func (s *Std) Line(label, defaultVal string) (string, error) {
	if defaultVal != "" {
		s.Printf("%s [%s]: ", label, defaultVal)
	} else {
		s.Printf("%s: ", label)
	}
	line, err := s.readLine()
	if err != nil {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultVal, nil
	}
	return line, nil
}

// Secret implements UI. It reads with echo disabled on a TTY; on a non-TTY
// stdin it fails rather than echoing the secret back to the terminal.
func (s *Std) Secret(label string) (string, error) {
	f, ok := s.stdinFile()
	if !ok {
		return "", fmt.Errorf("secret input requires a terminal")
	}
	s.Printf("%s: ", label)
	raw, err := term.ReadPassword(int(f.Fd()))
	s.Printf("\n")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

// stdinFile returns the configured input stream when it is a terminal,
// falling back to os.Stdin. Secret input must never come from a pipe or
// redirect that would echo the value.
func (s *Std) stdinFile() (*os.File, bool) {
	in := s.In
	if in == nil {
		in = os.Stdin
	}
	f, ok := in.(*os.File)
	if !ok {
		return nil, false
	}
	fi, err := f.Stat()
	if err != nil || (fi.Mode()&os.ModeCharDevice) == 0 {
		return nil, false
	}
	return f, true
}

// Printf implements UI.
func (s *Std) Printf(format string, args ...any) {
	w := s.Err
	if w == nil {
		w = os.Stderr
	}
	_, _ = fmt.Fprintf(w, format, args...)
}

func (s *Std) reader() *bufio.Reader {
	in := s.In
	if in == nil {
		in = os.Stdin
	}
	if s.br == nil || s.brIn != in {
		s.br = bufio.NewReaderSize(in, 64*1024)
		s.brIn = in
	}
	return s.br
}

func (s *Std) readLine() (string, error) {
	line, err := s.reader().ReadString('\n')
	if err != nil {
		// A final line without a trailing newline is still a valid answer.
		if errors.Is(err, io.EOF) && len(line) > 0 {
			return strings.TrimRight(line, "\r\n"), nil
		}
		if errors.Is(err, io.EOF) {
			return "", io.EOF
		}
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (s *Std) isTerminal() bool {
	in := s.In
	if in == nil {
		in = os.Stdin
	}
	f, ok := in.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func stdinIsTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// ErrCanceled is returned when the operator declines a required confirmation.
var ErrCanceled = fmt.Errorf("canceled")
