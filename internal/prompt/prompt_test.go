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
