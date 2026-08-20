// Package names validates service and auth-key identifiers.
package names

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// serviceNameRE matches ScaleTail-style service directory names.
// Letters, digits, underscore, dot, hyphen; must start with alnum.
// Path traversal ".." is rejected separately.
var serviceNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// authkeyNameRE is the same shape as service names for stored key labels.
var authkeyNameRE = serviceNameRE

// maxNameLen caps service and auth-key identifier length.
const maxNameLen = 64

// validSegment enforces the shared shape constraints: non-empty, bounded
// length, and no path traversal.
func validSegment(name string) bool {
	if name == "" || len(name) > maxNameLen {
		return false
	}
	if strings.Contains(name, "..") {
		return false
	}
	return true
}

// ValidServiceName reports whether name is a safe service directory name.
func ValidServiceName(name string) bool {
	return validSegment(name) && serviceNameRE.MatchString(name)
}

// ValidateServiceName returns an error when name is invalid.
func ValidateServiceName(name string) error {
	if name == "" {
		return fmt.Errorf("service name is required")
	}
	if !ValidServiceName(name) {
		return fmt.Errorf("invalid service name: %s", name)
	}
	return nil
}

// ValidAuthkeyName reports whether name is a safe stored auth key label.
func ValidAuthkeyName(name string) bool {
	return validSegment(name) && authkeyNameRE.MatchString(name)
}

// ValidateAuthkeyName returns an error when name is invalid.
func ValidateAuthkeyName(name string) error {
	if name == "" {
		return fmt.Errorf("stored key name is required")
	}
	if !ValidAuthkeyName(name) {
		return fmt.Errorf("stored key names may only contain letters, numbers, dot, underscore, and hyphen")
	}
	return nil
}

// ValidTSAuthkey reports whether value looks like a Tailscale auth key.
// Secrets must never appear on CLI flags; this only checks shape.
func ValidTSAuthkey(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if strings.ContainsAny(value, "\r\n") {
		return false
	}
	if strings.HasPrefix(value, "//") || strings.HasPrefix(value, "#") {
		return false
	}
	return strings.HasPrefix(value, "tskey-auth-")
}

// ValidateRepoURL accepts https://, ssh://, or git@ host URLs without
// embedded credentials (userinfo). Operators should use SSH agent or a
// credential helper instead of putting tokens in the URL.
func ValidateRepoURL(raw string) error {
	switch {
	case strings.HasPrefix(raw, "https://"):
		u, err := url.Parse(raw)
		if err != nil {
			return fmt.Errorf("invalid ScaleTail repository URL")
		}
		if u.User != nil {
			return fmt.Errorf("repository URL must not contain credentials; use SSH agent or a credential helper")
		}
		if u.Host == "" {
			return fmt.Errorf("invalid ScaleTail repository URL: missing host")
		}
		return nil
	case strings.HasPrefix(raw, "ssh://"):
		u, err := url.Parse(raw)
		if err != nil {
			return fmt.Errorf("invalid ScaleTail repository URL")
		}
		// ssh://user@host/path is common and not a secret; reject password userinfo.
		if u.User != nil {
			if _, hasPass := u.User.Password(); hasPass {
				return fmt.Errorf("repository URL must not contain a password; use SSH agent or a credential helper")
			}
		}
		if u.Host == "" {
			return fmt.Errorf("invalid ScaleTail repository URL: missing host")
		}
		return nil
	case strings.HasPrefix(raw, "git@"):
		// git@host:path form — no password embedded in standard SCP-like syntax.
		if strings.Contains(raw, "://") {
			return fmt.Errorf("unsupported ScaleTail repository URL")
		}
		rest := strings.TrimPrefix(raw, "git@")
		if rest == "" || strings.HasPrefix(rest, ":") || strings.HasPrefix(rest, "/") {
			return fmt.Errorf("invalid ScaleTail repository URL: missing host")
		}
		if idx := strings.Index(rest, ":"); idx >= 0 {
			if idx == 0 {
				return fmt.Errorf("invalid ScaleTail repository URL: missing host")
			}
		} else if idx := strings.Index(rest, "/"); idx >= 0 {
			if idx == 0 {
				return fmt.Errorf("invalid ScaleTail repository URL: missing host")
			}
		}
		host := rest
		if idx := strings.IndexAny(rest, ":/"); idx >= 0 {
			host = rest[:idx]
		}
		if host == "" {
			return fmt.Errorf("invalid ScaleTail repository URL: missing host")
		}
		return nil
	default:
		return fmt.Errorf("unsupported ScaleTail repository URL (expected https://, ssh://, or git@)")
	}
}

// RedactRepoURL strips userinfo from a URL for display/logs. Non-URL forms
// (git@...) are returned unchanged.
func RedactRepoURL(raw string) string {
	if strings.HasPrefix(raw, "https://") {
		u, err := url.Parse(raw)
		if err != nil {
			// Fallback: never return raw credential URL on parse failure.
			return regexp.MustCompile(`(?i)(https://)[^\s/@]+@`).ReplaceAllString(raw, `${1}redacted@`)
		}
		if u.User != nil {
			u.User = nil
			return u.String()
		}
		return raw
	}
	if strings.HasPrefix(raw, "ssh://") {
		u, err := url.Parse(raw)
		if err != nil {
			return regexp.MustCompile(`(?i)(ssh://)[^\s/@]+@`).ReplaceAllString(raw, `${1}redacted@`)
		}
		if u.User != nil {
			if _, hasPass := u.User.Password(); hasPass {
				u.User = nil
				return u.String()
			}
			// Username-only ssh URL is not a secret; preserve it.
			return raw
		}
		return raw
	}
	return raw
}
