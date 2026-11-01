package deploy

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackspiering/tailarr/internal/config"
)

// managedDeployment creates a managed deployment of "web" without compose up.
func managedDeployment(t *testing.T) (repo, deployRoot, dest string) {
	t.Helper()
	repo = t.TempDir()
	deployRoot = t.TempDir()
	setupTemplate(t, repo, "web", "HOSTNAME=x\n")
	dest = filepath.Join(deployRoot, "web")
	if err := copyTemplate(filepath.Join(repo, "services", "web"), dest); err != nil {
		t.Fatal(err)
	}
	if err := writeOverride("web", dest); err != nil {
		t.Fatal(err)
	}
	return repo, deployRoot, dest
}

// lockedContainerDir mimics a sidecar state dir that a root container
// created with mode 0700: a non-root operator cannot list it.
func lockedContainerDir(t *testing.T, dest string) string {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root reads mode 0000 directories; test needs a non-root user")
	}
	locked := filepath.Join(dest, "ts", "state")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	return locked
}

func TestStopAndRestartSkipUnreadableContainerData(t *testing.T) {
	repo, deployRoot, dest := managedDeployment(t)
	lockedContainerDir(t, dest)
	withFakeCompose(t, func(dir string, args ...string) error { return nil })
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	if err := m.Stop("web"); err != nil {
		t.Errorf("Stop must not read container data: %v", err)
	}
	if err := m.Restart("web"); err != nil {
		t.Errorf("Restart must not read container data: %v", err)
	}
}

func TestStopIgnoresSymlinksInContainerData(t *testing.T) {
	repo, deployRoot, dest := managedDeployment(t)
	data := filepath.Join(dest, "web-data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("current.db", filepath.Join(data, "latest.db")); err != nil {
		t.Fatal(err)
	}
	withFakeCompose(t, func(dir string, args ...string) error { return nil })
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	if err := m.Stop("web"); err != nil {
		t.Fatalf("Stop must not refuse a symlink the container wrote into its data: %v", err)
	}
}

func TestStopRefusesSymlinkedManagedFiles(t *testing.T) {
	for _, name := range []string{"compose.yaml", overrideFilename, ".env"} {
		repo, deployRoot, dest := managedDeployment(t)
		target := filepath.Join(t.TempDir(), name)
		if err := os.Rename(filepath.Join(dest, name), target); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dest, name)); err != nil {
			t.Fatal(err)
		}
		called := false
		withFakeCompose(t, func(dir string, args ...string) error { called = true; return nil })
		m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
		if err := m.Stop("web"); !errors.Is(err, ErrSymlink) {
			t.Errorf("%s: Stop must refuse a symlinked managed file, got %v", name, err)
		}
		if called {
			t.Errorf("%s: compose must not run after a symlink refusal", name)
		}
	}
}

func TestRemoveExplainsUnreadableContainerData(t *testing.T) {
	repo, deployRoot, dest := managedDeployment(t)
	lockedContainerDir(t, dest)
	withFakeCompose(t, func(dir string, args ...string) error { return nil })
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	err := m.Remove("web")
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("expected a permission error, got %v", err)
	}
	if !strings.Contains(err.Error(), "run Tailarr as root") {
		t.Fatalf("error must tell the operator to run as root, got %v", err)
	}
	if _, serr := os.Stat(filepath.Join(dest, "compose.yaml")); serr != nil {
		t.Fatalf("deployment must stay intact: %v", serr)
	}
}

func TestRemoveFreesNameWhenFilesCannotBeDeleted(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can delete a read-only directory; test needs a non-root user")
	}
	repo, deployRoot, dest := managedDeployment(t)
	stuck := filepath.Join(dest, "data", "sub")
	if err := os.MkdirAll(stuck, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stuck, "db"), []byte("container data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A 0555 directory with a file stands in for files that a root container
	// wrote: this user can read them but not delete them.
	if err := os.Chmod(stuck, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = filepath.Walk(deployRoot, func(p string, info os.FileInfo, err error) error {
			if err == nil && info.IsDir() {
				_ = os.Chmod(p, 0o755)
			}
			return nil
		})
	})
	var composeCalls []string
	withFakeCompose(t, func(dir string, args ...string) error {
		composeCalls = append(composeCalls, strings.Join(args, " "))
		return nil
	})
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	err := m.Remove("web")
	if err == nil {
		t.Fatal("expected an error about the files that could not be deleted")
	}
	for _, want := range []string{"containers for web are down", "name is free", "run as root", "rm -rf", "The backup is"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("the service directory must be gone so the name is free: %v", err)
	}
	trash, _ := filepath.Glob(filepath.Join(deployRoot, config.BackupDirName, trashDirName, "web-*"))
	if len(trash) != 1 || !strings.Contains(err.Error(), trash[0]) {
		t.Fatalf("expected one trash entry named in the error, got %v", trash)
	}
	// The name is free: Deploy works again.
	if err := m.Deploy("web", DeployOpts{}); err != nil {
		t.Fatalf("redeploy after a partial remove: %v", err)
	}
}

