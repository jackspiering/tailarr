package deploy

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jackspiering/tailarr/internal/config"
)

// lockProbeUI tries to take a lock whenever it prompts, the way a second
// Tailarr instance would while this one waits for an answer.
type lockProbeUI struct {
	lockPath string
	errs     *[]error
	confirm  bool
	line     string
}

func (u lockProbeUI) probe() {
	l, err := AcquireLock(u.lockPath, 200*time.Millisecond)
	if err != nil {
		*u.errs = append(*u.errs, err)
		return
	}
	_ = l.Release()
}

func (u lockProbeUI) Confirm(string, bool) (bool, error)  { u.probe(); return u.confirm, nil }
func (u lockProbeUI) Line(string, string) (string, error) { u.probe(); return u.line, nil }
func (u lockProbeUI) Secret(string) (string, error)       { u.probe(); return "", nil }
func (u lockProbeUI) Printf(string, ...any)               {}

func TestDeployReleasesCatalogLockBeforePrompts(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	setupTemplate(t, repo, "web", "GREETING=\n")
	withFakeCompose(t, func(string, ...string) error { return nil })
	var errs []error
	m := &Manager{
		Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot},
		UI:  lockProbeUI{lockPath: RepoLockPath(repo), errs: &errs, line: "hi"},
	}
	if err := m.DeployWith("web", DeployOpts{}); err != nil {
		t.Fatal(err)
	}
	if len(errs) > 0 {
		t.Fatalf("catalog lock held during env prompt: %v", errs)
	}
}

func TestApplyConfirmsBeforeTakingLocks(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	managedDeploy(t, repo, deployRoot)
	lockPath, err := ServiceLockPath(deployRoot, "web")
	if err != nil {
		t.Fatal(err)
	}
	var errs []error
	m := &Manager{
		Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot},
		UI:  lockProbeUI{lockPath: lockPath, errs: &errs, confirm: false},
	}
	if err := m.Apply("web", DeployOpts{}); err == nil {
		t.Fatal("expected canceled apply")
	}
	if len(errs) > 0 {
		t.Fatalf("service lock held during apply confirm: %v", errs)
	}
}

// managedDeploy creates a managed deployment of the "web" template.
func managedDeploy(t *testing.T, repo, deployRoot string) string {
	t.Helper()
	setupTemplate(t, repo, "web", "HOSTNAME=x\n")
	dest := filepath.Join(deployRoot, "web")
	if err := copyTemplate(filepath.Join(repo, "services", "web"), dest); err != nil {
		t.Fatal(err)
	}
	if err := writeOverride("web", dest); err != nil {
		t.Fatal(err)
	}
	return dest
}
