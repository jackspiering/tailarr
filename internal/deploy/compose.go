package deploy

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Compose runs `docker compose` in dir with the given args.
func Compose(dir string, args ...string) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker is required: %w", err)
	}
	full := append([]string{"compose"}, args...)
	cmd := exec.Command("docker", full...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// ComposeOutput runs docker compose and returns combined output.
func ComposeOutput(dir string, args ...string) (string, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return "", fmt.Errorf("docker is required: %w", err)
	}
	full := append([]string{"compose"}, args...)
	cmd := exec.Command("docker", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// DockerOK reports whether docker CLI is available.
func DockerOK() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}

// ComposeOK reports whether `docker compose version` works.
func ComposeOK() bool {
	if !DockerOK() {
		return false
	}
	cmd := exec.Command("docker", "compose", "version")
	return cmd.Run() == nil
}

// DaemonOK reports whether the Docker daemon is reachable.
func DaemonOK() bool {
	if !DockerOK() {
		return false
	}
	cmd := exec.Command("docker", "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}
