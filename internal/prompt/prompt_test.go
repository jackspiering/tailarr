package prompt

import (
	"io"
	"strings"
	"testing"
)

func TestReadLineDoesNotDropFollowingLines(t *testing.T) {
	in := strings.NewReader("one\ntwo\n")
	s := &Std{In: in, Err: io.Discard}
	a, err := s.Line("A", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Line("B", "")
	if err != nil {
		t.Fatal(err)
	}
	if a != "one" || b != "two" {
		t.Fatalf("got %q %q", a, b)
	}
}

func TestReadLineAcceptsFinalLineWithoutNewline(t *testing.T) {
	in := strings.NewReader("only")
	s := &Std{In: in, Err: io.Discard}
	got, err := s.Line("A", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "only" {
		t.Fatalf("got %q", got)
	}
}

func TestSecretRequiresTerminal(t *testing.T) {
	// A non-terminal input stream must fail rather than echo the secret or
	// fall back to the real stdin of the test process.
	s := &Std{In: strings.NewReader("tskey-auth-x"), Err: io.Discard}
	if _, err := s.Secret("TS_AUTHKEY"); err == nil {
		t.Fatal("expected an error for non-terminal secret input")
	}
}
