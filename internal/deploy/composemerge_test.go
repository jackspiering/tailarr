package deploy

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackspiering/tailarr/internal/config"
)

const (
	mergeCatalogV1 = "services:\n  app:\n    image: app:1\n    restart: always\n\n  tailscale:\n    image: tailscale:1\n"
	mergeCatalogV2 = "services:\n  app:\n    image: app:1\n    restart: always\n\n  tailscale:\n    image: tailscale:2\n"
)

func requireGitBinary(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

// mergeFixture is a managed deployment of "web" with compose text deployed
// and, when base is not empty, a recorded base. The catalog then holds catalog.
type mergeFixture struct {
	m    *Manager
	ui   *previewUI
	tpl  string
	dest string
	root string
}

func newMergeFixture(t *testing.T, base, deployed, catalog string) *mergeFixture {
	t.Helper()
	repo, root := t.TempDir(), t.TempDir()
	dest := managedDeploy(t, repo, root)
	tpl := filepath.Join(repo, "services", "web")
	writePreviewFile(t, filepath.Join(dest, "compose.yaml"), deployed)
	if base != "" {
		writePreviewFile(t, filepath.Join(dest, composeBaseFilename), base)
	}
	writePreviewFile(t, filepath.Join(tpl, "compose.yaml"), catalog)
	ui := &previewUI{confirm: true}
	return &mergeFixture{
		m:    &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: root}, UI: ui},
		ui:   ui,
		tpl:  tpl,
		dest: dest,
		root: root,
	}
}

func (f *mergeFixture) read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.dest, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDeployRecordsComposeBase(t *testing.T) {
	repo, root := t.TempDir(), t.TempDir()
	setupTemplate(t, repo, "web", "HOSTNAME=x\n")
	withFakeCompose(t, func(string, ...string) error { return nil })
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: root}}
	if err := m.Deploy("web", DeployOpts{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "web", composeBaseFilename))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "services:\n  app:\n    image: alpine:latest\n" {
		t.Fatalf("base = %q, want the catalog compose file", got)
	}
}

func TestApplyMergesComposeEditsWithCatalogChanges(t *testing.T) {
	requireGitBinary(t)
	edited := strings.Replace(mergeCatalogV1, "restart: always", "restart: unless-stopped", 1)
	f := newMergeFixture(t, mergeCatalogV1, edited, mergeCatalogV2)
	withFakeCompose(t, func(string, ...string) error { return nil })
	if err := f.m.Apply("web", DeployOpts{}); err != nil {
		t.Fatal(err)
	}
	got := f.read(t, "compose.yaml")
	for _, want := range []string{"restart: unless-stopped", "tailscale:2"} {
		if !strings.Contains(got, want) {
			t.Errorf("compose.yaml lacks %q:\n%s", want, got)
		}
	}
	if base := f.read(t, composeBaseFilename); base != mergeCatalogV2 {
		t.Errorf("base = %q, want the new catalog file", base)
	}
	mustContain(t, f.ui.text(), "merged", "your edits stay", "merged the catalog changes into your edits")
}

