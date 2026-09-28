package deploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackspiering/tailarr/internal/interrupt"
	"github.com/jackspiering/tailarr/internal/scaletail"
	"github.com/jackspiering/tailarr/internal/security/redact"
)

// composeFn is the compose executor. Tests may replace it with a fake.
var composeFn = defaultCompose

var (
	outputMu sync.Mutex
	output   io.Writer
)

// SetOutput sends docker compose output to w instead of the terminal. The TUI
// passes a writer that shows each line in its output panel. Output is always
// redacted first. A nil w restores os.Stdout and os.Stderr.
func SetOutput(w io.Writer) {
	outputMu.Lock()
	defer outputMu.Unlock()
	output = w
}

// composeWriters returns the redacted stdout and stderr for one compose run.
// With SetOutput both are the same writer, so exec never writes to it from
// two goroutines at once.
func composeWriters() (stdout, stderr io.Writer) {
	outputMu.Lock()
	defer outputMu.Unlock()
	if output != nil {
		w := redact.Writer(output)
		return w, w
	}
	return redact.Writer(os.Stdout), redact.Writer(os.Stderr)
}

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
		cmd.Env, _ = filterComposeEnv(os.Environ(), envFileKeys(dir))
		_, cmd.Stderr = composeWriters()
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
	configureComposeCmd(cmd)
	// Bound Wait after cancel so a child that ignores SIGINT or inherited
	// stdio cannot stall restore.
	cmd.WaitDelay = 10 * time.Second
	cmd.Dir = dir
	// Redact diagnostics: a compose error echoing the interpolated TS_AUTHKEY
	// would otherwise print the raw secret to the terminal. Compose never
	// reads stdin: it must not compete with the TUI for keys.
	stdout, stderr := composeWriters()
	tail := &lastLine{}
	cmd.Stdout = stdout
	cmd.Stderr = io.MultiWriter(stderr, tail)
	if stdout == stderr {
		cmd.Stdout = cmd.Stderr
	}
	// Compose interpolation prefers the process environment over .env, so an
	// exported TZ or TS_AUTHKEY would silently override the deployed values.
	// Filtering makes the deployment .env authoritative and limits secret
	// exposure to the compose subprocess.
	env, dropped := filterComposeEnv(os.Environ(), envFileKeys(dir))
	cmd.Env = env
	for _, key := range dropped {
		_, _ = fmt.Fprintf(stderr, "ignoring %s from the process environment\n", key)
	}
	err := cmd.Run()
	for _, w := range []io.Writer{stdout, stderr} {
		if f, ok := w.(interface{ Flush() error }); ok {
			_ = f.Flush()
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%w: docker compose %s: %v", ErrInterrupted, strings.Join(args, " "), err)
		}
		if reason := tail.String(); reason != "" {
			return fmt.Errorf("%w: docker compose %s: %v: %s", ErrComposeFailed, strings.Join(args, " "), err, reason)
		}
		return fmt.Errorf("%w: docker compose %s: %v", ErrComposeFailed, strings.Join(args, " "), err)
	}
	return nil
}

// lastLine keeps the last non-empty line written to it, redacted and capped.
// Compose ends a failure with its reason, which the error would otherwise lose
// once the output scrolls away.
type lastLine struct {
	mu   sync.Mutex
	buf  []byte
	last string
}

const maxReasonLen = 240

func (l *lastLine) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexAny(l.buf, "\r\n")
		if i < 0 {
			break
		}
		l.keep(l.buf[:i])
		l.buf = l.buf[i+1:]
	}
	// A progress bar without newlines must not grow the buffer forever.
	if len(l.buf) > 4096 {
		l.buf = l.buf[len(l.buf)-4096:]
	}
	return len(p), nil
}

func (l *lastLine) keep(line []byte) {
	if s := strings.TrimSpace(string(line)); s != "" {
		l.last = s
	}
}

// String returns the last line, redacted and at most maxReasonLen bytes.
func (l *lastLine) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.keep(l.buf)
	l.buf = nil
	s := redact.Text(l.last)
	if len(s) > maxReasonLen {
		s = s[:maxReasonLen] + "..."
	}
	return s
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

// filterComposeEnv drops Tailarr settings, Compose CLI knobs, secret-like
// keys, and keys the deployment .env sets (fileKeys), so the merged .env is
// authoritative. Variables docker itself needs (PATH, HOME, DOCKER_*) are
// always kept. dropped lists COMPOSE_* key names (never values) for an
// operator warning.
func filterComposeEnv(environ []string, fileKeys map[string]bool) (kept, dropped []string) {
	for _, e := range environ {
		key, _, _ := strings.Cut(e, "=")
		switch {
		case strings.HasPrefix(key, "COMPOSE_"):
			dropped = append(dropped, key)
		case strings.HasPrefix(key, "TAILARR_") || redact.LooksSecret(key):
			continue
		case fileKeys[key] && !dockerNeeds(key):
			continue
		default:
			kept = append(kept, e)
		}
	}
	return kept, dropped
}

// dockerNeeds reports whether the docker CLI reads key to find its config,
// credential helpers, or a remote daemon. A .env never replaces those.
func dockerNeeds(key string) bool {
	switch key {
	case "PATH", "HOME", "TMPDIR", "SSH_AUTH_SOCK":
		return true
	}
	return strings.HasPrefix(key, "DOCKER_") || strings.HasPrefix(key, "XDG_")
}

// envFileKeys returns the keys that dir/.env sets. A missing or unreadable
// file yields none; compose reports its own error for a broken .env.
func envFileKeys(dir string) map[string]bool {
	keys, err := scaletail.ReadEnvKeys(filepath.Join(dir, ".env"))
	if err != nil || len(keys) == 0 {
		return nil
	}
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	return set
}
