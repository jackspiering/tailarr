package redact

import (
	"strings"
	"testing"
)

func TestText(t *testing.T) {
	t.Parallel()
	in := "deployed with TS_AUTHKEY=tskey-auth-SECRETVALUE and ok=1"
	out := Text(in)
	if strings.Contains(out, "SECRETVALUE") {
		t.Fatalf("secret leaked: %s", out)
	}
	if !strings.Contains(out, Redacted) {
		t.Fatalf("expected redaction: %s", out)
	}
	if !strings.Contains(out, "ok=1") {
		t.Fatalf("non-secret mangled: %s", out)
	}
}

func TestPreview(t *testing.T) {
	t.Parallel()
	if Preview("tskey-auth-abc") != Redacted {
		t.Fatal("preview must never show secret")
	}
}

func TestEnvLine(t *testing.T) {
	t.Parallel()
	if EnvLine("HOSTNAME=box") != "HOSTNAME=box" {
		t.Fatal("hostname should pass")
	}
	out := EnvLine("TS_AUTHKEY=tskey-auth-xyz")
	if strings.Contains(out, "xyz") {
		t.Fatalf("leaked: %s", out)
	}
}

func TestLooksSecret(t *testing.T) {
	t.Parallel()
	if !LooksSecret("TS_AUTHKEY") {
		t.Fatal("TS_AUTHKEY")
	}
	if LooksSecret("HOSTNAME") {
		t.Fatal("HOSTNAME is not secret")
	}
}
