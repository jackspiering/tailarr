package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/interrupt"
	"github.com/jackspiering/tailarr/internal/logging"
)

func TestBackupPathForCollision(t *testing.T) {
	root := t.TempDir()
	stamp := "20200101T000000Z"
	first, err := backupPathFor(root, "web", stamp)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(first, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "web-"+stamp+"-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	next, err := backupPathFor(root, "web", stamp)
	if err != nil {
		t.Fatal(err)
	}
	if next != filepath.Join(root, "web-"+stamp+"-2") {
		t.Fatalf("got %s", next)
	}
}

func TestBackupPathForAbortsOnStatError(t *testing.T) {
	// A regular file as the "root" makes Lstat fail with ENOTDIR for every
	// candidate, which must surface as an error instead of looping forever.
	fileRoot := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(fileRoot, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := backupPathFor(fileRoot, "web", "20200101T000000Z"); err == nil {
		t.Fatal("expected an error when candidate inspection fails")
	}
}

func TestBackupAndRestore(t *testing.T) {
	deployRoot := t.TempDir()
	svc := filepath.Join(deployRoot, "demo")
	if err := os.MkdirAll(filepath.Join(svc, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svc, "compose.yaml"), []byte("x:1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svc, ".env"), []byte("TS_AUTHKEY=tskey-auth-x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svc, "data", "file"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	backup, err := Backup(deployRoot, "demo", svc)
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" {
		t.Fatal("empty backup")
	}
	if _, err := os.Stat(filepath.Join(svc, "data", "file")); err != nil {
		t.Fatal("copy backup must leave the deployment in place")
	}
	data, err := os.ReadFile(filepath.Join(backup, "data", "file"))
	if err != nil || string(data) != "keep\n" {
		t.Fatalf("backup copy failed: %v %q", err, data)
	}
	if _, err := os.Stat(filepath.Join(backup, ".env")); err != nil {
		t.Fatal("backup copy must include .env")
	}
}

func TestLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.lock")
	l1, err := AcquireLock(path, DefaultLockTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if err := l1.Release(); err != nil {
		t.Fatal(err)
	}
	l2, err := AcquireLock(path, DefaultLockTimeout)
	if err != nil {
		t.Fatal(err)
	}
	_ = l2.Release()
}

func TestLockReleaseDoesNotSteal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.lock")
	l1, err := AcquireLock(path, DefaultLockTimeout)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate another process rewriting the lock with different token after l1 closed file.
	// We write a different owner without releasing l1's in-memory token.
	if err := os.WriteFile(path, []byte("99999\nother-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Release should not remove a lock it no longer owns.
	if err := l1.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("lock should still exist when ownership does not match")
	}
}

func TestAcquireLockBacksOffWhenReclaimedDuringCreate(t *testing.T) {
	if !flockAvailable() {
		t.Skip("flock is not available on this platform")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "t.lock")
	var rival *os.File
	prev := afterLockCreate
	t.Cleanup(func() {
		afterLockCreate = prev
		if rival != nil {
			releaseFlock(rival)
			_ = rival.Close()
		}
	})
	// A rival opens the still-empty file and flocks it before the creator.
	afterLockCreate = func(p string) {
		afterLockCreate = prev
		f, err := os.OpenFile(p, os.O_RDWR, 0)
		if err != nil {
			t.Error(err)
			return
		}
		rival = f
		if !tryFlock(f) {
			t.Error("rival could not flock the new lock file")
			return
		}
		if err := writeLockIdentity(f, os.Getpid(), "rival-token"); err != nil {
			t.Error(err)
		}
	}
	if l, err := AcquireLock(path, 300*time.Millisecond); err == nil {
		_ = l.Release()
		t.Fatal("creator must back off when a rival holds the flock")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "rival-token") {
		t.Fatalf("creator overwrote the rival's identity: %q", data)
	}
}

// deadPID returns a PID that is guaranteed no longer running.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	return pid
}

func TestAcquireLockReclaimsDeadPID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.lock")
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\nstale-token\n", deadPID(t))), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := AcquireLock(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = l.Release()
}

func TestAcquireLockReclaimsReusedPID(t *testing.T) {
	// PID reuse: the recorded owner PID is alive but belongs to an unrelated
	// process, so the flock-verified free lock must be reclaimable. Requires
	// /proc/<pid>/comm (Linux).
	if runtime.GOOS != "linux" {
		t.Skip("pid/comm detection is Linux-only; other platforms stay conservative")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "t.lock")
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\nreused-token\n", cmd.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := AcquireLock(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = l.Release()
}

func TestAcquireLockKeepsLiveTailarrOwner(t *testing.T) {
	// The test binary itself stands in for a live Tailarr process: its comm
	// matches os.Executable(), so the lock must not be stolen.
	if runtime.GOOS != "linux" {
		t.Skip("pid/comm detection is Linux-only; other platforms stay conservative")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "t.lock")
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\nlive-token\n", os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireLock(path, time.Second); err == nil {
		t.Fatal("live Tailarr owner lock must not be stolen")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("lock file should remain")
	}
}

func TestStaleLockReclaimedForDeadPID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.lock")
	// A fresh lock whose owner is gone must be reclaimed immediately,
	// regardless of age (previously a dead-owner lock was only removed after
	// 2*timeout, making the next run fail once).
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\nstale-token\n", deadPID(t))), 0o600); err != nil {
		t.Fatal(err)
	}
	if !tryRemoveStaleLock(path, DefaultLockTimeout) {
		t.Fatal("expected fresh dead-PID lock to be reclaimed")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("lock file should be removed")
	}
}

func TestStaleLockKeepsLiveOwner(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.lock")
	// A live owner must never have its lock stolen, even when fresh.
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\nlive-token\n", os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if tryRemoveStaleLock(path, DefaultLockTimeout) {
		t.Fatal("live owner lock must not be stolen")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("lock file should remain")
	}
}

