package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	oldBinary = "old tailarr binary\n"
	newBinary = "new tailarr binary\n"
)

// releaseServer serves a latest release v9.9.9 with the asset for the host
// platform. sums overrides the SHA256SUMS body when it is not empty.
func releaseServer(t *testing.T, asset, sums string) *httptest.Server {
	t.Helper()
	name := fmt.Sprintf("tailarr-%s-%s", runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256([]byte(asset))
	if sums == "" {
		sums = hex.EncodeToString(sum[:]) + "  " + name + "\n"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/jackspiering/tailarr/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"tag_name":"v9.9.9"}`)
	})
	mux.HandleFunc("/jackspiering/tailarr/releases/download/v9.9.9/"+name, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, asset)
	})
	mux.HandleFunc("/jackspiering/tailarr/releases/download/v9.9.9/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, sums)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// upgradeTarget writes the binary that Upgrade may replace and returns its
// path and options that point at srv. gh is hidden so the attestation check
// is skipped unless a test installs a fake.
func upgradeTarget(t *testing.T, srv *httptest.Server) (string, Options) {
	t.Helper()
	switch runtime.GOOS {
	case "linux", "darwin":
	default:
		t.Skip("self-upgrade supports linux and darwin only")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("self-upgrade supports amd64 and arm64 only")
	}
	fakeGH(t, -1)
	exe := filepath.Join(t.TempDir(), "tailarr")
	if err := os.WriteFile(exe, []byte(oldBinary), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe, Options{
		Current:     "0.1.0",
		Client:      srv.Client(),
		apiBase:     srv.URL,
		releaseBase: srv.URL,
		exePath:     exe,
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUpgradeInstallsVerifiedBinary(t *testing.T) {
	srv := releaseServer(t, newBinary, "")
	exe, opts := upgradeTarget(t, srv)
	var out strings.Builder
	opts.Out = &out
	tag, err := Upgrade(opts)
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v9.9.9" {
		t.Fatalf("tag = %q", tag)
	}
	if got := readFile(t, exe); got != newBinary {
		t.Fatalf("binary = %q, want the new release", got)
	}
	if info, err := os.Stat(exe); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("installed mode = %v, %v", info, err)
	}
	if !strings.Contains(out.String(), "Checksum OK") || !strings.Contains(out.String(), "attestation") {
		t.Fatalf("progress lacks checksum or attestation lines:\n%s", out.String())
	}
}

func TestUpgradeKeepsBinaryOnChecksumMismatch(t *testing.T) {
	name := fmt.Sprintf("tailarr-%s-%s", runtime.GOOS, runtime.GOARCH)
	srv := releaseServer(t, newBinary, strings.Repeat("0", 64)+"  "+name+"\n")
	exe, opts := upgradeTarget(t, srv)
	_, err := Upgrade(opts)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected a checksum mismatch, got %v", err)
	}
	if got := readFile(t, exe); got != oldBinary {
		t.Fatalf("binary was replaced despite a bad checksum: %q", got)
	}
}

func TestUpgradeKeepsBinaryWhenChecksumMissing(t *testing.T) {
	srv := releaseServer(t, newBinary, strings.Repeat("0", 64)+"  tailarr-other-asset\n")
	exe, opts := upgradeTarget(t, srv)
	if _, err := Upgrade(opts); err == nil || !strings.Contains(err.Error(), "not found in SHA256SUMS") {
		t.Fatalf("expected a missing checksum error, got %v", err)
	}
	if got := readFile(t, exe); got != oldBinary {
		t.Fatalf("binary was replaced: %q", got)
	}
}

func TestUpgradeKeepsBinaryWhenAttestationFails(t *testing.T) {
	srv := releaseServer(t, newBinary, "")
	exe, opts := upgradeTarget(t, srv)
	fakeGH(t, 1)
	if _, err := Upgrade(opts); err == nil || !strings.Contains(err.Error(), "attestation check failed") {
		t.Fatalf("expected an attestation failure, got %v", err)
	}
	if got := readFile(t, exe); got != oldBinary {
		t.Fatalf("binary was replaced despite a failed attestation: %q", got)
	}
}

func TestUpgradeStopsAtUpToDateVersion(t *testing.T) {
	srv := releaseServer(t, newBinary, "")
	exe, opts := upgradeTarget(t, srv)
	opts.Current = "9.9.9"
	if _, err := Upgrade(opts); !errors.Is(err, ErrUpToDate) {
		t.Fatalf("expected ErrUpToDate, got %v", err)
	}
	opts.Force = true
	if _, err := Upgrade(opts); err != nil {
		t.Fatalf("a forced upgrade must install: %v", err)
	}
	if got := readFile(t, exe); got != newBinary {
		t.Fatalf("forced upgrade did not replace the binary: %q", got)
	}
}

func TestUpgradeKeepsBinaryWhenCanceledBeforeInstall(t *testing.T) {
	srv := releaseServer(t, newBinary, "")
	exe, opts := upgradeTarget(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts.Context = ctx
	// The cancel lands after the attestation step and before the swap.
	opts.Out = cancelOnWrite{match: "Checksum OK", cancel: cancel}
	_, err := Upgrade(opts)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if got := readFile(t, exe); got != oldBinary {
		t.Fatalf("a canceled upgrade replaced the binary: %q", got)
	}
}

// cancelOnWrite cancels a context when progress output contains match.
type cancelOnWrite struct {
	match  string
	cancel context.CancelFunc
}

func (c cancelOnWrite) Write(p []byte) (int, error) {
	if strings.Contains(string(p), c.match) {
		c.cancel()
	}
	return len(p), nil
}

func TestUpgradeRefusesRedirectToForeignHost(t *testing.T) {
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, newBinary)
	}))
	defer foreign.Close()
	name := fmt.Sprintf("tailarr-%s-%s", runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256([]byte(newBinary))
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/jackspiering/tailarr/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"tag_name":"v9.9.9"}`)
	})
	mux.HandleFunc("/jackspiering/tailarr/releases/download/v9.9.9/"+name, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, foreign.URL+"/asset", http.StatusFound)
	})
	mux.HandleFunc("/jackspiering/tailarr/releases/download/v9.9.9/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, hex.EncodeToString(sum[:])+"  "+name+"\n")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	exe, opts := upgradeTarget(t, srv)
	// The default client carries the redirect policy.
	opts.Client = nil
	_, err := Upgrade(opts)
	if err == nil || !strings.Contains(err.Error(), "refusing redirect") {
		t.Fatalf("expected a refused redirect, got %v", err)
	}
	if got := readFile(t, exe); got != oldBinary {
		t.Fatalf("binary was replaced: %q", got)
	}
}

