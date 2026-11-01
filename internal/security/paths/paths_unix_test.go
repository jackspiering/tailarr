//go:build unix

package paths

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

func TestReadRegularFileRefusesFifo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	var err error
	mustReturn(t, "ReadRegularFile with a FIFO", func() { _, _, err = ReadRegularFile(path, 1024) })
	if err == nil {
		t.Fatal("ReadRegularFile must refuse a FIFO")
	}
}
