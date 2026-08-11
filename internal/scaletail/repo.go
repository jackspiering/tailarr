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
func Refresh(repoURL, repoPath, repoRef string, noRefresh bool) error {
	if err := names.ValidateRepoURL(repoURL); err != nil {
		return err
	}
	if err := names.ValidateRepoRef(repoRef); err != nil {
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
		if repoRef != "" {
			return checkoutRef(repoPath, repoRef)
		}
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

	return cloneRepo(repoURL, repoPath, repoRef)
}

func cloneRepo(repoURL, repoPath, repoRef string) error {
	// Commit SHAs cannot be passed to git clone --branch.
	if repoRef != "" && names.IsCommitSHA(repoRef) {
		args := gitHardened("clone", "--no-checkout", repoURL, repoPath)
		cmd := exec.Command("git", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("ScaleTail git clone failed: %w", err)
		}
		return checkoutRef(repoPath, repoRef)
	}

	args := gitHardened("clone", "--depth", "1")
	if repoRef != "" {
		args = append(args, "--branch", repoRef)
	}
	args = append(args, repoURL, repoPath)
	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ScaleTail git clone failed: %w", err)
	}
	return nil
}

func checkoutRef(repoPath, ref string) error {
	// For SHAs, fetch the object; for branches/tags, fetch that ref.
	var fetch *exec.Cmd
	if names.IsCommitSHA(ref) {
		// Unshallow-friendly: fetch the commit (may need full history on shallow clones).
		fetch = exec.Command("git", gitHardened("-C", repoPath, "fetch", "--prune", "origin", ref)...)
	} else {
		fetch = exec.Command("git", gitHardened("-C", repoPath, "fetch", "--prune", "origin", ref)...)
	}
	fetch.Stdout = os.Stdout
	fetch.Stderr = os.Stderr
	if err := fetch.Run(); err != nil {
		// Fallback: fetch all and try checkout by name/sha.
		fetchAll := exec.Command("git", gitHardened("-C", repoPath, "fetch", "--prune", "origin")...)
		fetchAll.Stdout = os.Stdout
		fetchAll.Stderr = os.Stderr
		if err2 := fetchAll.Run(); err2 != nil {
			return fmt.Errorf("could not fetch pinned ScaleTail ref: %s: %w", ref, err)
		}
	}

	// Prefer FETCH_HEAD when fetch of specific ref succeeded; else checkout ref directly.
	co := exec.Command("git", gitHardened("-C", repoPath, "checkout", "--detach", ref)...)
	co.Stdout = os.Stdout
	co.Stderr = os.Stderr
	if err := co.Run(); err != nil {
		// Try FETCH_HEAD as last resort.
		co2 := exec.Command("git", gitHardened("-C", repoPath, "checkout", "--detach", "FETCH_HEAD")...)
		co2.Stdout = os.Stdout
		co2.Stderr = os.Stderr
		if err2 := co2.Run(); err2 != nil {
			return fmt.Errorf("could not check out pinned ScaleTail ref: %s: %w", ref, err)
		}
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
		return fmt.Errorf("ScaleTail repo is detached and no default branch could be determined; set --repo-ref")
	}
	co := exec.Command("git", gitHardened("-C", repoPath, "checkout", "-B", branch, "origin/"+branch)...)
	co.Stdout = os.Stdout
	co.Stderr = os.Stderr
	if err := co.Run(); err != nil {
		return fmt.Errorf("could not leave detached HEAD for pull: %w", err)
	}
	return nil
}
