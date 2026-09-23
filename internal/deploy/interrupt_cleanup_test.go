package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/interrupt"
)

func TestDeployCleansUpAfterInterrupt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a shell-backed fake docker executable")
	}
	repo := t.TempDir()
	deployRoot := t.TempDir()
	setupTemplate(t, repo, "web", "HOSTNAME=x\n")
	marks := t.TempDir()
	started := filepath.Join(marks, "started")
	down := filepath.Join(marks, "down")
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \"$*\" in\n" +
		"*version*) exit 0 ;;\n" +
		"*--services*) echo app; exit 0 ;;\n" +
		"*\" up \"*) echo started > " + strconv.Quote(started) + "; exec sleep 30 ;;\n" +
		"*\" down \"*) echo down > " + strconv.Quote(down) + "; exit 0 ;;\n" +
		"*) exit 0 ;;\nesac\n"
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
		errCh <- m.DeployWith("web", DeployOpts{})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(started); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(started); err != nil {
		t.Fatal("compose up did not start")
	}
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, ErrInterrupted) {
			t.Fatalf("expected ErrInterrupted, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("deploy did not return after interrupt")
	}
	if _, err := os.Stat(down); err != nil {
		t.Fatal("compose down must still run after an interrupt so no sidecar keeps running")
	}
	if _, err := os.Stat(filepath.Join(deployRoot, "web")); !os.IsNotExist(err) {
		t.Fatalf("partial deployment not removed after interrupt: %v", err)
	}
}
