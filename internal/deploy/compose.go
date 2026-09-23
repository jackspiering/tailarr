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

	"github.com/jackspiering/tailarr/internal/interrupt"
	"github.com/jackspiering/tailarr/internal/security/redact"
)

// composeFn is the compose executor. Tests may replace it with a fake.
var composeFn = defaultCompose

// cleanupTimeout bounds compose calls that must run after an interrupt.
const cleanupTimeout = 2 * time.Minute

// Compose runs `docker compose` in dir with the given args. The operator's
// interrupt cancels it.
func Compose(dir string, args ...string) error {
	return composeFn(interrupt.Context(), dir, args...)
}

// composeCleanup runs `docker compose` for cleanup after a failed operation.
// It ignores the interrupt that may have caused the failure, so a Ctrl+C
// during deploy still takes the containers down. A new SIGINT or SIGTERM, or
// cleanupTimeout, still stops it.
func composeCleanup(dir string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	return composeFn(ctx, dir, args...)
}

func composeServiceNames(dir, base string) ([]string, error) {
	if base == "" {
		base = "compose.yaml"
	}
	// No separate ComposeOK probe: a failing config call falls back to the scan.
	if DockerOK() {
		ctx, cancel := probeContext()
		defer cancel()
		cmd := exec.CommandContext(ctx, "docker", "compose", "-f", base, "config", "--services")
		cmd.Dir = dir
		cmd.Env, _ = filterComposeEnv(os.Environ())
		cmd.Stderr = redact.Writer(os.Stderr)
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

func defaultCompose(parent context.Context, dir string, args ...string) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("%w: docker is required: %v", ErrComposeFailed, err)
	}
	// Cancel when parent is canceled (the shared interrupt context for Compose,
	// a timeout for composeCleanup) or when SIGINT/SIGTERM arrives directly. Do not
	// re-raise: the caller must restore a failed apply before the process exits.
	// Run waits for that sequence, so Quit does not race the restore defer.
	ctx, stop := signal.NotifyContext(parent, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	full := append([]string{"compose"}, args...)
	cmd := exec.CommandContext(ctx, "docker", full...)
	// Bound Wait after cancel so a child that inherited stdio cannot stall restore.
	cmd.WaitDelay = 5 * time.Second
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
	env, dropped := filterComposeEnv(os.Environ())
	cmd.Env = env
	for _, key := range dropped {
		_, _ = fmt.Fprintf(stderr, "ignoring %s from the process environment\n", key)
	}
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
	ctx, cancel := probeContext()
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "compose", "version")
	return cmd.Run() == nil
}

// DaemonOK reports whether the Docker daemon is reachable.
func DaemonOK() bool {
	if !DockerOK() {
		return false
	}
	ctx, cancel := probeContext()
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// probeTimeout bounds Docker probes so a stalled context cannot freeze the TUI.
var probeTimeout = 10 * time.Second

func probeContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(interrupt.Context(), probeTimeout)
}

// filterComposeEnv drops Tailarr settings, Compose CLI knobs, and secret-like
// keys so the merged .env is authoritative. DOCKER_* is kept for remote daemons.
// dropped lists COMPOSE_* key names (never values) for an operator warning.
func filterComposeEnv(environ []string) (kept, dropped []string) {
	for _, e := range environ {
		key, _, _ := strings.Cut(e, "=")
		switch {
		case strings.HasPrefix(key, "COMPOSE_"):
			dropped = append(dropped, key)
		case strings.HasPrefix(key, "TAILARR_") || redact.LooksSecret(key):
			continue
		default:
			kept = append(kept, e)
		}
	}
	return kept, dropped
}