func TestSafeGitHubRedirect(t *testing.T) {
	rows := []struct {
		url  string
		want bool
	}{
		{"https://github.com/o/r/releases/download/v1/a", true},
		{"https://api.github.com/repos/o/r", true},
		{"https://objects.githubusercontent.com/x", true},
		{"https://release-assets.githubusercontent.com/x", true},
		{"https://evil.example.com/x", false},
		{"https://github.com.evil.example.com/x", false},
		{"https://notgithub.com/x", false},
	}
	for _, r := range rows {
		req, err := http.NewRequest(http.MethodGet, r.url, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := safeGitHubRedirect(req, nil) == nil; got != r.want {
			t.Errorf("safeGitHubRedirect(%s) allowed = %v, want %v", r.url, got, r.want)
		}
	}
	req, _ := http.NewRequest(http.MethodGet, "https://github.com/x", nil)
	if err := safeGitHubRedirect(req, make([]*http.Request, 10)); err == nil {
		t.Error("the redirect limit was not enforced")
	}
}

func TestDownloadEnforcesSizeLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "12345678901")
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "asset")
	err := download(context.Background(), srv.Client(), srv.URL, path, 10)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected a size limit error, got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("oversized download left behind: %v", err)
	}
	if err := download(context.Background(), srv.Client(), srv.URL, path, 11); err != nil {
		t.Fatalf("a download at the limit must pass: %v", err)
	}
}

func TestDownloadRejectsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "asset")
	if err := download(context.Background(), srv.Client(), srv.URL, path, 10); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected an HTTP error, got %v", err)
	}
}
