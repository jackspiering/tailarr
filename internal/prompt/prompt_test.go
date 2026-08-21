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

func TestAssumeYesSkipsOnlyDefaultYes(t *testing.T) {
	yes := &Std{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard, AssumeYes: true}
	ok, err := yes.Confirm("continue?", true)
	if err != nil || !ok {
		t.Fatalf("default-yes should auto-accept: ok=%v err=%v", ok, err)
	}

	// Default-no stays gated under AssumeYes. Confirm still requires a terminal
	// and an answer; it does not return (false, nil) without reading.
	no := &Std{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard, AssumeYes: true}
	ok, err = no.Confirm("Remove stored auth key prod?", false)
	if ok {
		t.Fatal("default-no must not auto-accept")
	}
	if err == nil {
		t.Fatal("non-terminal default-no must still require an answer")
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
