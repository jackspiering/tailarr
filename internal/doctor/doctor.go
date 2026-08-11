// Package doctor checks host readiness for Tailarr operations.
package doctor

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/jackspiering/tailarr/internal/config"
	"github.com/jackspiering/tailarr/internal/deploy"
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

	if cfg.RepoRef != "" {
		r.add(Info, "repo-ref", "pinned ScaleTail ref: "+cfg.RepoRef)
	}
	r.add(Info, "paths", fmt.Sprintf("config=%s repo=%s deploy=%s", cfg.ConfigPath, cfg.RepoPath, cfg.DeployPath))
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
	// Probe access without privilege escalation.
	if wantWrite {
		probe := filepath.Join(path, ".tailarr-doctor-write-probe")
		if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
			r.add(Warn, label, "not writable by this user: "+path)
			return
		}
		_ = os.Remove(probe)
	}
	r.add(OK, label, path)
}

// Write prints human-readable results to w.
func Write(w io.Writer, r Result) {
	for _, c := range r.Checks {
		mark := ".."
		switch c.Level {
		case OK:
			mark = "ok"
		case Warn:
			mark = "!!"
		case Fail:
			mark = "XX"
		case Info:
			mark = "--"
		}
		fmt.Fprintf(w, "[%s] %-12s %s\n", mark, c.Name, c.Message)
	}
	if r.Healthy() {
		fmt.Fprintln(w, "\nDoctor: no hard failures.")
	} else {
		fmt.Fprintln(w, "\nDoctor: one or more checks failed.")
	}
}
