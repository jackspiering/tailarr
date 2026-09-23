//go:build unix

package deploy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/interrupt"
)

func inode(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Sys().(*syscall.Stat_t).Ino
}

func TestApplyFailureKeepsDataDirInPlace(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	dest := managedDeploy(t, repo, deployRoot)
	data := filepath.Join(dest, "data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "db"), []byte("live\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dataIno := inode(t, data)
	composeIno := inode(t, filepath.Join(dest, "compose.yaml"))
	origCompose, _ := os.ReadFile(filepath.Join(dest, "compose.yaml"))
	if err := os.WriteFile(filepath.Join(repo, "services", "web", "compose.yaml"), []byte("services:\n  app:\n    image: new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	withFakeCompose(t, func(dir string, args ...string) error {
		if strings.Contains(strings.Join(args, " "), "pull") {
			// A container writes while Apply runs.
			if err := os.WriteFile(filepath.Join(data, "db"), []byte("written during apply\n"), 0o644); err != nil {
				t.Error(err)
			}
			return fmt.Errorf("%w: pull failed", ErrComposeFailed)
		}
		return nil
	})
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	err := m.Apply("web", DeployOpts{})
	if !errors.Is(err, ErrComposeFailed) || !strings.Contains(err.Error(), "previous deployment restored") {
		t.Fatalf("expected restored pull failure, got %v", err)
	}
	if got := inode(t, data); got != dataIno {
		t.Fatal("data directory was replaced; running containers would lose their bind mount")
	}
	if b, err := os.ReadFile(filepath.Join(data, "db")); err != nil || string(b) != "written during apply\n" {
		t.Fatalf("data written during apply was lost: %v %q", err, b)
	}
	if got := inode(t, filepath.Join(dest, "compose.yaml")); got != composeIno {
		t.Fatal("compose.yaml must be restored in place")
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "compose.yaml")); string(b) != string(origCompose) {
		t.Fatalf("compose.yaml not restored: %q", b)
	}
	entries, _ := os.ReadDir(filepath.Join(deployRoot, config.BackupDirName))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".partial") {
			t.Fatalf("unexpected scratch tree %s", e.Name())
		}
		if _, err := os.Stat(filepath.Join(deployRoot, config.BackupDirName, e.Name(), "data")); err == nil {
			t.Fatal("apply backup must not copy container data")
		}
	}
}

