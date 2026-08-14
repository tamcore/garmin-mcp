package oauthserver

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// strippedSecret is the method-stripping alias attack: a caller that defines its
// own type from Secret loses String, GoString, MarshalJSON and LogValue, so fmt
// falls back to reflection over the fields.
type strippedSecret Secret

func mustSecret(t *testing.T) Secret {
	t.Helper()
	secret, err := NewSecret()
	if err != nil {
		t.Fatalf("NewSecret: %v", err)
	}
	return secret
}

func secretEncodings(secret Secret) []string {
	revealed := secret.Reveal()
	raw, err := base64.RawURLEncoding.DecodeString(revealed)
	if err != nil {
		raw = []byte(revealed)
	}
	return []string{
		revealed,
		string(raw),
		hex.EncodeToString(raw),
		base64.StdEncoding.EncodeToString(raw),
		fmt.Sprintf("%v", raw),
		fmt.Sprintf("%d", raw),
	}
}

func assertNoSecretMaterial(t *testing.T, label, rendered string, secret Secret) {
	t.Helper()
	for _, encoding := range secretEncodings(secret) {
		if encoding != "" && strings.Contains(rendered, encoding) {
			t.Fatalf("%s leaked secret material: %q", label, rendered)
		}
	}
}

func TestNewSecretCarriesAtLeast256BitsOfEntropy(t *testing.T) {
	secret := mustSecret(t)

	raw, err := base64.RawURLEncoding.DecodeString(secret.Reveal())
	if err != nil {
		t.Fatalf("secret is not base64url: %v", err)
	}
	if len(raw)*8 < 256 {
		t.Fatalf("secret carries %d bits, want at least 256", len(raw)*8)
	}
}

func TestNewSecretIsUnique(t *testing.T) {
	seen := make(map[string]struct{}, 128)
	for range 128 {
		revealed := mustSecret(t).Reveal()
		if _, dup := seen[revealed]; dup {
			t.Fatal("NewSecret returned a duplicate value")
		}
		seen[revealed] = struct{}{}
	}
}

func TestSecretLookupIsDeterministicAndDistinct(t *testing.T) {
	secret := mustSecret(t)
	other := mustSecret(t)

	if !secret.Lookup().Equal(SecretFromString(secret.Reveal()).Lookup()) {
		t.Fatal("Lookup is not stable across a re-presented secret")
	}
	if secret.Lookup().Equal(other.Lookup()) {
		t.Fatal("distinct secrets produced the same lookup")
	}
	if secret.Lookup().Hex() == "" {
		t.Fatal("Lookup.Hex is empty")
	}
	if strings.Contains(secret.Lookup().Hex(), secret.Reveal()) {
		t.Fatal("Lookup.Hex contains the secret")
	}
}

func TestParseLookupRoundTrips(t *testing.T) {
	want := mustSecret(t).Lookup()

	got, err := ParseLookup(want.Hex())
	if err != nil {
		t.Fatalf("ParseLookup: %v", err)
	}
	if !got.Equal(want) {
		t.Fatal("ParseLookup did not round-trip")
	}
}

func TestParseLookupRejectsMalformedInput(t *testing.T) {
	for name, input := range map[string]string{
		"no characters":           "",
		"short":                   "abcd",
		"non-hex":                 strings.Repeat("z", 64),
		"more than 64 characters": strings.Repeat("a", 65),
		"odd bytes":               strings.Repeat("a", 63),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseLookup(input); err == nil {
				t.Fatalf("ParseLookup(%q) accepted malformed input", input)
			}
		})
	}
}

func TestZeroSecretAndZeroLookupReportThemselves(t *testing.T) {
	var secret Secret
	var lookup Lookup

	if !secret.IsZero() {
		t.Fatal("the zero Secret does not report IsZero")
	}
	if secret.Reveal() != "" {
		t.Fatal("the zero Secret revealed a value")
	}
	if !lookup.IsZero() {
		t.Fatal("the zero Lookup does not report IsZero")
	}
	if !secret.Lookup().IsZero() {
		t.Fatal("the zero Secret hashed to a non-zero lookup")
	}
}

func TestSecretRenderingsNeverRevealMaterial(t *testing.T) {
	secret := mustSecret(t)

	encoded, err := json.Marshal(secret)
	if err != nil {
		t.Fatalf("json.Marshal(Secret): %v", err)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("issued", slog.Any("secret", secret))

	renderings := map[string]string{
		labelString:         secret.String(),
		labelGoString:       secret.GoString(),
		labelFmtV:           fmt.Sprintf("%v", secret),
		labelFmtS:           fmt.Sprintf("as-%s", secret),
		labelFmtPlusV:       fmt.Sprintf("%+v", secret),
		labelFmtSharpV:      fmt.Sprintf("%#v", secret),
		"fmt %v of pointer": fmt.Sprintf("%v", &secret),
		"MarshalJSON":       string(encoded),
		"slog":              buf.String(),
	}
	for label, rendered := range renderings {
		assertNoSecretMaterial(t, label, rendered, secret)
		if rendered == "" {
			t.Fatalf("%s rendered as the empty string", label)
		}
	}
}

func TestSecretAliasCannotBypassRedaction(t *testing.T) {
	secret := mustSecret(t)
	stripped := strippedSecret(secret)

	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d", "%x"} {
		assertNoSecretMaterial(t, "method-stripped alias under "+verb,
			fmt.Sprintf(verb, stripped), secret)
		assertNoSecretMaterial(t, "pointer to a method-stripped alias under "+verb,
			fmt.Sprintf(verb, &stripped), secret)
	}
}

func TestZeroSecretRendersWithoutPanicking(t *testing.T) {
	var secret Secret

	if secret.String() == "" || secret.GoString() == "" {
		t.Fatal("the zero Secret rendered as the empty string")
	}
	if _, err := json.Marshal(secret); err != nil {
		t.Fatalf("json.Marshal(zero Secret): %v", err)
	}
}
