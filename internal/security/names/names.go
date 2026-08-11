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

// ValidServiceName reports whether name is a safe service directory name.
func ValidServiceName(name string) bool {
	if name == "" {
		return false
	}
	if strings.Contains(name, "..") {
		return false
	}
	return serviceNameRE.MatchString(name)
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
	if name == "" {
		return false
	}
	return authkeyNameRE.MatchString(name)
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
			return fmt.Errorf("invalid ScaleTail repository URL: %w", err)
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
			return fmt.Errorf("invalid ScaleTail repository URL: %w", err)
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
			return fmt.Errorf("unsupported ScaleTail repository URL: %s", raw)
		}
		return nil
	default:
		return fmt.Errorf("unsupported ScaleTail repository URL (expected https://, ssh://, or git@): %s", raw)
	}
}

// RedactRepoURL strips userinfo from a URL for display/logs. Non-URL forms
// (git@...) are returned unchanged.
func RedactRepoURL(raw string) string {
	if strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "ssh://") {
		u, err := url.Parse(raw)
		if err != nil {
			return raw
		}
		if u.User != nil {
			u.User = nil
			return u.String()
		}
	}
	return raw
}

// ValidateRepoRef rejects refs that look like flags or contain whitespace.
func ValidateRepoRef(ref string) error {
	if ref == "" {
		return nil
	}
	if strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, " \t\n\r") {
		return fmt.Errorf("invalid ScaleTail repository ref: %s", ref)
	}
	return nil
}

// IsCommitSHA reports whether ref looks like a full or abbreviated git SHA.
func IsCommitSHA(ref string) bool {
	if len(ref) < 7 || len(ref) > 40 {
		return false
	}
	for _, c := range ref {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}
