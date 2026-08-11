// Package redact strips secrets from log lines and UI previews.
package redact

import (
	"regexp"
	"strings"
)

// Redacted is the placeholder shown instead of secret material.
const Redacted = "[redacted]"

// Patterns that indicate a secret-bearing env assignment.
var secretKeyRE = regexp.MustCompile(`(?i)\b(TS_AUTHKEY|AUTH_?KEY|PASSWORD|SECRET|TOKEN|PRIVATE_?KEY|API_?KEY)\b`)

// lineSecretRE matches KEY=value forms where KEY looks sensitive.
var lineSecretRE = regexp.MustCompile(`(?i)([A-Za-z0-9_]*?(?:TS_AUTHKEY|AUTH_?KEY|PASSWORD|SECRET|TOKEN|PRIVATE_?KEY|API_?KEY)[A-Za-z0-9_]*)=([^\s]+)`)

// tskeyRE matches raw Tailscale auth key material in free text.
var tskeyRE = regexp.MustCompile(`tskey-auth-[A-Za-z0-9_-]+`)

// Text redacts secret-looking material from s for safe logging.
func Text(s string) string {
	out := lineSecretRE.ReplaceAllString(s, `${1}=`+Redacted)
	out = tskeyRE.ReplaceAllString(out, Redacted)
	return out
}

// Preview always returns the redacted placeholder for UI display of secrets.
func Preview(_ string) string {
	return Redacted
}

// LooksSecret reports whether key name appears to hold a secret.
func LooksSecret(key string) bool {
	return secretKeyRE.MatchString(key)
}

// EnvLine redacts a single KEY=value line if the key is secret-like or the
// value looks like a tskey.
func EnvLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return line
	}
	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return Text(line)
	}
	key = strings.TrimSpace(key)
	if LooksSecret(key) || strings.HasPrefix(strings.TrimSpace(value), "tskey-auth-") {
		return key + "=" + Redacted
	}
	return line
}
