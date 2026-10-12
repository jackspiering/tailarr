package upgrade

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ghNotLoggedIn is the gh exit code for a missing login.
const ghNotLoggedIn = 4

// releaseWorkflowPath is the workflow that builds and attests release assets.
// Moving or renaming it breaks the attestation check of older releases, so
// keep the path stable.
const releaseWorkflowPath = ".github/workflows/release.yml"

// provenanceTimeout bounds the attestation lookup.
var provenanceTimeout = 2 * time.Minute

// checkProvenance verifies the GitHub build attestation of the downloaded
// asset with the GitHub CLI. The checksum only proves the download matches
// SHA256SUMS from the same release; the attestation proves the release
// workflow of repo built it. Without gh, or when gh is not logged in, the
// check is skipped and note says why. A failed verification is an error.
func checkProvenance(assetPath, repo string) (verified bool, note string, err error) {
	gh, lerr := exec.LookPath("gh")
	if lerr != nil {
		return false, "build attestation not checked: install the GitHub CLI (gh) to verify it", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), provenanceTimeout)
	defer cancel()
	// Pin the signer to the release workflow: --repo alone accepts an
	// attestation from any workflow in the repository.
	out, rerr := exec.CommandContext(ctx, gh, "attestation", "verify", assetPath, "--repo", repo,
		"--signer-workflow", repo+"/"+releaseWorkflowPath).CombinedOutput()
	if rerr == nil {
		return true, "", nil
	}
	var exitErr *exec.ExitError
	if errors.As(rerr, &exitErr) && exitErr.ExitCode() == ghNotLoggedIn {
		return false, "build attestation not checked: run gh auth login to verify it", nil
	}
	return false, "", fmt.Errorf("build attestation check failed for %s: %v: %s", repo, rerr, strings.TrimSpace(string(out)))
}
