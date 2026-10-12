package scaletail

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const refreshURL = "https://example.com/org/ScaleTail.git"

// fakeGit puts a git script on PATH that logs every call and answers from
// FAKE_* environment variables. It returns the log path.
func fakeGit(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed fake git")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "git.log")
	script := `#!/bin/sh
echo "$*" >> "$FAKE_GIT_LOG"
while [ "$1" = "-c" ]; do shift 2; done
if [ "$1" = "-C" ]; then shift 2; fi
case "$1" in
clone)
	if [ -n "$FAKE_CLONE_FAIL" ]; then echo "fatal: clone exploded"; exit 128; fi
	for last; do :; done
	mkdir -p "$last/services"
	echo "Cloning into catalog"
	;;
pull)
	if [ -n "$FAKE_PULL_FAIL" ]; then echo "fatal: not fast-forward"; exit 1; fi
	echo "Already up to date."
	;;
remote) echo "$FAKE_ORIGIN" ;;
symbolic-ref) exit "${FAKE_SYMREF_EXIT:-0}" ;;
rev-parse) if [ -n "$FAKE_ORIGIN_HEAD" ]; then echo "$FAKE_ORIGIN_HEAD"; else exit 1; fi ;;
show-ref) case "$*" in *"origin/$FAKE_BRANCH") exit 0 ;; *) exit 1 ;; esac ;;
rev-list) echo "${FAKE_AHEAD:-0}" ;;
checkout) ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_GIT_LOG", logPath)
	t.Setenv("FAKE_ORIGIN", refreshURL)
	return logPath
}

func gitCalls(t *testing.T, logPath string) []string {
	t.Helper()
	b, err := os.ReadFile(logPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func anyCall(calls []string, parts ...string) bool {
	for _, c := range calls {
		ok := true
		for _, p := range parts {
			if !strings.Contains(c, p) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func clonedRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "scaletail")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestRefreshClonesShallowWhenMissing(t *testing.T) {
	logPath := fakeGit(t)
	repo := filepath.Join(t.TempDir(), "scaletail")
	out, err := Refresh(refreshURL, repo)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Cloning into catalog") {
		t.Fatalf("clone output not returned: %q", out)
	}
	calls := gitCalls(t, logPath)
	if len(calls) != 1 || !strings.Contains(calls[0], "clone --depth 1 "+refreshURL+" "+repo) {
		t.Fatalf("unexpected git calls: %v", calls)
	}
	for _, flag := range []string{"protocol.ext.allow=never", "protocol.file.allow=never"} {
		if !strings.Contains(calls[0], flag) {
			t.Errorf("clone is missing hardening flag %s: %s", flag, calls[0])
		}
	}
}

func TestRefreshClonesIntoEmptyDirectory(t *testing.T) {
	logPath := fakeGit(t)
	repo := filepath.Join(t.TempDir(), "scaletail")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Refresh(refreshURL, repo); err != nil {
		t.Fatal(err)
	}
	if !anyCall(gitCalls(t, logPath), "clone --depth 1") {
		t.Fatal("an empty directory must be cloned into")
	}
}

func TestRefreshReportsCloneFailureOutput(t *testing.T) {
	fakeGit(t)
	t.Setenv("FAKE_CLONE_FAIL", "1")
	_, err := Refresh(refreshURL, filepath.Join(t.TempDir(), "scaletail"))
	if err == nil || !strings.Contains(err.Error(), "clone failed") || !strings.Contains(err.Error(), "clone exploded") {
		t.Fatalf("expected clone failure with git output, got %v", err)
	}
}

func TestRefreshPullsExistingCloneFastForwardOnly(t *testing.T) {
	logPath := fakeGit(t)
	repo := clonedRepo(t)
	out, err := Refresh(refreshURL, repo)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Already up to date.") {
		t.Fatalf("pull output not returned: %q", out)
	}
	calls := gitCalls(t, logPath)
	if !anyCall(calls, "pull --ff-only") {
		t.Fatalf("expected pull --ff-only, got %v", calls)
	}
	if anyCall(calls, "clone") || anyCall(calls, "checkout") {
		t.Fatalf("an existing clone on a branch must only pull: %v", calls)
	}
}

func TestRefreshRefusesPullFromOtherRepository(t *testing.T) {
	logPath := fakeGit(t)
	t.Setenv("FAKE_ORIGIN", "https://example.com/other/Repo.git")
	if _, err := Refresh(refreshURL, clonedRepo(t)); err == nil || !strings.Contains(err.Error(), "tracks") {
		t.Fatalf("expected an origin mismatch error, got %v", err)
	}
	if anyCall(gitCalls(t, logPath), "pull") {
		t.Fatal("pull ran against the wrong origin")
	}
}

func TestRefreshReportsPullFailureOutput(t *testing.T) {
	fakeGit(t)
	t.Setenv("FAKE_PULL_FAIL", "1")
	if _, err := Refresh(refreshURL, clonedRepo(t)); err == nil || !strings.Contains(err.Error(), "not fast-forward") {
		t.Fatalf("expected pull failure with git output, got %v", err)
	}
}

func TestRefreshUsesLocalTreeWithoutGitDirectory(t *testing.T) {
	logPath := fakeGit(t)
	repo := filepath.Join(t.TempDir(), "scaletail")
	if err := os.MkdirAll(filepath.Join(repo, "services"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := Refresh(refreshURL, repo)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "Using local") {
		t.Fatalf("unexpected result %q", out)
	}
	if calls := gitCalls(t, logPath); len(calls) != 0 {
		t.Fatalf("a local tree must not run git: %v", calls)
	}
}

func TestRefreshRefusesNonEmptyDirectoryThatIsNotACatalog(t *testing.T) {
	fakeGit(t)
	repo := filepath.Join(t.TempDir(), "scaletail")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "notes.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Refresh(refreshURL, repo); err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("expected a refusal, got %v", err)
	}
}

func TestRefreshRefusesSymlinkedRepoPath(t *testing.T) {
	fakeGit(t)
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "scaletail")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Refresh(refreshURL, link); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected a symlink refusal, got %v", err)
	}
}

func TestEnsureOnBranchLeavesDetachedHead(t *testing.T) {
	logPath := fakeGit(t)
	t.Setenv("FAKE_SYMREF_EXIT", "1")
	t.Setenv("FAKE_ORIGIN_HEAD", "origin/main")
	if err := ensureOnBranch(clonedRepo(t)); err != nil {
		t.Fatal(err)
	}
	if !anyCall(gitCalls(t, logPath), "checkout -B main origin/main") {
		t.Fatalf("expected a checkout of main, got %v", gitCalls(t, logPath))
	}
}

func TestEnsureOnBranchRefusesToAbandonCommits(t *testing.T) {
	logPath := fakeGit(t)
	t.Setenv("FAKE_SYMREF_EXIT", "1")
	t.Setenv("FAKE_ORIGIN_HEAD", "origin/main")
	t.Setenv("FAKE_AHEAD", "2")
	err := ensureOnBranch(clonedRepo(t))
	if err == nil || !strings.Contains(err.Error(), "2 detached commit(s)") {
		t.Fatalf("expected a detached commits refusal, got %v", err)
	}
	if anyCall(gitCalls(t, logPath), "checkout") {
		t.Fatal("checkout ran although commits would be abandoned")
	}
}

func TestEnsureOnBranchFallsBackToMaster(t *testing.T) {
	logPath := fakeGit(t)
	t.Setenv("FAKE_SYMREF_EXIT", "1")
	t.Setenv("FAKE_BRANCH", "master")
	if err := ensureOnBranch(clonedRepo(t)); err != nil {
		t.Fatal(err)
	}
	if !anyCall(gitCalls(t, logPath), "checkout -B master origin/master") {
		t.Fatalf("expected a checkout of master, got %v", gitCalls(t, logPath))
	}
}

func TestEnsureOnBranchFailsWithoutDefaultBranch(t *testing.T) {
	fakeGit(t)
	t.Setenv("FAKE_SYMREF_EXIT", "1")
	t.Setenv("FAKE_BRANCH", "none")
	if err := ensureOnBranch(clonedRepo(t)); err == nil || !strings.Contains(err.Error(), "no default branch") {
		t.Fatalf("expected a no default branch error, got %v", err)
	}
}

func TestSameRepoURL(t *testing.T) {
	rows := []struct {
		a, b string
		want bool
	}{
		{"https://github.com/o/r.git", "https://github.com/o/r", true},
		{"https://github.com/o/r/", "https://github.com/o/r.git", true},
		{"HTTPS://GitHub.com/O/R", "https://github.com/o/r", true},
		{"https://github.com/o/r", "https://github.com/o/other", false},
		{"git@github.com:o/r.git", "https://github.com/o/r.git", false},
	}
	for _, r := range rows {
		if got := sameRepoURL(r.a, r.b); got != r.want {
			t.Errorf("sameRepoURL(%q, %q) = %v, want %v", r.a, r.b, got, r.want)
		}
	}
}
