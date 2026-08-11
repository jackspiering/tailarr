//go:build !unix

package deploy

import "os"

// On non-Unix platforms, treat unknown processes as alive so we never steal locks.
func processAliveSignal(p *os.Process) bool {
	return p != nil
}
