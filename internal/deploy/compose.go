package deploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

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
	return composeServiceNames(dir, composeBaseName(dir))
}

func composeServiceNames(dir, base string) ([]string, error) {
	if base == "" {
		base = "compose.yaml"
	}
	if DockerOK() && ComposeOK() {
		ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, "docker", "compose", "-f", base, "config", "--services")
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
	indent := -1
	for _, line := range strings.Split(string(data), "\n") {
		trim := strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(trim) == "services:" {
			inServices = true
			indent = -1
			continue
		}
		if !inServices {
			continue
		}
		// Top-level key ends services block.
		if len(trim) > 0 && trim[0] != ' ' && trim[0] != '\t' && !strings.HasPrefix(strings.TrimSpace(trim), "#") {
			break
		}
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		if !strings.HasSuffix(s, ":") || strings.Contains(s, " ") {
			continue
		}
		spaces := countLeadingSpaces(line)
		if spaces == 0 {
			continue
		}
		// First service key sets the indent; nested keys (ports:, image:) are deeper.
		if indent < 0 {
			indent = spaces
		}
		if spaces != indent {
			continue
		}
		name := strings.TrimSuffix(s, ":")
		if name != "" && name != "services" {
			names = append(names, name)
		}
	}
	return names, nil
}

func countLeadingSpaces(s string) int {
	n := 0
	for ; n < len(s) && s[n] == ' '; n++ {
	}
	return n
}

func defaultCompose(dir string, args ...string) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("%w: docker is required: %v", ErrComposeFailed, err)
	}
	// Catch SIGTERM/SIGINT so CommandContext can kill the docker child and we
	// return to the caller. Do not re-raise: DeployWith must be allowed to
	// restore a BackupMove dest before the process exits. NotifyContext also
	// overrides the default SIGINT disposition so Ctrl-C during leaveTUI does
	// not kill Tailarr before that restore runs.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	full := append([]string{"compose"}, args...)
	cmd := exec.CommandContext(ctx, "docker", full...)
	cmd.Dir = dir
	// Redact diagnostics: a compose error echoing the interpolated TS_AUTHKEY
	// would otherwise print the raw secret to the terminal.
	stdout := redact.Writer(os.Stdout)
	stderr := redact.Writer(os.Stderr)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
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
	err := cmd.Run()
	if f, ok := stdout.(interface{ Flush() error }); ok {
		_ = f.Flush()
	}
	if f, ok := stderr.(interface{ Flush() error }); ok {
		_ = f.Flush()
	}
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%w: docker compose %s: %v", ErrInterrupted, strings.Join(args, " "), err)
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
	// Hash the deploy root so two roots hosting the same service never share a
	// Compose project. Truncating a sanitized path can drop the root fingerprint
	// when the service name is long (up to 64 chars).
	sum := sha256.Sum256([]byte(filepath.Clean(deployPath)))
	root := hex.EncodeToString(sum[:])[:8]
	svc := strings.ToLower(service)
	svc = projectNameRE.ReplaceAllString(svc, "-")
	svc = strings.Trim(svc, "-")
	if svc == "" {
		svc = "svc"
	}
	if len(svc) > 40 {
		svc = strings.Trim(svc[:40], "-")
	}
	return "tailarr-" + root + "-" + svc
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
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "compose", "version")
	return cmd.Run() == nil
}

// DaemonOK reports whether the Docker daemon is reachable.
func DaemonOK() bool {
	if !DockerOK() {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// probeTimeout bounds Docker probes so a stalled context cannot freeze the TUI.
var probeTimeout = 10 * time.Second
