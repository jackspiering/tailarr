package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWithin(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	child := filepath.Join(root, "svc")
	ok, err := Within(child, root)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("expected %s within %s", child, root)
	}
	// Outside
	other := t.TempDir()
	outside := filepath.Join(other, "x")
	ok, err = Within(outside, root)
	if err != nil {
		// parent of outside is other, which exists; Within should return false not error
		// if parent exists
	}
	if ok {
		t.Fatal("outside path should not be within root")
	}
}

func TestJoinUnder(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	p, err := JoinUnder(root, "myservice")
	if err != nil {
		t.Fatal(err)
	}
	rootAbs, err := AbsExistingDir(root)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(rootAbs, "myservice")
	if p != want {
		t.Fatalf("got %s want %s", p, want)
	}
	if _, err := JoinUnder(root, "../escape"); err == nil {
		t.Fatal("expected error for ../escape")
	}
	if _, err := JoinUnder(root, "a/b"); err == nil {
		t.Fatal("expected error for nested segment")
	}
}

func TestRefuseSymlink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := RefuseSymlink(link, "test"); err == nil {
		t.Fatal("expected refuse symlink")
	}
	if err := RefuseSymlink(target, "test"); err != nil {
		t.Fatal(err)
	}
}

func TestContainsSymlinks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	found, err := ContainsSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if found != "" {
		t.Fatalf("unexpected symlink: %s", found)
	}
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sub, filepath.Join(root, "l")); err != nil {
		t.Fatal(err)
	}
	found, err = ContainsSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if found == "" {
		t.Fatal("expected to find symlink")
	}
}

func TestEnsureDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	p := filepath.Join(root, "a", "b")
	if err := EnsureDir(p, "nested"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil || !info.IsDir() {
		t.Fatal("dir not created")
	}
}
