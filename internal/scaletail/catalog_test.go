package scaletail

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListAvailableTestdata(t *testing.T) {
	// go test runs with cwd = package directory.
	repo := filepath.Join("..", "..", "testdata", "scaletail")
	if _, err := os.Stat(filepath.Join(repo, "services")); err != nil {
		t.Fatalf("cannot locate testdata/scaletail: %v", err)
	}

	svcs, err := ListAvailable(repo)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, s := range svcs {
		names[s.Name] = true
	}
	if !names["good-service"] || !names["another-svc"] {
		t.Fatalf("expected good services, got %v", names)
	}
	if names["bad..name"] {
		t.Fatal("invalid name should be skipped")
	}
	if names["no-compose"] || names["no-env"] {
		t.Fatal("incomplete services should be skipped")
	}
	if names["skip-symlink"] {
		// empty dir without compose - skipped
	}
}

func TestListAvailableTemp(t *testing.T) {
	root := t.TempDir()
	svc := filepath.Join(root, "services", "demo")
	if err := os.MkdirAll(svc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svc, "compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svc, ".env"), []byte("A=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// symlink service dir should be skipped
	if err := os.Symlink(svc, filepath.Join(root, "services", "linked")); err != nil {
		t.Fatal(err)
	}

	svcs, err := ListAvailable(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(svcs) != 1 || svcs[0].Name != "demo" {
		t.Fatalf("got %#v", svcs)
	}
}

func TestListDeployed(t *testing.T) {
	root := t.TempDir()
	svc := filepath.Join(root, "web")
	if err := os.MkdirAll(svc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svc, "compose.yml"), []byte("x: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// backup dir ignored
	_ = os.Mkdir(filepath.Join(root, ".tailarr_backups"), 0o700)

	svcs, err := ListDeployed(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(svcs) != 1 || svcs[0].Name != "web" {
		t.Fatalf("got %#v", svcs)
	}
}

func TestComposeFileIn(t *testing.T) {
	dir := t.TempDir()
	if _, ok := ComposeFileIn(dir); ok {
		t.Fatal("empty")
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, ok := ComposeFileIn(dir)
	if !ok || filepath.Base(p) != "compose.yml" {
		t.Fatalf("got %s %v", p, ok)
	}
}
