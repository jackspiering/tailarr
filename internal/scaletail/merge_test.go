package scaletail

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

const mergeBase = "services:\n  app:\n    image: app:1\n    restart: always\n\n  tailscale:\n    image: tailscale:1\n"

func TestMergeFileKeepsBothChanges(t *testing.T) {
	requireGit(t)
	ours := strings.Replace(mergeBase, "restart: always", "restart: unless-stopped", 1)
	theirs := strings.Replace(mergeBase, "tailscale:1", "tailscale:2", 1)
	merged, conflict, err := MergeFile([]byte(ours), []byte(mergeBase), []byte(theirs))
	if err != nil || conflict {
		t.Fatalf("MergeFile = conflict %v, err %v", conflict, err)
	}
	for _, want := range []string{"restart: unless-stopped", "tailscale:2", "app:1"} {
		if !strings.Contains(string(merged), want) {
			t.Errorf("merged text lacks %q:\n%s", want, merged)
		}
	}
}

func TestMergeFileReportsConflictWithoutMarkers(t *testing.T) {
	requireGit(t)
	ours := strings.Replace(mergeBase, "app:1", "app:mine", 1)
	theirs := strings.Replace(mergeBase, "app:1", "app:2", 1)
	merged, conflict, err := MergeFile([]byte(ours), []byte(mergeBase), []byte(theirs))
	if err != nil {
		t.Fatal(err)
	}
	if !conflict || merged != nil {
		t.Fatalf("MergeFile = %q, conflict %v; want a conflict and no text", merged, conflict)
	}
}

func TestMergeFileRefusesLargeInput(t *testing.T) {
	big := make([]byte, maxMergeBytes+1)
	if _, _, err := MergeFile(big, nil, nil); err == nil {
		t.Fatal("MergeFile accepted an input over the limit")
	}
}

func TestKeyCommentsReturnsTheBlockAboveEachKey(t *testing.T) {
	data := []byte("# Header\n\n# The port.\n# Default 80.\nPORT=80\nNAME=x\n\n# Old\n\nLONE=1\n")
	got := KeyComments(data)
	if strings.Join(got["PORT"], "|") != "# The port.|# Default 80." {
		t.Errorf("PORT comments = %q", got["PORT"])
	}
	for _, k := range []string{"NAME", "LONE"} {
		if len(got[k]) != 0 {
			t.Errorf("%s comments = %q, want none", k, got[k])
		}
	}
}

func TestWriteEnvFileWithNotesAddsCommentsForNewKeysOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("# Mine\nOLD=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	notes := map[string][]string{"OLD": {"# template comment for OLD"}, "NEW": {"# What NEW does."}}
	if err := WriteEnvFileWithNotes(path, EnvMap{"OLD": "1", "NEW": "2"}, []string{"OLD", "NEW"}, notes); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "# Mine\nOLD=1\n\n# What NEW does.\nNEW=2\n"; string(got) != want {
		t.Fatalf(".env = %q, want %q", got, want)
	}
}
