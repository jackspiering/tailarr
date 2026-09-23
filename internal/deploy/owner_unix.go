//go:build unix

package deploy

import (
	"os"
	"syscall"
)

// preserveOwner gives path the owner and group recorded in info. Only root
// can change ownership, so other operators keep their own uid and gid.
func preserveOwner(path string, info os.FileInfo) error {
	if os.Geteuid() != 0 {
		return nil
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	return os.Lchown(path, int(st.Uid), int(st.Gid))
}
