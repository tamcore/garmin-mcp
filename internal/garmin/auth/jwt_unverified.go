package auth

// Unverified JWT claim reading.
//
// Garmin signs DI tokens with a key this client does not have, so no claim read
// here is authenticated. Every function in this file is named Unverified* for
// that reason: the values it returns are scheduling metadata and diagnostics
// only. Never authorize an MCP caller, a principal, a scope or a tool from one.
//
// Source: _decode_jwt_payload, _extract_client_id_from_jwt and
// _token_expires_soon in client.py (0.3.10). The hardening upstream added — an
// alg "none" header is rejected, and a bool, non-numeric, non-finite or
// overflowing exp is refused rather than raised — is reproduced and tightened:
// a token with no signature segment is rejected too, and a string exp is not
// coerced to a number.

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"math"
	"strings"
	"time"
)

// Bounds for an untrusted token. A hostile or malformed token must be cheap to
// refuse, so nothing is decoded before the sizes are checked.
const (
	// maxTokenLen bounds the whole token string.
	maxTokenLen = 8192
	// maxSegmentLen bounds one base64 segment.
	maxSegmentLen = 4096
	// maxClientIDLen bounds an accepted client_id claim.
	maxClientIDLen = 128
	// maxExpSeconds bounds an accepted exp claim, in Unix seconds. It is far
	// past any real token lifetime (year 33658) and well inside the range
	// time.Unix can represent, so no accepted value can overflow.
	maxExpSeconds = 1e12
)

// UnverifiedExpiry reports the exp claim of token as an instant.
//
// The claim is NOT verified: the signature is never checked, so the value is
// server-asserted text that only decides when to schedule a refresh. The second
// result is false for anything unusable — an opaque token, an unsigned or
// alg "none" token, an oversized token, a missing claim, or a bool, string,
// container, non-finite, negative or overflowing exp — and the first result is
// then the zero instant.
func UnverifiedExpiry(token string) (time.Time, bool) {
	claims, ok := unverifiedClaims(token)
	if !ok {
		return time.Time{}, false
	}

	raw, ok := claims["exp"]
	if !ok {
		return time.Time{}, false
	}

	// A json.Number is produced only for a JSON number, so a bool, a string,
	// null, an object and an array all fail this assertion.
	number, ok := raw.(json.Number)
	if !ok {
		return time.Time{}, false
	}

	seconds, err := number.Float64()
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return time.Time{}, false
	}
	if seconds <= 0 || seconds > maxExpSeconds {
		return time.Time{}, false
	}

	whole, frac := math.Modf(seconds)
	return time.Unix(int64(whole), int64(frac*float64(time.Second))).UTC(), true
}

// UnverifiedClientID reports the client_id claim of token.
//
// Like UnverifiedExpiry the claim is not verified. It is used only to label the
// stored token set with the DI client the token belongs to, so a refresh reuses
// the same client id. The second result is false when the claim is absent, not a
// string, empty or over maxClientIDLen.
func UnverifiedClientID(token string) (string, bool) {
	claims, ok := unverifiedClaims(token)
	if !ok {
		return "", false
	}

	value, isString := claims["client_id"].(string)
	if !isString || value == "" || len(value) > maxClientIDLen {
		return "", false
	}
	return value, true
}

// unverifiedClaims decodes the payload of a JWT-shaped token without verifying
// its signature. Numbers are kept as json.Number so a caller can reject a value
// that is not a number at all, which float64 coercion would hide.
func unverifiedClaims(token string) (map[string]any, bool) {
	if token == "" || len(token) > maxTokenLen {
		return nil, false
	}

	header, payload, ok := splitSignedSegments(token)
	if !ok {
		return nil, false
	}

	if !hasSigningAlgorithm(header) {
		return nil, false
	}

	claims, ok := decodeJSONObject(payload)
	if !ok {
		return nil, false
	}
	return claims, true
}

// splitSignedSegments returns the header and payload segments of a token that
// carries all three JWT segments. A token with two segments, or with an empty
// signature segment, is unsigned and refused: upstream accepted it, and an
// unsigned payload is exactly the trivially forgeable shape to reject.
func splitSignedSegments(token string) (header, payload string, ok bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", "", false
	}
	if parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", false
	}
	if len(parts[0]) > maxSegmentLen || len(parts[1]) > maxSegmentLen {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// hasSigningAlgorithm reports whether the header names an algorithm other than
// "none". Case is folded, so "NoNe" is refused too.
func hasSigningAlgorithm(headerSegment string) bool {
	header, ok := decodeJSONObject(headerSegment)
	if !ok {
		return false
	}

	alg, isString := header["alg"].(string)
	if !isString || alg == "" {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(alg), "none")
}

// decodeJSONObject base64url-decodes one segment and decodes it as a single JSON
// object. Trailing content after the object is rejected, and numbers are kept as
// json.Number.
func decodeJSONObject(segment string) (map[string]any, bool) {
	raw, err := decodeSegment(segment)
	if err != nil {
		return nil, false
	}

	decoder := json.NewDecoder(strings.NewReader(string(raw)))
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