func TestRemoveLeavesNoTrashOnSuccess(t *testing.T) {
	repo, deployRoot, dest := managedDeployment(t)
	withFakeCompose(t, func(string, ...string) error { return nil })
	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	if err := m.Remove("web"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("deployment still exists: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(deployRoot, config.BackupDirName, trashDirName, "*")); len(left) != 0 {
		t.Fatalf("trash not emptied: %v", left)
	}
}

// setBeforeBackupCopy installs a copy hook for one test and restores it after.
func setBeforeBackupCopy(t *testing.T, hook func(string)) {
	t.Helper()
	prev := beforeBackupCopy
	beforeBackupCopy = hook
	t.Cleanup(func() { beforeBackupCopy = prev })
}

// backedUpBaseNames returns the base names of the regular files under dir.
func backedUpBaseNames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			names[filepath.Base(p)] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func TestBackupSkipsFileDeletedDuringWalk(t *testing.T) {
	root := t.TempDir()
	svc := filepath.Join(root, "web")
	if err := os.MkdirAll(svc, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(svc, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	setBeforeBackupCopy(t, func(p string) {
		if filepath.Base(p) == "a" {
			_ = os.Remove(filepath.Join(svc, "b"))
		}
	})
	dest, err := Backup(root, "web", svc)
	if err != nil {
		t.Fatalf("Backup failed when a file vanished during the walk: %v", err)
	}
	got := backedUpBaseNames(t, dest)
	if !got["a"] || !got["c"] {
		t.Errorf("backup is missing a or c: %v", got)
	}
	if got["b"] {
		t.Errorf("backup copied b, which was deleted before its copy: %v", got)
	}
}

func TestBackupSkipsFileDeletedBeforeOpen(t *testing.T) {
	root := t.TempDir()
	svc := filepath.Join(root, "web")
	if err := os.MkdirAll(svc, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(svc, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	setBeforeBackupCopy(t, func(p string) {
		if filepath.Base(p) == "b" {
			_ = os.Remove(p)
		}
	})
	dest, err := Backup(root, "web", svc)
	if err != nil {
		t.Fatalf("Backup failed when a file was deleted before open: %v", err)
	}
	if !backedUpBaseNames(t, dest)["a"] {
		t.Errorf("backup is missing a")
	}
}

func TestBackupStillFailsOnUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads mode 0 files; cannot test permission failure")
	}
	root := t.TempDir()
	svc := filepath.Join(root, "web")
	if err := os.MkdirAll(svc, 0o700); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(svc, "locked")
	if err := os.WriteFile(locked, []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}
	_, err := Backup(root, "web", svc)
	if err == nil {
		t.Fatal("Backup succeeded on an unreadable file")
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("error does not wrap fs.ErrPermission: %v", err)
	}
}

func TestApplyNamesFixForForeignBackupDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can open mode 0 directories; test needs a non-root user")
	}
	repo, deployRoot, _ := managedDeployment(t)
	backups := filepath.Join(deployRoot, config.BackupDirName)
	if err := os.Mkdir(backups, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(backups, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(backups, 0o700) })
	withFakeCompose(t, func(string, ...string) error { return nil })

	m := &Manager{Cfg: &config.Config{RepoPath: repo, DeployPath: deployRoot}}
	err := m.Apply("web", DeployOpts{})
	if err == nil {
		t.Fatal("expected Apply to fail on a backup directory that it cannot use")
	}
	if !strings.Contains(err.Error(), "with chown") {
		t.Fatalf("error should name the chown fix, got: %v", err)
	}
}

func TestAdminDirsTakeDeployRootOwner(t *testing.T) {
	_, deployRoot, dest := managedDeployment(t)
	type call struct{ path, ref string }
	var calls []call
	prev := chownLike
	chownLike = func(path, ref string) error {
		calls = append(calls, call{path, ref})
		return nil
	}
	t.Cleanup(func() { chownLike = prev })

	if _, err := Backup(deployRoot, "web", dest); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotManaged(deployRoot, "web", dest, dest); err != nil {
		t.Fatal(err)
	}
	lockPath, err := ServiceLockPath(deployRoot, "web")
	if err != nil {
		t.Fatal(err)
	}
	l, err := AcquireLock(lockPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Release() }()

	for _, name := range []string{
		config.BackupDirName,
		filepath.Join(config.BackupDirName, "apply-snapshots"),
		config.LockDirName,
	} {
		want := filepath.Join(deployRoot, name)
		found := false
		for _, c := range calls {
			if c.path == want && c.ref == deployRoot {
				found = true
			}
		}
		if !found {
			t.Errorf("no chownLike call for %s with ref %s; calls: %v", want, deployRoot, calls)
		}
	}
}
