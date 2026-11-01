//go:build unix

package scaletail

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func mustReturn(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { fn(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s is still blocked after 5s", what)
	}
}

func TestParseEnvFileRefusesFifo(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	var err error
	mustReturn(t, "ParseEnvFile with a FIFO", func() { _, err = ParseEnvFile(path) })
	if err == nil {
		t.Fatal("ParseEnvFile must refuse a FIFO")
	}
}
