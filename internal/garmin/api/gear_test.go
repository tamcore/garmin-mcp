package api_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

const testGearUUID = "1f3c9d2a-0000-4000-8000-abcdef012345"

func newGear(t *testing.T, h harness) *api.Gear {
	t.Helper()

	gear, err := api.NewGear(h.rc)
	if err != nil {
		t.Fatalf("NewGear() = %v", err)
	}
	return gear
}

func mustGearUUID(t *testing.T) api.GearUUID {
	t.Helper()

	gear, err := api.ParseGearUUID(testGearUUID)
	if err != nil {
		t.Fatalf("ParseGearUUID() = %v", err)
	}
	return gear
}

// TestGearUUIDRefusesAnythingThatIsNotOne is the path-safety test: only a parsed
// identifier reaches a URL path, so no separator or traversal segment can.
func TestGearUUIDRefusesAnythingThatIsNotOne(t *testing.T) {
	t.Parallel()

	rejected := []string{
		"", "not-a-uuid", "../../etc/passwd",
		"1f3c9d2a-0000-4000-8000-abcdef01234",
		"1f3c9d2a-0000-4000-8000-abcdefg12345",
		"1f3c9d2a/0000/4000/8000/abcdef012345",
	}
	for _, value := range rejected {
		if _, err := api.ParseGearUUID(value); !errors.Is(err, client.ErrValidation) {
			t.Errorf("ParseGearUUID(%q) = %v, want ErrValidation", value, err)
		}
	}
	if got := mustGearUUID(t).String(); got != testGearUUID {
		t.Errorf("String() = %q, want the validated identifier", got)
	}
}

// TestGearLinkAndUnlinkTargetTheirPaths pins both gear writes.
func TestGearLinkAndUnlinkTargetTheirPaths(t *testing.T) {
	t.Parallel()

	linkPath := client.PathGearPrefix + "/link/" + testGearUUID + "/activity/18446744"
	unlinkPath := client.PathGearPrefix + "/unlink/" + testGearUUID + "/activity/18446744"
	script := testkit.NewScript().
		With(linkPath, testkit.JSON(http.StatusOK, `{"uuid":"`+testGearUUID+`"}`)).
		With(unlinkPath, testkit.JSON(http.StatusOK, `{"uuid":"`+testGearUUID+`"}`))
	h := newHarness(t, script, client.Limits{})
	gear := newGear(t, h)

	if _, err := gear.Add(t.Context(), h.session, mustGearUUID(t), mustID(t)); err != nil {
		t.Fatalf("Add() = %v", err)
	}
	if _, err := gear.Remove(t.Context(), h.session, mustGearUUID(t), mustID(t)); err != nil {
		t.Fatalf("Remove() = %v", err)
	}

	requests := h.server.Requests()
	if len(requests) != 2 {
		t.Fatalf("the fake received %d requests, want 2", len(requests))
	}
	for index, want := range []string{linkPath, unlinkPath} {
		if requests[index].Path != want {
			t.Errorf("path = %q, want %q", requests[index].Path, want)
		}
		if requests[index].Method != http.MethodPut {
			t.Errorf("method = %q, want PUT", requests[index].Method)
		}
	}
}

// TestGearForActivityFiltersByActivityAndTolerates covers the read: the activity
// is a query parameter and an unknown field is ignored.
func TestGearForActivityFiltersByActivityAndTolerates(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathGearFilter, testkit.JSON(http.StatusOK,
		`[{"uuid":"`+testGearUUID+`","displayName":"Trail shoes","gearTypeName":"Shoes",`+
			`"surpriseField":{"nested":true}}]`))
	h := newHarness(t, script, client.Limits{})

	items, err := newGear(t, h).ForActivity(t.Context(), h.session, mustID(t))
	if err != nil {
		t.Fatalf("ForActivity() = %v", err)
	}
	if len(items) != 1 || items[0].DisplayName == nil || *items[0].DisplayName != "Trail shoes" {
		t.Fatalf("ForActivity() = %+v, want the one decoded item", items)
	}
	if got := h.server.Requests()[0].Query.Get(client.QueryActivityID); got != "18446744" {
		t.Errorf("activityId = %q, want the activity identifier", got)
	}
}

