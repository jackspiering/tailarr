//go:build !unix

package deploy

import "os"

// On non-Unix platforms, treat unknown processes as alive so we never steal locks.
func processAliveSignal(p *os.Process) bool {
	return p != nil
}

// processIsTailarr conservatively treats every live owner as a Tailarr
// process on non-Unix platforms so a live lock holder is never evicted.
func processIsTailarr(pid int) bool {
	return true
}
