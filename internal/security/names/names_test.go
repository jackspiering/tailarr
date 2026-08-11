package names

import "testing"

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
}

func TestValidateRepoRef(t *testing.T) {
	t.Parallel()
	if err := ValidateRepoRef("v1.2.3"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRepoRef("main"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRepoRef("-rf"); err == nil {
		t.Error("flag-like ref should fail")
	}
	if err := ValidateRepoRef("has space"); err == nil {
		t.Error("whitespace ref should fail")
	}
}
