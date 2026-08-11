package scaletail

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/jackspiering/tailarr/internal/security/atomic"
	"github.com/jackspiering/tailarr/internal/security/names"
)

// EnvMap is KEY -> value for dotenv-style files.
type EnvMap map[string]string

// ParseEnvFile reads a KEY=VALUE file without shell evaluation.
// Empty values and comments are preserved semantics: keys with empty values are kept.
func ParseEnvFile(path string) (EnvMap, error) {
	out := make(EnvMap)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		// Strip optional surrounding quotes on value.
		value = unquote(value)
		out[key] = value
	}
	return out, sc.Err()
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
		if lv, ok := local[k]; ok && lv != "" {
			out[k] = lv
		} else if lv, ok := local[k]; ok {
			// explicit empty local: prefer non-empty template, else empty
			if tv != "" {
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
			if lv, ok := local[k]; ok && lv != "" {
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
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var keys []string
	seen := make(map[string]bool)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, _, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	return keys, sc.Err()
}

// MissingRequired returns keys that are empty in merged (common deploy prompts).
func MissingRequired(merged EnvMap, keys []string) []string {
	var miss []string
	for _, k := range keys {
		if strings.TrimSpace(merged[k]) == "" {
			miss = append(miss, k)
		}
	}
	return miss
}

// ValidateMergedTSAuthkey ensures TS_AUTHKEY if present is well-formed.
func ValidateMergedTSAuthkey(merged EnvMap) error {
	v, ok := merged["TS_AUTHKEY"]
	if !ok {
		return nil
	}
	if strings.TrimSpace(v) == "" {
		return nil
	}
	if !names.ValidTSAuthkey(v) {
		return fmt.Errorf("TS_AUTHKEY must start with tskey-auth-")
	}
	return nil
}

// WriteEnvFile writes KEY=VALUE lines with mode 600.
func WriteEnvFile(path string, merged EnvMap, keyOrder []string) error {
	var b strings.Builder
	written := make(map[string]bool)
	for _, k := range keyOrder {
		if v, ok := merged[k]; ok {
			fmt.Fprintf(&b, "%s=%s\n", k, v)
			written[k] = true
		}
	}
	for k, v := range merged {
		if !written[k] {
			fmt.Fprintf(&b, "%s=%s\n", k, v)
		}
	}
	return atomic.WriteFileString(path, b.String(), 0o600)
}
