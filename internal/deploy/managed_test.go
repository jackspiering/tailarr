package deploy

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackspiering/tailarr/internal/config"
)

// managedDeployment creates a managed deployment of "web" without compose up.
func managedDeployment(t *testing.T) (repo, deployRoot, dest string) {
	t.Helper()
	repo = t.TempDir()
	deployRoot = t.TempDir()
	setupTemplate(t, repo, "web", "HOSTNAME=x\n")
	dest = filepath.Join(deployRoot, "web")
	if err := copyTemplate(filepath.Join(repo, "services", "web"), dest); err != nil {
		t.Fatal(err)
	}
	if err := writeOverride("web", dest); err != nil {
		t.Fatal(err)
	}
	return repo, deployRoot, dest
}

// lockedContainerDir mimics a sidecar state dir that a root container
// created with mode 0700: a non-root operator cannot list it.
func lockedContainerDir(t *testing.T, dest string) string {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root reads mode 0000 directories; test needs a non-root user")
	}
	locked := filepath.Join(dest, "ts", "state")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	return locked
}

func TestStopAndRestartSkipUnreadableContainerData(t *testing.T) {
	repo, deployRoot, dest := managedDeployment(t)
	lockedContainerDir(t, dest)
	withFakeCompose(t, func(dir string, args ...string) error { return nil })
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	if err := m.Stop("web"); err != nil {
		t.Errorf("Stop must not read container data: %v", err)
	}
	if err := m.Restart("web"); err != nil {
		t.Errorf("Restart must not read container data: %v", err)
	}
}

func TestStopIgnoresSymlinksInContainerData(t *testing.T) {
	repo, deployRoot, dest := managedDeployment(t)
	data := filepath.Join(dest, "web-data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("current.db", filepath.Join(data, "latest.db")); err != nil {
		t.Fatal(err)
	}
	withFakeCompose(t, func(dir string, args ...string) error { return nil })
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	if err := m.Stop("web"); err != nil {
		t.Fatalf("Stop must not refuse a symlink the container wrote into its data: %v", err)
	}
}

func TestStopRefusesSymlinkedManagedFiles(t *testing.T) {
	for _, name := range []string{"compose.yaml", overrideFilename, ".env"} {
		repo, deployRoot, dest := managedDeployment(t)
		target := filepath.Join(t.TempDir(), name)
		if err := os.Rename(filepath.Join(dest, name), target); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dest, name)); err != nil {
			t.Fatal(err)
		}
		called := false
		withFakeCompose(t, func(dir string, args ...string) error { called = true; return nil })
		m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
		if err := m.Stop("web"); !errors.Is(err, ErrSymlink) {
			t.Errorf("%s: Stop must refuse a symlinked managed file, got %v", name, err)
		}
		if called {
			t.Errorf("%s: compose must not run after a symlink refusal", name)
		}
	}
}

func TestRemoveExplainsUnreadableContainerData(t *testing.T) {
	repo, deployRoot, dest := managedDeployment(t)
	lockedContainerDir(t, dest)
	withFakeCompose(t, func(dir string, args ...string) error { return nil })
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	err := m.RemoveWith("web", DeployOpts{})
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("expected a permission error, got %v", err)
	}
	if !strings.Contains(err.Error(), "run Tailarr as root") {
		t.Fatalf("error must tell the operator to run as root, got %v", err)
	}
	if _, serr := os.Stat(filepath.Join(dest, "compose.yaml")); serr != nil {
		t.Fatalf("deployment must stay intact: %v", serr)
	}
}
