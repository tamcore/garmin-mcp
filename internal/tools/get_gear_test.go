package tools

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// testGearUUID is the same synthetic canonical UUID internal/garmin/api's own gear
// tests use. testRetiredGearUUID is a second synthetic UUID for the retired bike.
const (
	testGearUUID        = "1f3c9d2a-0000-4000-8000-abcdef012345"
	testRetiredGearUUID = "2f3c9d2a-0000-4000-8000-abcdef012346"
)

// gearListBody is a synthetic account gear inventory: one active shoe and one
// retired bike, matching gear_management.py's get_gear curation
// (gear_management.py:82-124).
const gearListBody = `[` +
	`{"uuid":"` + testGearUUID + `","displayName":"Synthetic Shoes","customMakeModel":"Synth Runner",` +
	`"gearTypeName":"Shoes","gearStatusName":"active","dateBegin":"2025-01-01T00:00:00.0",` +
	`"dateEnd":null,"maximumMeters":800000},` +
	`{"uuid":"` + testRetiredGearUUID + `","displayName":"Synthetic Bike",` +
	`"gearTypeName":"Bike","gearStatusName":"RETIRED","dateBegin":"2024-01-01T00:00:00.0",` +
	`"dateEnd":"2025-06-01T00:00:00.0"}` +
	`]`

// gearDefaultsBody names the shoe as the default for activity type 1 (upstream's
// ACTIVITY_TYPE_MAPPING labels 1 "Running"; this tool keeps the numeric key raw
// rather than porting that guessed table — see get_gear.go's own doc comment).
const gearDefaultsBody = `[{"uuid":"` + testGearUUID + `","activityTypePk":1}]`

const gearStatsBody = `{"totalActivities":12,"totalDistance":150000}`

func gearDefaultsPathFor(profileID string) string {
	return client.PathGearUserDefaultsPrefix + "/" + profileID + "/" + client.ActivityTypesSegment
}

// gearScript scripts the last-used, gear, defaults and shoe-stats reads. The retired
// bike's own stats read is scripted separately with a 404, matching
// api.Gear.Stats's documented tolerance for gear with no stats document yet.
func gearScript(t *testing.T, statsBehavior testkit.Behavior) testkit.Script {
	t.Helper()

	return testkit.NewScript().
		With(client.PathDeviceLastUsed, testkit.JSON(http.StatusOK, deviceLastUsedBody)).
		With(client.PathGearFilter, testkit.JSON(http.StatusOK, gearListBody)).
		With(gearDefaultsPathFor("135790"), testkit.JSON(http.StatusOK, gearDefaultsBody)).
		With(client.PathGearStatsPrefix+"/"+testGearUUID, statsBehavior).
		With(client.PathGearStatsPrefix+"/"+testRetiredGearUUID,
			testkit.JSON(http.StatusNotFound, `{"message":"not found"}`))
}

func TestGetGearDeclaresTheUpstreamContract(t *testing.T) {
	t.Parallel()

	contract := getGearContract()
	if got := contract.Spec.Name; got != ToolGetGear {
		t.Errorf("wire name = %q, want %q", got, ToolGetGear)
	}
	if got := contract.Spec.Category; got != categoryDevice {
		t.Errorf("log category = %q, want %q", got, categoryDevice)
	}
	if got := contract.Schema.Required(); len(got) != 0 {
		t.Errorf("required = %v, want none: include_stats is optional", got)
	}
	properties := contract.Schema.Properties()
	if len(properties) != 1 || properties[0].Name != argIncludeStats {
		t.Fatalf("declared properties = %+v, want exactly include_stats", properties)
	}
	if properties[0].Default != true {
		t.Errorf("include_stats default = %v, want true", properties[0].Default)
	}
}

func TestGetGearReturnsTheCuratedInventoryWithStats(t *testing.T) {
	t.Parallel()

	svc, fake := deviceToolsService(t, gearScript(t, testkit.JSON(http.StatusOK, gearStatsBody)))
	session := deviceToolsSession(t, deviceToolsServer(t, svc))

	result := callDeviceTool(t, session, ToolGetGear, nil)
	out := deviceToolStructured(t, result)

	assertGearCounts(t, out)
	assertActiveGearEntry(t, entry(t, list(t, out, "gear"), 0))
	assertGearDefaultsSummary(t, out)

	second := entry(t, list(t, out, "gear"), 1)
	if _, present := second["stats"]; present {
		t.Error("gear[1].stats is present, want it omitted: its stats read answered 404")
	}

	if got := len(fake.Requests()); got != 5 {
		t.Errorf("dispatched %d requests, want 5 (last-used, gear, defaults, and one stats read per item)", got)
	}
}

// assertGearCounts pins the account-wide gear, active and retired counts.
func assertGearCounts(t *testing.T, out map[string]any) {
	t.Helper()

	if got, _ := out["gear_count"].(float64); got != 2 {
		t.Fatalf("gear_count = %v, want 2", got)
	}
	if got, _ := out["active_count"].(float64); got != 1 {
		t.Errorf("active_count = %v, want 1", got)
	}
	if got, _ := out["retired_count"].(float64); got != 1 {
		t.Errorf("retired_count = %v, want 1", got)
	}
}

