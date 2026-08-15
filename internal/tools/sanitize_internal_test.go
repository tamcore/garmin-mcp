package tools

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// The fixture is synthetic. Every value below is invented and none of it is a
// recording of a real account.
const sanitizeFixture = `{
  "userProfilePK": 900001,
  "calendarDate": "2026-01-31",
  "latency": 12,
  "relation": "self",
  "startLatitude": 1.5,
  "endLongitude": -2.5,
  "nested": {
    "ownerDisplayName": "fake-tester",
    "detail": {"userId": 4242, "value": 7},
    "samples": [{"lat": 1.0, "lon": 2.0, "level": 3}]
  }
}`

// The identifying key and value names the sanitiser regressions assert on, named once
// so a rename shows up in one place.
const (
	keyUserProfilePK  = "userProfilePK"
	keyStartLatitude  = "startLatitude"
	keyEndLongitude   = "endLongitude"
	keyCamelUserID    = "userId"
	keyOwnerDisplay   = "ownerDisplayName"
	fixtureProfilePK  = "900001"
	fixtureIdentifier = "4242"
)

// The identifiers the fixture carries, rendered as they would appear in the result if
// the sanitiser let them through.
var sanitizeForbidden = []string{
	keyUserProfilePK, fixtureProfilePK, keyStartLatitude, keyEndLongitude, "1.5", "-2.5",
	keyOwnerDisplay, cardioDisplayName, keyCamelUserID, fixtureIdentifier,
	`"lat"`, `"lon"`,
}

// TestSanitizeUntypedDropsIdentifiersAtEveryDepth is the class regression: an
// identifier at the top level, inside a nested object, inside a doubly nested object
// and inside an array element must all be gone, and the count must say how many.
func TestSanitizeUntypedDropsIdentifiersAtEveryDepth(t *testing.T) {
	t.Parallel()

	outcome := sanitizeUntyped(decodeFixture(t, sanitizeFixture))

	rendered := renderJSON(t, outcome.Value)
	for _, forbidden := range sanitizeForbidden {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("the sanitised document still carries %q: %s", forbidden, rendered)
		}
	}
	// userProfilePK, startLatitude, endLongitude, ownerDisplayName, userId, lat, lon.
	if outcome.Dropped != 7 {
		t.Errorf("Dropped = %d, want 7", outcome.Dropped)
	}
	if outcome.Truncated {
		t.Error("Truncated = true for a small document")
	}
}

// TestSanitizeUntypedKeepsEveryNonIdentifyingKey is the other half: a denylist that
// over-matches would silently delete the data the caller asked for.
func TestSanitizeUntypedKeepsEveryNonIdentifyingKey(t *testing.T) {
	t.Parallel()

	outcome := sanitizeUntyped(decodeFixture(t, sanitizeFixture))

	rendered := renderJSON(t, outcome.Value)
	for _, kept := range []string{
		"calendarDate", "2026-01-31", "latency", "relation", "nested", "detail",
		"value", "samples", "level",
	} {
		if !strings.Contains(rendered, kept) {
			t.Errorf("the sanitiser dropped %q, which identifies nobody: %s", kept, rendered)
		}
	}
}

// TestSanitizeUntypedBoundsDepth proves a deeply nested document costs a bounded
// number of frames and is reported as cut rather than returned as if whole.
func TestSanitizeUntypedBoundsDepth(t *testing.T) {
	t.Parallel()

	deep := strings.Repeat(`{"a":`, maxSanitizeDepth+50) + `1` +
		strings.Repeat(`}`, maxSanitizeDepth+50)

	outcome := sanitizeUntyped(decodeFixture(t, deep))

	if !outcome.Truncated {
		t.Error("Truncated = false for a document nested past the depth bound")
	}
}

// TestSanitizeUntypedBoundsNodeCount proves a wide document is cut at the node bound.
func TestSanitizeUntypedBoundsNodeCount(t *testing.T) {
	t.Parallel()

	elements := make([]string, 0, maxSanitizeNodes+10)
	for i := range maxSanitizeNodes + 10 {
		elements = append(elements, strconv.Itoa(i))
	}

	outcome := sanitizeUntyped(decodeFixture(t, "["+strings.Join(elements, ",")+"]"))

	if !outcome.Truncated {
		t.Error("Truncated = false for a document wider than the node bound")
	}
}

// TestIsIdentifyingKeyIsDeliberateAboutSubstrings pins the over-match decision: the
// three-letter coordinate names match whole keys only, and the long fragments match
// anywhere.
func TestIsIdentifyingKeyIsDeliberateAboutSubstrings(t *testing.T) {
	t.Parallel()

	for _, key := range []string{
		"lat", "LON", keyUserProfilePK, "ownerProfilePK", keyCamelUserID, "userProfileId",
		"ownerId", keyOwnerDisplay, "displayName", keyStartLatitude, keyEndLongitude,
		"positionLat", "positions",
	} {
		if !isIdentifyingKey(key) {
			t.Errorf("isIdentifyingKey(%q) = false, want it dropped", key)
		}
	}
	for _, key := range []string{
		"latency", "relation", "translate", "along", "belong", "calendarDate",
		"stressLevel", "durationInMilliseconds",
	} {
		if isIdentifyingKey(key) {
			t.Errorf("isIdentifyingKey(%q) = true, want it kept", key)
		}
	}
}

func decodeFixture(t *testing.T, document string) any {
	t.Helper()

	var decoded any
	if err := json.Unmarshal([]byte(document), &decoded); err != nil {
		t.Fatalf("decoding the fixture: %v", err)
	}
	return decoded
}

func renderJSON(t *testing.T, value any) string {
	t.Helper()

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshalling the sanitised document: %v", err)
	}
	return string(encoded)
}
