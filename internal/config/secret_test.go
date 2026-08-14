package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// sentinelSecret is the synthetic secret material every redaction test looks
// for. It appears in no other position in the repository, so a hit in rendered
// output is proof of a leak.
const sentinelSecret = "SENTINEL-MASTER-KEY-6f2b"

func TestSecretPresence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		secret    Secret
		wantSet   bool
		wantValue string
	}{
		{name: "zero value is unset", secret: Secret{}},
		{name: "empty string is unset", secret: NewSecret("")},
		{name: "whitespace is unset", secret: NewSecret("   ")},
		{name: "value is set", secret: NewSecret(sentinelSecret), wantSet: true, wantValue: sentinelSecret},
		{
			name:      "value is trimmed",
			secret:    NewSecret("  " + sentinelSecret + " \n"),
			wantSet:   true,
			wantValue: sentinelSecret,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.secret.IsSet(); got != tc.wantSet {
				t.Errorf("IsSet() = %v, want %v", got, tc.wantSet)
			}
			if got := tc.secret.Reveal(); got != tc.wantValue {
				t.Errorf("Reveal() returned %d bytes, want %d", len(got), len(tc.wantValue))
			}
		})
	}
}

// secretRenderings collects every way a Secret can reach a human or a log sink:
// the shared renderer plus the redacting methods themselves.
func secretRenderings(t *testing.T, secret Secret) map[string]string {
	t.Helper()

	out := renderAll(t, secret)
	out["String()"] = secret.String()
	out["GoString()"] = secret.GoString()
	out["LogValue()"] = fmt.Sprint(secret.LogValue())
	return out
}

func TestSecretNeverRendersItsValue(t *testing.T) {
	t.Parallel()

	secret := NewSecret(sentinelSecret)

	for verb, rendering := range secretRenderings(t, secret) {
		if strings.Contains(rendering, sentinelSecret) {
			t.Errorf("%s rendering leaks the secret: %s", verb, rendering)
		}
		if !strings.Contains(rendering, redactedMarker) {
			t.Errorf("%s rendering = %q, want the %q marker", verb, rendering, redactedMarker)
		}
	}
}

func TestUnsetSecretRendersAsUnset(t *testing.T) {
	t.Parallel()

	var secret Secret

	if got := secret.String(); got != unsetMarker {
		t.Errorf("String() = %q, want %q", got, unsetMarker)
	}

	encoded, err := json.Marshal(secret)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if got, want := string(encoded), `"`+unsetMarker+`"`; got != want {
		t.Errorf("json.Marshal = %s, want %s", got, want)
	}
}
