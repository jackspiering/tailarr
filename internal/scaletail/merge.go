package scaletail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/jackspiering/tailarr/internal/interrupt"
)

// mergeTimeout bounds one git merge-file run. It merges small local files.
var mergeTimeout = 30 * time.Second

// maxMergeBytes bounds each input and the merged output of MergeFile.
const maxMergeBytes = 1 << 20

// MergeFile merges the changes from base to theirs into ours with
// "git merge-file", the way git merges one file. It returns the merged text
// and whether the two sides changed the same lines. On a conflict merged is
// nil: Tailarr never writes conflict markers into a file that compose runs.
// The inputs go to private temp files and never appear in an error.
func MergeFile(ours, base, theirs []byte) (merged []byte, conflict bool, err error) {
	for _, b := range [][]byte{ours, base, theirs} {
		if len(b) > maxMergeBytes {
			return nil, false, fmt.Errorf("merge: file is larger than %d bytes", maxMergeBytes)
		}
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil, false, fmt.Errorf("merge: git is not installed: %w", err)
	}
	dir, err := os.MkdirTemp("", "tailarr-merge-")
	if err != nil {
		return nil, false, fmt.Errorf("merge: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	names := []string{"deployed", "base", "catalog"}
	for i, b := range [][]byte{ours, base, theirs} {
		if err := os.WriteFile(filepath.Join(dir, names[i]), b, 0o600); err != nil {
			return nil, false, fmt.Errorf("merge: %w", err)
		}
	}

	ctx, cancel := context.WithTimeout(interrupt.Context(), mergeTimeout)
	defer cancel()
	args := gitHardened("merge-file", "-p",
		"-L", "deployed", "-L", "base", "-L", "catalog",
		"deployed", "base", "catalog")
	cmd := exec.CommandContext(ctx, "git", args...)
	configureGitCmd(cmd)
	cmd.Dir = dir
	// User and system config could change how git merges; ignore both.
	cmd.Env = append(gitEnv(os.Environ()), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	cmd.Stdout = &limitWriter{w: &out, n: maxMergeBytes}
	cmd.Stderr = io.Discard
	err = cmd.Run()
	if ctx.Err() != nil {
		return nil, false, fmt.Errorf("merge: git merge-file stopped: %w", ctx.Err())
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr) && exitErr.ExitCode() >= 1 && exitErr.ExitCode() <= 127:
		// git merge-file exits with the number of conflicts, capped at 127.
		return nil, true, nil
	default:
		return nil, false, fmt.Errorf("merge: git merge-file failed: %w", err)
	}
	if lw := cmd.Stdout.(*limitWriter); lw.over {
		return nil, false, fmt.Errorf("merge: result is larger than %d bytes", maxMergeBytes)
	}
	return out.Bytes(), false, nil
}

// limitWriter keeps at most n bytes and records that more arrived.
type limitWriter struct {
	w    io.Writer
	n    int
	over bool
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if len(p) > l.n {
		l.over = true
		_, _ = l.w.Write(p[:l.n])
		l.n = 0
		return len(p), nil
	}
	l.n -= len(p)
	return l.w.Write(p)
}
