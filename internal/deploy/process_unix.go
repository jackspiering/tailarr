//go:build unix

package deploy

import (
	"errors"
	"os"
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