func TestStaleLockUnparseableOnlyWhenOld(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.lock")
	// Corrupt (unparseable) content is only reclaimed once older than maxAge.
	if err := os.WriteFile(path, []byte("not-a-pid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if tryRemoveStaleLock(path, DefaultLockTimeout) {
		t.Fatal("fresh corrupt lock must not be reclaimed")
	}
	old := time.Now().Add(-2 * DefaultLockTimeout)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if !tryRemoveStaleLock(path, DefaultLockTimeout) {
		t.Fatal("old corrupt lock should be reclaimed")
	}
}

func TestCopyTemplateAndOverride(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	svcTpl := filepath.Join(repo, "services", "app")
	if err := os.MkdirAll(svcTpl, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svcTpl, "compose.yaml"), []byte("services:\n  app:\n    image: alpine:latest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svcTpl, ".env"), []byte("HOSTNAME=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(deployRoot, "app")
	if err := copyTemplate(svcTpl, dest); err != nil {
		t.Fatal(err)
	}
	if err := writeOverride("svc", dest); err != nil {
		t.Fatal(err)
	}
	if !IsManaged(dest) {
		t.Fatal("expected managed marker")
	}
	if _, err := os.Stat(filepath.Join(dest, "compose.yaml")); err != nil {
		t.Fatal(err)
	}
	_ = config.Default()
	_ = logging.New(filepath.Join(t.TempDir(), "t.log"), 1024)
}

func TestSafeRemoveTree(t *testing.T) {
	root := t.TempDir()
	svc := filepath.Join(root, "s")
	if err := os.MkdirAll(filepath.Join(svc, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := safeRemoveTree(svc, root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(svc); !os.IsNotExist(err) {
		t.Fatal("not removed")
	}
}

func TestSafeRemoveTreeRefusesPathThroughSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "s")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := safeRemoveTree(filepath.Join(root, "link", "s"), root); err == nil {
		t.Fatal("expected refusal for a path that resolves outside root")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("directory outside root was removed: %v", err)
	}
}

func TestServiceLockPath(t *testing.T) {
	p, err := ServiceLockPath("/opt/docker/stacks", "web")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p, config.LockDirName) {
		t.Fatalf("expected locks under deploy path: %s", p)
	}
	if filepath.Base(p) != "web.lock" {
		t.Fatal(p)
	}
	// Parent of deploy is NOT used (avoids /.tailarr_locks when deploy is /stacks).
	if strings.HasPrefix(p, string(os.PathSeparator)+config.LockDirName) && !strings.Contains(p, "stacks") {
		t.Fatalf("lock escaped deploy path: %s", p)
	}
	if _, err := ServiceLockPath("/x", "../y"); err == nil {
		t.Fatal("expected invalid name")
	}
}

func TestProjectNameDistinctPerRoot(t *testing.T) {
	a := ProjectName("/opt/docker/stacks", "web")
	b := ProjectName("/var/stacks", "web")
	if a == b {
		t.Fatalf("project names collided: %s", a)
	}
	if !strings.Contains(a, "web") || !strings.HasPrefix(a, "tailarr") {
		t.Fatalf("unexpected name %s", a)
	}
	long := strings.Repeat("s", 64)
	rootA := "/very/long/path/that/ends/with/docker/stacks"
	rootB := "/other/long/path/that/ends/with/docker/stacks"
	if ProjectName(rootA, long) == ProjectName(rootB, long) {
		t.Fatal("long service name dropped deploy-root fingerprint")
	}
}

func withFakeCompose(t *testing.T, fn func(dir string, args ...string) error) {
	t.Helper()
	prev := composeFn
	composeFn = func(_ context.Context, dir string, args ...string) error { return fn(dir, args...) }
	t.Cleanup(func() { composeFn = prev })
}

func setupTemplate(t *testing.T, repo, name string, env string) string {
	t.Helper()
	dir := filepath.Join(repo, "services", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:\n  app:\n    image: alpine:latest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRemoveFailsClosedOnComposeError(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	setupTemplate(t, repo, "web", "HOSTNAME=x\n")

	// Create a managed deployment without going through compose up.
	dest := filepath.Join(deployRoot, "web")
	if err := copyTemplate(filepath.Join(repo, "services", "web"), dest); err != nil {
		t.Fatal(err)
	}
	if err := writeOverride("svc", dest); err != nil {
		t.Fatal(err)
	}

	withFakeCompose(t, func(dir string, args ...string) error {
		return fmt.Errorf("%w: simulated down failure", ErrComposeFailed)
	})

	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	err := m.Remove("web")
	if err == nil {
		t.Fatal("expected remove to fail when compose down fails")
	}
	if !errors.Is(err, ErrComposeFailed) && !strings.Contains(err.Error(), "left intact") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatal("deployment directory must remain after failed remove")
	}
}

func TestRemoveRejectsUnmanaged(t *testing.T) {
	deployRoot := t.TempDir()
	dest := filepath.Join(deployRoot, "manual")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// No Tailarr marker.
	m := &Manager{Cfg: &config.Config{DeployPath: deployRoot}}
	err := m.Remove("manual")
	if !errors.Is(err, ErrNotManaged) {
		t.Fatalf("expected ErrNotManaged, got %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatal("unmanaged dir must not be deleted")
	}
}

func TestDeployRejectsExistingManaged(t *testing.T) {
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

	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	err := m.Deploy("web", DeployOpts{})
	if !errors.Is(err, ErrAlreadyDeployed) {
		t.Fatalf("expected ErrAlreadyDeployed, got %v", err)
	}
	if !strings.Contains(err.Error(), "use Apply") {
		t.Fatalf("expected Apply hint, got %v", err)
	}
}

func TestApplyRejectsMissingDest(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	setupTemplate(t, repo, "web", "HOSTNAME=x\n")
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	err := m.Apply("web", DeployOpts{})
	if !errors.Is(err, ErrNotDeployed) {
		t.Fatalf("expected ErrNotDeployed, got %v", err)
	}
	if !strings.Contains(err.Error(), "use Deploy") {
		t.Fatalf("expected Deploy hint, got %v", err)
	}
}

func TestApplyRejectsUnmanaged(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	setupTemplate(t, repo, "web", "HOSTNAME=x\n")
	dest := filepath.Join(deployRoot, "web")
	if err := copyTemplate(filepath.Join(repo, "services", "web"), dest); err != nil {
		t.Fatal(err)
	}
	// No Tailarr marker.
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	err := m.Apply("web", DeployOpts{})
	if !errors.Is(err, ErrNotManaged) {
		t.Fatalf("expected ErrNotManaged, got %v", err)
	}
}

func TestApplyPreservesEnvAndDestOnlyFiles(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	setupTemplate(t, repo, "web", "TS_AUTHKEY=\nHOSTNAME=template\nNEWKEY=\n")

	dest := filepath.Join(deployRoot, "web")
	if err := copyTemplate(filepath.Join(repo, "services", "web"), dest); err != nil {
		t.Fatal(err)
	}
	if err := writeOverride("web", dest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, ".env"), []byte("TS_AUTHKEY=tskey-auth-SECRET\nHOSTNAME=local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dest, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "data", "keep"), []byte("yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "custom.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "services", "web", "compose.yaml"), []byte("services:\n  app:\n    image: alpine:new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "services", "web", "extra.conf"), []byte("from-template\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var sawPull, sawDown bool
	var upCount atomic.Int32
	withFakeCompose(t, func(dir string, args ...string) error {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "pull") {
			sawPull = true
		}
		if strings.Contains(joined, "down") {
			sawDown = true
		}
		if strings.Contains(joined, "up") {
			upCount.Add(1)
		}
		return nil
	})

	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	if err := m.Apply("web", DeployOpts{}); err != nil {
		t.Fatal(err)
	}
	if !sawPull {
		t.Fatal("expected compose pull")
	}
	if sawDown {
		t.Fatal("apply must not compose down")
	}
	if upCount.Load() < 1 {
		t.Fatal("expected compose up")
	}
	env, err := os.ReadFile(filepath.Join(dest, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), "TS_AUTHKEY=tskey-auth-SECRET") {
		t.Fatalf("secret lost on apply: %s", env)
	}
	if !strings.Contains(string(env), "HOSTNAME=local") {
		t.Fatalf("local env lost: %s", env)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "data", "keep")); err != nil || string(data) != "yes\n" {
		t.Fatalf("dest-only data lost: %v %q", err, data)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "custom.txt")); err != nil || string(data) != "mine\n" {
		t.Fatalf("dest-only file lost: %v %q", err, data)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "compose.yaml")); err != nil || !strings.Contains(string(data), "alpine:new") {
		t.Fatalf("template compose not synced: %v %q", err, data)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "extra.conf")); err != nil || string(data) != "from-template\n" {
		t.Fatalf("new template file not copied: %v %q", err, data)
	}
}

