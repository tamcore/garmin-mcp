package tools

import (
	"encoding/json"
	"testing"
)

// FuzzSanitizeUntyped exercises the recursive walk that strips
// person/place-identifying keys out of an untyped Garmin document before it
// reaches a result. This is the code every passthrough tool depends on for its
// privacy guarantee, and its input is a Garmin JSON document, which drifts and
// is otherwise untrusted. It performs no I/O: the corpus is decoded JSON, held
// entirely in memory.
//
// The property is the privacy guarantee itself: walk the sanitized value and
// fail if any key the sanitizer claims to strip is still present at any depth,
// however deeply nested.
func FuzzSanitizeUntyped(f *testing.F) {
	f.Add(`{"lat":1.0,"lon":2.0,"note":"ok"}`)
	f.Add(`{"userProfilePK":123,"nested":{"ownerId":5,"deep":[1,2,3]}}`)
	f.Add(`[1,2,3]`)
	f.Add(`null`)
	f.Add(`"just a string"`)
	f.Add(`{"a":{"b":{"c":{"d":1}}}}`)
	f.Add(`[{"email":"a@b.com"},{"nested":{"username":"x"}}]`)
	f.Fuzz(func(t *testing.T, data string) {
		var value any
		if err := json.Unmarshal([]byte(data), &value); err != nil {
			t.Skip()
		}
		out := sanitizeUntyped(value)
		assertNoIdentifyingKeys(t, out.Value)
	})
}

// assertNoIdentifyingKeys walks a sanitized value and fails on the first key
// the sanitizer is supposed to have removed.
func assertNoIdentifyingKeys(t *testing.T, value any) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		for key, element := range typed {
			if isIdentifyingKey(key) {
				t.Fatalf("sanitizeUntyped left an identifying key %q in the result", key)
			}
			assertNoIdentifyingKeys(t, element)
		}
	case []any:
		for _, element := range typed {
			assertNoIdentifyingKeys(t, element)
		}
	}
}
