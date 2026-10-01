package output

import (
	"regexp"
	"strings"
)

const redacted = "[REDACTED]"

var (
	jwtPattern    = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]*`)
	bearerPattern = regexp.MustCompile(`(?i)(bearer\s+)[^\s"',\\]+`)
	// key=value / "key": "value" pairs whose key looks like a credential.
	secretKVPattern = regexp.MustCompile(`(?i)("?(?:authorization|cookie|set-cookie|x-api-key|api[_-]?key|access[_-]?token|refresh[_-]?token|client[_-]?secret|password)"?\s*[:=]\s*"?)([^"\\\s,}]+)`)
)

// Redactor removes known secret values and credential-shaped strings from
// anything the CLI prints (stdout, stderr, verbose logs).
type Redactor struct {
	secrets []string
}

// AddSecret registers an exact value that must never be printed.
func (r *Redactor) AddSecret(s string) {
	if len(s) >= 8 {
		r.secrets = append(r.secrets, s)
	}
}

// Redact returns s with every secret and credential-shaped substring replaced.
func (r *Redactor) Redact(s string) string {
	for _, secret := range r.secrets {
		s = strings.ReplaceAll(s, secret, redacted)
	}
	s = jwtPattern.ReplaceAllString(s, redacted)
	s = bearerPattern.ReplaceAllString(s, "${1}"+redacted)
	s = secretKVPattern.ReplaceAllString(s, "${1}"+redacted)
	return s
}