// TestGearWritesRefuseAnUnsetIdentifier keeps validation ahead of dispatch.
func TestGearWritesRefuseAnUnsetIdentifier(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	gear := newGear(t, h)

	if _, err := gear.Add(t.Context(), h.session, api.GearUUID{}, mustID(t)); !errors.Is(
		err, client.ErrValidation) {
		t.Errorf("Add() without a gear uuid = %v, want ErrValidation", err)
	}
	if _, err := gear.Remove(t.Context(), h.session, mustGearUUID(t), client.ID{}); !errors.Is(
		err, client.ErrValidation) {
		t.Errorf("Remove() without an activity = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestGearListDecodesTheProfileFilteredArray covers get_gear: the profile id is a
// query parameter, not a caller-supplied selector reaching a URL path.
func TestGearListDecodesTheProfileFilteredArray(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathGearFilter, testkit.JSON(http.StatusOK,
		`[{"uuid":"`+testGearUUID+`","displayName":"Trail shoes","gearTypeName":"Shoes",`+
			`"maximumMeters":800000,"surpriseField":{"nested":true}}]`))
	h := newHarness(t, script, client.Limits{})

	items, err := newGear(t, h).List(t.Context(), h.session, client.NewNumber(9001))
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	if len(items) != 1 || items[0].DisplayName == nil || *items[0].DisplayName != "Trail shoes" {
		t.Fatalf("List() = %+v, want the one decoded item", items)
	}
	if meters, ok := items[0].MaximumMeters.Int64(); !ok || meters != 800000 {
		t.Errorf("MaximumMeters = %v/%v, want 800000", meters, ok)
	}
	if got := h.server.Requests()[0].Query.Get(client.QueryUserProfilePK); got != "9001" {
		t.Errorf("userProfilePk = %q, want 9001", got)
	}
}

// TestGearListRefusesAnUnsetProfileID keeps validation ahead of dispatch.
func TestGearListRefusesAnUnsetProfileID(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	if _, err := newGear(t, h).List(t.Context(), h.session, client.Number{}); !errors.Is(
		err, client.ErrValidation) {
		t.Errorf("List() without a profile id = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

const gearDefaultsPath = client.PathGearUserDefaultsPrefix + "/9001/" + client.ActivityTypesSegment

// TestGearDefaultsDecodesTheArray covers get_gear_defaults.
func TestGearDefaultsDecodesTheArray(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(gearDefaultsPath, testkit.JSON(http.StatusOK,
		`[{"uuid":"`+testGearUUID+`","activityTypePk":1}]`))
	h := newHarness(t, script, client.Limits{})

	defaults, err := newGear(t, h).Defaults(t.Context(), h.session, client.NewNumber(9001))
	if err != nil {
		t.Fatalf("Defaults() = %v", err)
	}
	if len(defaults) != 1 || defaults[0].UUID == nil || *defaults[0].UUID != testGearUUID {
		t.Fatalf("Defaults() = %+v, want the one decoded item", defaults)
	}
	if pk, ok := defaults[0].ActivityTypePk.Int64(); !ok || pk != 1 {
		t.Errorf("ActivityTypePk = %v/%v, want 1", pk, ok)
	}
}

// TestGearStatsDecodesTheDocument covers get_gear_stats.
func TestGearStatsDecodesTheDocument(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathGearStatsPrefix+"/"+testGearUUID,
		testkit.JSON(http.StatusOK, `{"totalActivities":12,"totalDistance":150000}`))
	h := newHarness(t, script, client.Limits{})

	stats, err := newGear(t, h).Stats(t.Context(), h.session, mustGearUUID(t))
	if err != nil {
		t.Fatalf("Stats() = %v", err)
	}
	if activities, ok := stats.TotalActivities.Int64(); !ok || activities != 12 {
		t.Errorf("TotalActivities = %v/%v, want 12", activities, ok)
	}
	if distance, ok := stats.TotalDistance.Int64(); !ok || distance != 150000 {
		t.Errorf("TotalDistance = %v/%v, want 150000", distance, ok)
	}
}

// TestGearStatsTolerates404 matches get_gear_stats's own try/except: retired
// gear commonly has no stats document.
func TestGearStatsTolerates404(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathGearStatsPrefix+"/"+testGearUUID,
		testkit.JSON(http.StatusNotFound, `{"message":"not found"}`))
	h := newHarness(t, script, client.Limits{})

	stats, err := newGear(t, h).Stats(t.Context(), h.session, mustGearUUID(t))
	if err != nil {
		t.Fatalf("Stats() = %v, want no error for a 404", err)
	}
	if stats.TotalActivities.IsSet() {
		t.Errorf("Stats() = %+v, want the zero value for a 404", stats)
	}
}

// TestGearStatsRefusesAnUnsetUUID keeps validation ahead of dispatch.
func TestGearStatsRefusesAnUnsetUUID(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	if _, err := newGear(t, h).Stats(t.Context(), h.session, api.GearUUID{}); !errors.Is(
		err, client.ErrValidation) {
		t.Errorf("Stats() without a uuid = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}
