package scaletail

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunGitTimeoutNamesRepoPathAndKillsGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group kill is unix-only")
	}
	dir := t.TempDir()
	repo := filepath.Join(dir, "scaletail")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(dir, "child.pid")
	script := "#!/bin/sh\nsleep 30 &\necho $! > " + strconv.Quote(pidFile) + "\nwait\n"
	git := filepath.Join(dir, "git")
	if err := os.WriteFile(git, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	old := gitOpTimeout
	gitOpTimeout = 200 * time.Millisecond
	t.Cleanup(func() {
		gitOpTimeout = old
		if b, err := os.ReadFile(pidFile); err == nil {
			pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
			if pid > 0 {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})

	_, err := runGit(repo, "-C", repo, "pull", "--ff-only")
	if err == nil {
		t.Fatal("expected timeout")
	}
	if !strings.Contains(err.Error(), repo) {
		t.Fatalf("timeout error %q does not name repo path", err)
	}
	if strings.Contains(err.Error(), "remove /.git") {
		t.Fatalf("timeout error used an empty directory: %q", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var pid int
	for time.Now().Before(deadline) {
		b, readErr := os.ReadFile(pidFile)
		if readErr != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		break
	}
	if pid <= 0 {
		t.Fatal("fake git did not record a child pid")
	}
	time.Sleep(50 * time.Millisecond)
	if processAlive(pid) {
		t.Fatalf("git child %d still alive after timeout", pid)
	}
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil
}

func TestGitEnvDisablesPrompts(t *testing.T) {
	env := strings.Join(gitEnv([]string{"PATH=/bin"}), "\n")
	for _, want := range []string{"GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND=ssh -o BatchMode=yes"} {
		if !strings.Contains(env, want) {
			t.Errorf("gitEnv missing %s:\n%s", want, env)
		}
	}
	custom := strings.Join(gitEnv([]string{"GIT_SSH_COMMAND=ssh -i key"}), "\n")
	if strings.Contains(custom, "BatchMode") {
		t.Errorf("gitEnv must keep an operator GIT_SSH_COMMAND:\n%s", custom)
	}
}

func TestRunGitExplainsRefusedPrompt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed fake git")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"if [ \"$GIT_TERMINAL_PROMPT\" = 0 ]; then\n" +
		"  echo \"fatal: could not read Username for 'https://example.com': terminal prompts disabled\" >&2; exit 128\n" +
		"fi\nsleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	start := time.Now()
	_, err := runGit(dir, "clone", "https://example.com/r.git", filepath.Join(dir, "r"))
	if err == nil || !strings.Contains(err.Error(), "credential helper") {
		t.Fatalf("expected credential hint, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("git waited for a prompt")
	}
}

func TestCheckOriginRefusesOtherRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q", repo}, {"-C", repo, "remote", "add", "origin", "https://example.com/org/old.git"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if err := checkOrigin(repo, "https://example.com/org/old"); err != nil {
		t.Fatalf("same repository refused: %v", err)
	}
	err := checkOrigin(repo, "https://example.com/org/new.git")
	if err == nil || !strings.Contains(err.Error(), "tracks https://example.com/org/old.git") {
		t.Fatalf("expected origin mismatch, got %v", err)
	}
}
