package store

import (
	"encoding/json"
	"log/slog"
)

// Secret carries one piece of opaque credential material a caller hands to the
// store: an MCP access or refresh token, an authorization code, an authorization
// transaction handle, an OAuth client secret, or a stable Garmin account
// identifier. The store never persists the value — only a keyed-HMAC lookup value
// derived from it — so a Secret exists to get material safely from the caller to
// the hash, and no further.
//
// A Secret is secret-bearing and follows the same convention as TokenSet: the
// material sits behind two levels of unexported indirection, so a reflective
// logger, a direct field print and a method-stripping alias
// (type Raw store.Secret) all see an address rather than the value. String,
// GoString, MarshalJSON and LogValue report presence and length class, never
// content.
//
// A Secret is immutable. The zero value is inert and reports IsZero.
type Secret struct {
	// parts is a pointer on purpose. fmt follows a pointer only at the top level,
	// so a nested unexported pointer renders as an address, whereas a nested
	// unexported struct renders its field values.
	parts *secretParts
}

// secretParts holds the material. The field is a pointer for the reason given on
// tokenParts: fmt's badVerb path re-prints a value at depth zero, and depth zero
// dereferences a pointer to a struct and prints its unexported fields verbatim.
type secretParts struct {
	value *secret
}

// NewSecret wraps material. An empty string produces a Secret that reports IsZero,
// because an absent credential and an empty one must be indistinguishable to
// everything downstream.
func NewSecret(material string) Secret {
	if material == "" {
		return Secret{}
	}
	return Secret{parts: &secretParts{value: heldSecret(material)}}
}

// Reveal returns the material, or "" for the zero Secret.
//
// It is named Reveal rather than String or Value so that every call site reads as
// a deliberate disclosure, and so that fmt never reaches it: a String method would
// be called by every %v in the program.
func (s Secret) Reveal() string {
	if s.parts == nil {
		return ""
	}
	return secretValue(s.parts.value)
}

// IsZero reports whether the Secret carries nothing.
func (s Secret) IsZero() bool { return s.parts == nil || secretValue(s.parts.value) == "" }

// redactedSecret is the only shape a Secret is ever rendered or serialized in.
//
// The length is reported as a bucket rather than exactly. An exact length is a
// distinguisher an attacker reading logs can use to tell one credential format
// from another, and the operator question the field answers — "is something there,
// and is it plausibly the right kind of thing" — needs no more than the bucket.
type redactedSecret struct {
	Type    string `json:"type"`
	Present bool   `json:"present"`
	Size    string `json:"size"`
}

// Size buckets for redacted rendering.
const (
	sizeAbsent = "absent"
	sizeShort  = "short" // under 32 bytes: too small to be a 256-bit token
	sizeToken  = "token" // 32 to 128 bytes: the shape opaque MCP tokens have
	sizeLarge  = "large" // over 128 bytes
	shortBytes = 32
	largeBytes = 128
)

func sizeBucket(length int) string {
	switch {
	case length == 0:
		return sizeAbsent
	case length < shortBytes:
		return sizeShort
	case length <= largeBytes:
		return sizeToken
	}
	return sizeLarge
}

func (s Secret) redacted() redactedSecret {
	length := len(s.Reveal())
	return redactedSecret{
		Type:    "store.Secret",
		Present: length > 0,
		Size:    sizeBucket(length),
	}
}

// String reports presence and size class, never the material.
func (s Secret) String() string {
	red := s.redacted()
	return "store.Secret{present:" + presence(red.Present) + " size:" + quoteLabel(red.Size) + "}"
}

// GoString satisfies the %#v verb with the same redacted rendering.
func (s Secret) GoString() string { return s.String() }

// MarshalJSON serializes the redacted form, so a Secret embedded in a diagnostic
// dump or an error payload cannot leak its material.
func (s Secret) MarshalJSON() ([]byte, error) { return json.Marshal(s.redacted()) }

// LogValue implements slog.LogValuer, so structured logging is safe by default.
func (s Secret) LogValue() slog.Value {
	red := s.redacted()
	return slog.GroupValue(
		slog.String("type", red.Type),
		slog.Bool("present", red.Present),
		slog.String("size", red.Size),
	)
}
