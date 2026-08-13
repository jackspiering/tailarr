//go:build unix && !linux

package deploy

// procCommName is unavailable outside Linux. Owners are treated as Tailarr
// so a live lock holder is never evicted.
func procCommName(pid int) (string, bool) {
	return "", false
}