func TestApplyStartsRestoredFilesAfterUpFailure(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	dest := managedDeploy(t, repo, deployRoot)
	if err := os.WriteFile(filepath.Join(repo, "services", "web", "compose.yaml"), []byte("services:\n  app:\n    image: new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var ups []string
	withFakeCompose(t, func(dir string, args ...string) error {
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, " up ") {
			return nil
		}
		compose, _ := os.ReadFile(filepath.Join(dir, "compose.yaml"))
		ups = append(ups, string(compose))
		if len(ups) == 1 {
			return fmt.Errorf("%w: up failed", ErrComposeFailed)
		}
		return nil
	})
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	err := m.Apply("web", DeployOpts{})
	if !errors.Is(err, ErrComposeFailed) || !strings.Contains(err.Error(), "restored and started") {
		t.Fatalf("expected restored and started, got %v", err)
	}
	if len(ups) != 2 {
		t.Fatalf("expected a second up with restored files, got %d", len(ups))
	}
	if strings.Contains(ups[1], "image: new") {
		t.Fatalf("second up used the new compose file: %q", ups[1])
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatal(err)
	}
}

func TestApplyDoesNotStartAgainAfterInterrupt(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	managedDeploy(t, repo, deployRoot)
	ups := 0
	withFakeCompose(t, func(dir string, args ...string) error {
		if strings.Contains(strings.Join(args, " "), " up ") {
			ups++
			return fmt.Errorf("%w: canceled", ErrInterrupted)
		}
		return nil
	})
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	err := m.Apply("web", DeployOpts{})
	if !errors.Is(err, ErrInterrupted) || !strings.Contains(err.Error(), "run Restart") {
		t.Fatalf("expected interrupted apply with Restart hint, got %v", err)
	}
	if ups != 1 {
		t.Fatalf("an interrupted apply must not run up again, got %d ups", ups)
	}
}

func TestApplyFailureRemovesFilesItCreated(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	dest := managedDeploy(t, repo, deployRoot)
	tpl := filepath.Join(repo, "services", "web")
	if err := os.WriteFile(filepath.Join(tpl, "extra.conf"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tpl, "conf.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tpl, "conf.d", "a.conf"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withFakeCompose(t, func(dir string, args ...string) error {
		if strings.Contains(strings.Join(args, " "), "pull") {
			return fmt.Errorf("%w: pull failed", ErrComposeFailed)
		}
		return nil
	})
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	if err := m.Apply("web", DeployOpts{}); err == nil {
		t.Fatal("expected apply failure")
	}
	for _, p := range []string{"extra.conf", "conf.d"} {
		if _, err := os.Lstat(filepath.Join(dest, p)); !os.IsNotExist(err) {
			t.Errorf("%s created by the failed apply was not removed: %v", p, err)
		}
	}
}

func TestApplyIgnoresUnreadableContainerData(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("needs a non-root Unix user")
	}
	repo := t.TempDir()
	deployRoot := t.TempDir()
	dest := managedDeploy(t, repo, deployRoot)
	state := filepath.Join(dest, "ts", "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(state, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(state, 0o755) })
	withFakeCompose(t, func(string, ...string) error { return nil })
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	if err := m.Apply("web", DeployOpts{}); err != nil {
		t.Fatalf("apply must not read container data: %v", err)
	}
}

func TestBackupSkipsSocketsAndFIFOs(t *testing.T) {
	root := t.TempDir()
	svc := filepath.Join(root, "svc")
	if err := os.MkdirAll(filepath.Join(svc, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svc, "data", "keep"), []byte("k\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Unix socket paths are length-limited; keep the name short.
	l, err := net.Listen("unix", filepath.Join(svc, "data", "s"))
	if err != nil {
		t.Skipf("cannot create unix socket: %v", err)
	}
	defer func() { _ = l.Close() }()
	if err := syscall.Mkfifo(filepath.Join(svc, "data", "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	var backup string
	go func() {
		var err error
		backup, err = Backup(root, "svc", svc, BackupCopy)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("backup blocked on a FIFO")
	}
	if b, err := os.ReadFile(filepath.Join(backup, "data", "keep")); err != nil || string(b) != "k\n" {
		t.Fatalf("regular file not copied: %v %q", err, b)
	}
	for _, name := range []string{"s", "pipe"} {
		if _, err := os.Lstat(filepath.Join(backup, "data", name)); !os.IsNotExist(err) {
			t.Errorf("special file %s must be skipped: %v", name, err)
		}
	}
}

func TestBackupKeepsModesAndMtime(t *testing.T) {
	root := t.TempDir()
	svc := filepath.Join(root, "svc")
	data := filepath.Join(svc, "data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(data, "f")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Set modes the default umask would strip.
	if err := os.Chmod(file, 0o664); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(data, 0o775); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	backup, err := Backup(root, "svc", svc, BackupCopy)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(backup, "data", "f"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o664 {
		t.Errorf("file mode = %v, want 0664", fi.Mode().Perm())
	}
	if !fi.ModTime().Equal(old) {
		t.Errorf("file mtime = %v, want %v", fi.ModTime(), old)
	}
	di, err := os.Stat(filepath.Join(backup, "data"))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o775 {
		t.Errorf("dir mode = %v, want 0775", di.Mode().Perm())
	}
}

func TestBackupStopsOnInterrupt(t *testing.T) {
	root := t.TempDir()
	svc := filepath.Join(root, "svc")
	if err := os.MkdirAll(filepath.Join(svc, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	interrupt.Set(ctx)
	t.Cleanup(interrupt.Clear)
	if _, err := Backup(root, "svc", svc, BackupCopy); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("expected ErrInterrupted, got %v", err)
	}
}

func TestFormatBytes(t *testing.T) {
	cases := map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 5 << 20: "5.0 MiB", 3 << 30: "3.0 GiB"}
	for n, want := range cases {
		if got := formatBytes(n); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", n, got, want)
		}
	}
}
