package store

import (
	"encoding/json"
	"log/slog"
	"slices"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// This file follows internal/garmin/protocol/redact.go: a secret-bearing type
// renders through one redacted shape, and String, GoString, MarshalJSON and
// LogValue all use it. The di_token and di_refresh_token collapse to a presence
// flag. The scheduling expiry is reported as-is, and the di_client_id is reported
// only when it is a client id Garmin actually uses, because both are needed to
// correlate a refresh in the logs.

// labelUnknown is what an unrecognized client id renders as.
const labelUnknown = "unknown"

// knownDIClientID renders a client id only when it is one of the candidate DI
// client ids.
//
// The field is not a secret, but it is also not ours: it comes from Garmin's
// unverified client_id claim or, worse, verbatim from an imported 0.3.x token file
// that anyone may have written. Echoing it would put attacker-chosen text — possibly
// secret-shaped, possibly a forged log line — into every record that mentions the
// token set. internal/garmin/auth applies the same allowlist to its own TokenSet;
// the shared list lives in internal/garmin/protocol.
func knownDIClientID(value string) string {
	if value == "" {
		return ""
	}
	if slices.Contains(protocol.DIClientIDs(), value) {
		return value
	}
	return labelUnknown
}

// redactedTokenSet is the only shape a TokenSet is ever rendered or serialized in.
type redactedTokenSet struct {
	Type                string `json:"type"`
	TokenPresent        bool   `json:"tokenPresent"`
	RefreshTokenPresent bool   `json:"refreshTokenPresent"`
	ClientID            string `json:"clientId,omitempty"`
	ExpiresAt           string `json:"expiresAt,omitempty"`
}

func (s TokenSet) redacted() redactedTokenSet {
	red := redactedTokenSet{Type: "store.TokenSet"}
	if s.parts == nil {
		return red
	}
	red.TokenPresent = secretValue(s.parts.token) != ""
	red.RefreshTokenPresent = secretValue(s.parts.refreshToken) != ""
	red.ClientID = knownDIClientID(secretValue(s.parts.clientID))
	if !s.parts.expiresAt.IsZero() {
		red.ExpiresAt = s.parts.expiresAt.UTC().Format(time.RFC3339)
	}
	return red
}

// String reports presence and non-secret metadata, never token material.
func (s TokenSet) String() string {
	red := s.redacted()
	return "store.TokenSet{token:" + presence(red.TokenPresent) +
		" refreshToken:" + presence(red.RefreshTokenPresent) +
		" clientId:" + quoteLabel(red.ClientID) +
		" expiresAt:" + quoteLabel(red.ExpiresAt) + "}"
}

// GoString satisfies the %#v verb with the same redacted rendering.
func (s TokenSet) GoString() string { return s.String() }

// MarshalJSON serializes the redacted form, so a TokenSet embedded in a
// diagnostic dump or a log record cannot leak its tokens. The on-disk record is
// serialized separately, inside the encrypted envelope.
func (s TokenSet) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.redacted())
}

// LogValue implements slog.LogValuer, so structured logging is safe by default:
// every handler receives the redacted group instead of walking the value.
func (s TokenSet) LogValue() slog.Value {
	red := s.redacted()
	return slog.GroupValue(
		slog.String("type", red.Type),
		slog.Bool("tokenPresent", red.TokenPresent),
		slog.Bool("refreshTokenPresent", red.RefreshTokenPresent),
		slog.String("clientId", red.ClientID),
		slog.String("expiresAt", red.ExpiresAt),
	)
}

func presence(present bool) string {
	if present {
		return "present"
	}
	return "absent"
}

func quoteLabel(value string) string {
	if value == "" {
		return `""`
	}
	return `"` + value + `"`
}
