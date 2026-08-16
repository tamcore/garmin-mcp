//go:build garminlive

package live

import (
	"os"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/tools"
)

// settingsCalorieDelta is the arbitrary, bounded, fully reversible change this test
// makes to the account's real calorie goal. It names nothing about the account: it is
// added to whatever the account's own current goal already is, and the cleanup below
// restores the original exactly.
const settingsCalorieDelta = 111

// envNutritionSettingsAck is a fifth gate, narrower than and additional to the four
// AGENTS.md names: it covers only this one test.
const envNutritionSettingsAck = "GARMIN_LIVE_NUTRITION_SETTINGS_ACK"

// nutritionSettingsAckValue is the exact value envNutritionSettingsAck must carry,
// spelled out rather than truthy, like every other acknowledgement this suite reads.
const nutritionSettingsAckValue = "i-accept-live-nutrition-settings-override"

// TestLiveNutritionSettingsRoundTrip drives set_nutrition_daily_settings against the
// account's real, permanent daily nutrition-goal document — there is no owned copy of
// it to create and remove the way every other write test in this package has.
//
// Garmin's nutrition-goal document is normally set once and inherited across days
// (nutrition.py:108-109: "settings are typically set once and inherited across days,
// but Garmin accepts per-day overrides"), and neither this codebase nor upstream
// exposes any way to delete or reset a per-day override once one exists. Writing this
// test's original *value* back therefore does not prove the account's original
// *shape* is restored: the PUT this test issues can materialise a day-specific
// override where the account previously had none, and writing the same figure back
// leaves that override in place. That is a structural change this suite cannot undo,
// so the test does not run unless it is separately acknowledged.
//
// The current value is read first. If it cannot be read, the test skips: writing a
// value this suite cannot restore would leave the account changed with no test of
// this suite's able to put it back, which set_nutrition_daily_settings' whole
// read-modify-write contract exists to avoid. Otherwise the original is restored in a
// t.Cleanup that runs whatever the test does next, so a failing assertion still
// leaves the account's goal *value* exactly as this run found it — its *shape* is the
// residual risk the acknowledgement above accepts.
func TestLiveNutritionSettingsRoundTrip(t *testing.T) {
	w := liveWriteEnv(t)
	if os.Getenv(envNutritionSettingsAck) != nutritionSettingsAckValue {
		t.Skipf("not run — this write can materialise a permanent per-day nutrition-goal "+
			"override that nothing in this codebase or upstream can remove; set %s=%s to "+
			"accept that risk", envNutritionSettingsAck, nutritionSettingsAckValue)
	}
	date := time.Now().UTC().AddDate(0, 0, -1).Format(time.DateOnly)
	w.foods.allowSettingsDate(date)

	current := w.call(t, tools.ToolGetNutritionDailySettings, map[string]any{argDate: date})
	original, ok := current["calorie_goal"].(float64)
	if !ok {
		t.Skip("not run — the account has no current calorie goal to read and safely restore")
	}
	originalGoal := int64(original)

	t.Cleanup(func() {
		restored := w.call(t, tools.ToolSetNutritionDailySettings,
			map[string]any{argDate: date, "calorie_goal": originalGoal})
		if got, ok := restored["calorie_goal"].(float64); !ok || int64(got) != originalGoal {
			t.Errorf("%s could not restore the account's original calorie goal",
				tools.ToolSetNutritionDailySettings)
		}
	})

	changed := originalGoal + settingsCalorieDelta
	updated := w.call(t, tools.ToolSetNutritionDailySettings,
		map[string]any{argDate: date, "calorie_goal": changed})
	if got, ok := updated["calorie_goal"].(float64); !ok || int64(got) != changed {
		t.Errorf("%s did not read back the calorie goal it was given",
			tools.ToolSetNutritionDailySettings)
	}
	if status, _ := updated["status"].(string); status != "updated" {
		t.Errorf("%s reported a status other than updated", tools.ToolSetNutritionDailySettings)
	}

	reread := w.call(t, tools.ToolGetNutritionDailySettings, map[string]any{argDate: date})
	if got, ok := reread["calorie_goal"].(float64); !ok || int64(got) != changed {
		t.Errorf("%s did not persist the calorie goal that was written",
			tools.ToolSetNutritionDailySettings)
	}
}
