package deploy

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jackspiering/tailarr/internal/config"
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
