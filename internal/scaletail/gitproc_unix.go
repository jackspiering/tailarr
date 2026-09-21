//go:build unix

package scaletail

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureGitCmd puts git in its own process group and kills that group when
// the command context is canceled. CommandContext's default Cancel only kills
// the direct child, which leaves git-remote helpers and index.lock behind.
func configureGitCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == nil || errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
}
