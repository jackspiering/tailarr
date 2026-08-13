//go:build unix

package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

func processAliveSignal(p *os.Process) bool {
	if p == nil {
		return false
	}
	err := p.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	// EPERM means the PID exists but belongs to another uid; treat as alive
	// so we never steal another operator's lock on a shared deploy root.
	return errors.Is(err, syscall.EPERM)
}

// processIsTailarr reports whether pid appears to be a running Tailarr
// process. Linux compares /proc/<pid>/comm with this binary's name; where
// per-process names are unavailable, conservatively report true so a live
// owner is never evicted.
func processIsTailarr(pid int) bool {
	name, ok := procCommName(pid)
	if !ok {
		return true
	}
	self, err := os.Executable()
	if err != nil {
		return true
	}
	// /proc/<pid>/comm is truncated to 15 chars; compare on the same window.
	self = filepath.Base(self)
	if len(self) > 15 {
		self = self[:15]
	}
	return name == self
}