func TestApplyRestoresOnInterrupt(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	setupTemplate(t, repo, "web", "TS_AUTHKEY=tskey-auth-from-template\nHOSTNAME=t\n")

	dest := filepath.Join(deployRoot, "web")
	if err := copyTemplate(filepath.Join(repo, "services", "web"), dest); err != nil {
		t.Fatal(err)
	}
	if err := writeOverride("web", dest); err != nil {
		t.Fatal(err)
	}
	marker := "ORIGINAL-DEPLOYMENT"
	if err := os.WriteFile(filepath.Join(dest, "marker.txt"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	origCompose := "services:\n  app:\n    image: original\n"
	if err := os.WriteFile(filepath.Join(dest, "compose.yaml"), []byte(origCompose), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "services", "web", "compose.yaml"), []byte("services:\n  app:\n    image: new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	withFakeCompose(t, func(dir string, args ...string) error {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "up") {
			return fmt.Errorf("%w: canceled", ErrInterrupted)
		}
		return nil
	})

	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	err := m.Apply("web", DeployOpts{})
	if err == nil {
		t.Fatal("expected apply failure")
	}
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("expected ErrInterrupted, got %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "marker.txt"))
	if err != nil || string(data) != marker {
		t.Fatalf("previous deployment not restored after interrupt: %v %q", err, data)
	}
	compose, err := os.ReadFile(filepath.Join(dest, "compose.yaml"))
	if err != nil || string(compose) != origCompose {
		t.Fatalf("compose not restored: %v %q", err, compose)
	}
}

func TestApplyRestoresOnComposeUpFailure(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	setupTemplate(t, repo, "web", "TS_AUTHKEY=tskey-auth-from-template\nHOSTNAME=t\n")

	dest := filepath.Join(deployRoot, "web")
	if err := copyTemplate(filepath.Join(repo, "services", "web"), dest); err != nil {
		t.Fatal(err)
	}
	if err := writeOverride("web", dest); err != nil {
		t.Fatal(err)
	}
	marker := "ORIGINAL-DEPLOYMENT"
	if err := os.WriteFile(filepath.Join(dest, "marker.txt"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, ".env"), []byte("TS_AUTHKEY=tskey-auth-OLD\nHOSTNAME=old\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	withFakeCompose(t, func(dir string, args ...string) error {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "up") {
			return fmt.Errorf("%w: up failed", ErrComposeFailed)
		}
		return nil
	})

	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	err := m.Apply("web", DeployOpts{})
	if err == nil {
		t.Fatal("expected apply failure")
	}
	data, err := os.ReadFile(filepath.Join(dest, "marker.txt"))
	if err != nil || string(data) != marker {
		t.Fatalf("previous deployment not restored: %v %q", err, data)
	}
	env, err := os.ReadFile(filepath.Join(dest, ".env"))
	if err != nil || !strings.Contains(string(env), "tskey-auth-OLD") {
		t.Fatalf("old env not restored: %v %q", err, env)
	}
}

