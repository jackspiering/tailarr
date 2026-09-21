package scaletail

import (
	"os"
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
