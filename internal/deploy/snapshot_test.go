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
		backup, err = Backup(root, "svc", svc)
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
	backup, err := Backup(root, "svc", svc)
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
	if _, err := Backup(root, "svc", svc); !errors.Is(err, ErrInterrupted) {
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

func TestApplyKeepsRemoveBackupOfContainerData(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	managedDeploy(t, repo, deployRoot)
	removed := filepath.Join(deployRoot, config.BackupDirName, "web-20200101T000000Z")
	if err := os.MkdirAll(filepath.Join(removed, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	dataFile := filepath.Join(removed, "data", "db")
	if err := os.WriteFile(dataFile, []byte("only copy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	withFakeCompose(t, func(string, ...string) error { return nil })
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	for i := 0; i < 3; i++ {
		if err := m.Apply("web", DeployOpts{}); err != nil {
			t.Fatal(err)
		}
	}
	if b, err := os.ReadFile(dataFile); err != nil || string(b) != "only copy\n" {
		t.Fatalf("Apply pruned the Remove backup: %v %q", err, b)
	}
	snaps, err := os.ReadDir(applySnapshotRoot(deployRoot))
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 2 {
		t.Fatalf("expected 2 apply snapshots, got %d", len(snaps))
	}
}

func TestApplyRestoresWhenOlderBackupsHaveFutureStamps(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	dest := managedDeploy(t, repo, deployRoot)
	orig, _ := os.ReadFile(filepath.Join(dest, "compose.yaml"))
	snapRoot := applySnapshotRoot(deployRoot)
	for _, name := range []string{"web-29990101T000000Z", "web-29990102T000000Z"} {
		if err := os.MkdirAll(filepath.Join(snapRoot, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "services", "web", "compose.yaml"), []byte("services:\n  app:\n    image: new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withFakeCompose(t, func(dir string, args ...string) error {
		if strings.Contains(strings.Join(args, " "), "pull") {
			return fmt.Errorf("%w: pull failed", ErrComposeFailed)
		}
		return nil
	})
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	err := m.Apply("web", DeployOpts{})
	if !errors.Is(err, ErrComposeFailed) || !strings.Contains(err.Error(), "previous deployment restored") {
		t.Fatalf("expected restored failure, got %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "compose.yaml")); string(b) != string(orig) {
		t.Fatalf("compose.yaml not restored: %q", b)
	}
}

func TestPruneBackupsKeepingNeverRemovesProtectedPath(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"web-20200101T000000Z", "web-29990101T000000Z", "web-29990102T000000Z"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	protect := filepath.Join(root, "web-20200101T000000Z")
	if err := pruneBackupsKeeping(root, "web", 2, protect); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(protect); err != nil {
		t.Fatalf("protected backup removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "web-29990102T000000Z")); err != nil {
		t.Fatalf("newest other backup removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "web-29990101T000000Z")); !os.IsNotExist(err) {
		t.Fatal("expected the older other backup to be pruned")
	}
}

func TestRemoveOffersToDeleteApplySnapshots(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	managedDeploy(t, repo, deployRoot)
	withFakeCompose(t, func(string, ...string) error { return nil })
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	if err := m.Apply("web", DeployOpts{}); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	m.UI = recordUI{out: &out}
	if err := m.Remove("web"); err != nil {
		t.Fatal(err)
	}
	left, err := listServiceBackups(deployRoot, "web")
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("Remove left backups after the operator accepted deletion: %v", left)
	}
}

func TestCopyFileAppliesTemplateMode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.sh")
	dst := filepath.Join(dir, "out", "dst.sh")
	if err := os.WriteFile(src, []byte("new\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old content that is longer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dst, 0o644); err != nil {
		t.Fatal(err)
	}
	ino := inode(t, dst)
	if err := copyFile(src, dst, 0o755); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("mode = %o, want 755", got)
	}
	if b, _ := os.ReadFile(dst); string(b) != "new\n" {
		t.Fatalf("content = %q", b)
	}
	if inode(t, dst) != ino {
		t.Fatal("copyFile replaced the inode; a single-file bind mount would break")
	}
}

func TestCopyFileRefusesSymlinkDestination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	victim := filepath.Join(dir, "victim")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("template\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(victim, []byte("host file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, dst); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := copyFile(src, dst, 0o644); !errors.Is(err, ErrSymlink) {
		t.Fatalf("expected ErrSymlink, got %v", err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "host file\n" {
		t.Fatalf("symlink target was written: %q", b)
	}
}

func TestCopyFileRefusesSymlinkSource(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret")
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(secret, []byte("host secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, src); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	dst := filepath.Join(dir, "dst")
	if err := copyFile(src, dst, 0o644); !errors.Is(err, ErrSymlink) {
		t.Fatalf("expected ErrSymlink, got %v", err)
	}
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		t.Fatalf("destination was created: %v", err)
	}
}

func TestCopyFileFailsOnFIFODestination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("template\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(dst, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- copyFile(src, dst, 0o644) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error for a FIFO destination")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("copyFile blocked on a FIFO destination")
	}
}

func TestCopyFileFailsOnFIFOSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := syscall.Mkfifo(src, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- copyFile(src, filepath.Join(dir, "dst"), 0o644) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error for a FIFO source")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("copyFile blocked on a FIFO source")
	}
}

func TestApplyReportsFailedRestore(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	managedDeploy(t, repo, deployRoot)
	withFakeCompose(t, func(dir string, args ...string) error {
		if !strings.Contains(strings.Join(args, " "), "pull") {
			return nil
		}
		// The snapshot is lost while Apply runs, so restore has nothing to copy back.
		snaps, err := os.ReadDir(applySnapshotRoot(deployRoot))
		if err != nil || len(snaps) != 1 {
			t.Errorf("expected one snapshot, got %v %v", snaps, err)
			return nil
		}
		if err := os.Remove(filepath.Join(applySnapshotRoot(deployRoot), snaps[0].Name(), "compose.yaml")); err != nil {
			t.Error(err)
		}
		return fmt.Errorf("%w: pull failed", ErrComposeFailed)
	})
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	err := m.Apply("web", DeployOpts{})
	if err == nil {
		t.Fatal("expected apply failure")
	}
	for _, want := range []string{"pull failed", "failed to restore previous files from", applySnapshotRoot(deployRoot)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "previous deployment restored") {
		t.Fatalf("a failed restore must not claim success: %v", err)
	}
}

func TestApplyReportsFailedStartAfterRestore(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	managedDeploy(t, repo, deployRoot)
	var ups int
	withFakeCompose(t, func(dir string, args ...string) error {
		if strings.Contains(strings.Join(args, " "), " up ") {
			ups++
			return fmt.Errorf("%w: up %d failed", ErrComposeFailed, ups)
		}
		return nil
	})
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	err := m.Apply("web", DeployOpts{})
	if !errors.Is(err, ErrComposeFailed) {
		t.Fatalf("expected ErrComposeFailed, got %v", err)
	}
	for _, want := range []string{"previous files restored, but starting them also failed", "up 2 failed", "up 1 failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
	if ups != 2 {
		t.Fatalf("expected the original up and one restart, got %d", ups)
	}
}

func TestApplyFailedRestoreKeepsInterruptSentinel(t *testing.T) {
	repo := t.TempDir()
	deployRoot := t.TempDir()
	managedDeploy(t, repo, deployRoot)
	withFakeCompose(t, func(dir string, args ...string) error {
		if !strings.Contains(strings.Join(args, " "), "pull") {
			return nil
		}
		snaps, _ := os.ReadDir(applySnapshotRoot(deployRoot))
		if len(snaps) == 1 {
			_ = os.Remove(filepath.Join(applySnapshotRoot(deployRoot), snaps[0].Name(), "compose.yaml"))
		}
		return fmt.Errorf("%w: canceled", ErrInterrupted)
	})
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	err := m.Apply("web", DeployOpts{})
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("a failed restore must keep ErrInterrupted so a batch stops, got %v", err)
	}
	if !strings.Contains(err.Error(), "failed to restore previous files") {
		t.Fatalf("restore failure not reported: %v", err)
	}
}
