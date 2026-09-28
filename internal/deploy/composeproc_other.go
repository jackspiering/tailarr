//go:build !unix

package deploy

import "os/exec"

func configureComposeCmd(cmd *exec.Cmd) {}
