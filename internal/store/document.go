package store

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// tokenSource names where a document came from, for error messages only. The
// document's value is never reported: it may be the refresh token itself.
//
// Source: the login() failure path in __init__.py (0.3.10), which logs the source
// kind and the length instead of the tokenstore value.
type tokenSource string

const (
	sourceRecord tokenSource = "encrypted-record"
	sourcePath   tokenSource = "path"
	sourceInline tokenSource = "inline-json"
)

// tokenDocument is the serialized token set. The field names are the 0.3.x wire
// names, so one decoder reads both our own encrypted payload and an imported
// garmin_tokens.json.
//
// expires_at is our own addition and is absent from a 0.3.x file. When it is
// absent, the expiry is recovered from the unverified exp claim of the DI token.
type tokenDocument struct {
	Token        string `json:"di_token"`
	RefreshToken string `json:"di_refresh_token"`
	ClientID     string `json:"di_client_id,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"`
}

// encodeRecordPayload serializes set for the encrypted record, including the
// scheduling expiry so a reader need not parse the JWT again.
func encodeRecordPayload(set TokenSet) []byte {
	document := tokenDocument{
		Token:        set.Token(),
		RefreshToken: set.RefreshToken(),
		ClientID:     set.ClientID(),
	}
	if expiry := set.ExpiresAt(); !expiry.IsZero() {
		document.ExpiresAt = expiry.UTC().Format(time.RFC3339)
	}
	// A struct of strings cannot fail to marshal, so the error is unreachable.
	encoded, _ := json.Marshal(document)
	return encoded
}

// encodeLegacyDocument serializes set in exactly the 0.3.x shape: the three di_*
// fields and nothing else, so an older client reads it unchanged.
//
// Source: Client.dumps in client.py (0.3.10).
func encodeLegacyDocument(set TokenSet) []byte {
	encoded, _ := json.Marshal(tokenDocument{
		Token:        set.Token(),
		RefreshToken: set.RefreshToken(),
		ClientID:     set.ClientID(),
	})
	return encoded
}

// decodeTokenDocument parses raw into a TokenSet. The error names the source kind
// and the length, never the content.
func decodeTokenDocument(raw []byte, source tokenSource) (TokenSet, error) {
	var document tokenDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return TokenSet{}, fmt.Errorf("store: token document is unreadable (source=%s, %d bytes): %w",
			source, len(raw), ErrIncompatibleTokenFile)
	}
	if document.Token == "" || document.RefreshToken == "" {
		return TokenSet{}, fmt.Errorf("store: token document lacks di_token or di_refresh_token "+
			"(source=%s, %d bytes): %w", source, len(raw), ErrIncompatibleTokenFile)
	}
	return NewTokenSet(document.Token, document.RefreshToken, document.ClientID,
		documentExpiry(document)), nil
}

// documentExpiry prefers the recorded expires_at and falls back to the unverified
// exp claim of the DI token.
func documentExpiry(document tokenDocument) time.Time {
	if document.ExpiresAt != "" {
		if parsed, err := time.Parse(time.RFC3339, document.ExpiresAt); err == nil {
			return parsed
		}
	}
	return unverifiedExpiry(document.Token)
}

// maxExpirySeconds bounds a claimed expiry to roughly the year 2200, so a hostile
// claim cannot produce an absurd instant.
const maxExpirySeconds = 7_258_118_400

// unverifiedExpiry reads the exp claim of a JWT without verifying its signature.
//
// This is scheduling metadata only: it decides when to refresh and must never
// authorize anything. Every malformed shape yields the zero time rather than an
// error, because a server-controlled claim must not be able to fail a load.
//
// Source: Client._is_token_expired in client.py (0.3.10), which coerces exp
// defensively: a non-numeric, boolean, non-finite or overflowing value must not
// raise and take down every request.
func unverifiedExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}

	var claims struct {
		Exp json.Number `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}
	}
	seconds, err := claims.Exp.Float64()
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return time.Time{}
	}
	if seconds <= 0 || seconds > maxExpirySeconds {
		return time.Time{}
	}
	return time.Unix(int64(seconds), 0).UTC()
}
