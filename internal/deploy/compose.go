package deploy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/jackspiering/tailarr/internal/scaletail"
	"github.com/jackspiering/tailarr/internal/security/redact"
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
	if p, ok := scaletail.ComposeFileIn(dir); ok {
		base = filepath.Base(p)
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
	// Forward SIGTERM (systemd stop, TUI quit) by canceling the context, which
	// makes exec.CommandContext kill the docker compose child. If the run was
	// aborted this way we then re-raise SIGTERM so the process exits with
	// correct systemd semantics instead of returning into the TUI. SIGINT needs
	// no handling here: terminal Ctrl-C reaches the child via the shared
	// process group.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()

	full := append([]string{"compose"}, args...)
	cmd := exec.CommandContext(ctx, "docker", full...)
	cmd.Dir = dir
	// Redact diagnostics: a compose error echoing the interpolated TS_AUTHKEY
	// would otherwise print the raw secret to the terminal.
	cmd.Stdout = redact.Writer(os.Stdout)
	cmd.Stderr = redact.Writer(os.Stderr)
	cmd.Stdin = os.Stdin
	// Compose interpolation prefers shell env over .env, so an exported
	// TS_AUTHKEY (or any other secret-like var) would silently override the
	// merged .env. Filtering makes the merged .env authoritative and limits
	// secret exposure to the compose subprocess.
	var env []string
	for _, e := range os.Environ() {
		key, _, _ := strings.Cut(e, "=")
		if strings.HasPrefix(key, "TAILARR_") || redact.LooksSecret(key) {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = env
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			// SIGTERM received: CommandContext already killed the child. Reset
			// the handler and re-raise SIGTERM so the default disposition
			// terminates us; give the signal time to land rather than
			// returning into the TUI. If we are somehow still alive after the
			// sleep, fall through and return the wrapped error.
			signal.Reset(syscall.SIGTERM)
			_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
			time.Sleep(time.Second)
		}
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
		root = root[len(root)-24:]
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
