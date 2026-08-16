package api_test

import (
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// sentinelGearName is at least 5 characters, so it cannot collide with a log
// line's own timestamp digits (see leakLogOptions' doc comment).
const sentinelGearName = "SENTINEL-GEAR-DISPLAY-NAME"

// TestGearListAndDefaultsModelsAreNotLoggable proves that GearItem (with its new
// MaximumMeters field), GearDefault and GearStats report shape rather than
// content when handed to slog.
func TestGearListAndDefaultsModelsAreNotLoggable(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(client.PathGearFilter, testkit.JSON(http.StatusOK,
			`[{"uuid":"`+testGearUUID+`","displayName":"`+sentinelGearName+`","maximumMeters":800000}]`)).
		With(client.PathGearUserDefaultsPrefix+"/9001/"+client.ActivityTypesSegment,
			testkit.JSON(http.StatusOK, `[{"uuid":"`+testGearUUID+`","activityTypePk":1}]`)).
		With(client.PathGearStatsPrefix+"/"+testGearUUID,
			testkit.JSON(http.StatusOK, `{"totalActivities":12,"totalDistance":150000}`))
	h := newHarness(t, script, client.Limits{})
	gear := newGear(t, h)

	items, err := gear.List(t.Context(), h.session, client.NewNumber(9001))
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	defaults, err := gear.Defaults(t.Context(), h.session, client.NewNumber(9001))
	if err != nil {
		t.Fatalf("Defaults() = %v", err)
	}
	stats, err := gear.Stats(t.Context(), h.session, mustGearUUID(t))
	if err != nil {
		t.Fatalf("Stats() = %v", err)
	}
	if len(items) != 1 || len(defaults) != 1 {
		t.Fatalf("fixtures did not decode as expected: items=%+v defaults=%+v", items, defaults)
	}

	models := map[string]any{
		"GearItem":    items[0],
		"GearDefault": defaults[0],
		"GearStats":   stats,
	}
	for name, value := range models {
		var logged strings.Builder
		slog.New(slog.NewTextHandler(&logged, leakLogOptions())).Info("garmin read", "model", value)

		rendered := logged.String()
		if strings.Contains(rendered, sentinelGearName) {
			t.Errorf("logging %s leaks %q: %s", name, sentinelGearName, rendered)
		}
		if strings.Contains(rendered, testGearUUID) {
			t.Errorf("logging %s leaks the uuid %q: %s", name, testGearUUID, rendered)
		}
		if !strings.Contains(rendered, "model") {
			t.Errorf("logging %s produced no model group: %s", name, rendered)
		}
	}
}
