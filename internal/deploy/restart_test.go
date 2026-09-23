package deploy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/logging"
)

func TestRestartStopsThenStartsInDependencyOrder(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	setupTemplate(t, repo, "web", "HOSTNAME=x\n")
	dest := filepath.Join(deployRoot, "web")
	if err := copyTemplate(filepath.Join(repo, "services", "web"), dest); err != nil {
		t.Fatal(err)
	}
	if err := writeOverride("web", dest); err != nil {
		t.Fatal(err)
	}
	var calls []string
	withFakeCompose(t, func(dir string, args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	})
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	if err := m.Restart("web"); err != nil {
		t.Fatalf("Restart failed: %v", err)
	}
	// compose restart restarts a network_mode: service: app in parallel with
	// its sidecar, so the app joins a dead namespace. up honors depends_on.
	proj := strings.Join(composeProjectArgs(deployRoot, "web"), " ")
	want := []string{
		proj + " stop",
		proj + " -f compose.yaml -f " + overrideFilename + " up -d --remove-orphans",
	}
	if !slices.Equal(calls, want) {
		t.Fatalf("restart compose calls:\n got %q\nwant %q", calls, want)
	}
}

func TestRestartSaysServiceIsStoppedWhenUpFails(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	managedDeploy(t, repo, deployRoot)
	withFakeCompose(t, func(dir string, args ...string) error {
		if strings.Contains(strings.Join(args, " "), " up ") {
			return fmt.Errorf("%w: port taken", ErrComposeFailed)
		}
		return nil
	})
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	err := m.Restart("web")
	if !errors.Is(err, ErrComposeFailed) || !strings.Contains(err.Error(), "it is stopped now") {
		t.Fatalf("expected stopped-service error, got %v", err)
	}
}

// recordUI confirms every prompt and records Printf output.
type recordUI struct{ out *strings.Builder }

func (u recordUI) Confirm(string, bool) (bool, error)  { return true, nil }
func (u recordUI) Line(string, string) (string, error) { return "", nil }
func (u recordUI) Secret(string) (string, error)       { return "", nil }
func (u recordUI) Printf(format string, args ...any)   { fmt.Fprintf(u.out, format, args...) }

func TestRemoveReportsBackupsItCannotDelete(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	managedDeploy(t, repo, deployRoot)
	stuck := filepath.Join(deployRoot, config.BackupDirName, "web-20200101T000000Z")
	if err := os.MkdirAll(stuck, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc", filepath.Join(stuck, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	withFakeCompose(t, func(string, ...string) error { return nil })
	logPath := filepath.Join(t.TempDir(), "tailarr.log")
	var out strings.Builder
	m := &Manager{
		Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot},
		Log: logging.New(logPath, 1<<20),
		UI:  recordUI{out: &out},
	}
	if err := m.RemoveWith("web", DeployOpts{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Could not delete "+stuck) {
		t.Fatalf("operator not told about the stuck backup:\n%s", out.String())
	}
	data, _ := os.ReadFile(logPath)
	if !strings.Contains(string(data), "removed 1 of 2 backups for web") {
		t.Fatalf("log must report the real count:\n%s", data)
	}
}