// assertActiveGearEntry pins the first (active) gear entry's curated fields,
// including its usage statistics and default activity-type association.
func assertActiveGearEntry(t *testing.T, first map[string]any) {
	t.Helper()

	if got, _ := first["status"].(string); got != valueActive {
		t.Errorf("gear[0].status = %q, want %q (active sorts first)", got, valueActive)
	}
	if got, _ := first["date_begin"].(string); got != "2025-01-01" {
		t.Errorf("gear[0].date_begin = %q, want the date-only prefix", got)
	}
	if got, _ := first["max_distance_km"].(float64); got != 800 {
		t.Errorf("gear[0].max_distance_km = %v, want 800", got)
	}
	types, ok := first["default_activity_types"].([]any)
	if !ok || len(types) != 1 || types[0].(float64) != 1 {
		t.Errorf("gear[0].default_activity_types = %#v, want [1]", first["default_activity_types"])
	}
	assertActiveGearStats(t, first)
}

func assertActiveGearStats(t *testing.T, first map[string]any) {
	t.Helper()

	stats, ok := first["stats"].(map[string]any)
	if !ok {
		t.Fatal("gear[0].stats is missing")
	}
	if got, _ := stats["total_activities"].(float64); got != 12 {
		t.Errorf("stats.total_activities = %v, want 12", got)
	}
	if got, _ := stats["total_distance_km"].(float64); got != 150 {
		t.Errorf("stats.total_distance_km = %v, want 150", got)
	}
}

// assertGearDefaultsSummary pins the account-wide activity-type-to-gear-name summary.
func assertGearDefaultsSummary(t *testing.T, out map[string]any) {
	t.Helper()

	defaults, ok := out["defaults"].(map[string]any)
	if !ok || defaults["1"] != "Synthetic Shoes" {
		t.Errorf("defaults = %#v, want {\"1\": \"Synthetic Shoes\"}", out["defaults"])
	}
}

func TestGetGearSkipsStatsWhenIncludeStatsIsFalse(t *testing.T) {
	t.Parallel()

	// Scripting no behavior for the stats path proves it is never dispatched: an
	// unscripted path answers 404 from the fake, which would fail the call if
	// Stats were still invoked.
	script := testkit.NewScript().
		With(client.PathDeviceLastUsed, testkit.JSON(http.StatusOK, deviceLastUsedBody)).
		With(client.PathGearFilter, testkit.JSON(http.StatusOK, gearListBody)).
		With(gearDefaultsPathFor("135790"), testkit.JSON(http.StatusOK, gearDefaultsBody))
	svc, fake := deviceToolsService(t, script)
	session := deviceToolsSession(t, deviceToolsServer(t, svc))

	result := callDeviceTool(t, session, ToolGetGear, map[string]any{argIncludeStats: false})
	out := deviceToolStructured(t, result)

	gear := list(t, out, "gear")
	first := entry(t, gear, 0)
	if _, present := first["stats"]; present {
		t.Error("gear[0].stats is present, want it omitted when include_stats is false")
	}
	if got := len(fake.Requests()); got != 3 {
		t.Errorf("dispatched %d requests, want 3 (last-used, gear, defaults; no stats)", got)
	}
}

func TestGetGearTreatsAStatsFailureAsAWholeCallFailure(t *testing.T) {
	t.Parallel()

	svc, _ := deviceToolsService(t, gearScript(t,
		testkit.JSON(http.StatusInternalServerError, `{"message":"synthetic"}`)))
	session := deviceToolsSession(t, deviceToolsServer(t, svc))

	result := callDeviceTool(t, session, ToolGetGear, nil)
	if !result.IsError {
		t.Fatal("a stats-fetch failure succeeded, want an error result: this tool " +
			"does not swallow per-item stats errors the way upstream's bare except does")
	}
}

func TestGetGearReportsNoGearAsEmpty(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(client.PathDeviceLastUsed, testkit.JSON(http.StatusOK, deviceLastUsedBody)).
		With(client.PathGearFilter, testkit.JSON(http.StatusOK, `[]`)).
		With(gearDefaultsPathFor("135790"), testkit.JSON(http.StatusOK, `[]`))
	svc, _ := deviceToolsService(t, script)
	session := deviceToolsSession(t, deviceToolsServer(t, svc))

	result := callDeviceTool(t, session, ToolGetGear, nil)
	out := deviceToolStructured(t, result)
	if got, _ := out["gear_count"].(float64); got != 0 {
		t.Errorf("gear_count = %v, want 0", got)
	}
}

func TestGetGearRefusesARegistryItDoesNotHave(t *testing.T) {
	t.Parallel()

	svc, _ := deviceToolsService(t, testkit.NewScript())
	err := registerGetGear(nil, svc)
	if !errors.Is(err, mcpserver.ErrMissingDependency) {
		t.Errorf("registering on no registry = %v, want ErrMissingDependency", err)
	}
}
