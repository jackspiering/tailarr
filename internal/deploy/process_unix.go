//go:build unix

package deploy

import (
	"os"
	"syscall"
)

func processAliveSignal(p *os.Process) bool {
	if p == nil {
		return false
	}
	err := p.Signal(syscall.Signal(0))
	return err == nil
}
