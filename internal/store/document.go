package store

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
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

// documentExpiry reports the earlier of the recorded expires_at and the unverified
// exp claim of the DI token, ignoring either when it is absent or out of bounds.
//
// The earlier value wins on purpose. Both sources are untrusted: expires_at may come
// from an imported file that anything wrote, and exp comes from a JWT this process
// never verifies. Preferring the earlier one means neither source can push the
// scheduled refresh further out than the other allows, which is the only direction
// that matters — an expiry too far in the future suppresses the refresh and leaves a
// dead token in place, while one too early only costs an extra refresh.
func documentExpiry(document tokenDocument) time.Time {
	claimed := unverifiedExpiry(document.Token)
	recorded := recordedExpiry(document.ExpiresAt)

	switch {
	case recorded.IsZero():
		return claimed
	case claimed.IsZero():
		return recorded
	case claimed.Before(recorded):
		return claimed
	}
	return recorded
}

// recordedExpiry parses an expires_at field, refusing an unparsable value and one
// outside the bound.
func recordedExpiry(value string) time.Time {
	if strings.TrimSpace(value) == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	if seconds := parsed.Unix(); seconds <= 0 || seconds > maxExpirySeconds {
		return time.Time{}
	}
	return parsed.UTC()
}

// Bounds for an untrusted token. A hostile or malformed token must be cheap to
// refuse, so nothing is decoded before the sizes are checked.
const (
	// maxTokenBytes bounds the whole token string.
	maxTokenBytes = 8192
	// maxSegmentBytes bounds one base64 segment.
	maxSegmentBytes = 4096
	// maxExpirySeconds bounds a claimed expiry to roughly the year 2200, so a
	// hostile claim cannot produce an absurd instant.
	maxExpirySeconds = 7_258_118_400
)

// unverifiedExpiry reads the exp claim of a JWT without verifying its signature.
//
// This is scheduling metadata only: it decides when to refresh and must never
// authorize anything. Every malformed shape yields the zero time rather than an
// error, because a server-controlled claim must not be able to fail a load.
//
// The hardening — size and segment bounds, three non-empty segments so an unsigned
// token is refused, an alg header that is present and is not "none", exp kept as a
// json.Number so a string is not coerced, and no trailing content after the JSON
// object — is deliberately a duplicate of auth.UnverifiedExpiry in
// internal/garmin/auth. The duplication is intentional rather than shared: auth is
// this package's consumer, not a library for it, and a storage package that imported
// its consumer would invert the dependency and make the auth package unusable
// without a token store. internal/securefile is not the home for it either: that
// package is filesystem hardening and knows nothing about tokens. Keep the two
// parsers in step by hand.
//
// Source: Client._is_token_expired and _decode_jwt_payload in client.py (0.3.10),
// which coerce exp defensively so a non-numeric, boolean, non-finite or overflowing
// value cannot take down every request.
func unverifiedExpiry(token string) time.Time {
	claims, ok := unverifiedClaims(token)
	if !ok {
		return time.Time{}
	}

	// A json.Number is produced only for a JSON number, so a bool, a string, null,
	// an object and an array all fail this assertion.
	number, isNumber := claims["exp"].(json.Number)
	if !isNumber {
		return time.Time{}
	}
	seconds, err := number.Float64()
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return time.Time{}
	}
	if seconds <= 0 || seconds > maxExpirySeconds {
		return time.Time{}
	}
	return time.Unix(int64(seconds), 0).UTC()
}

// unverifiedClaims decodes the payload of a signed JWT-shaped token. Numbers stay
// json.Number so a caller can reject a value that is not a number at all.
func unverifiedClaims(token string) (map[string]any, bool) {
	if token == "" || len(token) > maxTokenBytes {
		return nil, false
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, false
	}
	if len(parts[0]) > maxSegmentBytes || len(parts[1]) > maxSegmentBytes {
		return nil, false
	}
	if !hasSigningAlgorithm(parts[0]) {
		return nil, false
	}
	return decodeJSONObject(parts[1])
}

// hasSigningAlgorithm reports whether the header names an algorithm other than
// "none". Case is folded, so "NoNe" is refused too.
func hasSigningAlgorithm(headerSegment string) bool {
	header, ok := decodeJSONObject(headerSegment)
	if !ok {
		return false
	}
	alg, isString := header["alg"].(string)
	return isString && alg != "" && !strings.EqualFold(strings.TrimSpace(alg), "none")
}

// decodeJSONObject base64url-decodes one segment and decodes it as a single JSON
// object. Trailing content after the object is rejected.
func decodeJSONObject(segment string) (map[string]any, bool) {
	raw, err := decodeSegment(segment)
	if err != nil {
		return nil, false
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, false
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, false
	}
	return object, true
}

// decodeSegment accepts both the unpadded encoding Garmin emits and a padded
// segment, because upstream re-pads before decoding.
func decodeSegment(segment string) ([]byte, error) {
	if strings.HasSuffix(segment, "=") {
		return base64.URLEncoding.DecodeString(segment)
	}
	return base64.RawURLEncoding.DecodeString(segment)
}
