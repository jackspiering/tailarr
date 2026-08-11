package scaletail

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

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
	fetch := exec.Command("git", gitHardened("-C", repoPath, "fetch", "--prune", "origin", ref)...)
	fetch.Stdout = os.Stdout
	fetch.Stderr = os.Stderr
	if err := fetch.Run(); err != nil {
		return fmt.Errorf("could not fetch pinned ScaleTail ref: %s: %w", ref, err)
	}
	co := exec.Command("git", gitHardened("-C", repoPath, "checkout", "--detach", "FETCH_HEAD")...)
	co.Stdout = os.Stdout
	co.Stderr = os.Stderr
	if err := co.Run(); err != nil {
		return fmt.Errorf("could not check out pinned ScaleTail ref: %s: %w", ref, err)
	}
	return nil
}
