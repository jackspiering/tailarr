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
	// Values redact to end of line so whitespace-containing secrets cannot
	// partially leak; trailing diagnostics on the same line are sacrificed.
	if strings.Contains(out, "ok=1") {
		t.Fatalf("text after a secret on the same line must be redacted: %s", out)
	}
}

func TestTextRedactsMultiWordValue(t *testing.T) {
	t.Parallel()
	out := Text("failed: DB_PASSWORD=correct horse battery staple")
	if strings.Contains(out, "correct") || strings.Contains(out, "staple") {
		t.Fatalf("multi-word secret leaked: %s", out)
	}
	if !strings.Contains(out, "DB_PASSWORD="+Redacted) {
		t.Fatalf("expected redaction: %s", out)
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

func TestLooksSecretCatalogKeys(t *testing.T) {
	t.Parallel()
	for _, k := range []string{
		"ENCRYPTION_KEY", "APP_KEY", "APP_ENCRYPTION_KEY", "MEILI_MASTER_KEY",
		"MAXMIND_LICENSE_KEY", "GOTIFY_DEFAULTUSER_PASS", "HBOX_AUTH_API_KEY_PEPPER",
		"SECRET_KEY_BASE", "DB_PASSWD", "GPG_PASSPHRASE", "REGISTRY_CREDENTIALS",
		"PASSWORD_SALT", "key",
	} {
		if !LooksSecret(k) {
			t.Errorf("LooksSecret(%q) = false, want true", k)
		}
	}
	for _, k := range []string{"KEYBOARD_LAYOUT", "MONKEY", "PASSENGER", "BYPASS_CACHE", "SALTY", "KEYS"} {
		if LooksSecret(k) {
			t.Errorf("LooksSecret(%q) = true, want false", k)
		}
	}
}

func TestTextRedactsTable(t *testing.T) {
	t.Parallel()
	rows := []struct{ in, leak string }{
		{"ENCRYPTION_KEY=abc123", "abc123"},
		{"x KEY=abc123", "abc123"},
		{"GOTIFY_DEFAULTUSER_PASS=hunter2", "hunter2"},
		{"DB_PASSWORD: hunter2", "hunter2"},
		{"client_secret: xyz789", "xyz789"},
		{"db_pass: hunter2", "hunter2"},
		{"connect postgres://user:pass@db/x now", "pass@"},
		{"redis://:s3cr3t@cache:6379", "s3cr3t"},
		{"key tskey-client-kAbC123-SeCrEt", "SeCrEt"},
		{"key tskey-api-kAbC123-SeCrEt", "SeCrEt"},
		{"tskey-auth-kAbC123-SeCrEt", "SeCrEt"},
		{"Authorization: Basic dXNlcjpwYXNz", "dXNlcjpwYXNz"},
		{`{"MEILI_MASTER_KEY":"abc123"}`, "abc123"},
	}
	for _, r := range rows {
		out := Text(r.in)
		if strings.Contains(out, r.leak) {
			t.Errorf("Text(%q) leaked %q: %s", r.in, r.leak, out)
		}
		if !strings.Contains(out, Redacted) && !strings.Contains(out, "redacted@") {
			t.Errorf("Text(%q) shows no redaction: %s", r.in, out)
		}
	}
}

func TestTextKeepsNonSecretKeys(t *testing.T) {
	t.Parallel()
	for _, in := range []string{
		"KEYBOARD_LAYOUT=us",
		"MONKEY=banana",
		"BYPASS_CACHE=1",
		"HOSTNAME=box",
		"https://example.com/repo.git",
		"service: web",
	} {
		if out := Text(in); out != in {
			t.Errorf("Text(%q) = %q, want unchanged", in, out)
		}
	}
}
