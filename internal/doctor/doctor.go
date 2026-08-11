// Package doctor checks host readiness for Tailarr operations.
package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/deploy"
	"github.com/jackspiering/tailarr/internal/security/names"
	"github.com/jackspiering/tailarr/internal/security/paths"
)

// Level is check severity.
type Level string

const (
	OK   Level = "ok"
	Warn Level = "warn"
	Fail Level = "fail"
	Info Level = "info"
)

// Check is one doctor result line.
type Check struct {
	Level   Level
	Name    string
	Message string
}

// Result aggregates checks.
type Result struct {
	Checks []Check
}

// Healthy is true when no Fail checks exist.
func (r Result) Healthy() bool {
	for _, c := range r.Checks {
		if c.Level == Fail {
			return false
		}
	}
	return true
}

// Run performs non-privileged host checks.
func Run(cfg config.Config) Result {
	var r Result
	r.addCommand("git", true)
	r.addCommand("docker", true)
	if deploy.DockerOK() {
		if deploy.ComposeOK() {
			r.add(OK, "compose", "docker compose is available")
		} else {
			r.add(Fail, "compose", "Docker Compose v2 is required (docker compose version failed)")
		}
		if deploy.DaemonOK() {
			r.add(OK, "daemon", "Docker daemon is reachable")
		} else {
			r.add(Warn, "daemon", "Docker daemon is not accessible to this user")
		}
	}

	r.checkPath("config dir", filepath.Dir(cfg.ConfigPath), true)
	r.checkPath("ScaleTail parent", filepath.Dir(cfg.RepoPath), true)
	r.checkPath("deploy path", cfg.DeployPath, true)
	r.checkPath("log dir", filepath.Dir(cfg.LogPath), true)
	r.checkPath("authkeys dir", filepath.Dir(cfg.AuthkeysPath), true)

	if paths.IsSymlink(cfg.ConfigPath) {
		r.add(Warn, "config", "config file is a symlink: "+cfg.ConfigPath)
	}
	if paths.IsSymlink(cfg.RepoPath) {
		r.add(Fail, "repo", "ScaleTail path must not be a symlink: "+cfg.RepoPath)
	}
	if paths.IsSymlink(cfg.DeployPath) {
		r.add(Fail, "deploy", "deployment root must not be a symlink: "+cfg.DeployPath)
	}

	if st, err := os.Stat(filepath.Join(cfg.RepoPath, "services")); err == nil && st.IsDir() {
		r.add(OK, "catalog", "ScaleTail services directory present")
	} else {
		r.add(Warn, "catalog", "ScaleTail services directory not found (run list/deploy to clone)")
	}

	// Redact credentials if a misconfigured URL slipped through.
	safeURL := names.RedactRepoURL(cfg.RepoURL)
	r.add(Info, "paths", fmt.Sprintf("config=%s repo=%s deploy=%s url=%s",
		cfg.ConfigPath, cfg.RepoPath, cfg.DeployPath, safeURL))
	return r
}

func (r *Result) add(level Level, name, msg string) {
	r.Checks = append(r.Checks, Check{Level: level, Name: name, Message: msg})
}

func (r *Result) addCommand(name string, required bool) {
	if _, err := exec.LookPath(name); err != nil {
		if required {
			r.add(Fail, name, name+" not found in PATH")
		} else {
			r.add(Warn, name, name+" not found in PATH")
		}
		return
	}
	r.add(OK, name, name+" found in PATH")
}

func (r *Result) checkPath(label, path string, wantWrite bool) {
	if path == "" {
		r.add(Fail, label, "path is empty")
		return
	}
	if paths.IsSymlink(path) {
		r.add(Fail, label, "must not be a symlink: "+path)
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			r.add(Warn, label, "missing (will be created when needed): "+path)
			return
		}
		r.add(Fail, label, err.Error())
		return
	}
	if !info.IsDir() {
		r.add(Fail, label, "exists but is not a directory: "+path)
		return
	}
	// Probe write access with O_EXCL unique file; never follow/overwrite via symlink.
	if wantWrite {
		if err := probeWritable(path); err != nil {
			r.add(Warn, label, "not writable by this user: "+path)
			return
		}
	}
	r.add(OK, label, path)
}

// probeWritable creates an exclusive probe file and removes it.
// Refuses to write if a probe path already exists (including as a symlink).
func probeWritable(dir string) error {
	name := fmt.Sprintf(".tailarr-doctor-write-probe-%d-%d", os.Getpid(), time.Now().UnixNano())
	probe := filepath.Join(dir, name)
	// Lstat: if anything is already there (symlink or file), do not touch it.
	if _, err := os.Lstat(probe); err == nil {
		return fmt.Errorf("probe path already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, _ = f.Write([]byte("ok"))
	_ = f.Close()
	if err := os.Remove(probe); err != nil {
		return err
	}
	return nil
}
