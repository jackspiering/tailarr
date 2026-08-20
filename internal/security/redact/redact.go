// Package redact strips secrets from log lines and UI previews.
package redact

import (
	"bytes"
	"io"
	"regexp"
	"strings"
)

// Redacted is the placeholder shown instead of secret material.
const Redacted = "[redacted]"

// secretKeywords is the single source of truth for key names that hold
// secrets (union of the redact and scaletail/env keyword lists).
var secretKeywords = []string{
	"TS_AUTHKEY",
	"AUTHKEY", "AUTH_KEY",
	"PASSWORD",
	"SECRET",
	"TOKEN",
	"PRIVATE", "PRIVATE_KEY",
	"API_KEY", "APIKEY",
}

// secretKeyPattern is the regexp twin of secretKeywords: it matches the same
// key names (with and without underscore variants) inside redaction patterns.
const secretKeyPattern = `(?:TS_AUTHKEY|AUTH_?KEY|PASSWORD|SECRET|TOKEN|PRIVATE(?:_?KEY)?|API_?KEY)`

// lineSecretRE matches KEY=value forms where KEY looks sensitive. The value
// runs to end of line so whitespace-containing secrets are fully redacted
// (redaction over-covers by design; a partial secret is worse than losing
// trailing text from a diagnostic line).
var lineSecretRE = regexp.MustCompile(`(?i)([A-Za-z0-9_]*?` + secretKeyPattern + `[A-Za-z0-9_]*)=([^\n]*)`)

// jsonSecretRE matches "KEY":"value" JSON object members.
// Prefix/suffix wildcards allow client_secret, access_token, etc.
var jsonSecretRE = regexp.MustCompile(`(?i)("[A-Za-z0-9_]*?` + secretKeyPattern + `[A-Za-z0-9_]*"\s*:\s*")[^"]*(")`)

// colonSecretRE matches KEY: value forms where KEY looks sensitive.
// Value runs to end of line so multi-word secrets are fully redacted.
var colonSecretRE = regexp.MustCompile(`(?i)(\b` + secretKeyPattern + `\b\s*:\s*)[^\n]+`)

// urlUserinfoRE matches scheme://userinfo@ so URL credentials never reach logs.
// Allows any non-space chars up to last @, including '/' in passwords (percent-encoded or raw).
var urlUserinfoRE = regexp.MustCompile(`(?i)(https?|ssh)://[^@\s]+@`)

// bearerRE matches Authorization: Bearer <token>.
var bearerRE = regexp.MustCompile(`(?i)(\bAuthorization\s*:\s*Bearer\s+)[^\s,;]+`)

// tskeyRE matches raw Tailscale auth key material in free text.
var tskeyRE = regexp.MustCompile(`tskey-auth-[A-Za-z0-9_-]+`)

// Text redacts secret-looking material from s for safe logging.
func Text(s string) string {
	out := lineSecretRE.ReplaceAllString(s, `${1}=`+Redacted)
	out = urlUserinfoRE.ReplaceAllString(out, `${1}://redacted@`)
	out = jsonSecretRE.ReplaceAllString(out, `${1}`+Redacted+`${2}`)
	out = colonSecretRE.ReplaceAllString(out, `${1}`+Redacted)
	out = bearerRE.ReplaceAllString(out, `${1}`+Redacted)
	out = tskeyRE.ReplaceAllString(out, Redacted)
	return out
}

// Preview always returns the redacted placeholder for UI display of secrets.
func Preview(_ string) string {
	return Redacted
}

// LooksSecret reports whether key name appears to hold a secret.
// Keywords must match a whole '_' segment so TIMEOUT is not treated as TOKEN.
func LooksSecret(key string) bool {
	k := strings.ToUpper(key)
	for _, p := range secretKeywords {
		if secretKeyMatch(k, p) {
			return true
		}
	}
	return false
}

func secretKeyMatch(key, pat string) bool {
	if key == pat {
		return true
	}
	for i := 0; i+len(pat) <= len(key); i++ {
		if key[i:i+len(pat)] != pat {
			continue
		}
		if i > 0 && key[i-1] != '_' {
			continue
		}
		after := i + len(pat)
		if after < len(key) && key[after] != '_' {
			continue
		}
		return true
	}
	return false
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

// Writer returns a line-atomic redacting writer: each complete line is
// passed through Text before being written to w. Partial lines are buffered
// until the next newline. The returned value also implements io.WriteCloser;
// Close flushes any residual unterminated line and, when w is itself an
// io.WriteCloser, closes it.
func Writer(w io.Writer) io.Writer {
	return &lineWriter{w: w}
}

// lineWriter is the concrete redacting writer returned by Writer.
type lineWriter struct {
	w   io.Writer
	buf []byte
}

// Write buffers p and forwards complete lines through Text.
func (lw *lineWriter) Write(p []byte) (int, error) {
	lw.buf = append(lw.buf, p...)
	for {
		i := bytes.IndexByte(lw.buf, '\n')
		if i < 0 {
			break
		}
		line := lw.buf[:i+1]
		if _, err := io.WriteString(lw.w, Text(string(line))); err != nil {
			return 0, err
		}
		lw.buf = lw.buf[i+1:]
	}
	return len(p), nil
}

// Flush redacts and writes any residual unterminated line without closing w.
func (lw *lineWriter) Flush() error {
	if len(lw.buf) == 0 {
		return nil
	}
	_, err := io.WriteString(lw.w, Text(string(lw.buf)))
	lw.buf = nil
	return err
}

// Close flushes any residual unterminated line, then closes w when it is an
// io.WriteCloser.
func (lw *lineWriter) Close() error {
	if err := lw.Flush(); err != nil {
		return err
	}
	if wc, ok := lw.w.(io.WriteCloser); ok {
		return wc.Close()
	}
	return nil
}
