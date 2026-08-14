package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"strings"
	"testing"
)

// verbGoSyntax is the Go-syntax fmt verb, which reflects over a value unless the
// type provides GoString. Every redaction test renders it.
const verbGoSyntax = "%#v"

// strippedSecret is a method-stripping alias of Secret: the conversion keeps the
// layout and drops String, GoString, MarshalJSON, and LogValue. It is the
// adversary's cheapest attack — one type declaration — so the material must be
// unreachable by reflection rather than only by method.
type strippedSecret Secret

// strippedConfig is the same attack against Config.
type strippedConfig Config

// secretHolder embeds a Secret in an unrelated struct, the shape a caller
// produces when it carries configuration around in its own type.
type secretHolder struct {
	Name string
	Key  Secret
	Secret
}

// strippedHolder holds the stripped alias, so neither the outer nor the inner
// type contributes a redacting method.
type strippedHolder struct {
	Name string
	Key  strippedSecret
}

// renderAll collects every rendering path by which a value can reach a human or a
// log sink: the fmt verbs, a pointer to the value, the value inside a slice and a
// map, encoding/json, and both slog handlers, directly and nested in a group.
//
// It is generic so the pointer renderings are a *T rather than a pointer to an
// interface, which would only print an address and prove nothing. Every redaction
// test in this package renders through it, so a newly discovered leak path is
// added once and checked everywhere.
func renderAll[T any](t *testing.T, value T) map[string]string {
	t.Helper()

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	encodedPtr, err := json.Marshal(&value)
	if err != nil {
		t.Fatalf("json.Marshal pointer: %v", err)
	}

	out := map[string]string{
		"%v":         fmt.Sprintf("%v", value),
		"%+v":        fmt.Sprintf("%+v", value),
		verbGoSyntax: fmt.Sprintf(verbGoSyntax, value),
		// The conversion to any keeps go vet from type-checking %s against a type
		// parameter; the verb is deliberately inapplicable to a struct, because
		// that is the path fmt takes through badVerb.
		"%s":               fmt.Sprintf("[%s]", any(value)),
		"pointer %v":       fmt.Sprintf("%v", &value),
		"pointer %+v":      fmt.Sprintf("%+v", &value),
		"pointer %#v":      fmt.Sprintf(verbGoSyntax, &value),
		"in slice %+v":     fmt.Sprintf("%+v", []T{value}),
		"in slice %#v":     fmt.Sprintf(verbGoSyntax, []T{value}),
		"in map %+v":       fmt.Sprintf("%+v", map[string]T{"v": value}),
		"in map %#v":       fmt.Sprintf(verbGoSyntax, map[string]T{"v": value}),
		"json.Marshal":     string(encoded),
		"json.Marshal ptr": string(encodedPtr),
	}
	maps.Copy(out, logRenderings(value))
	return out
}

// logRenderings drives both slog handlers with the value directly and nested
// inside a group, an attribute list, and a slice.
func logRenderings[T any](value T) map[string]string {
	var jsonBuf, textBuf, jsonNested, textNested bytes.Buffer
	slog.New(slog.NewJSONHandler(&jsonBuf, nil)).Info("effective", "value", value)
	slog.New(slog.NewTextHandler(&textBuf, nil)).Info("effective", "value", value)
	slog.New(slog.NewJSONHandler(&jsonNested, nil)).
		WithGroup("outer").With("inner", &value).Info("effective", "again", []T{value})
	slog.New(slog.NewTextHandler(&textNested, nil)).
		WithGroup("outer").With("inner", &value).Info("effective", "again", []T{value})

	return map[string]string{
		"slog json":      jsonBuf.String(),
		"slog text":      textBuf.String(),
		"slog json deep": jsonNested.String(),
		"slog text deep": textNested.String(),
	}
}

// assertNoLeak fails when any rendering of value contains material.
func assertNoLeak[T any](t *testing.T, label string, value T, material ...string) {
	t.Helper()

	for name, rendering := range renderAll(t, value) {
		for _, secret := range material {
			if strings.Contains(rendering, secret) {
				t.Errorf("%s: %s rendering leaks %q:\n%s", label, name, secret, rendering)
			}
		}
	}
}

// TestStrippedSecretAliasCannotRevealTheValue is the HIGH finding: a
// method-stripping alias must not turn a Secret into a printable string.
func TestStrippedSecretAliasCannotRevealTheValue(t *testing.T) {
	t.Parallel()

	assertNoLeak(t, "strippedSecret", strippedSecret(NewSecret(sentinelSecret)), sentinelSecret)
}

// TestStrippedConfigAliasCannotRevealSecretMaterial is the same attack against
// Config, whose secret-bearing fields are exported.
func TestStrippedConfigAliasCannotRevealSecretMaterial(t *testing.T) {
	t.Parallel()

	assertNoLeak(t, "strippedConfig", strippedConfig(populatedConfig(t)), sentinelSecret, sentinelTokens)
}

