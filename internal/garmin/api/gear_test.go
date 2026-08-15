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
