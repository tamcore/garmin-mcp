package auth_test

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
)

// jwtLike assembles a JWT-shaped string from raw header and payload JSON. The
// signature is synthetic: nothing in this package verifies it, which is exactly
// what the tests below pin down.
func jwtLike(headerJSON, payloadJSON, signature string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(headerJSON)) + "." + enc([]byte(payloadJSON)) + "." + signature
}

const (
	signedHeader   = `{"alg":"RS256","typ":"JWT"}`
	unsignedHeader = `{"alg":"none","typ":"JWT"}`
	fakeSignature  = "c2lnbmF0dXJl"
)

func TestUnverifiedExpiry(t *testing.T) {
	valid := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		token string
		want  time.Time
		ok    bool
	}{
		{
			name:  "integer exp",
			token: jwtLike(signedHeader, `{"exp":1786104000}`, fakeSignature),
			want:  valid,
			ok:    true,
		},
		{
			name:  "fractional exp keeps sub-second precision",
			token: jwtLike(signedHeader, `{"exp":1786104000.5}`, fakeSignature),
			want:  valid.Add(500 * time.Millisecond),
			ok:    true,
		},
		{
			name:  "alg none is rejected",
			token: jwtLike(unsignedHeader, `{"exp":1786104000}`, fakeSignature),
		},
		{
			name:  "alg none in mixed case is rejected",
			token: jwtLike(`{"alg":"NoNe"}`, `{"exp":1786104000}`, fakeSignature),
		},
		{
			name:  "unsigned payload without a signature segment is rejected",
			token: jwtLike(signedHeader, `{"exp":1786104000}`, ""),
		},
		{
			name: "two-segment token is rejected",
			token: base64.RawURLEncoding.EncodeToString([]byte(signedHeader)) + "." +
				base64.RawURLEncoding.EncodeToString([]byte(`{"exp":1786104000}`)),
		},
		{
			name:  "boolean exp is rejected",
			token: jwtLike(signedHeader, `{"exp":true}`, fakeSignature),
		},
		{
			name:  "string exp is rejected",
			token: jwtLike(signedHeader, `{"exp":"1786104000"}`, fakeSignature),
		},
		{
			name:  "object exp is rejected",
			token: jwtLike(signedHeader, `{"exp":{"seconds":1786104000}}`, fakeSignature),
		},
		{
			name:  "null exp is rejected",
			token: jwtLike(signedHeader, `{"exp":null}`, fakeSignature),
		},
		{
			name:  "missing exp is rejected",
			token: jwtLike(signedHeader, `{"client_id":"GARMIN_CONNECT_MOBILE_ANDROID_DI"}`, fakeSignature),
		},
		{
			name:  "non-finite exp is rejected",
			token: jwtLike(signedHeader, `{"exp":1e999}`, fakeSignature),
		},
		{
			name:  "overflowing exp is rejected",
			token: jwtLike(signedHeader, `{"exp":99999999999999999999}`, fakeSignature),
		},
		{
			name:  "negative exp is rejected",
			token: jwtLike(signedHeader, `{"exp":-1}`, fakeSignature),
		},
		{
			name:  "zero exp is rejected",
			token: jwtLike(signedHeader, `{"exp":0}`, fakeSignature),
		},
		{
			name:  "opaque non-JWT token is rejected",
			token: "di-token-secret-0300",
		},
		{
			name:  "empty token is rejected",
			token: "",
		},
		{
			name:  "undecodable header is rejected",
			token: "!!!." + base64.RawURLEncoding.EncodeToString([]byte(`{"exp":1786104000}`)) + "." + fakeSignature,
		},
		{
			name:  "undecodable payload is rejected",
			token: base64.RawURLEncoding.EncodeToString([]byte(signedHeader)) + ".!!!." + fakeSignature,
		},
		{
			name:  "non-object payload is rejected",
			token: jwtLike(signedHeader, `["exp",1786104000]`, fakeSignature),
		},
		{
			name:  "trailing JSON in the payload is rejected",
			token: jwtLike(signedHeader, `{"exp":1786104000}{"exp":1}`, fakeSignature),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := auth.UnverifiedExpiry(tc.token)

			if ok != tc.ok {
				t.Fatalf("UnverifiedExpiry ok = %v, want %v (got %v)", ok, tc.ok, got)
			}
			if !tc.ok {
				if !got.IsZero() {
					t.Fatalf("rejected token reported instant %v, want the zero instant", got)
				}
				return
			}
			if !got.Equal(tc.want) {
				t.Fatalf("UnverifiedExpiry = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUnverifiedExpiryAcceptsBase64PaddingVariants(t *testing.T) {
	// Garmin emits unpadded segments, but a padded segment must still decode:
	// upstream re-pads before decoding.
	padded := base64.URLEncoding.EncodeToString([]byte(signedHeader)) + "." +
		base64.URLEncoding.EncodeToString([]byte(`{"exp":1786104000}`)) + "." + fakeSignature

	got, ok := auth.UnverifiedExpiry(padded)
	if !ok {
		t.Fatal("padded segments were rejected")
	}
	if want := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("UnverifiedExpiry = %v, want %v", got, want)
	}
}

func TestUnverifiedClientID(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  string
		ok    bool
	}{
		{
			name:  "string client_id",
			token: jwtLike(signedHeader, `{"client_id":"GARMIN_CONNECT_MOBILE_ANDROID_DI"}`, fakeSignature),
			want:  "GARMIN_CONNECT_MOBILE_ANDROID_DI",
			ok:    true,
		},
		{
			name:  "numeric client_id is rejected",
			token: jwtLike(signedHeader, `{"client_id":42}`, fakeSignature),
		},
		{
			name:  "empty client_id is rejected",
			token: jwtLike(signedHeader, `{"client_id":""}`, fakeSignature),
		},
		{
			name:  "oversized client_id is rejected",
			token: jwtLike(signedHeader, `{"client_id":"`+longClientID()+`"}`, fakeSignature),
		},
		{
			name:  "missing client_id is rejected",
			token: jwtLike(signedHeader, `{"exp":1786104000}`, fakeSignature),
		},
		{
			name:  "unsigned token is rejected",
			token: jwtLike(unsignedHeader, `{"client_id":"GARMIN_CONNECT_MOBILE_ANDROID_DI"}`, fakeSignature),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := auth.UnverifiedClientID(tc.token)

			if ok != tc.ok || got != tc.want {
				t.Fatalf("UnverifiedClientID = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func longClientID() string {
	out := make([]byte, 200)
	for i := range out {
		out[i] = 'A'
	}
	return string(out)
}

// TestUnverifiedExpiryRejectsOversizedToken keeps a hostile payload from being
// decoded at all: the whole point is bounded, cheap scheduling metadata.
func TestUnverifiedExpiryRejectsOversizedToken(t *testing.T) {
	huge := make([]byte, 1<<16)
	for i := range huge {
		huge[i] = 'A'
	}
	token := base64.RawURLEncoding.EncodeToString([]byte(signedHeader)) + "." +
		string(huge) + "." + fakeSignature

	if _, ok := auth.UnverifiedExpiry(token); ok {
		t.Fatal("an oversized token was accepted")
	}
}
