//go:build !unix

package deploy

import "os"

func flockAvailable() bool { return false }

func tryFlock(f *os.File) bool { return false }

func releaseFlock(f *os.File) {}