func TestApplyRestoresAfterTypeMismatch(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	setupTemplate(t, repo, "web", "HOSTNAME=x\n")

	dest := filepath.Join(deployRoot, "web")
	if err := os.MkdirAll(filepath.Join(dest, "compose.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "compose.yaml", "old-marker"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := "services:\n  app:\n    image: old\n"
	if err := os.WriteFile(filepath.Join(dest, "compose.yml"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, ".env"), []byte("HOSTNAME=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeOverride("web", dest); err != nil {
		t.Fatal(err)
	}

	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	if err := m.Apply("web", DeployOpts{}); err == nil {
		t.Fatal("expected type mismatch")
	}
	marker, err := os.ReadFile(filepath.Join(dest, "compose.yaml", "old-marker"))
	if err != nil || string(marker) != "old\n" {
		t.Fatalf("old compose directory was not restored: %v %q", err, marker)
	}
	data, err := os.ReadFile(filepath.Join(dest, "compose.yml"))
	if err != nil || string(data) != old {
		t.Fatalf("old compose.yml was not restored: %v %q", err, data)
	}
}

func TestApplyKeepsVanishedTemplateFile(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()

	tpl := filepath.Join(repo, "services", "web")
	if err := os.MkdirAll(tpl, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tpl, "compose.yml"), []byte("services:\n  app:\n    image: new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tpl, ".env"), []byte("HOSTNAME=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(deployRoot, "web")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	old := "services:\n  app:\n    image: old\n"
	if err := os.WriteFile(filepath.Join(dest, "compose.yaml"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, ".env"), []byte("HOSTNAME=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeOverride("web", dest); err != nil {
		t.Fatal(err)
	}

	var used []string
	withFakeCompose(t, func(dir string, args ...string) error {
		used = append(used, strings.Join(args, " "))
		return nil
	})

	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	if err := m.Apply("web", DeployOpts{}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "compose.yaml")); err != nil || string(data) != old {
		t.Fatalf("dest-only compose.yaml must stay: %v %q", err, data)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "compose.yml")); err != nil || !strings.Contains(string(data), "image: new") {
		t.Fatalf("template compose.yml not installed: %v %q", err, data)
	}
	joined := strings.Join(used, "\n")
	if !strings.Contains(joined, "-f compose.yml pull") {
		t.Fatalf("apply must pull the template compose file, got %q", joined)
	}
	if !strings.Contains(joined, "-f compose.yml -f "+overrideFilename+" up") {
		t.Fatalf("apply must up the template compose file, got %q", joined)
	}
}

func TestApplyFailsWhenTemplateMissing(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	dest := filepath.Join(deployRoot, "web")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	composeBody := "services:\n  app:\n    image: original\n"
	if err := os.WriteFile(filepath.Join(dest, "compose.yaml"), []byte(composeBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, ".env"), []byte("HOSTNAME=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeOverride("web", dest); err != nil {
		t.Fatal(err)
	}

	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	if err := m.Apply("web", DeployOpts{}); err == nil {
		t.Fatal("expected apply to fail when the template is missing")
	}
	data, err := os.ReadFile(filepath.Join(dest, "compose.yaml"))
	if err != nil || string(data) != composeBody {
		t.Fatalf("compose file must survive a failed apply: %v %q", err, data)
	}
}

// healthFromOutput returns the health of each service in raw `docker ps -a`
// output, the way CollectOverview classifies it.
func healthFromOutput(raw string, services []string) map[string]Health {
	rows := parsePS(raw)
	out := make(map[string]Health, len(services))
	for _, s := range services {
		_, out[s] = serviceContainers(rows, s)
	}
	return out
}

func TestHealthFromOutput(t *testing.T) {
	raw := strings.Join([]string{
		"app-web\trunning\tUp 2 hours (healthy)\t",
		"tailscale-web\trunning\tUp 2 hours\t",
		"other-container\trunning\tUp (health: starting)\tapi",
		"app-api\texited\tExited (0) 1 hour ago\t",
	}, "\n")
	got := healthFromOutput(raw, []string{"web", "api", "none"})
	// Worst container wins: the tailscale sidecar has no healthcheck, so the
	// service reports running/no-healthcheck rather than healthy.
	if got["web"] != HealthRunning {
		t.Fatalf("web: got %s", got["web"])
	}
	// api has a starting container and an exited one; exited is worse.
	if got["api"] != HealthExited {
		t.Fatalf("api: got %s", got["api"])
	}
	// Service with no matching container is stopped, not unknown.
	if got["none"] != HealthStopped {
		t.Fatalf("none: got %s", got["none"])
	}
}

func TestHealthFromOutputHealthy(t *testing.T) {
	raw := strings.Join([]string{
		"app-web\trunning\tUp 2 hours (healthy)\t",
	}, "\n")
	got := healthFromOutput(raw, []string{"web"})
	if got["web"] != HealthHealthy {
		t.Fatalf("web: got %s", got["web"])
	}
}

func TestHealthFromOutputUnhealthyWins(t *testing.T) {
	raw := strings.Join([]string{
		"app-web\trunning\tUp (healthy)\t",
		"tailscale-web\trunning\tUp (unhealthy)\t",
	}, "\n")
	got := healthFromOutput(raw, []string{"web"})
	if got["web"] != HealthUnhealthy {
		t.Fatalf("worst health should win: got %s", got["web"])
	}
}
func TestDockerStatusCommandsTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a shell-backed fake docker executable")
	}
	dir := t.TempDir()
	docker := filepath.Join(dir, "docker")
	if err := os.WriteFile(docker, []byte("#!/bin/sh\nexec sleep 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	oldTimeout := probeTimeout
	probeTimeout = 10 * time.Millisecond
	t.Cleanup(func() { probeTimeout = oldTimeout })

	deployRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(deployRoot, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployRoot, "web", "compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := CollectOverview(deployRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.DockerErr, "timed out") || len(st.Services) != 1 || st.Services[0].Health != HealthUnknown {
		t.Fatalf("timed-out docker ps must mark health unknown: %+v", st)
	}
}

func TestContainerMatchesService(t *testing.T) {
	if !containerMatchesService("app-web", "", "web") {
		t.Fatal("app-web should match web")
	}
	if containerMatchesService("app-web-ui", "", "web") {
		t.Fatal("app-web-ui must not match web")
	}
	if !containerMatchesService("other", "web", "web") {
		t.Fatal("tailarr.service label should match")
	}
}

func TestDeployRejectsEmptyAuthkey(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	setupTemplate(t, repo, "web", "TS_AUTHKEY=\nHOSTNAME=x\n")

	withFakeCompose(t, func(dir string, args ...string) error {
		return nil
	})

	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot, AuthkeysPath: filepath.Join(deployRoot, "keys")}}
	err := m.Deploy("web", DeployOpts{})
	if !errors.Is(err, ErrEmptyAuthkey) {
		t.Fatalf("expected ErrEmptyAuthkey, got %v", err)
	}
}

func TestScanComposeServiceNames(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "compose.yaml")
	body := "services:\n  web:\n    image: x\n    ports:\n      - \"80:80\"\n    environment:\n      FOO: bar\n  api:\n    image: y\n    volumes:\n      - data:/data\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	names, err := scanComposeServiceNames(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "web" || names[1] != "api" {
		t.Fatalf("%v", names)
	}
	for _, n := range names {
		if n == "ports" || n == "environment" || n == "volumes" || n == "image" {
			t.Fatalf("nested key treated as service: %v", names)
		}
	}
}

func TestIsManagedRequiresStructuredMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, overrideFilename)

	if IsManaged(dir) {
		t.Fatal("missing override must not be managed")
	}

	if err := os.WriteFile(path, []byte("x: tailarr.managed-notreally\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if IsManaged(dir) {
		t.Fatal("substring lookalike must not be managed")
	}

	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:\n  app:\n    image: alpine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeOverride("demo", dir); err != nil {
		t.Fatal(err)
	}
	if !IsManaged(dir) {
		t.Fatal("writeOverrideUsing output must be managed")
	}

	markerDir := t.TempDir()
	if err := writeMarkerOnly(markerDir); err != nil {
		t.Fatal(err)
	}
	if !IsManaged(markerDir) {
		t.Fatal("writeMarkerOnly output must be managed")
	}
}

func TestWriteOverrideLabels(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:\n  app:\n    image: alpine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeOverride("demo", dir); err != nil {
		t.Fatal(err)
	}
	if !IsManaged(dir) {
		t.Fatal("not managed")
	}
	data, _ := os.ReadFile(filepath.Join(dir, overrideFilename))
	if !strings.Contains(string(data), "tailarr.managed") {
		t.Fatalf("%s", data)
	}
}

func TestWriteOverrideSkipsInvalidServiceNames(t *testing.T) {
	dir := t.TempDir()
	// A service name the YAML scan fallback misreads as a key: the emitted
	// override must never contain invalid YAML.
	body := "services:\n  bad\"name:\n    image: alpine\n"
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeOverride("demo", dir); err != nil {
		t.Fatal(err)
	}
	if !IsManaged(dir) {
		t.Fatal("managed marker must be written even when all names are invalid")
	}
	data, err := os.ReadFile(filepath.Join(dir, overrideFilename))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `bad"name`) {
		t.Fatalf("invalid service name leaked into override: %s", data)
	}
}

