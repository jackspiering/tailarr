// Package upgrade self-updates the Tailarr binary from GitHub releases.
//
// The latest release tag is resolved through the GitHub API, the asset and
// its SHA256SUMS file are downloaded from the matching GitHub release, and
// the running binary is replaced atomically only after the checksum matches.
package upgrade

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jackspiering/tailarr/internal/security/atomic"
)

// ErrUpToDate reports that the installed version already matches the latest
// release and no upgrade is needed.
var ErrUpToDate = errors.New("already up to date")

// DefaultRepo is the default GitHub owner/repository for releases.
const DefaultRepo = "jackspiering/tailarr"

// Options configures Latest and Upgrade.
type Options struct {
	// Repo is the GitHub owner/repository (default DefaultRepo).
	Repo string
	// Current is the installed version string (default: version.Version).
	Current string
	// Force reinstalls even when Current already matches the latest release.
	Force bool
	// Client is the HTTP client used for all requests (default: 30s timeout).
	Client *http.Client
	// Out receives progress lines (default: no output).
	Out io.Writer

	// apiBase and releaseBase override the GitHub endpoints (tests only).
	apiBase     string
	releaseBase string
}

const (
	defaultAPIBase     = "https://api.github.com"
	defaultReleaseBase = "https://github.com"
)

// releaseInfo is the minimal GitHub API response needed to find the latest tag.
type releaseInfo struct {
	TagName string `json:"tag_name"`
}

func (o Options) repo() string {
	if o.Repo == "" {
		return DefaultRepo
	}
	return o.Repo
}

func (o Options) client() *http.Client {
	if o.Client != nil {
		return o.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (o Options) apiURL(repo string) string {
	base := o.apiBase
	if base == "" {
		base = defaultAPIBase
	}
	return base + "/repos/" + repo + "/releases/latest"
}

func (o Options) releaseURL(repo, tag string) string {
	base := o.releaseBase
	if base == "" {
		base = defaultReleaseBase
	}
	return base + "/" + repo + "/releases/download/" + tag
}

func (o Options) progress(format string, args ...any) {
	if o.Out != nil {
		_, _ = fmt.Fprintf(o.Out, format, args...)
	}
}

// Latest fetches the latest release tag (for example "v0.3.0") for the repo.
func Latest(opts Options) (string, error) {
	repo := opts.repo()
	if !strings.Contains(repo, "/") || strings.HasPrefix(repo, "/") || strings.HasSuffix(repo, "/") {
		return "", fmt.Errorf("invalid repository %q (want owner/repo)", repo)
	}
	url := opts.apiURL(repo)
	resp, err := opts.client().Get(url)
	if err != nil {
		return "", fmt.Errorf("fetch latest release: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch latest release: HTTP %s", resp.Status)
	}
	var info releaseInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&info); err != nil {
		return "", fmt.Errorf("decode latest release: %w", err)
	}
	if info.TagName == "" {
		return "", fmt.Errorf("latest release has no tag_name")
	}
	return info.TagName, nil
}

// Upgrade replaces the running binary with the latest release. It returns the
// installed tag on success, and ErrUpToDate (wrapped) when Current is already
// at or above the latest release and Force is false.
func Upgrade(opts Options) (string, error) {
	tag, err := Latest(opts)
	if err != nil {
		return "", err
	}
	if !opts.Force && Compare(opts.Current, tag) >= 0 {
		return "", fmt.Errorf("%w: installed %s, latest %s", ErrUpToDate, opts.Current, tag)
	}

	osName, arch := runtime.GOOS, runtime.GOARCH
	switch osName {
	case "linux", "darwin":
	default:
		return "", fmt.Errorf("unsupported OS for self-upgrade: %s", osName)
	}
	switch arch {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf("unsupported architecture for self-upgrade: %s", arch)
	}
	asset := fmt.Sprintf("tailarr-%s-%s", osName, arch)

	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate binary: %w", err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", fmt.Errorf("resolve binary path: %w", err)
	}

	opts.progress("Downloading %s %s ...\n", asset, tag)
	tmpdir, err := os.MkdirTemp("", "tailarr-upgrade-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpdir) }()

	base := opts.releaseURL(opts.repo(), tag)
	assetPath := filepath.Join(tmpdir, asset)
	if err := download(opts.client(), base+"/"+asset, assetPath); err != nil {
		return "", fmt.Errorf("download %s: %w", asset, err)
	}
	sumsPath := filepath.Join(tmpdir, "SHA256SUMS")
	if err := download(opts.client(), base+"/SHA256SUMS", sumsPath); err != nil {
		return "", fmt.Errorf("download SHA256SUMS: %w", err)
	}
	data, err := os.ReadFile(assetPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", asset, err)
	}
	if err := verifySum(data, sumsPath, asset); err != nil {
		return "", err
	}
	opts.progress("Checksum OK\n")

	// Replace the binary atomically in place (temp file + rename).
	if err := atomic.WriteFile(exe, data, 0o755); err != nil {
		return "", fmt.Errorf("install binary: %w", err)
	}
	opts.progress("Installed %s\n", exe)
	return tag, nil
}

// download fetches url into path (mode 0600, unlinked on error).
func download(client *http.Client, url, path string) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	return f.Close()
}

// verifySum checks data against the asset's entry in the SHA256SUMS file at
// sumsPath. The asset name must match exactly (no path traversal).
func verifySum(data []byte, sumsPath, asset string) error {
	if asset == "" || asset == "." || strings.Contains(asset, "/") {
		return fmt.Errorf("invalid asset name %q", asset)
	}
	sums, err := os.ReadFile(sumsPath)
	if err != nil {
		return fmt.Errorf("read checksums: %w", err)
	}
	var expected string
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == asset {
			expected = fields[0]
			break
		}
	}
	if expected == "" {
		return fmt.Errorf("checksum for %s not found in SHA256SUMS", asset)
	}
	sum := sha256.Sum256(data)
	if actual := hex.EncodeToString(sum[:]); !strings.EqualFold(expected, actual) {
		return fmt.Errorf("checksum mismatch for %s (expected %s, got %s)", asset, expected, actual)
	}
	return nil
}
