package store

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	syntheticSignature = "c3ludGhldGljLXNpZ25hdHVyZQ"
	// opaqueToken is a token this package cannot read an expiry from, so the other
	// source decides.
	opaqueToken = "opaque-token"
)

// syntheticJWT builds a three-segment token from a header and a payload object.
func syntheticJWT(header, payload string) string {
	encode := func(part string) string {
		return base64.RawURLEncoding.EncodeToString([]byte(part))
	}
	return encode(header) + "." + encode(payload) + "." + syntheticSignature
}

func tokenExpiring(at time.Time) string {
	return syntheticJWT(`{"alg":"HS256"}`, `{"exp":`+strconv.FormatInt(at.Unix(), 10)+`}`)
}

func TestUnverifiedExpiryReadsASignedToken(t *testing.T) {
	want := testExpiry()

	if got := unverifiedExpiry(tokenExpiring(want)); !got.Equal(want) {
		t.Fatalf("unverifiedExpiry = %v, want %v", got, want)
	}
}

func TestUnverifiedExpiryRefusesUnsignedAndOversizedTokens(t *testing.T) {
	longSegment := strings.Repeat("A", maxSegmentBytes+1)
	signed := syntheticJWT(`{"alg":"HS256"}`, `{"exp":1767225600}`)

	cases := map[string]string{
		"two segments":           strings.Join(strings.Split(signed, ".")[:2], "."),
		"empty signature":        strings.Join(strings.Split(signed, ".")[:2], ".") + ".",
		"empty header":           "." + strings.Split(signed, ".")[1] + "." + syntheticSignature,
		"alg none":               syntheticJWT(`{"alg":"none"}`, `{"exp":1767225600}`),
		"alg NoNe":               syntheticJWT(`{"alg":"NoNe"}`, `{"exp":1767225600}`),
		"alg missing":            syntheticJWT(`{"typ":"JWT"}`, `{"exp":1767225600}`),
		"alg not a string":       syntheticJWT(`{"alg":42}`, `{"exp":1767225600}`),
		"header not an object":   syntheticJWT(`"header"`, `{"exp":1767225600}`),
		"payload not an object":  syntheticJWT(`{"alg":"HS256"}`, `"payload"`),
		"trailing content":       syntheticJWT(`{"alg":"HS256"}`, `{"exp":1767225600} {}`),
		"token over the bound":   strings.Repeat("A", maxTokenBytes+1),
		"segment over the bound": longSegment + "." + longSegment + "." + syntheticSignature,
	}

	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			if got := unverifiedExpiry(token); !got.IsZero() {
				t.Fatalf("unverifiedExpiry of %s = %v, want the zero time", name, got)
			}
		})
	}
}

// TestDocumentExpiryNeverLetsAnImportedExpiryDelayARefresh is the point of the
// bound: expires_at arrives from an imported file, so a value beyond the token's own
// claim must not suppress a proactive refresh.
func TestDocumentExpiryNeverLetsAnImportedExpiryDelayARefresh(t *testing.T) {
	claimed := testExpiry()

	got := documentExpiry(tokenDocument{
		Token:     tokenExpiring(claimed),
		ExpiresAt: "3000-01-01T00:00:00Z",
	})
	if !got.Equal(claimed) {
		t.Fatalf("documentExpiry = %v, want the token's own claim %v", got, claimed)
	}
}

func TestDocumentExpiryPrefersTheEarlierOfTheTwo(t *testing.T) {
	claimed := testExpiry()
	earlier := claimed.Add(-24 * time.Hour)

	got := documentExpiry(tokenDocument{
		Token:     tokenExpiring(claimed),
		ExpiresAt: earlier.Format(time.RFC3339),
	})
	if !got.Equal(earlier) {
		t.Fatalf("documentExpiry = %v, want the earlier recorded expiry %v", got, earlier)
	}
}

func TestDocumentExpiryFallsBackToEachSourceAlone(t *testing.T) {
	recorded := testExpiry()

	fromRecord := documentExpiry(tokenDocument{
		Token:     opaqueToken,
		ExpiresAt: recorded.Format(time.RFC3339),
	})
	if !fromRecord.Equal(recorded) {
		t.Fatalf("documentExpiry with an opaque token = %v, want %v", fromRecord, recorded)
	}

	fromToken := documentExpiry(tokenDocument{Token: tokenExpiring(recorded)})
	if !fromToken.Equal(recorded) {
		t.Fatalf("documentExpiry with no expires_at = %v, want %v", fromToken, recorded)
	}
}

func TestDocumentExpiryRefusesAnAbsurdRecordedExpiry(t *testing.T) {
	cases := map[string]string{
		"unparsable":       "tomorrow",
		"beyond the bound": "9999-12-31T23:59:59Z",
		"before the epoch": "1969-01-01T00:00:00Z",
		"blank":            " ",
	}

	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			got := documentExpiry(tokenDocument{Token: opaqueToken, ExpiresAt: value})
			if !got.IsZero() {
				t.Fatalf("documentExpiry with %s = %v, want the zero time", name, got)
			}
		})
	}
}

// TestDecodeTokenDocumentAppliesTheBoundedExpiry keeps the hardening reachable
// through the decoder every caller actually uses.
func TestDecodeTokenDocumentAppliesTheBoundedExpiry(t *testing.T) {
	claimed := testExpiry()
	raw, err := json.Marshal(tokenDocument{
		Token:        tokenExpiring(claimed),
		RefreshToken: testRefreshToken,
		ExpiresAt:    "3000-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("encode document: %v", err)
	}

	set, err := decodeTokenDocument(raw, sourceRecord)
	if err != nil {
		t.Fatalf("decodeTokenDocument: %v", err)
	}
	if !set.ExpiresAt().Equal(claimed) {
		t.Fatalf("ExpiresAt() = %v, want the token's own claim %v", set.ExpiresAt(), claimed)
	}
}
