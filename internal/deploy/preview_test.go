package deploy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/prompt"
)

// previewUI records every Printf and Confirm in order. When lockPath is set,
// each Printf also tries to take that lock, the way a second Tailarr would.
type previewUI struct {
	confirm  bool
	events   []string
	out      []string
	lockPath string
	lockErrs []error
}

func (u *previewUI) Confirm(string, bool) (bool, error) {
	u.events = append(u.events, "confirm")
	return u.confirm, nil
}

func (u *previewUI) Line(string, string) (string, error) { return "", nil }
func (u *previewUI) Secret(string) (string, error)       { return "", nil }

func (u *previewUI) Printf(format string, args ...any) {
	u.events = append(u.events, "printf")
	u.out = append(u.out, strings.TrimRight(fmt.Sprintf(format, args...), "\n"))
	if u.lockPath != "" {
		l, err := AcquireLock(u.lockPath, 200*time.Millisecond)
		if err != nil {
			u.lockErrs = append(u.lockErrs, err)
			return
		}
		_ = l.Release()
	}
}

func (u *previewUI) text() string { return strings.Join(u.out, "\n") }

// previewEnv is a managed "web" deployment with its catalog template.
type previewEnv struct {
	m    *Manager
	ui   *previewUI
	tpl  string
	dest string
}

func newPreviewEnv(t *testing.T) *previewEnv {
	t.Helper()
	repo, root := t.TempDir(), t.TempDir()
	dest := managedDeploy(t, repo, root)
	ui := &previewUI{}
	return &previewEnv{
		m:    &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: root}, UI: ui},
		ui:   ui,
		tpl:  filepath.Join(repo, "services", "web"),
		dest: dest,
	}
}

func writePreviewFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// decline runs Apply with a declined confirm and returns the printed text.
func (p *previewEnv) decline(t *testing.T) string {
	t.Helper()
	p.ui.confirm = false
	err := p.m.Apply("web", DeployOpts{})
	if !errors.Is(err, prompt.ErrCanceled) {
		t.Fatalf("Apply = %v, want a canceled error", err)
	}
	return p.ui.text()
}

func mustNotContain(t *testing.T, out string, bad ...string) {
	t.Helper()
	for _, b := range bad {
		if strings.Contains(out, b) {
			t.Errorf("output contains %q:\n%s", b, out)
		}
	}
}