// TestSecretInAnotherStructIsSafe covers a Secret carried by a foreign type,
// both as a named field and embedded, with and without the redacting methods.
func TestSecretInAnotherStructIsSafe(t *testing.T) {
	t.Parallel()

	secret := NewSecret(sentinelSecret)

	assertNoLeak(t, "secretHolder", secretHolder{Name: "master", Key: secret, Secret: secret}, sentinelSecret)
	assertNoLeak(t, "strippedHolder",
		strippedHolder{Name: "master", Key: strippedSecret(secret)}, sentinelSecret)
	assertNoLeak(t, "config in holder",
		struct {
			Label string
			Cfg   Config
		}{Label: "effective", Cfg: populatedConfig(t)}, sentinelSecret, sentinelTokens)
}

// TestSecretTypeExposesNoFieldCarryingMaterial is the structural guarantee: no
// path of exported fields reaches the material, so a reflective logger that
// walks only what it may read cannot find it.
func TestSecretTypeExposesNoFieldCarryingMaterial(t *testing.T) {
	t.Parallel()

	for _, field := range reflect.VisibleFields(reflect.TypeFor[Secret]()) {
		if field.PkgPath == "" {
			t.Errorf("Secret has exported field %q: secret material must not be reachable", field.Name)
		}
	}

	assertNoReachableMaterial(t, "Config", reflect.ValueOf(populatedConfig(t)), sentinelSecret, sentinelTokens)
	assertNoReachableMaterial(t, "Secret", reflect.ValueOf(NewSecret(sentinelSecret)), sentinelSecret)
}

// maxWalkDepth bounds the reflective walk, which must terminate on a cyclic or
// deeply nested shape.
const maxWalkDepth = 12

// assertNoReachableMaterial walks value through exported fields only — the same
// reach a reflective logger has — and fails when any string it can read carries
// secret material.
func assertNoReachableMaterial(t *testing.T, path string, value reflect.Value, material ...string) {
	t.Helper()

	walkReachable(t, path, value, 0, material)
}

func walkReachable(t *testing.T, path string, value reflect.Value, depth int, material []string) {
	t.Helper()

	if depth > maxWalkDepth || !value.IsValid() || !value.CanInterface() {
		return
	}

	switch value.Kind() {
	case reflect.String:
		for _, secret := range material {
			if strings.Contains(value.String(), secret) {
				t.Errorf("%s is a reflectively readable string carrying %q", path, secret)
			}
		}
	case reflect.Struct:
		walkFields(t, path, value, depth, material)
	case reflect.Pointer, reflect.Interface:
		if !value.IsNil() {
			walkReachable(t, path+"->", value.Elem(), depth+1, material)
		}
	case reflect.Slice, reflect.Array:
		for i := range value.Len() {
			walkReachable(t, fmt.Sprintf("%s[%d]", path, i), value.Index(i), depth+1, material)
		}
	case reflect.Map:
		for _, key := range value.MapKeys() {
			walkReachable(t, path+"[key]", value.MapIndex(key), depth+1, material)
		}
	default:
	}
}

// walkFields descends the exported fields of a struct value.
func walkFields(t *testing.T, path string, value reflect.Value, depth int, material []string) {
	t.Helper()

	for i := range value.NumField() {
		field := value.Type().Field(i)
		if field.PkgPath != "" {
			continue
		}
		walkReachable(t, path+"."+field.Name, value.Field(i), depth+1, material)
	}
}

// TestSecretMaterialRedactsItself covers the second line of defence: a path that
// does reach the material through the Stringer or GoStringer interface — a direct
// print of the pointer inside this package, for one — must still see a marker.
func TestSecretMaterialRedactsItself(t *testing.T) {
	t.Parallel()

	material := secretMaterial(sentinelSecret)

	renderings := map[string]string{
		"%v":         fmt.Sprintf("%v", &material),
		verbGoSyntax: fmt.Sprintf(verbGoSyntax, &material),
		"String()":   material.String(),
		"GoString()": material.GoString(),
	}

	for name, rendering := range renderings {
		if strings.Contains(rendering, sentinelSecret) {
			t.Errorf("%s rendering of the material leaks it: %s", name, rendering)
		}
		if rendering != redactedMarker {
			t.Errorf("%s rendering = %q, want %q", name, rendering, redactedMarker)
		}
	}
}

// TestSecretRenderingStaysUseful is the counter-test: the fix may not be "print
// nothing at all". A set secret and an unset one must stay distinguishable in
// every rendering — that difference is what an operator reads effective
// configuration for — and Reveal must still return the material to a deliberate
// caller. TestSecretNeverRendersItsValue covers the set half.
func TestSecretRenderingStaysUseful(t *testing.T) {
	t.Parallel()

	if got := NewSecret(sentinelSecret).Reveal(); got != sentinelSecret {
		t.Errorf("Reveal() returned %d bytes, want %d", len(got), len(sentinelSecret))
	}

	for name, rendering := range secretRenderings(t, Secret{}) {
		if !strings.Contains(rendering, unsetMarker) {
			t.Errorf("%s rendering of an unset secret = %q, want the %q marker", name, rendering, unsetMarker)
		}
	}
}
