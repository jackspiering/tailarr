//go:build unix

package deploy

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureComposeCmd puts docker compose in its own process group and sends
// that group SIGINT when the command context is canceled. The docker CLI runs
// compose as a plugin child: the default SIGKILL of the CLI alone leaves the
// plugin running, so an interrupted up or pull would go on in the background.
// SIGINT lets both stop cleanly, as a terminal Ctrl+C would.
func configureComposeCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
		if err == nil || errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
}
