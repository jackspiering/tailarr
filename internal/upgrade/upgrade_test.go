package upgrade

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"0.2.0", "0.2.0", 0},
		{"0.2.0", "0.3.0", -1},
		{"0.3.0", "0.2.0", 1},
		{"0.2.0", "0.2.1", -1},
		{"v0.2.0", "0.2.0", 0},
		{"1.10.0", "1.9.9", 1},
		{"0.2.0", "v0.2.0+meta", 0},
		{"0.3.0-rc.1", "0.3.0", -1},
		{"0.3.0-rc.1", "0.3.0-rc.2", -1},
		{"0.3.0-rc.2", "0.3.0-rc.10", -1},
		{"0.3.0-rc.1", "0.3.0-rc.1", 0},
		{"0.3.0-alpha", "0.3.0-beta", -1},
		{"0.3.0-beta", "0.3.0-rc.1", -1},
		{"0.3.0-dev", "0.2.0", 1},
		{"0.2.0", "1.0.0", -1},
		{"unknown", "0.2.0", 1},
	}
	for _, tt := range tests {
		if got := Compare(tt.a, tt.b); got != tt.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestLatest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/jackspiering/tailarr/releases/latest" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_, _ = fmt.Fprint(w, `{"tag_name":"v0.3.0","name":"v0.3.0"}`)
	}))
	defer srv.Close()

	client := srv.Client()
	got, err := Latest(Options{Repo: "jackspiering/tailarr", Client: client, apiBase: srv.URL})
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if got != "v0.3.0" {
		t.Fatalf("Latest = %q, want v0.3.0", got)
	}
}

func TestLatestHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer srv.Close()
	_, err := Latest(Options{Client: srv.Client(), apiBase: srv.URL})
	if err == nil {
		t.Fatal("Latest succeeded, want error")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("error = %v, want HTTP status mention", err)
	}
}

func TestLatestInvalidRepo(t *testing.T) {
	if _, err := Latest(Options{Repo: "not-a-repo"}); err == nil {
		t.Fatal("Latest with invalid repo succeeded, want error")
	}
}

func TestVerifySum(t *testing.T) {
	dir := t.TempDir()
	data := []byte("tailarr binary")
	sum := sha256.Sum256(data)
	sumsPath := filepath.Join(dir, "SHA256SUMS")
	lines := []string{
		"0000000000000000000000000000000000000000000000000000000000000000  tailarr-linux-amd64",
		hex.EncodeToString(sum[:]) + "  tailarr-darwin-arm64",
	}
	if err := os.WriteFile(sumsPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := verifySum(data, sumsPath, "tailarr-darwin-arm64"); err != nil {
		t.Fatalf("verifySum: %v", err)
	}
	if err := verifySum(data, sumsPath, "tailarr-linux-amd64"); err == nil {
		t.Fatal("verifySum accepted a mismatched checksum")
	}
	if err := verifySum(data, sumsPath, "tailarr-windows-amd64"); err == nil {
		t.Fatal("verifySum accepted an asset missing from SHA256SUMS")
	}
	if err := verifySum(data, sumsPath, "../evil"); err == nil {
		t.Fatal("verifySum accepted a path-like asset name")
	}
}
