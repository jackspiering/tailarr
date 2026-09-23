//go:build integration

// Integration tests exercise the real `docker compose` lifecycle through the
// exported Manager API. They need a reachable Docker daemon and are excluded
// from the default test run; CI and `-tags integration` runs include them.
// Without a daemon every test skips with a reason instead of failing.
package deploy

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackspiering/tailarr/internal/config"
)

// integrationImage is the smallest useful long-running container.
const integrationImage = "busybox:musl"

var (
	dockerGuardOnce sync.Once
	dockerGuardWhy  string
)

// requireDocker skips the test unless a real Docker CLI, daemon, and compose
// v2 plugin are all usable. The probe result is computed once per run.
func requireDocker(t *testing.T) {
	t.Helper()
	dockerGuardOnce.Do(func() {
		switch {
		case !DockerOK():
			dockerGuardWhy = "docker CLI not found in PATH"
		case !DaemonOK():
			dockerGuardWhy = "docker daemon not reachable"
		case !ComposeOK():
			dockerGuardWhy = "docker compose v2 not available"
		}
	})
	if dockerGuardWhy != "" {
		t.Skipf("skipping integration test: %s", dockerGuardWhy)
	}
}

// integrationFixture builds an isolated deploy root with one service template.
type integrationFixture struct {
	m         *Manager
	deploy    string
	service   string
	container string
}

