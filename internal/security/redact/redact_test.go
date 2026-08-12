package redact

import (
	"io"
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

func TestTextURLUserinfo(t *testing.T) {
	t.Parallel()
	out := Text("clone https://alice:s3cr3t@example.com/repo.git now")
	if strings.Contains(out, "s3cr3t") || strings.Contains(out, "alice:") {
		t.Fatalf("userinfo leaked: %s", out)
	}
	if !strings.Contains(out, "https://redacted@example.com/repo.git") {
		t.Fatalf("expected scheme-preserving redaction: %s", out)
	}
}

func TestTextJSONSecret(t *testing.T) {
	t.Parallel()
	out := Text(`{"PASSWORD":"hunter2","HOSTNAME":"box"}`)
	if strings.Contains(out, "hunter2") {
		t.Fatalf("json secret leaked: %s", out)
	}
	if !strings.Contains(out, `"HOSTNAME":"box"`) {
		t.Fatalf("non-secret json mangled: %s", out)
	}
}

func TestTextColonSecret(t *testing.T) {
	t.Parallel()
	out := Text("PASSWORD: hunter2")
	if strings.Contains(out, "hunter2") {
		t.Fatalf("colon secret leaked: %s", out)
	}
	if !strings.Contains(out, "PASSWORD: "+Redacted) {
		t.Fatalf("expected colon redaction: %s", out)
	}
}

func TestTextAuthorizationBearer(t *testing.T) {
	t.Parallel()
	out := Text("Authorization: Bearer abc123token")
	if strings.Contains(out, "abc123token") {
		t.Fatalf("bearer token leaked: %s", out)
	}
	if !strings.Contains(out, "Bearer "+Redacted) {
		t.Fatalf("expected bearer redaction: %s", out)
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
	secrets := []string{
		"TS_AUTHKEY", "AUTHKEY", "AUTH_KEY", "PASSWORD", "SECRET",
		"TOKEN", "PRIVATE", "PRIVATE_KEY", "API_KEY", "APIKEY",
		"DB_PASSWORD", "GITHUB_TOKEN", "MY_PRIVATE_VALUE",
	}
	for _, k := range secrets {
		if !LooksSecret(k) {
			t.Errorf("LooksSecret(%q) = false, want true", k)
		}
	}
	if LooksSecret("HOSTNAME") {
		t.Fatal("HOSTNAME is not secret")
	}
	for _, k := range []string{"TIMEOUT", "CONNECT_TIMEOUT", "SECRETARY"} {
		if LooksSecret(k) {
			t.Errorf("LooksSecret(%q) = true, want false", k)
		}
	}
}

func TestWriterLineAtomic(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	w := Writer(&b)
	if _, err := w.Write([]byte("PASSWORD=")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), Redacted) {
		t.Fatal("partial line flushed before newline")
	}
	if _, err := w.Write([]byte("hunter2\n")); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.Contains(out, "hunter2") {
		t.Fatalf("secret leaked: %s", out)
	}
	if !strings.Contains(out, "PASSWORD="+Redacted) {
		t.Fatalf("expected redaction: %s", out)
	}
}

func TestWriterRedactsEachLine(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	w := Writer(&b)
	if _, err := w.Write([]byte("HOSTNAME=box\nTS_AUTHKEY=tskey-auth-secret\n")); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "HOSTNAME=box") {
		t.Fatalf("plain line mangled: %s", out)
	}
	if strings.Contains(out, "tskey-auth-secret") {
		t.Fatalf("secret leaked: %s", out)
	}
}

func TestWriterFlushResidual(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	w := Writer(&b)
	if _, err := w.Write([]byte("SECRET=abc")); err != nil {
		t.Fatal(err)
	}
	flusher, ok := w.(interface{ Flush() error })
	if !ok {
		t.Fatal("Writer must support Flush")
	}
	if err := flusher.Flush(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "abc") {
		t.Fatalf("residual leaked: %s", b.String())
	}
	if !strings.Contains(b.String(), "SECRET="+Redacted) {
		t.Fatalf("residual not flushed: %s", b.String())
	}
}

func TestWriterCloseFlushesResidual(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	w := Writer(&b)
	if _, err := w.Write([]byte("SECRET=abc")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), Redacted) {
		t.Fatal("residual flushed before Close")
	}
	wc, ok := w.(io.WriteCloser)
	if !ok {
		t.Fatal("Writer must return an io.WriteCloser")
	}
	if err := wc.Close(); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.Contains(out, "abc") {
		t.Fatalf("residual leaked: %s", out)
	}
	if !strings.Contains(out, "SECRET="+Redacted) {
		t.Fatalf("residual not redacted: %s", out)
	}
}
