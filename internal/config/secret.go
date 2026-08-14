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
// configuration file. Every rendering path — String, GoString, MarshalJSON, and
// slog.LogValuer — reports a marker instead of the material, so a Secret cannot
// leak through a log line, a %+v, or a JSON dump of effective configuration.
//
// The methods alone are not the guarantee: a method-stripping alias
// (type raw config.Secret) drops all four, and fmt then reflects over the layout.
// The material therefore sits behind an unexported pointer to an unexported type.
// A flat unexported field would still be printed by %+v; a nested unexported
// pointer renders as an address, because fmt follows a pointer only at the top
// level. This is the same shape internal/garmin/protocol uses for its
// secret-bearing response and classification types.
//
// Only [Secret.Reveal] returns the material, and a caller has to ask for it
// deliberately.
//
// The zero value is an unset secret. A Secret is never mutated after
// construction, so copying one is safe.
type Secret struct {
	material *secretMaterial
}

// secretMaterial is the material itself. It is deliberately a defined string type
// rather than a struct: fmt dereferences a pointer at the top level only for an
// array, a slice, a struct or a map, which is the path %s on the enclosing struct
// takes through badVerb. A pointer to a string is reported as an address instead.
//
// It is written once, by [NewSecret], and never mutated.
type secretMaterial string

// String reports the marker, never the material, for every path that does reach
// the Stringer interface.
func (m secretMaterial) String() string { return redactedMarker }

// GoString reports the marker for %#v.
func (m secretMaterial) GoString() string { return redactedMarker }

// NewSecret wraps value as secret material. Surrounding whitespace is trimmed,
// because a secret delivered through an environment variable or a mounted file
// commonly arrives with a trailing newline, and a value that is empty after
// trimming counts as unset — it yields the zero Secret, so "unset" has exactly
// one representation.
func NewSecret(value string) Secret {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return Secret{}
	}
	material := secretMaterial(trimmed)
	return Secret{material: &material}
}

// IsSet reports whether the secret carries material.
func (s Secret) IsSet() bool { return s.material != nil }

// Reveal returns the secret material, or "" when unset. Call it only where the
// material is actually needed; never pass the result to a logger or an error.
func (s Secret) Reveal() string {
	if s.material == nil {
		return ""
	}
	return string(*s.material)
}

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
