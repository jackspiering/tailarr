package deploy

import (
	"os"
	"path/filepath"
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
}

func TestLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.lock")
	l1, err := AcquireLock(path, DefaultLockTimeout)
	if err != nil {
		t.Fatal(err)
	}
	// Second lock should fail quickly - use short timeout via modifying
	// We use default; hold and try - will wait 30s which is too long for unit test.
	// Release first then re-acquire.
	if err := l1.Release(); err != nil {
		t.Fatal(err)
	}
	l2, err := AcquireLock(path, DefaultLockTimeout)
	if err != nil {
		t.Fatal(err)
	}
	_ = l2.Release()
}

func TestCopyTemplateAndOverride(t *testing.T) {
	// Unit-only: no Docker Compose invocation.
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
	if filepath.Base(p) != "web.lock" {
		t.Fatal(p)
	}
	if _, err := ServiceLockPath("/x", "../y"); err == nil {
		t.Fatal("expected invalid name")
	}
}
