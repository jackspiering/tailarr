package deploy

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// composeFn is the compose executor. Tests may replace it with a fake.
var composeFn = defaultCompose

// Compose runs `docker compose` in dir with the given args.
func Compose(dir string, args ...string) error {
	return composeFn(dir, args...)
}

// ComposeServiceNames returns Compose service names for a deployment directory.
// Prefers `docker compose config --services`; falls back to a simple YAML scan.
func ComposeServiceNames(dir string) ([]string, error) {
	base := "compose.yaml"
	if p, ok := findComposeBase(dir); ok {
		base = p
	}
	if DockerOK() && ComposeOK() {
		cmd := exec.Command("docker", "compose", "-f", base, "config", "--services")
		cmd.Dir = dir
		out, err := cmd.Output()
		if err == nil {
			var names []string
			for _, line := range strings.Split(string(out), "\n") {
				line = strings.TrimSpace(line)
				if line != "" {
					names = append(names, line)
				}
			}
			if len(names) > 0 {
				return names, nil
			}
		}
	}
	return scanComposeServiceNames(filepath.Join(dir, base))
}

func findComposeBase(dir string) (string, bool) {
	for _, name := range []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"} {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return name, true
		}
	}
	return "", false
}

func scanComposeServiceNames(composePath string) ([]string, error) {
	data, err := os.ReadFile(composePath)
	if err != nil {
		return nil, err
	}
	var names []string
	inServices := false
	for _, line := range strings.Split(string(data), "\n") {
		trim := strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(trim) == "services:" {
			inServices = true
			continue
		}
		if inServices {
			// Top-level key ends services block.
			if len(trim) > 0 && trim[0] != ' ' && trim[0] != '\t' && !strings.HasPrefix(strings.TrimSpace(trim), "#") {
				break
			}
			// 2 or 4 space indent service name
			if strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "    ") {
				s := strings.TrimSpace(line)
				if strings.HasPrefix(s, "#") {
					continue
				}
				if strings.HasSuffix(s, ":") && !strings.Contains(s, " ") {
					name := strings.TrimSuffix(s, ":")
					if name != "" && name != "services" {
						names = append(names, name)
					}
				}
			}
		}
	}
	return names, nil
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
