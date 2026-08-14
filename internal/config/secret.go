package config

import (
	"encoding/json"
	"log/slog"
	"strings"
)

// Redaction markers. redactedMarker stands in for present secret material;
// unsetMarker distinguishes "there is a secret and you may not see it" from
// "there is no secret", which an operator reading effective configuration needs
// to tell apart.
//
// The delimiters are square brackets rather than the angle brackets the protocol
// package uses for its own markers: encoding/json escapes < and > to < and
// >, and a marker an operator cannot grep for in a JSON log line is a worse
// marker.
const (
	redactedMarker = "[redacted]"
	unsetMarker    = "[unset]"
)

// Secret holds inline secret material supplied by the environment or by a
// configuration file. The value sits in an unexported field and every rendering
// path — String, GoString, MarshalJSON, and slog.LogValuer — reports a marker
// instead, so a Secret cannot leak through a log line, a %+v, or a JSON dump of
// effective configuration.
//
// Only [Secret.Reveal] returns the material, and a caller has to ask for it
// deliberately.
//
// The zero value is an unset secret.
type Secret struct {
	value string
}

// NewSecret wraps value as secret material. Surrounding whitespace is trimmed,
// because a secret delivered through an environment variable or a mounted file
// commonly arrives with a trailing newline, and a value that is empty after
// trimming counts as unset.
func NewSecret(value string) Secret {
	return Secret{value: strings.TrimSpace(value)}
}

// IsSet reports whether the secret carries material.
func (s Secret) IsSet() bool { return s.value != "" }

// Reveal returns the secret material, or "" when unset. Call it only where the
// material is actually needed; never pass the result to a logger or an error.
func (s Secret) Reveal() string { return s.value }

// String reports a marker, never the material. It satisfies %v, %+v, and %s.
func (s Secret) String() string {
	if !s.IsSet() {
		return unsetMarker
	}
	return redactedMarker
}

// GoString reports the same marker for %#v, which would otherwise print the
// unexported field through reflection.
func (s Secret) GoString() string { return "config.Secret(" + s.String() + ")" }

// MarshalJSON serializes the marker, so a Secret embedded in a serialized
// configuration dump cannot leak.
func (s Secret) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

// LogValue implements slog.LogValuer, so structured logging is safe by default
// with every handler.
func (s Secret) LogValue() slog.Value { return slog.StringValue(s.String()) }
