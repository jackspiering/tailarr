package scaletail

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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

// gitOpTimeout bounds every git invocation so a stalled network op cannot
// freeze the TUI event loop indefinitely (no Ctrl-C escape, raw tty on kill).
const gitOpTimeout = 5 * time.Minute

// runGit runs git with hardened args under a gitOpTimeout deadline and returns
// combined output. A timeout surfaces as an error naming the timeout.
// WaitDelay ensures helper processes (git-remote-https) are killed with the parent.
func runGit(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitOpTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", gitHardened(args...)...)
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("git operation timed out after %s (check network and retry; if pull repeatedly fails with index.lock, remove %s/.git/*.lock)", gitOpTimeout, cmd.Dir)
	}
	return out, err
}

// Refresh clones or updates the ScaleTail repository.
// Git output is captured and returned so callers can render it without
// polluting a TUI alternate screen.
func Refresh(repoURL, repoPath string) (string, error) {
	if err := names.ValidateRepoURL(repoURL); err != nil {
		return "", err
	}
	if paths.IsSymlink(repoPath) {
		return "", fmt.Errorf("ScaleTail path must not be a symlink: %s", repoPath)
	}

	gitDir := filepath.Join(repoPath, ".git")

	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("git is required: %w", err)
	}

	parent := filepath.Dir(repoPath)
	if err := paths.EnsureDir(parent, "ScaleTail parent directory"); err != nil {
		return "", err
	}

	if st, err := os.Stat(gitDir); err == nil && st.IsDir() {
		// Unpinned: ensure we are on a branch before pull (detached HEAD breaks pull).
		if err := ensureOnBranch(repoPath); err != nil {
			return "", err
		}
		out, err := runGit("-C", repoPath, "pull", "--ff-only")
		if err != nil {
			return "", fmt.Errorf("ScaleTail git pull failed: %w: %s", err, out)
		}
		return string(out), nil
	}
	if _, err := os.Stat(repoPath); err == nil {
		// Path exists but is not a git repo.
		if _, err2 := os.Stat(filepath.Join(repoPath, "services")); err2 == nil {
			// Local tree without .git: treat as OK for offline use.
			return "Using local ScaleTail tree (not a git repository); skipped pull.", nil
		}
		// Allow clone into an existing empty directory (git clone accepts it).
		if entries, err := os.ReadDir(repoPath); err == nil && len(entries) == 0 {
			return cloneRepo(repoURL, repoPath)
		}
		return "", fmt.Errorf("%s exists but is not a git repository", repoPath)
	}

	return cloneRepo(repoURL, repoPath)
}
func cloneRepo(repoURL, repoPath string) (string, error) {
	out, err := runGit("clone", "--depth", "1", repoURL, repoPath)
	if err != nil {
		return "", fmt.Errorf("ScaleTail git clone failed: %w: %s", err, out)
	}
	return string(out), nil
}

// ensureOnBranch moves a detached HEAD onto the remote default branch so pull works.
func ensureOnBranch(repoPath string) error {
	// symbolic-ref fails when detached.
	if _, err := runGit("-C", repoPath, "symbolic-ref", "-q", "HEAD"); err == nil {
		return nil // already on a branch
	}
	// Detached: resolve origin/HEAD or fall back to main/master.
	out, err := runGit("-C", repoPath, "rev-parse", "--abbrev-ref", "origin/HEAD")
	branch := strings.TrimSpace(string(out))
	branch = strings.TrimPrefix(branch, "origin/")
	if err != nil || branch == "" || branch == "HEAD" {
		for _, candidate := range []string{"main", "master"} {
			if _, err := runGit("-C", repoPath, "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+candidate); err == nil {
				branch = candidate
				break
			}
		}
	}
	if branch == "" {
		return fmt.Errorf("ScaleTail repo is detached and no default branch could be determined")
	}
	// Do not silently abandon commits ahead of origin/<branch>.
	if out, err := runGit("-C", repoPath, "rev-list", "--count", "origin/"+branch+"..HEAD"); err == nil {
		if cnt := strings.TrimSpace(string(out)); cnt != "" && cnt != "0" {
			return fmt.Errorf("ScaleTail repo has %s detached commit(s) ahead of origin/%s; aborting checkout", cnt, branch)
		}
	}
	if _, err := runGit("-C", repoPath, "checkout", "-B", branch, "origin/"+branch); err != nil {
		return fmt.Errorf("could not leave detached HEAD for pull: %w", err)
	}
	return nil
}