func newIntegrationFixture(t *testing.T, service string) *integrationFixture {
	t.Helper()
	requireDocker(t)
	root := t.TempDir()
	cfg := &config.Config{
		ConfigPath:   filepath.Join(root, "config", "tailarr.conf"),
		RepoURL:      "https://github.com/example/scaletail",
		RepoPath:     filepath.Join(root, "scaletail"),
		DeployPath:   filepath.Join(root, "deploy"),
		LogPath:      filepath.Join(root, "tailarr.log"),
		AuthkeysPath: filepath.Join(root, "authkeys.json"),
		LogMaxBytes:  1 << 20,
	}
	f := &integrationFixture{
		m:         &Manager{Cfg: cfg},
		deploy:    cfg.DeployPath,
		service:   service,
		container: "app-" + service,
	}
	if err := os.MkdirAll(filepath.Join(cfg.RepoPath, "services", service), 0o755); err != nil {
		t.Fatal(err)
	}
	compose := "services:\n" +
		"  " + service + ":\n" +
		"    image: " + integrationImage + "\n" +
		"    command: sleep 100000\n" +
		"    container_name: " + f.container + "\n"
	if err := os.WriteFile(filepath.Join(cfg.RepoPath, "services", service, "compose.yaml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	env := "TEST_MESSAGE=hello-from-integration\n"
	if err := os.WriteFile(filepath.Join(cfg.RepoPath, "services", service, ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

// downProject tears down the compose project and removes leftovers. It is
// registered as t.Cleanup so containers never outlive a failing test.
func (f *integrationFixture) downProject(t *testing.T) {
	t.Helper()
	dir := filepath.Join(f.deploy, f.service)
	args := append(append([]string{}, composeProjectArgs(f.deploy, f.service)...),
		"-f", composeBaseName(dir), "-f", overrideFilename,
		"down", "--remove-orphans", "--volumes")
	_ = Compose(dir, args...)
	_ = exec.Command("docker", "rm", "-f", f.container).Run()
	_ = os.RemoveAll(dir)
}

// waitFor polls fn until it reports true or the timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

func TestIntegrationDeployStartsService(t *testing.T) {
	f := newIntegrationFixture(t, "tester")
	if err := f.m.DeployWith(f.service, DeployOpts{}); err != nil {
		t.Fatalf("DeployWith failed: %v", err)
	}
	t.Cleanup(func() { f.downProject(t) })

	waitFor(t, 60*time.Second, "container running in docker", func() bool {
		out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", f.container).Output()
		return err == nil && string(out) == "true\n"
	})

	st, err := CollectOverview(f.deploy)
	if err != nil {
		t.Fatalf("CollectOverview failed: %v", err)
	}
	found := false
	for _, n := range st.RunningNames {
		if n == f.service {
			found = true
		}
	}
	if !found {
		t.Fatalf("running services from docker ps must contain %q, got %v", f.service, st.RunningNames)
	}
	if h := st.ManagedHealth[f.service]; h != HealthRunning {
		t.Fatalf("managed health for %q must be running, got %s", f.service, h)
	}

	info, err := os.Stat(filepath.Join(f.deploy, f.service, ".env"))
	if err != nil {
		t.Fatalf("deployed .env missing: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("deployed .env must have mode 0600, got %04o", info.Mode().Perm())
	}
}

func TestIntegrationStopThenRemove(t *testing.T) {
	f := newIntegrationFixture(t, "removetest")
	if err := f.m.DeployWith(f.service, DeployOpts{}); err != nil {
		t.Fatalf("DeployWith failed: %v", err)
	}
	t.Cleanup(func() { f.downProject(t) })
	waitFor(t, 60*time.Second, "container running before stop", func() bool {
		out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", f.container).Output()
		return err == nil && string(out) == "true\n"
	})

	if err := f.m.Stop(f.service); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	waitFor(t, 30*time.Second, "container exited after stop", func() bool {
		out, err := exec.Command("docker", "inspect", "-f", "{{.State.Status}}", f.container).Output()
		return err == nil && string(out) == "exited\n"
	})

	dest := filepath.Join(f.deploy, f.service)
	if err := f.m.RemoveWith(f.service, DeployOpts{}); err != nil {
		t.Fatalf("RemoveWith failed: %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("deployment directory must be gone after remove, stat err: %v", err)
	}
	if _, err := exec.Command("docker", "inspect", f.container).Output(); err == nil {
		t.Fatalf("container %s must be removed from docker", f.container)
	}

	err := f.m.RemoveWith(f.service, DeployOpts{})
	if !errors.Is(err, ErrNotDeployed) {
		t.Fatalf("second RemoveWith must return ErrNotDeployed, got %v", err)
	}
}

func TestIntegrationUnmanagedRefused(t *testing.T) {
	f := newIntegrationFixture(t, "straytest")
	root := f.m.Cfg.DeployPath
	dest := filepath.Join(root, f.service)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	// A compose file without the Tailarr marker looks like a foreign stack.
	minimal := "services:\n  " + f.service + ":\n    image: " + integrationImage + "\n"
	if err := os.WriteFile(filepath.Join(dest, "compose.yaml"), []byte(minimal), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := f.m.Stop(f.service); !errors.Is(err, ErrNotManaged) {
		t.Fatalf("Stop on unmanaged dir must return ErrNotManaged, got %v", err)
	}
	if err := f.m.RemoveWith(f.service, DeployOpts{}); !errors.Is(err, ErrNotManaged) {
		t.Fatalf("RemoveWith on unmanaged dir must return ErrNotManaged, got %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("unmanaged directory must stay intact, stat err: %v", err)
	}
}

// newSidecarFixture mirrors the ScaleTail layout: the app shares the network
// namespace of a health-checked sidecar and waits for it to become healthy.
func newSidecarFixture(t *testing.T, service string) *integrationFixture {
	t.Helper()
	f := newIntegrationFixture(t, service)
	sidecar := "tailscale-" + service
	compose := "services:\n" +
		"  tailscale:\n" +
		"    image: " + integrationImage + "\n" +
		"    command: sleep 100000\n" +
		"    container_name: " + sidecar + "\n" +
		"    healthcheck:\n" +
		"      test: [\"CMD\", \"true\"]\n" +
		"      interval: 1s\n" +
		"      start_period: 1s\n" +
		"      start_interval: 1s\n" +
		"  application:\n" +
		"    image: " + integrationImage + "\n" +
		"    command: sleep 100000\n" +
		"    container_name: " + f.container + "\n" +
		"    network_mode: service:tailscale\n" +
		"    depends_on:\n" +
		"      tailscale:\n" +
		"        condition: service_healthy\n"
	if err := os.WriteFile(filepath.Join(f.m.Cfg.RepoPath, "services", service, "compose.yaml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", sidecar).Run() })
	return f
}

func containerRunning(name string) bool {
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", name).Output()
	return err == nil && string(out) == "true\n"
}

func TestIntegrationRestartKeepsSidecarAppRunning(t *testing.T) {
	f := newSidecarFixture(t, "restarttest")
	if err := f.m.DeployWith(f.service, DeployOpts{}); err != nil {
		t.Fatalf("DeployWith failed: %v", err)
	}
	t.Cleanup(func() { f.downProject(t) })
	waitFor(t, 60*time.Second, "app running before restart", func() bool { return containerRunning(f.container) })

	if err := f.m.Restart(f.service); err != nil {
		t.Fatalf("Restart of a running sidecar service failed: %v", err)
	}
	waitFor(t, 30*time.Second, "app running after restart", func() bool { return containerRunning(f.container) })

	if err := f.m.Stop(f.service); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if err := f.m.Restart(f.service); err != nil {
		t.Fatalf("Restart of a stopped sidecar service failed: %v", err)
	}
	waitFor(t, 30*time.Second, "app running after restart from stopped", func() bool { return containerRunning(f.container) })
}
