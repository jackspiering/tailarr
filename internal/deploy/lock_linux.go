//go:build linux

package deploy

import (
	"os"
	"strconv"
	"strings"
)

// procCommName returns the truncated command name of pid from /proc.
func procCommName(pid int) (string, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(data)), true
}
