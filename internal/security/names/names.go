// Package names validates service and auth-key identifiers.
package names

import (
	"fmt"
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
	if strings.HasPrefix(value, "//") || strings.HasPrefix(value, "#") {
		return false
	}
	return strings.HasPrefix(value, "tskey-auth-")
}

// ValidateRepoURL accepts https://, ssh://, or git@ host URLs.
func ValidateRepoURL(url string) error {
	switch {
	case strings.HasPrefix(url, "https://"):
		return nil
	case strings.HasPrefix(url, "ssh://"):
		return nil
	case strings.HasPrefix(url, "git@"):
		return nil
	default:
		return fmt.Errorf("unsupported ScaleTail repository URL (expected https://, ssh://, or git@): %s", url)
	}
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