func TestPruneBackups(t *testing.T) {
	root := t.TempDir()
	backupDir := filepath.Join(root, config.BackupDirName)
	// Include collision-suffixed entries (same second stamp) and a second
	// service that must be left untouched.
	names := []string{
		"web-20200101T000000Z",
		"web-20200101T000000Z-1",
		"web-20200101T000000Z-2",
		"web-20200102T000000Z",
		"api-20200101T000000Z",
	}
	for _, name := range names {
		if err := os.MkdirAll(filepath.Join(backupDir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := pruneBackups(backupDir, "web", 2); err != nil {
		t.Fatal(err)
	}
	// Newest two (string order) survive: the -2 collision and the later stamp.
	for _, gone := range []string{"web-20200101T000000Z", "web-20200101T000000Z-1"} {
		if _, err := os.Stat(filepath.Join(backupDir, gone)); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be pruned", gone)
		}
	}
	for _, kept := range []string{"web-20200101T000000Z-2", "web-20200102T000000Z"} {
		if _, err := os.Stat(filepath.Join(backupDir, kept)); err != nil {
			t.Fatalf("expected %s to be kept: %v", kept, err)
		}
	}
	// Other services' backups are not pruned.
	if _, err := os.Stat(filepath.Join(backupDir, "api-20200101T000000Z")); err != nil {
		t.Fatalf("other service backup pruned: %v", err)
	}
}

func TestBackupNameDoesNotCollideWithHyphenPrefix(t *testing.T) {
	if isServiceBackupName("web", "web-ui-20200101T000000Z") {
		t.Fatal("web-ui backup must not match service web")
	}
	if !isServiceBackupName("web-ui", "web-ui-20200101T000000Z") {
		t.Fatal("exact web-ui backup should match")
	}
	if !isServiceBackupName("web", "web-20200101T000000Z-2") {
		t.Fatal("collision suffix should match")
	}

	root := t.TempDir()
	backupDir := filepath.Join(root, config.BackupDirName)
	for _, name := range []string{"web-20200101T000000Z", "web-ui-20200102T000000Z"} {
		if err := os.MkdirAll(filepath.Join(backupDir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	got, err := listServiceBackups(root, "web")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != filepath.Join(backupDir, "web-20200101T000000Z") {
		t.Fatalf("listServiceBackups(web) = %v", got)
	}
	if err := pruneBackups(backupDir, "web", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(backupDir, "web-ui-20200102T000000Z")); err != nil {
		t.Fatal("web-ui backup must survive prune of web")
	}
}

func TestBackupPrunesToNewest(t *testing.T) {
	root := t.TempDir()
	svc := filepath.Join(root, "demo")
	if err := os.MkdirAll(filepath.Join(svc, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	var backups []string
	for i := 0; i < 3; i++ {
		b, err := Backup(root, "demo", svc)
		if err != nil {
			t.Fatal(err)
		}
		backups = append(backups, b)
	}
	remaining, err := listServiceBackups(root, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 2 {
		t.Fatalf("expected 2 backups after pruning, got %d: %v", len(remaining), remaining)
	}
	// The first (oldest) backup is gone; the newest is retained.
	if _, err := os.Stat(backups[0]); !os.IsNotExist(err) {
		t.Fatalf("oldest backup %s not pruned", backups[0])
	}
	if _, err := os.Stat(backups[2]); err != nil {
		t.Fatalf("newest backup %s missing: %v", backups[2], err)
	}
}

func TestDeployDoesNotReuseHistoricalBackupAuthkey(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	setupTemplate(t, repo, "web", "TS_AUTHKEY=\nHOSTNAME=x\n")
	backup := filepath.Join(deployRoot, config.BackupDirName, "web-20200101T000000Z")
	if err := os.MkdirAll(backup, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, ".env"), []byte("TS_AUTHKEY=tskey-auth-OLD\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	withFakeCompose(t, func(dir string, args ...string) error { return nil })

	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot, AuthkeysPath: filepath.Join(deployRoot, "keys")}}
	err := m.Deploy("web", DeployOpts{})
	if !errors.Is(err, ErrEmptyAuthkey) {
		t.Fatalf("expected ErrEmptyAuthkey, got %v", err)
	}
	if data, readErr := os.ReadFile(filepath.Join(deployRoot, "web", ".env")); readErr == nil && strings.Contains(string(data), "tskey-auth-OLD") {
		t.Fatalf("deploy reused historical backup key: %s", data)
	}
}

// fakeUI answers every Line and Secret prompt with fixed values.
type fakeUI struct {
	line, secret string
}

func (f fakeUI) Confirm(string, bool) (bool, error)  { return false, nil }
func (f fakeUI) Line(string, string) (string, error) { return f.line, nil }
func (f fakeUI) Secret(string) (string, error)       { return f.secret, nil }
func (f fakeUI) Printf(string, ...any)               {}

func TestMergeEnvQuotesPromptedValuesAndKeepsTemplateLines(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	env := "TZ=Europe/Amsterdam # See the tz list\n" +
		"WEBAPP_URL=http://${TS_URL}:3000\n" +
		"DB_PASSWORD=\n" +
		"GREETING=\n"
	templateDir := setupTemplate(t, repo, "web", env)
	dest := filepath.Join(deployRoot, "web")
	if err := copyTemplate(templateDir, dest); err != nil {
		t.Fatal(err)
	}
	m := &Manager{
		Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot},
		UI:  fakeUI{line: "hello world", secret: "p$ss #1"},
	}
	if err := m.mergeAndWriteEnv([]byte(env), dest, DeployOpts{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dest, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"TZ=Europe/Amsterdam # See the tz list\n",
		"WEBAPP_URL=http://${TS_URL}:3000\n",
		"DB_PASSWORD='p$ss #1'\n",
		"GREETING='hello world'\n",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("missing %q in:\n%s", want, data)
		}
	}
}

func TestDeployTakesDownContainersAfterFailedUp(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	setupTemplate(t, repo, "web", "HOSTNAME=x\n")
	var calls []string
	withFakeCompose(t, func(dir string, args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		if slices.Contains(args, "up") {
			return fmt.Errorf("%w: simulated up failure", ErrComposeFailed)
		}
		return nil
	})
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	if err := m.Deploy("web", DeployOpts{}); !errors.Is(err, ErrComposeFailed) {
		t.Fatalf("expected ErrComposeFailed, got %v", err)
	}
	if len(calls) != 2 || !strings.Contains(calls[1], "down --remove-orphans") {
		t.Fatalf("expected compose down after failed up, got %q", calls)
	}
	if _, err := os.Stat(filepath.Join(deployRoot, "web")); !os.IsNotExist(err) {
		t.Fatalf("partial deployment not removed: %v", err)
	}
}

func TestDeployKeepsDestWhenCleanupDownFails(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	setupTemplate(t, repo, "web", "HOSTNAME=x\n")
	withFakeCompose(t, func(dir string, args ...string) error {
		return fmt.Errorf("%w: simulated failure", ErrComposeFailed)
	})
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	if err := m.Deploy("web", DeployOpts{}); !errors.Is(err, ErrComposeFailed) {
		t.Fatalf("expected ErrComposeFailed, got %v", err)
	}
	dest := filepath.Join(deployRoot, "web")
	if !IsManaged(dest) {
		t.Fatal("deployment must stay managed so Remove can take containers down")
	}
}

func TestDeployDoesNotCopyWhileRepoLockHeld(t *testing.T) {
	if os.Getenv("TAILARR_HOLD_LOCK") == "1" {
		lock, err := AcquireLock(os.Getenv("TAILARR_LOCK_PATH"), 2*time.Second)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Println("held")
		_ = os.Stdout.Sync()
		time.Sleep(20 * time.Second)
		_ = lock.Release()
		return
	}
	repo := t.TempDir()
	deployRoot := t.TempDir()
	setupTemplate(t, repo, "web", "TS_AUTHKEY=tskey-auth-NEW\nHOSTNAME=x\n")
	withFakeCompose(t, func(dir string, args ...string) error { return nil })
	lockPath := RepoLockPath(repo)
	cmd := exec.Command(os.Args[0], "-test.run=^TestDeployDoesNotCopyWhileRepoLockHeld$")
	cmd.Env = append(os.Environ(), "TAILARR_HOLD_LOCK=1", "TAILARR_LOCK_PATH="+lockPath)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	buf := make([]byte, 8)
	if _, err := out.Read(buf); err != nil {
		t.Fatal(err)
	}
	old := repoLockTimeout
	repoLockTimeout = 300 * time.Millisecond
	t.Cleanup(func() { repoLockTimeout = old })

	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot, AuthkeysPath: filepath.Join(deployRoot, "keys")}}
	err = m.Deploy("web", DeployOpts{})
	if err == nil || !strings.Contains(err.Error(), "holds the lock") {
		t.Fatalf("expected repo lock error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(deployRoot, "web")); !os.IsNotExist(statErr) {
		t.Fatalf("deploy copied template while repo lock was held: %v", statErr)
	}
}

func TestDefaultComposeStopsWhenInterruptCanceled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a shell-backed fake docker executable")
	}
	dir := t.TempDir()
	bin := t.TempDir()
	script := "#!/bin/sh\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	interrupt.Set(ctx)
	t.Cleanup(func() {
		cancel()
		interrupt.Clear()
	})
	done := make(chan error, 1)
	go func() {
		done <- Compose(dir, "pull")
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, ErrInterrupted) {
			t.Fatalf("expected ErrInterrupted, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("compose did not stop when interrupt context was canceled")
	}
}

func TestFilterComposeEnv(t *testing.T) {
	kept, dropped := filterComposeEnv([]string{
		"COMPOSE_PROFILES=debug",
		"COMPOSE_FILE=other.yaml",
		"TAILARR_REPO_PATH=/opt/tailarr",
		"TS_AUTHKEY=tskey-auth-LEAK",
		"DOCKER_HOST=unix:///var/run/docker.sock",
		"PATH=/usr/bin",
		"TZ=UTC",
	}, nil)
	got := strings.Join(kept, "\n")
	for _, banned := range []string{"COMPOSE_PROFILES", "COMPOSE_FILE", "TAILARR_REPO_PATH", "TS_AUTHKEY"} {
		if strings.Contains(got, banned) {
			t.Fatalf("kept %s: %q", banned, got)
		}
	}
	for _, want := range []string{"DOCKER_HOST=unix:///var/run/docker.sock", "PATH=/usr/bin", "TZ=UTC"} {
		if !strings.Contains(got, want) {
			t.Fatalf("dropped %s: %q", want, got)
		}
	}
	if strings.Join(dropped, ",") != "COMPOSE_PROFILES,COMPOSE_FILE" {
		t.Fatalf("dropped keys: %v", dropped)
	}
}

func TestFilterComposeEnvLetsDotEnvWin(t *testing.T) {
	fileKeys := map[string]bool{"TZ": true, "SERVICE": true, "PATH": true, "DOCKER_HOST": true, "HOME": true}
	kept, _ := filterComposeEnv([]string{
		"TZ=UTC",
		"SERVICE=other",
		"PATH=/usr/bin",
		"HOME=/root",
		"DOCKER_HOST=unix:///var/run/docker.sock",
		"LANG=C.UTF-8",
	}, fileKeys)
	got := strings.Join(kept, "\n")
	for _, shadowed := range []string{"TZ=", "SERVICE="} {
		if strings.Contains(got, shadowed) {
			t.Errorf("kept %s although .env sets it: %q", shadowed, got)
		}
	}
	for _, want := range []string{"PATH=/usr/bin", "HOME=/root", "DOCKER_HOST=unix:///var/run/docker.sock", "LANG=C.UTF-8"} {
		if !strings.Contains(got, want) {
			t.Errorf("dropped %s: %q", want, got)
		}
	}
}

func TestDefaultComposeUsesDotEnvOverProcessEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a shell-backed fake docker executable")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TZ=Europe/Amsterdam\nSERVICE=web\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(t.TempDir(), "env.txt")
	bin := t.TempDir()
	script := "#!/bin/sh\nenv > " + strconv.Quote(envFile) + "\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TZ", "UTC")
	t.Setenv("SERVICE", "other")
	SetOutput(io.Discard)
	t.Cleanup(func() { SetOutput(nil) })
	if err := defaultCompose(context.Background(), dir, "up", "-d"); err != nil {
		t.Fatal(err)
	}
	dump, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(dump), "\n") {
		if line == "TZ=UTC" || line == "SERVICE=other" {
			t.Errorf("compose inherited %s, which overrides .env", line)
		}
	}
	if !strings.Contains(string(dump), "PATH=") {
		t.Error("compose lost PATH")
	}
}

func TestDefaultComposeKeepsFailureReason(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a shell-backed fake docker executable")
	}
	dir := t.TempDir()
	bin := t.TempDir()
	script := "#!/bin/sh\n" +
		"echo ' Container tailscale-web Waiting' >&2\n" +
		"echo 'dependency failed to start: container tailscale-web is unhealthy (TS_AUTHKEY=tskey-auth-LEAK)' >&2\n" +
		"echo '' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out strings.Builder
	SetOutput(&out)
	t.Cleanup(func() { SetOutput(nil) })
	err := defaultCompose(context.Background(), dir, "up", "-d")
	if !errors.Is(err, ErrComposeFailed) {
		t.Fatalf("expected ErrComposeFailed, got %v", err)
	}
	if !strings.Contains(err.Error(), "dependency failed to start: container tailscale-web is unhealthy") {
		t.Fatalf("error lost the compose reason: %v", err)
	}
	if strings.Contains(err.Error(), "tskey-auth-LEAK") || strings.Contains(out.String(), "tskey-auth-LEAK") {
		t.Fatalf("secret leaked: err=%v out=%q", err, out.String())
	}
	if !strings.Contains(out.String(), "Container tailscale-web Waiting") {
		t.Fatalf("output not sent to SetOutput writer: %q", out.String())
	}
}

func TestDefaultComposeInterruptSignalsPluginChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process groups are unix-only")
	}
	dir := t.TempDir()
	bin := t.TempDir()
	marks := t.TempDir()
	cli, plugin, ready := filepath.Join(marks, "cli"), filepath.Join(marks, "plugin"), filepath.Join(marks, "ready")
	// The fake docker CLI runs a fake plugin in the foreground, as docker
	// runs docker-compose. Both record the SIGINT they receive.
	child := "trap 'echo int > " + plugin + "; exit 0' INT; echo up > " + ready + "; while :; do sleep 0.1; done"
	script := "#!/bin/sh\ntrap 'echo int > " + cli + "; exit 0' INT\nsh -c " + strconv.Quote(child) + "\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	SetOutput(io.Discard)
	t.Cleanup(func() { SetOutput(nil) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- defaultCompose(ctx, dir, "up", "-d") }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, ErrInterrupted) {
			t.Fatalf("expected ErrInterrupted, got %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("compose did not stop after cancel")
	}
	for _, mark := range []string{cli, plugin} {
		if _, err := os.Stat(mark); err != nil {
			t.Errorf("%s did not receive SIGINT: %v", filepath.Base(mark), err)
		}
	}
}

func TestComposeServiceNamesFiltersEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a shell-backed fake docker executable")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:\n  app:\n    image: alpine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(dir, "env.txt")
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \"$*\" in\n*version*) exit 0 ;;\nesac\nenv > " + strconv.Quote(envFile) + "\necho app\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TS_AUTHKEY", "tskey-auth-LEAK")
	t.Setenv("COMPOSE_PROFILES", "debug")
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")
	names, err := composeServiceNames(dir, "compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "app" {
		t.Fatalf("names: %v", names)
	}
	dump, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	text := string(dump)
	if strings.Contains(text, "TS_AUTHKEY=") || strings.Contains(text, "COMPOSE_PROFILES=") {
		t.Fatalf("compose config inherited filtered env:\n%s", text)
	}
	if !strings.Contains(text, "DOCKER_HOST=") {
		t.Fatal("DOCKER_HOST should be kept")
	}
}