func mustContain(t *testing.T, out string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

func TestPreviewNeverPrintsEnvValues(t *testing.T) {
	p := newPreviewEnv(t)
	writePreviewFile(t, filepath.Join(p.dest, ".env"), "HOSTNAME=x\nTS_AUTHKEY=tskey-auth-DEPLOYEDSECRET\nDB_PASSWORD=hunter2\n")
	writePreviewFile(t, filepath.Join(p.tpl, ".env"), "HOSTNAME=y\nTS_AUTHKEY=\nDB_PASSWORD=templatesecret\nNEWVAR=newvalue\n")
	writePreviewFile(t, filepath.Join(p.tpl, "compose.yaml"), "services:\n  app:\n    image: alpine:3\n")
	out := p.decline(t)
	mustContain(t, out, "NEWVAR", "image: alpine:3")
	mustNotContain(t, out, "DEPLOYEDSECRET", "hunter2", "templatesecret", "newvalue", "HOSTNAME=")
}

func TestPreviewListsNewEnvKeysByName(t *testing.T) {
	p := newPreviewEnv(t)
	writePreviewFile(t, filepath.Join(p.tpl, ".env"), "HOSTNAME=x\nADDED_ONE=1\nADDED_TWO=2\n")
	out := p.decline(t)
	mustContain(t, out, "New .env variables", "ADDED_ONE", "ADDED_TWO")
	mustNotContain(t, out, "HOSTNAME", "ADDED_ONE=", "ADDED_TWO=")
}

func TestPreviewHidesInlineComposeSecrets(t *testing.T) {
	p := newPreviewEnv(t)
	writePreviewFile(t, filepath.Join(p.dest, "compose.yaml"), strings.Join([]string{
		"services:",
		"  app:",
		"    environment:",
		"      - TS_AUTHKEY=tskey-auth-oldkey",
		"      - POSTGRES_PASSWORD=oldhunter",
		"      POSTGRES_PASSWORD: oldcolon",
		"      - API_TOKEN = oldspaced",
		"",
	}, "\n"))
	writePreviewFile(t, filepath.Join(p.tpl, "compose.yaml"), strings.Join([]string{
		"services:",
		"  app:",
		"    environment:",
		"      - TS_AUTHKEY=tskey-auth-newkey",
		"      - POSTGRES_PASSWORD=newhunter",
		"      POSTGRES_PASSWORD: newcolon",
		"      - API_TOKEN = newspaced",
		"",
	}, "\n"))
	out := p.decline(t)
	mustContain(t, out, "TS_AUTHKEY", "POSTGRES_PASSWORD", "API_TOKEN", "@@")
	mustNotContain(t, out, "oldkey", "newkey", "oldhunter", "newhunter", "oldcolon", "newcolon", "oldspaced", "newspaced")
}

func TestPreviewMaskSecretCoversSpacedAssignment(t *testing.T) {
	for in, want := range map[string]string{
		"  - POSTGRES_PASSWORD = hunter2": "  - POSTGRES_PASSWORD = [redacted]",
		"  - \"API_TOKEN\" : abc":         "  - \"API_TOKEN\" : [redacted]",
		"  image: alpine:3":               "  image: alpine:3",
		"  KEYBOARD_LAYOUT = us":          "  KEYBOARD_LAYOUT = us",
	} {
		if got := previewMaskSecret(in); got != want {
			t.Errorf("previewMaskSecret(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUpgradingSection(t *testing.T) {
	cases := []struct {
		name, in string
		want     []string
	}{
		{"present", "# T\n\n## Intro\nhello\n\n## Upgrading\n\nRun this.\nThen that.\n\n## Other\nx\n",
			[]string{"## Upgrading", "Run this.", "Then that."}},
		{"absent", "# T\n\n## Intro\nhello\n", nil},
		{"last in file", "## Intro\nx\n## Upgrading\nStep one.\n", []string{"## Upgrading", "Step one."}},
		{"hash inside fence", "## Upgrading\nBefore.\n```sh\n## not a heading\necho hi\n```\nAfter.\n## Next\nno\n",
			[]string{"## Upgrading", "Before.", "```sh", "## not a heading", "echo hi", "```", "After."}},
		{"tilde fence", "## Upgrading\n~~~\n## still inside\n~~~\nDone.\n## Next\n",
			[]string{"## Upgrading", "~~~", "## still inside", "~~~", "Done."}},
		{"subheading stays", "## Upgrading\n### Notes\nkeep\n## Next\n", []string{"## Upgrading", "### Notes", "keep"}},
		{"heading in fence is ignored", "```\n## Upgrading\nfake\n```\n", nil},
		{"empty section", "## Upgrading\n\n## Next\n", []string{"## Upgrading"}},
	}
	for _, c := range cases {
		got := upgradingSection(c.in)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s: upgradingSection = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestPreviewPrintsUpgradingOnlyWhenFilesChange(t *testing.T) {
	p := newPreviewEnv(t)
	readme := "# Web\n\n## Upgrading\nRun the migration first.\n\n## Other\nignored text\n"
	writePreviewFile(t, filepath.Join(p.tpl, "README.md"), readme)
	writePreviewFile(t, filepath.Join(p.dest, "README.md"), readme)
	out := p.decline(t)
	mustContain(t, out, "No template changes")
	mustNotContain(t, out, "migration")

	writePreviewFile(t, filepath.Join(p.tpl, "compose.yaml"), "services: {}\n")
	p.ui.out = nil
	out = p.decline(t)
	mustContain(t, out, "Upgrading notes", "Run the migration first.")
	mustNotContain(t, out, "ignored text")
}

func TestPreviewCapsUpgradingSection(t *testing.T) {
	p := newPreviewEnv(t)
	var b strings.Builder
	b.WriteString("## Upgrading\n")
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&b, "note line %d\n", i)
	}
	writePreviewFile(t, filepath.Join(p.tpl, "README.md"), b.String())
	out := p.decline(t)
	mustContain(t, out, "note line 38", "... 61 more lines")
	mustNotContain(t, out, "note line 39\n", "note line 99")
}

func TestLineDiffChangedAddedRemovedIdentical(t *testing.T) {
	a := []string{"one", "two", "three", "four", "five", "six", "seven", "eight"}
	got, tooLarge := lineDiff(a, a)
	if tooLarge || len(got) != 0 {
		t.Fatalf("identical diff = %q, %v", got, tooLarge)
	}

	got, _ = lineDiff(a, []string{"one", "two", "THREE", "four", "five", "six", "seven", "eight"})
	want := []string{"@@ -1,6 +1,6 @@", "  one", "  two", "- three", "+ THREE", "  four", "  five", "  six"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("changed diff = %q, want %q", got, want)
	}

	got, _ = lineDiff([]string{"a", "b"}, []string{"a", "x", "b"})
	want = []string{"@@ -1,2 +1,3 @@", "  a", "+ x", "  b"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("added diff = %q, want %q", got, want)
	}

	got, _ = lineDiff([]string{"a", "x", "b"}, []string{"a", "b"})
	want = []string{"@@ -1,3 +1,2 @@", "  a", "- x", "  b"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("removed diff = %q, want %q", got, want)
	}

	got, _ = lineDiff(nil, []string{"only"})
	want = []string{"@@ -1,0 +1,1 @@", "+ only"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("from empty diff = %q, want %q", got, want)
	}
}

func TestLineDiffSplitsDistantChanges(t *testing.T) {
	var a, b []string
	for i := 0; i < 30; i++ {
		a = append(a, fmt.Sprintf("line %d", i))
		b = append(b, fmt.Sprintf("line %d", i))
	}
	b[2], b[25] = "changed two", "changed twenty-five"
	got, _ := lineDiff(a, b)
	hunks := 0
	for _, l := range got {
		if strings.HasPrefix(l, "@@") {
			hunks++
		}
	}
	if hunks != 2 {
		t.Fatalf("hunks = %d, want 2:\n%s", hunks, strings.Join(got, "\n"))
	}
	if strings.Contains(strings.Join(got, "\n"), "line 12") {
		t.Errorf("diff holds lines far from a change:\n%s", strings.Join(got, "\n"))
	}
}

func TestLineDiffTooLargeFallsBack(t *testing.T) {
	long := make([]string, previewDiffMaxInput+1)
	for i := range long {
		long[i] = fmt.Sprintf("l%d", i)
	}
	if _, tooLarge := lineDiff(long, nil); !tooLarge {
		t.Error("diff over the line bound was not refused")
	}

	// Two sides with no common lines make a table over the cell bound.
	a := make([]string, 1500)
	b := make([]string, 1500)
	for i := range a {
		a[i] = fmt.Sprintf("a%d", i)
		b[i] = fmt.Sprintf("b%d", i)
	}
	if _, tooLarge := lineDiff(a, b); !tooLarge {
		t.Error("diff over the cell bound was not refused")
	}
	out := previewComposeDiff("compose.yaml", []byte(strings.Join(a, "\n")), []byte(strings.Join(b, "\n")), false)
	mustContain(t, strings.Join(out, "\n"), "too large to diff")
}

func TestPreviewComposeDiffCapsLines(t *testing.T) {
	var a, b []string
	for i := 0; i < 300; i++ {
		a = append(a, fmt.Sprintf("old %d", i))
		b = append(b, fmt.Sprintf("new %d", i))
	}
	out := previewComposeDiff("compose.yaml", []byte(strings.Join(a, "\n")), []byte(strings.Join(b, "\n")), false)
	// Two header lines, the diff cap, and the note.
	if len(out) != 2+previewDiffMaxLines+1 {
		t.Fatalf("printed %d lines, want %d", len(out), 2+previewDiffMaxLines+1)
	}
	last := out[len(out)-1]
	if !strings.HasPrefix(last, "... ") || !strings.HasSuffix(last, " more lines") {
		t.Errorf("last line = %q, want a more-lines note", last)
	}
}

func TestPreviewLabelsDiffDirection(t *testing.T) {
	p := newPreviewEnv(t)
	writePreviewFile(t, filepath.Join(p.dest, "compose.yaml"), "services:\n  app:\n    image: edited-by-operator\n")
	out := p.decline(t)
	mustContain(t, out, "deployed (-)", "catalog (+)", "Lines marked -", "- ")
	mustContain(t, out, "-     image: edited-by-operator", "+     image: alpine:latest")
}

func TestPreviewListsNewChangedAndModeFiles(t *testing.T) {
	p := newPreviewEnv(t)
	writePreviewFile(t, filepath.Join(p.tpl, "extra.txt"), "new file\n")
	writePreviewFile(t, filepath.Join(p.tpl, "conf", "a.conf"), "a\n")
	writePreviewFile(t, filepath.Join(p.dest, "conf", "a.conf"), "old\n")
	writePreviewFile(t, filepath.Join(p.tpl, "run.sh"), "#!/bin/sh\n")
	writePreviewFile(t, filepath.Join(p.dest, "run.sh"), "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(p.tpl, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A file that only the deployment has stays out of the list.
	writePreviewFile(t, filepath.Join(p.dest, "data", "db.sqlite"), "data\n")
	out := p.decline(t)
	mustContain(t, out, "new      extra.txt", filepath.Join("conf", "a.conf"), "mode     run.sh (0644 to 0755)")
	mustContain(t, out, "changed  "+filepath.Join("conf", "a.conf"))
	mustNotContain(t, out, "db.sqlite", "changed  .env", "new      .env")
	// Only the compose file gets a line diff. Other files are listed by name.
	mustNotContain(t, out, "old", "new file")
}

func TestPreviewCapsFileList(t *testing.T) {
	p := newPreviewEnv(t)
	for i := 0; i < 60; i++ {
		writePreviewFile(t, filepath.Join(p.tpl, fmt.Sprintf("f%02d.txt", i)), "x\n")
	}
	out := p.decline(t)
	mustContain(t, out, "f00.txt", "f39.txt", "... 20 more files")
	mustNotContain(t, out, "f59.txt")
}

func TestPreviewComposeRenameIsNotDiffed(t *testing.T) {
	p := newPreviewEnv(t)
	if err := os.Remove(filepath.Join(p.tpl, "compose.yaml")); err != nil {
		t.Fatal(err)
	}
	writePreviewFile(t, filepath.Join(p.tpl, "compose.yml"), "services:\n  other:\n    image: busybox\n")
	out := p.decline(t)
	mustContain(t, out, "Compose file changes from compose.yaml to compose.yml", "new      compose.yml")
	mustNotContain(t, out, "@@", "busybox", "alpine")
}

func TestPreviewNoChangesStillReachesConfirm(t *testing.T) {
	p := newPreviewEnv(t)
	out := p.decline(t)
	mustContain(t, out, "No template changes")
	mustNotContain(t, out, "Template files Apply would write", "@@")
	if got := p.ui.events[len(p.ui.events)-1]; got != "confirm" {
		t.Errorf("last event = %q, want confirm", got)
	}
}

func TestPreviewPrintsBeforeConfirmAndHoldsNoLock(t *testing.T) {
	p := newPreviewEnv(t)
	lockPath, err := ServiceLockPath(p.m.Cfg.DeployPath, "web")
	if err != nil {
		t.Fatal(err)
	}
	p.ui.lockPath = lockPath
	writePreviewFile(t, filepath.Join(p.tpl, "compose.yaml"), "services: {}\n")
	p.decline(t)
	if len(p.ui.events) < 2 {
		t.Fatalf("events = %v, want printf lines then confirm", p.ui.events)
	}
	for i, e := range p.ui.events[:len(p.ui.events)-1] {
		if e != "printf" {
			t.Errorf("event %d = %q, want printf before the confirm", i, e)
		}
	}
	if p.ui.events[len(p.ui.events)-1] != "confirm" {
		t.Errorf("events = %v, want confirm last", p.ui.events)
	}
	if len(p.ui.lockErrs) > 0 {
		t.Fatalf("service lock held during preview: %v", p.ui.lockErrs)
	}
}

func TestPreviewReadFailureStillReachesConfirm(t *testing.T) {
	p := newPreviewEnv(t)
	if err := os.Remove(filepath.Join(p.tpl, "compose.yaml")); err != nil {
		t.Fatal(err)
	}
	out := p.decline(t)
	mustContain(t, out, "Warning: Apply preview is not available")
	if got := p.ui.events[len(p.ui.events)-1]; got != "confirm" {
		t.Errorf("last event = %q, want confirm", got)
	}
}

func TestPreviewTypeMismatchWarnsAndContinues(t *testing.T) {
	p := newPreviewEnv(t)
	writePreviewFile(t, filepath.Join(p.tpl, "conf"), "file\n")
	if err := os.MkdirAll(filepath.Join(p.dest, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := p.decline(t)
	mustContain(t, out, "Warning: template conf is a file but the deployed path is a directory")
}

func TestPreviewDoesNotFollowDeployedSymlinks(t *testing.T) {
	p := newPreviewEnv(t)
	secret := filepath.Join(t.TempDir(), "outside.txt")
	writePreviewFile(t, secret, "SYMLINK-TARGET-CONTENT\n")
	writePreviewFile(t, filepath.Join(p.tpl, "app.conf"), "template\n")
	if err := os.Symlink(secret, filepath.Join(p.dest, "app.conf")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	out := p.decline(t)
	mustContain(t, out, "Warning: deployed app.conf is a symlink")
	mustNotContain(t, out, "SYMLINK-TARGET-CONTENT")
}

func TestPreviewDoesNotFollowSymlinkedDeployedCompose(t *testing.T) {
	p := newPreviewEnv(t)
	secret := filepath.Join(t.TempDir(), "outside.yaml")
	writePreviewFile(t, secret, "services:\n  leak: SYMLINK-TARGET-CONTENT\n")
	if err := os.Remove(filepath.Join(p.dest, "compose.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(p.dest, "compose.yaml")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	out := strings.Join(p.m.previewLines("web", p.dest), "\n")
	mustContain(t, out, "Warning: deployed compose.yaml is a symlink")
	mustNotContain(t, out, "SYMLINK-TARGET-CONTENT", "leak")
}

func TestPreviewDoesNotBlockOnDeployedFIFO(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FIFOs need unix")
	}
	p := newPreviewEnv(t)
	writePreviewFile(t, filepath.Join(p.tpl, "pipe.conf"), "template\n")
	for _, name := range []string{"pipe.conf", "compose.yaml", ".env"} {
		path := filepath.Join(p.dest, name)
		_ = os.Remove(path)
		if err := syscall.Mkfifo(path, 0o600); err != nil {
			t.Skipf("mkfifo unavailable: %v", err)
		}
	}
	done := make(chan []string, 1)
	go func() { done <- p.m.previewLines("web", p.dest) }()
	select {
	case lines := <-done:
		out := strings.Join(lines, "\n")
		mustContain(t, out, "Warning: deployed pipe.conf is not a regular file", "Warning: deployed compose.yaml is not a regular file",
			"Warning: cannot read the deployed .env")
	case <-time.After(5 * time.Second):
		t.Fatal("preview blocked on a FIFO")
	}
}

func TestPreviewStripsEscapeSequences(t *testing.T) {
	p := newPreviewEnv(t)
	writePreviewFile(t, filepath.Join(p.dest, "compose.yaml"), "services:\n  app:\n    image: \x1b[31mred\x1b[0m\x1b]0;evil\x07old\n")
	writePreviewFile(t, filepath.Join(p.tpl, "compose.yaml"), "services:\n  app:\n    image: \x1b[2Jnew\x08\x7f\u202etext\n")
	writePreviewFile(t, filepath.Join(p.tpl, "README.md"), "## Upgrading\n\x1b[1mbold\x1b[0m step\x07\n")
	out := p.decline(t)
	mustContain(t, out, "redold", "newtext", "bold step")
	for _, r := range out {
		if r != '\n' && (r < 0x20 || r == 0x7f || r == '\u202e') {
			t.Fatalf("output holds control character %U:\n%q", r, out)
		}
	}
}

func TestApplyWithoutUIDoesNoPreview(t *testing.T) {
	repo, root := t.TempDir(), t.TempDir()
	managedDeploy(t, repo, root)
	withFakeCompose(t, func(string, ...string) error { return nil })
	calls := 0
	prev := previewFn
	previewFn = func(*Manager, string, string) { calls++ }
	t.Cleanup(func() { previewFn = prev })
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: root}}
	if err := m.Apply("web", DeployOpts{}); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("preview ran %d times without a UI", calls)
	}
}

func TestApplyCallsPreviewOncePerApply(t *testing.T) {
	p := newPreviewEnv(t)
	calls := 0
	prev := previewFn
	previewFn = func(*Manager, string, string) { calls++ }
	t.Cleanup(func() { previewFn = prev })
	p.decline(t)
	if calls != 1 {
		t.Fatalf("preview ran %d times, want 1", calls)
	}
}

func TestApplyAfterPreviewStillOverwritesTemplateFiles(t *testing.T) {
	p := newPreviewEnv(t)
	withFakeCompose(t, func(string, ...string) error { return nil })
	p.ui.confirm = true
	writePreviewFile(t, filepath.Join(p.dest, "compose.yaml"), "services: {}\n")
	if err := p.m.Apply("web", DeployOpts{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(p.dest, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "alpine:latest") {
		t.Errorf("compose.yaml = %q, want the catalog file", data)
	}
}

func TestPreviewComposeModeChangeIsNotReportedAsLineEndings(t *testing.T) {
	p := newPreviewEnv(t)
	compose := composeBaseName(p.tpl)
	if err := os.Chmod(filepath.Join(p.tpl, compose), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(p.dest, compose), 0o644); err != nil {
		t.Fatal(err)
	}
	out := p.decline(t)
	mustContain(t, out, "mode     "+compose+" (0644 to 0640)")
	mustNotContain(t, out, "Only line endings differ", "deployed (-) compared with catalog (+)")
}

func TestPreviewUpgradingLinesDoNotLookLikeComposeProgress(t *testing.T) {
	p := newPreviewEnv(t)
	writePreviewFile(t, filepath.Join(p.tpl, "extra.txt"), "new file\n")
	writePreviewFile(t, filepath.Join(p.tpl, "README.md"), "## Upgrading\n\nVolume data moved.\nVolume data needs a copy.\n")
	out := p.decline(t)
	mustContain(t, out, "  | Volume data moved.", "  | Volume data needs a copy.")
}
