package scaletail

import (
	"os"
	"path/filepath"
	"testing"
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
	if LooksSecret("DB_PASSWORD") != true {
		t.Fatal("expected secret")
	}
	keys := PlaceholderKeys(EnvMap{"A": "", "B": "x", "C": "//"}, []string{"A", "B", "C"})
	if len(keys) != 2 {
		t.Fatalf("%v", keys)
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
