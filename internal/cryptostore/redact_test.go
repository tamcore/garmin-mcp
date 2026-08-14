package cryptostore

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

// strippedKey is the method-stripping alias attack: a caller that defines its
// own type from Key loses String, GoString, MarshalJSON and LogValue, so fmt
// falls back to reflection over the fields.
type strippedKey Key

// keyEncodings returns every textual encoding of the key material that must
// never appear in a rendering of a Key.
func keyEncodings(key Key) []string {
	material := key.bytes()
	return []string{
		string(material),
		base64.StdEncoding.EncodeToString(material),
		base64.RawURLEncoding.EncodeToString(material),
		hex.EncodeToString(material),
		fmt.Sprintf("%v", material),
		fmt.Sprintf("%d", material),
	}
}

func assertNoKeyMaterial(t *testing.T, label, rendered string, key Key) {
	t.Helper()
	for _, encoding := range keyEncodings(key) {
		if encoding != "" && strings.Contains(rendered, encoding) {
			t.Fatalf("%s leaked key material: %q", label, rendered)
		}
	}
}

func TestKeyRenderingsNeverRevealMaterial(t *testing.T) {
	key := mustKey(t, 5)

	renderings := map[string]string{
		"String":            key.String(),
		"GoString":          key.GoString(),
		"fmt %v":            fmt.Sprintf("%v", key),
		"fmt %s":            fmt.Sprintf("as-%s", key),
		"fmt %+v":           fmt.Sprintf("%+v", key),
		"fmt %#v":           fmt.Sprintf("%#v", key),
		"fmt %v of pointer": fmt.Sprintf("%v", &key),
	}
	for label, rendered := range renderings {
		assertNoKeyMaterial(t, label, rendered, key)
	}
	if !strings.Contains(key.String(), "version:5") {
		t.Fatalf("Key.String() should report the key version, got %q", key.String())
	}
}

func TestKeyMarshalJSONRedacts(t *testing.T) {
	key := mustKey(t, 2)

	encoded, err := json.Marshal(key)
	if err != nil {
		t.Fatalf("json.Marshal(Key): %v", err)
	}
	assertNoKeyMaterial(t, "MarshalJSON", string(encoded), key)

	wrapped, err := json.Marshal(struct {
		Key Key `json:"key"`
	}{Key: key})
	if err != nil {
		t.Fatalf("json.Marshal(struct with Key): %v", err)
	}
	assertNoKeyMaterial(t, "MarshalJSON nested", string(wrapped), key)
}

func TestKeyLogValueRedacts(t *testing.T) {
	key := mustKey(t, 9)

	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("key loaded", slog.Any("key", key))

	assertNoKeyMaterial(t, "slog", buf.String(), key)
	if !strings.Contains(buf.String(), `"version":9`) {
		t.Fatalf("slog output should carry the key version, got %s", buf.String())
	}
}

// TestKeyAliasCannotBypassRedaction covers every verb, not only the reflective
// ones. %s and %q on a value with no String method reach fmt's badVerb path, which
// re-prints the value at depth zero, and depth zero dereferences a pointer to a
// struct and prints its unexported fields. A []byte field would surface there as
// its decimal bytes, which reveals the material just as plainly as the text does.
func TestKeyAliasCannotBypassRedaction(t *testing.T) {
	key := mustKey(t, 1)
	stripped := strippedKey(key)

	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d", "%x"} {
		rendered := fmt.Sprintf(verb, stripped)
		assertNoKeyMaterial(t, "method-stripped alias under "+verb, rendered, key)
		assertNoKeyMaterial(t, "pointer to a method-stripped alias under "+verb,
			fmt.Sprintf(verb, &stripped), key)
	}
}

func TestZeroKeyRendersWithoutPanicking(t *testing.T) {
	var key Key
	renderings := []string{
		key.String(),
		key.GoString(),
		fmt.Sprintf("%v", key),
	}
	for _, rendered := range renderings {
		if rendered == "" {
			t.Fatal("zero Key rendered as the empty string")
		}
	}
	if _, err := json.Marshal(key); err != nil {
		t.Fatalf("json.Marshal(zero Key): %v", err)
	}
}

func TestEnvelopeRenderingsNeverRevealCiphertextOrNonce(t *testing.T) {
	key := mustKey(t, 3)
	sealed, err := Encrypt(key, testPrincipal, testRecordType, []byte("plaintext secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	env, err := parseEnvelope(sealed)
	if err != nil {
		t.Fatalf("parseEnvelope: %v", err)
	}

	encoded, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("json.Marshal(envelope): %v", err)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("envelope", slog.Any("envelope", env))

	renderings := map[string]string{
		"String":      env.String(),
		"GoString":    env.GoString(),
		"fmt %v":      fmt.Sprintf("%v", env),
		"fmt %#v":     fmt.Sprintf("%#v", env),
		"MarshalJSON": string(encoded),
		"slog":        buf.String(),
	}
	forbidden := []string{
		hex.EncodeToString(env.sealed.nonce),
		hex.EncodeToString(env.sealed.ciphertext),
		base64.StdEncoding.EncodeToString(env.sealed.ciphertext),
		fmt.Sprintf("%v", env.sealed.ciphertext),
	}
	for label, rendered := range renderings {
		for _, secret := range forbidden {
			if secret != "" && strings.Contains(rendered, secret) {
				t.Fatalf("envelope %s leaked ciphertext or nonce: %q", label, rendered)
			}
		}
		if !strings.Contains(rendered, "3") {
			t.Fatalf("envelope %s should report key version 3, got %q", label, rendered)
		}
	}
}