func TestApplyRestoresWhenInterruptContextCanceled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a shell-backed fake docker executable")
	}
	repo := t.TempDir()
	deployRoot := t.TempDir()
	setupTemplate(t, repo, "web", "TS_AUTHKEY=tskey-auth-from-template\nHOSTNAME=t\n")
	dest := filepath.Join(deployRoot, "web")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	orig := "services:\n  app:\n    image: original\n"
	if err := os.WriteFile(filepath.Join(dest, "compose.yaml"), []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, ".env"), []byte("TS_AUTHKEY=tskey-auth-from-template\nHOSTNAME=old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, ".tailarr.compose.yaml"), []byte("services:\n  app:\n    labels:\n      tailarr.managed: \"true\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "services", "web", "compose.yaml"), []byte("services:\n  app:\n    image: new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	started := filepath.Join(t.TempDir(), "started")
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \"$*\" in\n*version*) exit 0 ;;\n*--services*) echo app; exit 0 ;;\n*pull*|*up*) echo started > " + strconv.Quote(started) + "; exec sleep 30 ;;\n*) exit 0 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	interrupt.Set(ctx)
	t.Cleanup(func() {
		cancel()
		interrupt.Clear()
	})

	errCh := make(chan error, 1)
	go func() {
		m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
		errCh <- m.Apply("web", DeployOpts{})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(started); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(started); err != nil {
		t.Fatal("compose did not start")
	}
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, ErrInterrupted) {
			t.Fatalf("expected ErrInterrupted, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("apply did not return after interrupt")
	}
	data, err := os.ReadFile(filepath.Join(dest, "compose.yaml"))
	if err != nil || string(data) != orig {
		t.Fatalf("previous deployment not restored: %v %q", err, data)
	}
}

func TestCollectOverviewUsesOneDockerPass(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a shell-backed fake docker executable")
	}
	deployRoot := t.TempDir()
	for _, svc := range []string{"web", "other"} {
		dir := filepath.Join(deployRoot, svc)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:\n  app:\n    image: x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeOverride("web", filepath.Join(deployRoot, "web")); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(t.TempDir(), "calls")
	bin := t.TempDir()
	script := "#!/bin/sh\necho \"$*\" >> " + strconv.Quote(calls) + "\n" +
		"printf 'app-TEST_web\\trunning\\tUp 3 minutes (healthy)\\tweb\\n'\n" +
		"printf 'tailscale-TEST_web\\trunning\\tUp 3 minutes (health: starting)\\tweb\\n'\n" +
		"printf 'app-other\\texited\\tExited (1) 2 minutes ago\\t\\n'\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.Remove(calls); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	st, err := CollectOverview(deployRoot)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(calls)
	if n := strings.Count(string(data), "ps -a"); n != 1 || strings.Count(string(data), "\n") != 1 {
		t.Fatalf("want one docker ps -a call, got:\n%s", data)
	}
	if st.DockerErr != "" || len(st.Services) != 2 {
		t.Fatalf("services=%+v dockerErr=%q", st.Services, st.DockerErr)
	}
	other, web := st.Services[0], st.Services[1]
	if !web.Managed || web.Health != HealthStarting || len(web.Containers) != 2 || web.Containers[0].Name != "app-TEST_web" {
		t.Fatalf("web: %+v", web)
	}
	if other.Managed || other.Health != HealthStopped || len(other.Containers) != 1 || other.Containers[0].Health != HealthExited {
		t.Fatalf("other: %+v", other)
	}
}

func TestCollectOverviewReportsDockerFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a shell-backed fake docker executable")
	}
	deployRoot := t.TempDir()
	dir := filepath.Join(deployRoot, "web")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\necho 'Cannot connect to the Docker daemon' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	st, err := CollectOverview(deployRoot)
	if err != nil {
		t.Fatal(err)
	}
	if st.DockerErr == "" || len(st.Services) != 1 || st.Services[0].Health != HealthUnknown {
		t.Fatalf("docker failure must mark health unknown: %+v", st)
	}
}

func TestDeployReusableKeyOnlyFillsDeclaredAuthkey(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	setupTemplate(t, repo, "web", "SERVICE=web\n")
	setupTemplate(t, repo, "api", "SERVICE=api\nTS_AUTHKEY=\n")
	withFakeCompose(t, func(string, ...string) error { return nil })
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	for _, svc := range []string{"web", "api"} {
		if err := m.Deploy(svc, DeployOpts{ReusableAuthKey: "tskey-auth-shared"}); err != nil {
			t.Fatalf("deploy %s: %v", svc, err)
		}
	}
	web, err := os.ReadFile(filepath.Join(deployRoot, "web", ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(web), "TS_AUTHKEY") {
		t.Fatalf("shared key written into a .env that does not declare it:\n%s", web)
	}
	api, err := os.ReadFile(filepath.Join(deployRoot, "api", ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(api), "TS_AUTHKEY=tskey-auth-shared") {
		t.Fatalf("declared TS_AUTHKEY not filled:\n%s", api)
	}
}

func TestComposeVerbDropsProjectAndFiles(t *testing.T) {
	got := composeVerb([]string{"-p", "tailarr-1234-web", "-f", "compose.yaml", "-f", ".tailarr.compose.yaml", "up", "-d", "--remove-orphans"})
	if got != "up -d --remove-orphans" {
		t.Fatalf("composeVerb = %q", got)
	}
}

func TestHealthFromOutputStoppedStackIsNotDown(t *testing.T) {
	raw := strings.Join([]string{
		"app-web\texited\tExited (137) 5 seconds ago\t",
		"tailscale-web\texited\tExited (0) 5 seconds ago\t",
		"app-api\trestarting\tRestarting (1) 2 seconds ago\t",
		"app-db\trunning\tUp 1 hour\t",
		"tailscale-db\texited\tExited (1) 1 minute ago\t",
	}, "\n")
	got := healthFromOutput(raw, []string{"web", "api", "db"})
	if got["web"] != HealthStopped {
		t.Errorf("all containers exited after stop: got %s, want stopped", got["web"])
	}
	if got["api"] != HealthExited {
		t.Errorf("crash loop: got %s, want exited", got["api"])
	}
	if got["db"] != HealthExited {
		t.Errorf("sidecar died while app runs: got %s, want exited", got["db"])
	}
}
