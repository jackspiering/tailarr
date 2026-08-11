package deploy

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// composeFn is the compose executor. Tests may replace it with a fake.
var composeFn = defaultCompose

// Compose runs `docker compose` in dir with the given args.
func Compose(dir string, args ...string) error {
	return composeFn(dir, args...)
}

func defaultCompose(dir string, args ...string) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("%w: docker is required: %v", ErrComposeFailed, err)
	}
	full := append([]string{"compose"}, args...)
	cmd := exec.Command("docker", full...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: docker compose %s: %v", ErrComposeFailed, strings.Join(args, " "), err)
	}
	return nil
}

// projectNameRE sanitizes service names for Compose -p project names.
var projectNameRE = regexp.MustCompile(`[^a-z0-9_-]+`)

// ProjectName returns a Compose project name unique to this Tailarr service
// under a given deploy root fingerprint, reducing cross-root collisions.
func ProjectName(deployPath, service string) string {
	// Hash deploy root into a short stable prefix so two roots both hosting
	// "web" do not share the same Compose project during down --remove-orphans.
	root := strings.ToLower(strings.ReplaceAll(deployPath, string(os.PathSeparator), "-"))
	root = projectNameRE.ReplaceAllString(root, "-")
	root = strings.Trim(root, "-")
	if len(root) > 24 {
		// Keep last path segments for readability + uniqueness.
		if len(root) > 24 {
			root = root[len(root)-24:]
		}
		root = strings.Trim(root, "-")
	}
	svc := strings.ToLower(service)
	svc = projectNameRE.ReplaceAllString(svc, "-")
	name := "tailarr"
	if root != "" {
		name += "-" + root
	}
	name += "-" + svc
	// Compose project names max ~63; keep conservative.
	if len(name) > 60 {
		name = name[len(name)-60:]
		name = strings.Trim(name, "-")
	}
	if name == "" {
		return "tailarr-" + svc
	}
	return name
}

// composeProjectArgs returns ["-p", projectName] for consistent project identity.
func composeProjectArgs(deployPath, service string) []string {
	return []string{"-p", ProjectName(deployPath, service)}
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
