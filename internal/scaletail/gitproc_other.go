//go:build !unix

package scaletail

import "os/exec"

func configureGitCmd(cmd *exec.Cmd) {}
