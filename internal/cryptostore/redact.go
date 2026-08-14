package cryptostore

import (
	"encoding/json"
	"log/slog"
	"strconv"
)

// This file follows internal/garmin/protocol/redact.go: a secret-bearing type
// renders through one redacted shape, and String, GoString, MarshalJSON and
// LogValue all use it. Nothing here reads key material, ciphertext or a nonce.

// redactedKey is the only shape a Key is ever rendered or serialized in.
type redactedKey struct {
	Type    string `json:"type"`
	Version int    `json:"version"`
	Loaded  bool   `json:"loaded"`
}

func (k Key) redacted() redactedKey {
	return redactedKey{Type: "cryptostore.Key", Version: k.version, Loaded: k.secret != nil}
}

// String reports the key version and whether material is loaded, never the key.
func (k Key) String() string {
	red := k.redacted()
	return "cryptostore.Key{version:" + strconv.Itoa(red.Version) +
		" material:" + presence(red.Loaded) + "}"
}

// GoString satisfies the %#v verb with the same redacted rendering.
func (k Key) GoString() string { return k.String() }

// MarshalJSON serializes the redacted form, so a Key embedded in a log record or
// a configuration dump cannot leak its material.
func (k Key) MarshalJSON() ([]byte, error) {
	return json.Marshal(k.redacted())
}

// LogValue implements slog.LogValuer, so structured logging is safe by default:
// every handler receives the redacted group instead of walking the value.
func (k Key) LogValue() slog.Value {
	red := k.redacted()
	return slog.GroupValue(
		slog.String("type", red.Type),
		slog.Int("version", red.Version),
		slog.Bool("loaded", red.Loaded),
	)
}

// redactedEnvelope is the only shape an envelope is ever rendered or serialized
// in. It reports sizes, never material.
type redactedEnvelope struct {
	Type            string `json:"type"`
	FormatVersion   int    `json:"formatVersion"`
	KeyVersion      int    `json:"keyVersion"`
	NonceBytes      int    `json:"nonceBytes"`
	CiphertextBytes int    `json:"ciphertextBytes"`
}

func (e envelope) redacted() redactedEnvelope {
	red := redactedEnvelope{
		Type:          "cryptostore.envelope",
		FormatVersion: int(e.formatVersion),
		KeyVersion:    int(e.keyVersion),
	}
	if e.sealed != nil {
		red.NonceBytes = len(e.sealed.nonce)
		red.CiphertextBytes = len(e.sealed.ciphertext)
	}
	return red
}

// String reports the envelope's versions and sizes, never its bytes.
func (e envelope) String() string {
	red := e.redacted()
	return "cryptostore.envelope{formatVersion:" + strconv.Itoa(red.FormatVersion) +
		" keyVersion:" + strconv.Itoa(red.KeyVersion) +
		" nonceBytes:" + strconv.Itoa(red.NonceBytes) +
		" ciphertextBytes:" + strconv.Itoa(red.CiphertextBytes) + "}"
}

// GoString satisfies the %#v verb with the same redacted rendering.
func (e envelope) GoString() string { return e.String() }

// MarshalJSON serializes the redacted form.
func (e envelope) MarshalJSON() ([]byte, error) {
	return json.Marshal(e.redacted())
}

// LogValue implements slog.LogValuer.
func (e envelope) LogValue() slog.Value {
	red := e.redacted()
	return slog.GroupValue(
		slog.String("type", red.Type),
		slog.Int("formatVersion", red.FormatVersion),
		slog.Int("keyVersion", red.KeyVersion),
		slog.Int("nonceBytes", red.NonceBytes),
		slog.Int("ciphertextBytes", red.CiphertextBytes),
	)
}

func presence(present bool) string {
	if present {
		return "present"
	}
	return "absent"
}
