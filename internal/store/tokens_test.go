package store

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// Synthetic token material. Nothing here is a real Garmin credential: the JWT is
// a hand-built header.payload.signature triple whose payload decodes to
// {"exp":1767225600}.
const (
	testToken        = "eyJhbGciOiJIUzI1NiJ9.eyJleHAiOjE3NjcyMjU2MDB9.c3ludGhldGljLXNpZ25hdHVyZQ"
	testRefreshToken = "synthetic-refresh-6b1f4c0d9a2e"
	// testClientID is one of the candidate DI client ids, so it survives the
	// allowlist a rendering applies.
	testClientID = "GARMIN_CONNECT_MOBILE_ANDROID_DI"
	// testForeignClientID is not a candidate DI client id. Garmin controls this
	// field and an imported token file supplies it verbatim, so an unrecognized
	// value must never reach a log line.
	testForeignClientID = "SECRET-LOOKING-VALUE-eyJhbGciOiJIUzI1NiJ9"
	testPrincipal       = "8f1c0f52-0f4b-4f2e-9a3a-7f2b1d6c5e40"
	testOther           = "1c4d9f70-0aa1-4f11-9df0-3b0c8a5e2d31"
)

func testExpiry() time.Time { return time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC) }

func newTestTokens() TokenSet {
	return NewTokenSet(testToken, testRefreshToken, testClientID, testExpiry())
}

// strippedTokenSet is the method-stripping alias attack: the alias loses String,
// GoString, MarshalJSON and LogValue, so fmt falls back to reflection.
type strippedTokenSet TokenSet

func TestNewTokenSetExposesItsFieldsThroughAccessors(t *testing.T) {
	set := newTestTokens()

	if got := set.Token(); got != testToken {
		t.Fatalf("Token() = %q, want the di_token", got)
	}
	if got := set.RefreshToken(); got != testRefreshToken {
		t.Fatalf("RefreshToken() = %q, want the di_refresh_token", got)
	}
	if got := set.ClientID(); got != testClientID {
		t.Fatalf("ClientID() = %q, want the di_client_id", got)
	}
	if got := set.ExpiresAt(); !got.Equal(testExpiry()) {
		t.Fatalf("ExpiresAt() = %v, want %v", got, testExpiry())
	}
	if set.IsZero() {
		t.Fatal("a populated TokenSet must not report IsZero")
	}
}

func TestZeroTokenSetIsInert(t *testing.T) {
	var set TokenSet

	if !set.IsZero() {
		t.Fatal("the zero TokenSet must report IsZero")
	}
	if set.Token() != "" || set.RefreshToken() != "" || set.ClientID() != "" {
		t.Fatal("the zero TokenSet must report empty credentials")
	}
	if !set.ExpiresAt().IsZero() {
		t.Fatal("the zero TokenSet must report a zero expiry")
	}
	if set.String() == "" {
		t.Fatal("the zero TokenSet must still render")
	}
	if _, err := json.Marshal(set); err != nil {
		t.Fatalf("json.Marshal(zero TokenSet): %v", err)
	}
}

func assertNoTokenSecrets(t *testing.T, label, rendered string, set TokenSet) {
	t.Helper()
	for _, secret := range []string{set.Token(), set.RefreshToken()} {
		if secret != "" && strings.Contains(rendered, secret) {
			t.Fatalf("%s leaked token material: %q", label, rendered)
		}
	}
}

func TestTokenSetRenderingsNeverRevealSecrets(t *testing.T) {
	set := newTestTokens()

	encoded, err := json.Marshal(set)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	nested, err := json.Marshal(struct {
		Tokens TokenSet `json:"tokens"`
	}{Tokens: set})
	if err != nil {
		t.Fatalf("json.Marshal nested: %v", err)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("tokens loaded", slog.Any("tokens", set))

	stripped := strippedTokenSet(set)
	renderings := map[string]string{
		"String":         set.String(),
		"GoString":       set.GoString(),
		"fmt %v":         fmt.Sprintf("%v", set),
		"fmt %s":         fmt.Sprintf("as-%s", set),
		"fmt %+v":        fmt.Sprintf("%+v", set),
		"fmt %#v":        fmt.Sprintf("%#v", set),
		"fmt pointer":    fmt.Sprintf("%v", &set),
		"MarshalJSON":    string(encoded),
		"nested JSON":    string(nested),
		"slog":           buf.String(),
		"stripped alias": fmt.Sprintf("%v|%+v|%#v", stripped, stripped, stripped),
	}
	for label, rendered := range renderings {
		assertNoTokenSecrets(t, label, rendered, set)
	}
}

// TestTokenSetAliasCannotBypassRedactionUnderAnyVerb covers the verbs the map above
// does not. %s and %q on a value with no String method reach fmt's badVerb path,
// which re-prints the value at depth zero, and depth zero dereferences a pointer to
// a struct and prints its unexported fields. A plain string or []byte field would
// surface the token there verbatim or as its decimal bytes.
func TestTokenSetAliasCannotBypassRedactionUnderAnyVerb(t *testing.T) {
	set := newTestTokens().WithToken(testToken).WithRefreshToken(testRefreshToken)
	stripped := strippedTokenSet(set)

	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d", "%x"} {
		assertNoTokenSecrets(t, "stripped alias under "+verb, fmt.Sprintf(verb, stripped), set)
		assertNoTokenSecrets(t, "pointer to a stripped alias under "+verb,
			fmt.Sprintf(verb, &stripped), set)
	}
}

