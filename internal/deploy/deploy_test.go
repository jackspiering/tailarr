package deploy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/logging"
)

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

	backup, err := Backup(deployRoot, "demo", svc, BackupMove)
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" {
		t.Fatal("empty backup")
	}
	if _, err := os.Stat(svc); !os.IsNotExist(err) {
		t.Fatal("service should have been moved")
	}

	// New template deploy dir
	if err := os.MkdirAll(svc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svc, "compose.yaml"), []byte("x:2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RestorePersistentData(backup, svc); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(svc, "data", "file"))
	if err != nil || string(data) != "keep\n" {
		t.Fatalf("restore failed: %v %q", err, data)
	}
	// .env is intentionally not restored by RestorePersistentData.
	if _, err := os.Stat(filepath.Join(svc, ".env")); !os.IsNotExist(err) {
		t.Fatal(".env should not be restored by RestorePersistentData")
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
	if err := writeOverride(dest); err != nil {
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
}

func withFakeCompose(t *testing.T, fn func(dir string, args ...string) error) {
	t.Helper()
	prev := composeFn
	composeFn = fn
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
	if err := writeOverride(dest); err != nil {
		t.Fatal(err)
	}

	withFakeCompose(t, func(dir string, args ...string) error {
		return fmt.Errorf("%w: simulated down failure", ErrComposeFailed)
	})

	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	err := m.Remove("web", false)
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
	err := m.Remove("manual", true)
	if !errors.Is(err, ErrNotManaged) {
		t.Fatalf("expected ErrNotManaged, got %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatal("unmanaged dir must not be deleted")
	}
}

func TestForceDeployPreservesEnvSecrets(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	setupTemplate(t, repo, "web", "TS_AUTHKEY=\nHOSTNAME=template\n")

	// Existing managed deployment with secrets.
	dest := filepath.Join(deployRoot, "web")
	if err := copyTemplate(filepath.Join(repo, "services", "web"), dest); err != nil {
		t.Fatal(err)
	}
	if err := writeOverride(dest); err != nil {
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

	var upCount atomic.Int32
	withFakeCompose(t, func(dir string, args ...string) error {
		// down then up
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "up") {
			upCount.Add(1)
		}
		return nil
	})

	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	if err := m.DeployWith("web", DeployOpts{Force: true}); err != nil {
		t.Fatal(err)
	}
	if upCount.Load() < 1 {
		t.Fatal("expected compose up")
	}
	env, err := os.ReadFile(filepath.Join(dest, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), "TS_AUTHKEY=tskey-auth-SECRET") {
		t.Fatalf("secret lost on force redeploy: %s", env)
	}
	if !strings.Contains(string(env), "HOSTNAME=local") {
		t.Fatalf("local env lost: %s", env)
	}
	// Data dir restored.
	if data, err := os.ReadFile(filepath.Join(dest, "data", "keep")); err != nil || string(data) != "yes\n" {
		t.Fatalf("data not restored: %v %q", err, data)
	}
}

func TestForceDeployRestoresOnComposeUpFailure(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	setupTemplate(t, repo, "web", "TS_AUTHKEY=tskey-auth-from-template\nHOSTNAME=t\n")

	dest := filepath.Join(deployRoot, "web")
	if err := copyTemplate(filepath.Join(repo, "services", "web"), dest); err != nil {
		t.Fatal(err)
	}
	if err := writeOverride(dest); err != nil {
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
		return nil // down ok
	})

	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	err := m.DeployWith("web", DeployOpts{Force: true})
	if err == nil {
		t.Fatal("expected deploy failure")
	}
	// Previous deployment restored.
	data, err := os.ReadFile(filepath.Join(dest, "marker.txt"))
	if err != nil || string(data) != marker {
		t.Fatalf("previous deployment not restored: %v %q", err, data)
	}
	env, err := os.ReadFile(filepath.Join(dest, ".env"))
	if err != nil || !strings.Contains(string(env), "tskey-auth-OLD") {
		t.Fatalf("old env not restored: %v %q", err, env)
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
	err := m.DeployWith("web", DeployOpts{})
	if !errors.Is(err, ErrEmptyAuthkey) {
		t.Fatalf("expected ErrEmptyAuthkey, got %v", err)
	}
}

func TestDeployUsesNamedAuthkey(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	setupTemplate(t, repo, "web", "TS_AUTHKEY=\nHOSTNAME=x\n")

	keysPath := filepath.Join(deployRoot, "authkeys.conf")
	if err := os.WriteFile(keysPath, []byte("prod=tskey-auth-FROMSTORE\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	withFakeCompose(t, func(dir string, args ...string) error { return nil })

	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot, AuthkeysPath: keysPath}}
	if err := m.DeployWith("web", DeployOpts{AuthKeyName: "prod"}); err != nil {
		t.Fatal(err)
	}
	env, err := os.ReadFile(filepath.Join(deployRoot, "web", ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), "TS_AUTHKEY=tskey-auth-FROMSTORE") {
		t.Fatalf("store key not applied: %s", env)
	}
}

func TestLatestBackup(t *testing.T) {
	root := t.TempDir()
	b1 := filepath.Join(root, config.BackupDirName, "web-20200101T000000Z")
	b2 := filepath.Join(root, config.BackupDirName, "web-20200102T000000Z")
	if err := os.MkdirAll(b1, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(b2, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := LatestBackup(root, "web")
	if err != nil {
		t.Fatal(err)
	}
	if got != b2 {
		t.Fatalf("got %s want %s", got, b2)
	}
}
