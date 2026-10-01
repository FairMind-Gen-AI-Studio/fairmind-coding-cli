// Package auth resolves the FairMind JWT from safe sources only: the
// FAIRMIND_TOKEN environment variable or the OS credential store. There is
// deliberately no command-line flag for the token (spec §11.1).
package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// EnvVar is the environment variable read for CI / agent execution.
const EnvVar = "FAIRMIND_TOKEN"

// Token sources.
const (
	SourceEnv      = "env"
	SourceKeychain = "keychain"
)

// Token wraps the raw JWT so that accidental formatting never prints it.
type Token struct {
	value  string
	Source string
}

// Value returns the raw token. Only the HTTP client should call this.
func (t Token) Value() string { return t.value }

// Empty reports whether no token was found.
func (t Token) Empty() bool { return t.value == "" }

func (t Token) String() string   { return "[REDACTED]" }
func (t Token) GoString() string { return "auth.Token{[REDACTED]}" }

// NewToken builds a Token (used by tests and the resolver).
func NewToken(value, source string) Token {
	return Token{value: strings.TrimSpace(value), Source: source}
}

// ErrNotFound is returned by a Keychain when no credential is stored.
var ErrNotFound = errors.New("credential not found")

// Keychain abstracts the OS credential store.
type Keychain interface {
	Get(profile string) (string, error)
	Set(profile, token string) error
	Delete(profile string) error
}

// Resolve returns the token for a profile: environment first, then keychain.
// A missing token yields an empty Token and no error.
func Resolve(profile string, getenv func(string) string, kc Keychain) (Token, error) {
	if v := strings.TrimSpace(getenv(EnvVar)); v != "" {
		return NewToken(v, SourceEnv), nil
	}
	if kc == nil {
		return Token{}, nil
	}
	v, err := kc.Get(profile)
	if errors.Is(err, ErrNotFound) {
		return Token{}, nil
	}
	if err != nil {
		return Token{}, err
	}
	return NewToken(v, SourceKeychain), nil
}

// Claims are the non-sensitive, UNVERIFIED claims shown by `auth status`.
// The CLI never makes authorization decisions from them (spec §13).
type Claims struct {
	Issuer    string   `json:"iss,omitempty"`
	Audience  []string `json:"aud,omitempty"`
	Scope     []string `json:"scope,omitempty"`
	ExpiresAt string   `json:"expires_at,omitempty"`
	Expired   bool     `json:"expired"`
}

// PeekClaims decodes the JWT payload without verifying the signature. It
// returns ok=false when the token is not JWT-shaped.
func PeekClaims(t Token, now time.Time) (Claims, bool) {
	parts := strings.Split(t.value, ".")
	if len(parts) != 3 {
		return Claims{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return Claims{}, false
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return Claims{}, false
	}
	var c Claims
	if s, ok := raw["iss"].(string); ok {
		c.Issuer = s
	}
	c.Audience = stringList(raw["aud"])
	c.Scope = stringList(raw["scope"])
	if len(c.Scope) == 0 {
		c.Scope = stringList(raw["scp"])
	}
	if exp, ok := raw["exp"].(float64); ok {
		at := time.Unix(int64(exp), 0).UTC()
		c.ExpiresAt = at.Format(time.RFC3339)
		c.Expired = now.After(at)
	}
	return c, true
}

func stringList(v any) []string {
	switch x := v.(type) {
	case string:
		return strings.Fields(x)
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