// TestTokenSetRenderingsSanitizeAnUnknownClientID is the log-injection half: the
// di_client_id of an imported token file is unverified text, so only a recognized
// DI client id is echoed and anything else collapses to a label.
func TestTokenSetRenderingsSanitizeAnUnknownClientID(t *testing.T) {
	set := NewTokenSet(testToken, testRefreshToken, testForeignClientID, testExpiry())

	encoded, err := json.Marshal(set)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("tokens", slog.Any("tokens", set))

	for label, rendered := range map[string]string{
		"String":      set.String(),
		"GoString":    set.GoString(),
		"MarshalJSON": string(encoded),
		"slog":        buf.String(),
	} {
		if strings.Contains(rendered, testForeignClientID) {
			t.Fatalf("%s echoed an unrecognized client id: %q", label, rendered)
		}
		if !strings.Contains(rendered, "unknown") {
			t.Fatalf("%s should report the client id as unknown, got %q", label, rendered)
		}
	}
	// The accessor still reports the stored value: a refresh has to send back the
	// client id the token was issued for, whatever it is.
	if set.ClientID() != testForeignClientID {
		t.Fatalf("ClientID() = %q, want the stored value", set.ClientID())
	}
}

func TestTokenSetRenderingsReportShape(t *testing.T) {
	set := newTestTokens()

	if !strings.Contains(set.String(), "token:present") {
		t.Fatalf("String() should report token presence, got %q", set.String())
	}
	if !strings.Contains(set.String(), "refreshToken:present") {
		t.Fatalf("String() should report refresh token presence, got %q", set.String())
	}
	// The client id is not a secret: it names the Garmin OAuth client and is
	// needed to correlate logs.
	if !strings.Contains(set.String(), testClientID) {
		t.Fatalf("String() should report the client id, got %q", set.String())
	}

	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("tokens", slog.Any("tokens", set))
	if !strings.Contains(buf.String(), `"tokenPresent":true`) {
		t.Fatalf("slog output should report token presence, got %s", buf.String())
	}
	if !strings.Contains(buf.String(), `"expiresAt":"2026-01-01T00:00:00Z"`) {
		t.Fatalf("slog output should report the expiry, got %s", buf.String())
	}
}

func TestTokenSetIsImmutable(t *testing.T) {
	original := newTestTokens()
	replaced := original.WithToken("rotated-token-value")

	if original.Token() != testToken {
		t.Fatal("WithToken mutated the receiver")
	}
	if replaced.Token() != "rotated-token-value" {
		t.Fatalf("WithToken did not apply, got %q", replaced.Token())
	}
	if replaced.RefreshToken() != original.RefreshToken() {
		t.Fatal("WithToken dropped the refresh token")
	}
}

// TestRotationHelpersCopy covers the pair Garmin's refresh needs: the refresh token
// rotates on every use, and the scheduling expiry moves with it.
func TestRotationHelpersCopy(t *testing.T) {
	original := newTestTokens()
	later := testExpiry().Add(time.Hour)

	rotated := original.WithRefreshToken("rotated-refresh").WithExpiresAt(later)

	if original.RefreshToken() != testRefreshToken || !original.ExpiresAt().Equal(testExpiry()) {
		t.Fatal("the rotation helpers mutated the receiver")
	}
	if rotated.RefreshToken() != "rotated-refresh" {
		t.Fatalf("WithRefreshToken did not apply, got %q", rotated.RefreshToken())
	}
	if !rotated.ExpiresAt().Equal(later) {
		t.Fatalf("WithExpiresAt did not apply, got %v", rotated.ExpiresAt())
	}
	if rotated.Token() != original.Token() || rotated.ClientID() != original.ClientID() {
		t.Fatal("the rotation helpers dropped an unrelated field")
	}
}

// TestUnverifiedExpiryToleratesHostileClaims covers the defensive coercion the
// upstream 0.3.10 client documents: exp is a server-controlled claim from an
// unverified JWT, so no shape of it may fail a load. Every bad shape yields the
// zero time.
func TestUnverifiedExpiryToleratesHostileClaims(t *testing.T) {
	jwtWith := func(payload string) string {
		return "eyJhbGciOiJIUzI1NiJ9." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".sig"
	}
	cases := map[string]string{
		"not a jwt":            opaqueToken,
		"two segments":         "a.b",
		"payload not base64":   "a.!!!.c",
		"payload not json":     jwtWith("not json"),
		"exp missing":          jwtWith(`{"sub":"x"}`),
		"exp is a string":      jwtWith(`{"exp":"soon"}`),
		"exp is a bool":        jwtWith(`{"exp":true}`),
		"exp is an object":     jwtWith(`{"exp":{"at":1}}`),
		"exp overflows float":  jwtWith(`{"exp":1e999}`),
		"exp is negative":      jwtWith(`{"exp":-5}`),
		"exp is absurdly far":  jwtWith(`{"exp":99999999999999}`),
		"exp is zero":          jwtWith(`{"exp":0}`),
		"exp is fractional ok": jwtWith(`{"exp":1767225600.75}`),
	}
	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			got := unverifiedExpiry(token)
			if name == "exp is fractional ok" {
				if !got.Equal(testExpiry()) {
					t.Fatalf("a fractional exp should still parse, got %v", got)
				}
				return
			}
			if !got.IsZero() {
				t.Fatalf("unverifiedExpiry = %v, want the zero time", got)
			}
		})
	}
}
