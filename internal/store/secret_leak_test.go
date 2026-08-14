package store_test

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/store"
)

// This mirrors internal/garmin/protocol/alias_leak_test.go.
//
// A type conversion strips the method set, so String, GoString, MarshalJSON and LogValue
// no longer apply. fmt then falls into its badVerb path for a verb the struct does not
// support, and that path re-prints the value at depth 0, where a pointer to a struct is
// dereferenced and every field is shown — including unexported ones, because fmt reflects
// instead of calling a method it cannot reach on an unexported field.
//
// These aliases exist only to prove the material stays unreadable anyway.
type (
	strippedSecret   store.Secret
	strippedTokenSet store.TokenSet
)

const (
	leakSecretSentinel  = "SENTINEL-MCP-REFRESH-TOKEN-4d81"
	leakDITokenSentinel = "SENTINEL-DI-TOKEN-7b2c"
	leakDIRefreshToken  = "SENTINEL-DI-REFRESH-1f90"
)

// decimalBytes renders s the way fmt prints a []byte, because leaked material shows up as
// decimal byte values rather than as text and stays fully recoverable.
func decimalBytes(s string) string {
	parts := make([]string, 0, len(s))
	for _, b := range []byte(s) {
		parts = append(parts, strconv.Itoa(int(b)))
	}
	return strings.Join(parts, " ")
}

func TestMethodStrippingAliasCannotRevealASecret(t *testing.T) {
	t.Parallel()

	secret := store.NewSecret(leakSecretSentinel)
	tokens := store.NewTokenSet(leakDITokenSentinel, leakDIRefreshToken, "client", time.Time{})

	needles := map[string]string{
		"mcp secret":         leakSecretSentinel,
		"di token":           leakDITokenSentinel,
		"di refresh token":   leakDIRefreshToken,
		"mcp secret (bytes)": decimalBytes(leakSecretSentinel),
		"di token (bytes)":   decimalBytes(leakDITokenSentinel),
		"di refresh (bytes)": decimalBytes(leakDIRefreshToken),
	}

	values := map[string]any{
		"Secret":       strippedSecret(secret),
		"TokenSet":     strippedTokenSet(tokens),
		"in a struct":  struct{ S strippedSecret }{strippedSecret(secret)},
		"in a slice":   []strippedSecret{strippedSecret(secret)},
		"in a map":     map[string]strippedSecret{"s": strippedSecret(secret)},
		"in a pointer": &[]strippedSecret{strippedSecret(secret)}[0],
		"both in one struct": struct {
			S strippedSecret
			T strippedTokenSet
		}{strippedSecret(secret), strippedTokenSet(tokens)},
	}

	for name, value := range values {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d", "%x"} {
			rendered := fmt.Sprintf(verb, value)
			for label, needle := range needles {
				if strings.Contains(rendered, needle) {
					t.Errorf("%s rendered with %s leaks the %s", name, verb, label)
				}
			}
		}
	}
}

// TestSecretRenderingsAreAllRedacted covers the methods that do apply, so the redaction is
// not only an accident of the alias case.
func TestSecretRenderingsAreAllRedacted(t *testing.T) {
	t.Parallel()

	secret := store.NewSecret(leakSecretSentinel)
	encoded, err := json.Marshal(secret)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	renderings := map[string]string{
		"String":      secret.String(),
		"GoString":    secret.GoString(),
		"MarshalJSON": string(encoded),
		"%v":          fmt.Sprintf("%v", secret),
		"%s":          secret.String(),
		"%q":          fmt.Sprintf("%q", secret),
		"%#v":         fmt.Sprintf("%#v", secret),
		"LogValue":    fmt.Sprintf("%v", secret.LogValue()),
		"in a slice":  fmt.Sprintf("%v", []store.Secret{secret}),
		"in a struct": fmt.Sprintf("%+v", struct{ S store.Secret }{secret}),
	}
	for name, rendered := range renderings {
		if strings.Contains(rendered, leakSecretSentinel) {
			t.Errorf("%s leaks the material: %s", name, rendered)
		}
		if !strings.Contains(rendered, "present") && !strings.Contains(rendered, "true") {
			t.Errorf("%s = %q, want it to report presence", name, rendered)
		}
	}

	// The counter-test: the accessor that exists to disclose still discloses.
	if secret.Reveal() != leakSecretSentinel {
		t.Error("Reveal() must return the material to an authorized caller")
	}
}

// TestZeroSecretIsInert: an absent credential and an empty one must be indistinguishable.
func TestZeroSecretIsInert(t *testing.T) {
	t.Parallel()

	var zero store.Secret
	if !zero.IsZero() || zero.Reveal() != "" {
		t.Error("the zero Secret must be inert")
	}
	// The field label is "present"; its value must be "absent".
	if !strings.Contains(zero.String(), "present:absent") {
		t.Errorf("the zero Secret renders as %q, want it to report absence", zero.String())
	}
	if !strings.Contains(zero.String(), `size:"absent"`) {
		t.Errorf("the zero Secret renders as %q, want size absent", zero.String())
	}
	if !store.NewSecret("").IsZero() {
		t.Error(`NewSecret("") must be indistinguishable from the zero Secret`)
	}
}

// TestSecretSizeBucketsHideTheExactLength: the rendering reports a class, not a length, so
// a log reader cannot fingerprint one credential format against another.
func TestSecretSizeBucketsHideTheExactLength(t *testing.T) {
	t.Parallel()

	cases := map[int]string{
		1:   "short",
		31:  "short",
		32:  "token",
		128: "token",
		129: "large",
	}
	for length, want := range cases {
		rendered := store.NewSecret(strings.Repeat("x", length)).String()
		if !strings.Contains(rendered, want) {
			t.Errorf("a %d byte secret renders as %q, want size %q", length, rendered, want)
		}
		if length > 9 && strings.Contains(rendered, strconv.Itoa(length)) {
			t.Errorf("a %d byte secret renders its exact length: %q", length, rendered)
		}
	}
}

// TestSecretLogValueIsAGroup keeps slog from walking the value: a handler must receive the
// redacted group rather than reflecting over the struct.
func TestSecretLogValueIsAGroup(t *testing.T) {
	t.Parallel()

	value := store.NewSecret(leakSecretSentinel).LogValue()
	if value.Kind() != slog.KindGroup {
		t.Fatalf("LogValue kind = %v, want a group", value.Kind())
	}
	found := map[string]bool{}
	for _, attr := range value.Group() {
		found[attr.Key] = true
		if strings.Contains(fmt.Sprintf("%v", attr.Value), leakSecretSentinel) {
			t.Errorf("attribute %q leaks the material", attr.Key)
		}
	}
	for _, key := range []string{"type", "present", "size"} {
		if !found[key] {
			t.Errorf("the log group has no %q attribute", key)
		}
	}
}
