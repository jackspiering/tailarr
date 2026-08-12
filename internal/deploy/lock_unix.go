//go:build unix

package deploy

import (
	"os"

	"golang.org/x/sys/unix"
)

func flockAvailable() bool { return true }

func tryFlock(f *os.File) bool {
	if f == nil {
		return false
	}
	return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) == nil
}

func releaseFlock(f *os.File) {
	if f == nil {
		return
	}
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
