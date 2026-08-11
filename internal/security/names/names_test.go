package names

import (
	"strings"
	"testing"
)

func TestValidServiceName(t *testing.T) {
	t.Parallel()
	valid := []string{"app", "My-Service", "svc_1", "a.b", "X", "tailscale-nginx"}
	invalid := []string{"", "../bad", "bad/name", ".hidden", "bad..name", "a/b", "foo/../bar", "-lead", " has space"}

	for _, name := range valid {
		if !ValidServiceName(name) {
			t.Errorf("ValidServiceName(%q) = false, want true", name)
		}
		if err := ValidateServiceName(name); err != nil {
			t.Errorf("ValidateServiceName(%q) unexpected error: %v", name, err)
		}
	}
	for _, name := range invalid {
		if ValidServiceName(name) {
			t.Errorf("ValidServiceName(%q) = true, want false", name)
		}
	}
	if err := ValidateServiceName(""); err == nil {
		t.Error("ValidateServiceName(\"\") expected error")
	}
}

func TestValidTSAuthkey(t *testing.T) {
	t.Parallel()
	if !ValidTSAuthkey("tskey-auth-kAbc123") {
		t.Error("expected valid key")
	}
	if ValidTSAuthkey("tskey-api-xxx") {
		t.Error("api key should fail")
	}
	if ValidTSAuthkey("//tskey-auth-x") {
		t.Error("comment-like should fail")
	}
	if ValidTSAuthkey("") {
		t.Error("empty should fail")
	}
}

func TestValidateRepoURL(t *testing.T) {
	t.Parallel()
	ok := []string{
		"https://github.com/tailscale-dev/ScaleTail.git",
		"ssh://git@github.com/org/repo.git",
		"git@github.com:org/repo.git",
	}
	for _, u := range ok {
		if err := ValidateRepoURL(u); err != nil {
			t.Errorf("ValidateRepoURL(%q): %v", u, err)
		}
	}
	if err := ValidateRepoURL("file:///tmp/repo"); err == nil {
		t.Error("file:// should be rejected")
	}
	if err := ValidateRepoURL("http://example.com/repo"); err == nil {
		t.Error("http:// should be rejected")
	}
	if err := ValidateRepoURL("https://user:token@github.com/org/repo.git"); err == nil {
		t.Error("https userinfo should be rejected")
	}
	if err := ValidateRepoURL("ssh://git:password@github.com/org/repo.git"); err == nil {
		t.Error("ssh password userinfo should be rejected")
	}
}

func TestRedactRepoURL(t *testing.T) {
	t.Parallel()
	got := RedactRepoURL("https://user:token@github.com/org/repo.git")
	if strings.Contains(got, "token") || strings.Contains(got, "user:") {
		t.Fatalf("credentials not redacted: %s", got)
	}
	if got != "https://github.com/org/repo.git" {
		t.Fatalf("got %s", got)
	}
}

func TestValidTSAuthkeyRejectsNewline(t *testing.T) {
	t.Parallel()
	if ValidTSAuthkey("tskey-auth-x\ninjected=1") {
		t.Fatal("newline should fail")
	}
}