func TestApplyKeepsComposeEditsWhenCatalogIsUnchanged(t *testing.T) {
	edited := strings.Replace(mergeCatalogV1, "app:1", "app:pinned", 1)
	f := newMergeFixture(t, mergeCatalogV1, edited, mergeCatalogV1)
	withFakeCompose(t, func(string, ...string) error { return nil })
	if err := f.m.Apply("web", DeployOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := f.read(t, "compose.yaml"); got != edited {
		t.Fatalf("compose.yaml = %q, want the edited file unchanged", got)
	}
	mustContain(t, f.ui.text(), "Your edits to compose.yaml stay")
}

func TestApplyRefusesComposeConflictAndChangesNothing(t *testing.T) {
	requireGitBinary(t)
	edited := strings.Replace(mergeCatalogV1, "tailscale:1", "tailscale:mine", 1)
	f := newMergeFixture(t, mergeCatalogV1, edited, mergeCatalogV2)
	composeRan := false
	withFakeCompose(t, func(string, ...string) error { composeRan = true; return nil })
	err := f.m.Apply("web", DeployOpts{})
	if !errors.Is(err, ErrComposeConflict) {
		t.Fatalf("Apply = %v, want ErrComposeConflict", err)
	}
	if !strings.Contains(err.Error(), composeBaseFilename) {
		t.Errorf("error does not say how to get past the conflict: %v", err)
	}
	if composeRan {
		t.Error("Apply ran compose after a conflict")
	}
	if got := f.read(t, "compose.yaml"); got != edited {
		t.Errorf("compose.yaml changed: %q", got)
	}
	if got := f.read(t, composeBaseFilename); got != mergeCatalogV1 {
		t.Errorf("base changed: %q", got)
	}
	if strings.Contains(f.read(t, "compose.yaml"), "<<<<<<<") {
		t.Error("conflict markers were written")
	}
	mustContain(t, f.ui.text(), "conflict compose.yaml", "Apply will refuse and change nothing")
}

func TestApplyFailureRollsBackComposeBase(t *testing.T) {
	requireGitBinary(t)
	edited := strings.Replace(mergeCatalogV1, "restart: always", "restart: unless-stopped", 1)
	f := newMergeFixture(t, mergeCatalogV1, edited, mergeCatalogV2)
	withFakeCompose(t, func(_ string, args ...string) error {
		if strings.Contains(strings.Join(args, " "), "pull") {
			return fmt.Errorf("%w: pull failed", ErrComposeFailed)
		}
		return nil
	})
	if err := f.m.Apply("web", DeployOpts{}); !errors.Is(err, ErrComposeFailed) {
		t.Fatalf("Apply = %v, want the pull failure", err)
	}
	if got := f.read(t, composeBaseFilename); got != mergeCatalogV1 {
		t.Fatalf("base = %q after a failed Apply, want the old catalog file", got)
	}
	if got := f.read(t, "compose.yaml"); got != edited {
		t.Fatalf("compose.yaml = %q after a failed Apply, want the edited file", got)
	}

	// The next Apply still sees the catalog change and merges it.
	withFakeCompose(t, func(string, ...string) error { return nil })
	if err := f.m.Apply("web", DeployOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := f.read(t, "compose.yaml"); !strings.Contains(got, "tailscale:2") || !strings.Contains(got, "unless-stopped") {
		t.Fatalf("compose.yaml = %q, want the merged file", got)
	}
}

func TestApplyFailureRemovesComposeBaseItCreated(t *testing.T) {
	f := newMergeFixture(t, "", mergeCatalogV1, mergeCatalogV2)
	withFakeCompose(t, func(_ string, args ...string) error {
		if strings.Contains(strings.Join(args, " "), "pull") {
			return fmt.Errorf("%w: pull failed", ErrComposeFailed)
		}
		return nil
	})
	if err := f.m.Apply("web", DeployOpts{}); !errors.Is(err, ErrComposeFailed) {
		t.Fatalf("Apply = %v, want the pull failure", err)
	}
	if _, err := os.Lstat(filepath.Join(f.dest, composeBaseFilename)); !os.IsNotExist(err) {
		t.Fatalf("base left behind after a failed Apply: %v", err)
	}
}

func TestApplyWithoutBaseReplacesComposeAndSaysWhere(t *testing.T) {
	edited := strings.Replace(mergeCatalogV1, "app:1", "app:pinned", 1)
	f := newMergeFixture(t, "", edited, mergeCatalogV2)
	withFakeCompose(t, func(string, ...string) error { return nil })
	if err := f.m.Apply("web", DeployOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := f.read(t, "compose.yaml"); got != mergeCatalogV2 {
		t.Fatalf("compose.yaml = %q, want the catalog file", got)
	}
	if got := f.read(t, composeBaseFilename); got != mergeCatalogV2 {
		t.Fatalf("base = %q, want the catalog file", got)
	}
	out := f.ui.text()
	mustContain(t, out, "replaced", "Your previous file is in "+filepath.Join(f.root, config.BackupDirName))
}

func TestApplyWithoutBaseSaysWhenOnlyCommentsDiffer(t *testing.T) {
	commented := "# my note\n" + mergeCatalogV2
	f := newMergeFixture(t, "", commented, mergeCatalogV2)
	withFakeCompose(t, func(string, ...string) error { return nil })
	if err := f.m.Apply("web", DeployOpts{}); err != nil {
		t.Fatal(err)
	}
	mustContain(t, f.ui.text(), "only comments or spacing differ")
	mustNotContain(t, f.ui.text(), "Your previous file is in")
}

func TestApplyDoesNotAddEnvKeyTheOperatorCommentedOut(t *testing.T) {
	f := newMergeFixture(t, "", mergeCatalogV1, mergeCatalogV1)
	writePreviewFile(t, filepath.Join(f.tpl, ".env"), "HOSTNAME=x\n# The optional feature.\nFEATURE=on\n# What NEW does.\nNEW=1\n")
	writePreviewFile(t, filepath.Join(f.dest, ".env"), "HOSTNAME=x\n# FEATURE=on\n")
	withFakeCompose(t, func(string, ...string) error { return nil })
	if err := f.m.Apply("web", DeployOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := f.read(t, ".env"); got != "HOSTNAME=x\n# FEATURE=on\n\n# What NEW does.\nNEW=1\n" {
		t.Fatalf(".env = %q", got)
	}
	out := f.ui.text()
	mustContain(t, out, "  NEW")
	mustNotContain(t, out, "  FEATURE")
}

func TestApplyKeepsDeployedServiceWhenCatalogRenamesIt(t *testing.T) {
	f := newMergeFixture(t, "", mergeCatalogV1, mergeCatalogV1)
	writePreviewFile(t, filepath.Join(f.tpl, ".env"), "SERVICE=newname\n")
	writePreviewFile(t, filepath.Join(f.dest, ".env"), "SERVICE=web\n")
	withFakeCompose(t, func(string, ...string) error { return nil })
	if err := f.m.Apply("web", DeployOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := f.read(t, ".env"); got != "SERVICE=web\n" {
		t.Fatalf(".env = %q, want the deployed SERVICE kept", got)
	}
}

func TestPreviewListsEnvKeysTheCatalogNoLongerHas(t *testing.T) {
	p := newPreviewEnv(t)
	writePreviewFile(t, filepath.Join(p.dest, ".env"), "HOSTNAME=x\nOLD_SECRET=hunter2\n")
	out := p.decline(t)
	mustContain(t, out, "no longer has", "  OLD_SECRET")
	mustNotContain(t, out, "hunter2")
}

func TestPreviewShowsMergedComposeResult(t *testing.T) {
	requireGitBinary(t)
	edited := strings.Replace(mergeCatalogV1, "restart: always", "restart: unless-stopped", 1)
	f := newMergeFixture(t, mergeCatalogV1, edited, mergeCatalogV2)
	f.ui.confirm = false
	_ = f.m.Apply("web", DeployOpts{})
	out := f.ui.text()
	mustContain(t, out, "compared with the merged result (+)", "+     image: tailscale:2")
	// The operator's edit is not shown as removed.
	mustNotContain(t, out, "-     restart: unless-stopped")
}
