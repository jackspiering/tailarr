// Package prompt provides interactive confirmation and value prompts for CLI/TUI.
package prompt

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// UI is the interactive surface used by deploy and lifecycle operations.
type UI interface {
	// Confirm asks a yes/no question. defaultYes controls empty-answer behavior.
	// When AssumeYes is true, returns true without reading.
	Confirm(question string, defaultYes bool) (bool, error)
	// Line reads a single line (echo on). Empty input returns defaultVal.
	Line(label, defaultVal string) (string, error)
	// Secret reads a single line with echo disabled when stdin is a TTY.
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
	if s.AssumeYes {
		s.Printf("%s %s: (auto-yes)\n", question, suffix)
		return true, nil
	}
	if !stdinIsTTY() {
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

// Secret implements UI.
func (s *Std) Secret(label string) (string, error) {
	s.Printf("%s: ", label)
	if stdinIsTTY() {
		fd := int(os.Stdin.Fd())
		raw, err := term.ReadPassword(fd)
		s.Printf("\n")
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(raw)), nil
	}
	line, err := s.readLine()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// Printf implements UI.
func (s *Std) Printf(format string, args ...any) {
	w := s.Err
	if w == nil {
		w = os.Stderr
	}
	_, _ = fmt.Fprintf(w, format, args...)
}

func (s *Std) readLine() (string, error) {
	in := s.In
	if in == nil {
		in = os.Stdin
	}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return "", err
		}
		return "", io.EOF
	}
	return sc.Text(), nil
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
