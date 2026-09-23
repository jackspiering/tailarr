package scaletail

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jackspiering/tailarr/internal/security/redact"
)

func TestParseAndMergeEnv(t *testing.T) {
	dir := t.TempDir()
	tpl := filepath.Join(dir, "template.env")
	local := filepath.Join(dir, "local.env")
	if err := os.WriteFile(tpl, []byte("TS_AUTHKEY=\nHOSTNAME=template\nFOO=bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte("HOSTNAME=local\nTS_AUTHKEY=tskey-auth-xyz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tm, err := ParseEnvFile(tpl)
	if err != nil {
		t.Fatal(err)
	}
	lm, err := ParseEnvFile(local)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := ReadEnvKeys(tpl)
	if err != nil {
		t.Fatal(err)
	}
	merged := MergeEnv(tm, lm, keys)
	if merged["HOSTNAME"] != "local" {
		t.Fatalf("HOSTNAME=%s", merged["HOSTNAME"])
	}
	if merged["TS_AUTHKEY"] != "tskey-auth-xyz" {
		t.Fatalf("auth=%s", merged["TS_AUTHKEY"])
	}
	if merged["FOO"] != "bar" {
		t.Fatalf("FOO=%s", merged["FOO"])
	}
	if err := ValidateMergedTSAuthkey(merged); err != nil {
		t.Fatal(err)
	}
	miss := MissingRequired(map[string]string{"A": "", "B": "x"}, []string{"A", "B"})
	if len(miss) != 1 || miss[0] != "A" {
		t.Fatalf("miss=%v", miss)
	}
}

func TestPlaceholderAndDefaults(t *testing.T) {
	if !IsPlaceholder("") || !IsPlaceholder("// comment") || !IsPlaceholder("# x") {
		t.Fatal("expected placeholders")
	}
	if IsPlaceholder("value") {
		t.Fatal("value is not placeholder")
	}
	if v, ok := DefaultForKey("PUID"); !ok || v != "1000" {
		t.Fatal(v, ok)
	}
	if !redact.LooksSecret("DB_PASSWORD") {
		t.Fatal("expected secret")
	}
	keys := PlaceholderKeys(EnvMap{"A": "", "B": "x", "C": "//"}, []string{"A", "B", "C"})
	if len(keys) != 2 {
		t.Fatalf("%v", keys)
	}
}

func TestParseEnvFileStripsBOM(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bom.env")
	if err := os.WriteFile(p, []byte("\ufeffTS_AUTHKEY=tskey-auth-bom\nHOSTNAME=box\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := ParseEnvFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if m["TS_AUTHKEY"] != "tskey-auth-bom" {
		t.Fatalf("BOM dropped first key: %#v", m)
	}
	if m["HOSTNAME"] != "box" {
		t.Fatalf("HOSTNAME=%q", m["HOSTNAME"])
	}
}

func TestReadEnvKeysStripsBOM(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bom.env")
	if err := os.WriteFile(p, []byte("\ufeffTS_AUTHKEY=\nHOSTNAME=box\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	keys, err := ReadEnvKeys(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] != "TS_AUTHKEY" || keys[1] != "HOSTNAME" {
		t.Fatalf("keys=%v", keys)
	}
}

func TestParseEnvFileRejectsInvalidKeys(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	if err := os.WriteFile(p, []byte("FOO-BAR=/data\nBAZ=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseEnvFile(p); err == nil {
		t.Fatal("expected invalid key error")
	}
	if _, err := ReadEnvKeys(p); err == nil {
		t.Fatal("expected invalid key error from ReadEnvKeys")
	}
	okPath := filepath.Join(dir, "ok.env")
	if err := os.WriteFile(okPath, []byte("export BAZ=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := ParseEnvFile(okPath)
	if err != nil {
		t.Fatal(err)
	}
	if m["BAZ"] != "1" {
		t.Fatalf("export prefix not merged: %#v", m)
	}
	if _, ok := m["export"]; ok {
		t.Fatal("export was kept as a key")
	}
}

func TestWriteEnvFileMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	if err := WriteEnvFile(p, EnvMap{"A": "1"}, []string{"A"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
}

func TestQuoteEnvValue(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"", "", true},
		{"Etc/UTC", "Etc/UTC", true},
		{"tskey-auth-abc123", "tskey-auth-abc123", true},
		{"it's", "it's", true},
		{"p$ss", "'p$ss'", true},
		{"abc #123", "'abc #123'", true},
		{`say "hi"`, `'say "hi"'`, true},
		{`C:\path`, `'C:\path'`, true},
		{"'lead", "", false},
		{"it's $5", "", false},
		{`trail\`, "", false},
		{"two\nlines", "", false},
	}
	for _, c := range cases {
		got, err := QuoteEnvValue(c.in)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("QuoteEnvValue(%q) = %q, %v; want %q, ok=%v", c.in, got, err, c.want, c.ok)
		}
	}
}

func TestEnvFileRoundTripKeepsRawValues(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.env")
	body := "TZ=Europe/Amsterdam # See the tz list\n" +
		"WEBAPP_URL=http://${TS_URL}:3000\n" +
		"LITERAL='a$b'\n" +
		"SMTP_PORT=\"587\"\n"
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := ParseEnvFile(src)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := ReadEnvKeys(src)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "dst.env")
	if err := WriteEnvFile(dst, m, keys); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("round trip changed file:\n%s", got)
	}
}

func TestQuotedEmptyValuesAreEmpty(t *testing.T) {
	if !IsPlaceholder(`""`) || !IsPlaceholder(`'# comment'`) {
		t.Fatal("quoted empty and quoted comment must be placeholders")
	}
	merged := MergeEnv(EnvMap{"A": "tpl"}, EnvMap{"A": `""`}, []string{"A"})
	if merged["A"] != "tpl" {
		t.Fatalf("quoted empty local overrode template: %q", merged["A"])
	}
	if err := ValidateMergedTSAuthkey(EnvMap{"TS_AUTHKEY": `"tskey-auth-x"`}); err != nil {
		t.Fatalf("quoted auth key rejected: %v", err)
	}
}
