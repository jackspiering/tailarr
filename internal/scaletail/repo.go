package scaletail

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jackspiering/tailarr/internal/security/names"
	"github.com/jackspiering/tailarr/internal/security/paths"
)

// gitHardened returns base args that disable risky git protocols.
func gitHardened(extra ...string) []string {
	args := []string{
		"-c", "protocol.ext.allow=never",
		"-c", "protocol.file.allow=never",
	}
	return append(args, extra...)
}

// Refresh clones or updates the ScaleTail repository.
// When noRefresh is true, only validates the local tree if present.
func Refresh(repoURL, repoPath string, noRefresh bool) error {
	if err := names.ValidateRepoURL(repoURL); err != nil {
		return err
	}
	if paths.IsSymlink(repoPath) {
		return fmt.Errorf("ScaleTail path must not be a symlink: %s", repoPath)
	}

	gitDir := filepath.Join(repoPath, ".git")
	if noRefresh {
		if _, err := os.Stat(gitDir); err != nil {
			// Allow non-git local trees (testdata / vendored copies).
			if _, err2 := os.Stat(filepath.Join(repoPath, "services")); err2 != nil {
				return fmt.Errorf("ScaleTail path missing or incomplete: %s", repoPath)
			}
		}
		return nil
	}

	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git is required: %w", err)
	}

	parent := filepath.Dir(repoPath)
	if err := paths.EnsureDir(parent, "ScaleTail parent directory"); err != nil {
		return err
	}

	if st, err := os.Stat(gitDir); err == nil && st.IsDir() {
		// Unpinned: ensure we are on a branch before pull (detached HEAD breaks pull).
		if err := ensureOnBranch(repoPath); err != nil {
			return err
		}
		cmd := exec.Command("git", gitHardened("-C", repoPath, "pull", "--ff-only")...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("ScaleTail git pull failed: %w", err)
		}
		return nil
	}

	if _, err := os.Stat(repoPath); err == nil {
		// Path exists but is not a git repo.
		if _, err2 := os.Stat(filepath.Join(repoPath, "services")); err2 == nil {
			// Local tree without .git: treat as OK for offline use.
			return nil
		}
		return fmt.Errorf("%s exists but is not a git repository", repoPath)
	}

	return cloneRepo(repoURL, repoPath)
}

func cloneRepo(repoURL, repoPath string) error {
	args := gitHardened("clone", "--depth", "1", repoURL, repoPath)
	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ScaleTail git clone failed: %w", err)
	}
	return nil
}

// ensureOnBranch moves a detached HEAD onto the remote default branch so pull works.
func ensureOnBranch(repoPath string) error {
	// symbolic-ref fails when detached.
	check := exec.Command("git", gitHardened("-C", repoPath, "symbolic-ref", "-q", "HEAD")...)
	if err := check.Run(); err == nil {
		return nil // already on a branch
	}
	// Detached: resolve origin/HEAD or fall back to main/master.
	out, err := exec.Command("git", gitHardened("-C", repoPath, "rev-parse", "--abbrev-ref", "origin/HEAD")...).Output()
	branch := strings.TrimSpace(string(out))
	branch = strings.TrimPrefix(branch, "origin/")
	if err != nil || branch == "" || branch == "HEAD" {
		for _, candidate := range []string{"main", "master"} {
			if exec.Command("git", gitHardened("-C", repoPath, "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+candidate)...).Run() == nil {
				branch = candidate
				break
			}
		}
	}
	if branch == "" {
		return fmt.Errorf("ScaleTail repo is detached and no default branch could be determined")
	}
	co := exec.Command("git", gitHardened("-C", repoPath, "checkout", "-B", branch, "origin/"+branch)...)
	co.Stdout = os.Stdout
	co.Stderr = os.Stderr
	if err := co.Run(); err != nil {
		return fmt.Errorf("could not leave detached HEAD for pull: %w", err)
	}
	return nil
}
