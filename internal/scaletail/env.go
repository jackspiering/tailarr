package scaletail

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/jackspiering/tailarr/internal/security/atomic"
	"github.com/jackspiering/tailarr/internal/security/names"
)

// EnvMap is KEY -> value for dotenv-style files. Values are the raw text after
// '=', including any quotes, so file values round-trip unchanged: Compose
// interpolates ${VAR} and strips " #" comments only in unquoted values. Store
// user input through QuoteEnvValue.
type EnvMap map[string]string

// ParseEnvFile reads a KEY=VALUE file without shell evaluation.
// Empty values and comments are preserved semantics: keys with empty values are kept.
func ParseEnvFile(path string) (EnvMap, error) {
	env, _, err := parseEnvPath(path)
	return env, err
}

// ParseEnv parses dotenv data without shell evaluation. It returns the values
// and the keys in first-seen order.
func ParseEnv(r io.Reader) (EnvMap, []string, error) {
	out := make(EnvMap)
	var keys []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		key, value, skip, err := parseEnvLine(sc.Text())
		if err != nil {
			return nil, nil, err
		}
		if skip {
			continue
		}
		if _, seen := out[key]; !seen {
			keys = append(keys, key)
		}
		out[key] = value
	}
	if err := sc.Err(); err != nil {
		return nil, nil, err
	}
	return out, keys, nil
}

func parseEnvPath(path string) (EnvMap, []string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(EnvMap), nil, nil
		}
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()
	env, keys, err := ParseEnv(f)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return env, keys, nil
}

// parseEnvLine parses one dotenv line. skip is true for blanks and comments.
// A non-comment line that is not KEY=VALUE with a valid key is an error so
// Apply cannot drop it on rewrite.
func parseEnvLine(line string) (key, value string, skip bool, err error) {
	line = strings.TrimPrefix(line, "\ufeff")
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", true, nil
	}
	if strings.HasPrefix(line, "export ") || strings.HasPrefix(line, "export\t") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "export"))
	}
	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", false, fmt.Errorf("invalid env line: expected KEY=VALUE")
	}
	key = strings.TrimSpace(key)
	if !validEnvKey(key) {
		return "", "", false, fmt.Errorf("invalid env key %q", key)
	}
	return key, strings.TrimSpace(value), false, nil
}

// validEnvKey reports whether key is a valid dotenv identifier: [A-Za-z_][A-Za-z0-9_]* .
func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, c := range key {
		if i == 0 {
			if c != '_' && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
				return false
			}
		} else {
			if c != '_' && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return false
			}
		}
	}
	return true
}

// QuoteEnvValue returns v in the form WriteEnvFile needs so Compose reads it
// literally. Values with whitespace, '$', '#', '"', a backslash, or a leading
// single quote are single-quoted. A value that Compose cannot read literally
// in single quotes is an error.
func QuoteEnvValue(v string) (string, error) {
	if strings.ContainsAny(v, "\n\r") {
		return "", fmt.Errorf("value must be a single line")
	}
	if !strings.ContainsAny(v, " \t$#\"\\") && !strings.HasPrefix(v, "'") {
		return v, nil
	}
	if strings.Contains(v, "'") || strings.HasSuffix(v, "\\") {
		return "", fmt.Errorf("value cannot combine a single quote or trailing backslash with spaces, '$', '#', or quotes")
	}
	return "'" + v + "'", nil
}

func unquote(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

// MergeEnv merges template keys with local overrides.
// Local non-empty values win; empty local keeps template (unless local key present with value).
// Order of keys follows template order via templateKeys, then extra local keys.
func MergeEnv(template, local EnvMap, templateKeys []string) EnvMap {
	out := make(EnvMap)
	seen := make(map[string]bool)
	for _, k := range templateKeys {
		tv := template[k]
		if lv, ok := local[k]; ok && unquote(lv) != "" {
			out[k] = lv
		} else if lv, ok := local[k]; ok {
			// explicit empty local: prefer non-empty template, else empty
			if unquote(tv) != "" {
				out[k] = tv
			} else {
				out[k] = lv
			}
		} else {
			out[k] = tv
		}
		seen[k] = true
	}
	for k, v := range local {
		if !seen[k] {
			out[k] = v
		}
	}
	// Include template keys not in templateKeys list
	for k, v := range template {
		if !seen[k] {
			if lv, ok := local[k]; ok && unquote(lv) != "" {
				out[k] = lv
			} else {
				out[k] = v
			}
		}
	}
	return out
}

// ReadEnvKeys returns keys in file order.
func ReadEnvKeys(path string) ([]string, error) {
	_, keys, err := parseEnvPath(path)
	return keys, err
}

// MissingRequired returns keys that are empty in merged (common deploy prompts).
func MissingRequired(merged EnvMap, keys []string) []string {
	var miss []string
	for _, k := range keys {
		if strings.TrimSpace(unquote(merged[k])) == "" {
			miss = append(miss, k)
		}
	}
	return miss
}

// IsPlaceholder reports whether a value is empty or a comment-style placeholder.
func IsPlaceholder(value string) bool {
	v := strings.TrimSpace(unquote(value))
	if v == "" {
		return true
	}
	return strings.HasPrefix(v, "//") || strings.HasPrefix(v, "#")
}

// DefaultForKey returns a common default for known env keys.
func DefaultForKey(key string) (string, bool) {
	switch key {
	case "PUID":
		return "1000", true
	case "PGID":
		return "1000", true
	case "DNS_SERVER":
		return "9.9.9.9", true
	case "TZ":
		return "Etc/UTC", true
	default:
		return "", false
	}
}

// PlaceholderKeys returns keys in order whose values are placeholders.
func PlaceholderKeys(merged EnvMap, keys []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, k := range keys {
		if IsPlaceholder(merged[k]) {
			out = append(out, k)
			seen[k] = true
		}
	}
	// Keys outside the template order are sorted so prompts keep a stable order.
	var extra []string
	for k, v := range merged {
		if !seen[k] && IsPlaceholder(v) {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

// ValidateMergedTSAuthkey ensures TS_AUTHKEY if present is well-formed.
// Empty values are allowed here; callers may require a non-empty key separately.
func ValidateMergedTSAuthkey(merged EnvMap) error {
	v, ok := merged["TS_AUTHKEY"]
	if !ok {
		return nil
	}
	v = unquote(v)
	if strings.TrimSpace(v) == "" {
		return nil
	}
	if !names.ValidTSAuthkey(v) {
		return fmt.Errorf("TS_AUTHKEY must start with tskey-auth-")
	}
	return nil
}

// WriteEnvFile writes KEY=VALUE lines with mode 600. Values are written as
// stored; quote user input with QuoteEnvValue first.
func WriteEnvFile(path string, merged EnvMap, keyOrder []string) error {
	var b strings.Builder
	written := make(map[string]bool)
	for _, k := range keyOrder {
		if v, ok := merged[k]; ok {
			fmt.Fprintf(&b, "%s=%s\n", k, v)
			written[k] = true
		}
	}
	// Remaining keys sorted for deterministic output.
	var extra []string
	for k := range merged {
		if !written[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		fmt.Fprintf(&b, "%s=%s\n", k, merged[k])
	}
	return atomic.WriteFileString(path, b.String(), 0o600)
}
