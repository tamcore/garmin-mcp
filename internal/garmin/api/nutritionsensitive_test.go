package api_test

import (
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// nutritionLogNeedles are fixture values a nutrition log line must never
// contain: a food name, a brand, a meal name and several distinctive
// macro/calorie/identifier figures. "79412" replaces an earlier two-character
// needle that risked colliding with an incidental digit elsewhere in a
// rendered log line. A function, not a package-level var: AGENTS.md allows no
// package-level mutable state, and this slice is never mutated after
// construction, so a function returning a fresh literal each call is the
// equivalent of a const for a value that cannot itself be one.
func nutritionLogNeedles() []string {
	return []string{
		"Confidential Casserole", "Acme Snack Foods", "MIDNIGHT_SNACK",
		"84621", "9137", "271", "79412", "SERVING-88217",
		"LOGDAY-SECRET-55217", "UNIT-CODE-QQXYZ99",
		"LOGENTRY-SECRET-33119", "60321", "MEALDATE-SECRET-71184",
	}
}

// collectNutritionModels builds one fixture of every nutrition model that
// carries a food name, a brand or a nutrition figure. FoodLogDay and
// ServingUnits are opaque-payload types with no exported constructor, so
// their fixtures are built through an actual harness round trip carrying a
// needle in the raw response, rather than left as zero values that would
// trivially pass regardless of what LogValue does.
func collectNutritionModels(t *testing.T) map[string]any {
	t.Helper()

	foodName := "Confidential Casserole"
	brand := "Acme Snack Foods"
	meta := api.FoodMetaData{
		FoodID: mustFoodText(t, "84621"), FoodName: &foodName, BrandName: &brand,
	}
	content := mustNutritionContent(t)
	item := api.FoodItem{Meta: &meta, Contents: client.NewList(content)}
	meal := api.Meal{
		MealID:   client.NewNumber(9137),
		MealName: client.NewText("MIDNIGHT_SNACK"),
	}
	settings := api.NutritionSettings{
		CalorieGoal: client.NewNumber(2100), CarbsGrams: client.NewNumber(271),
		FatGrams: client.NewNumber(79412), ProteinGrams: client.NewNumber(140),
	}

	return map[string]any{
		"FoodMetaData":      meta,
		"NutritionContent":  content,
		"FoodItem":          item,
		"Meal":              meal,
		"NutritionSettings": settings,
		"FoodSearchResult":  api.FoodSearchResult{Results: client.NewList(item)},
		"CustomFoodPage":    api.CustomFoodPage{CustomFoods: client.NewList(item)},
		"DailyMeals":        api.DailyMeals{Meals: client.NewList(meal)},
		"FoodLogDay":        mustFoodLogDayWithNeedle(t),
		"ServingUnits":      mustServingUnitsWithNeedle(t),
		"FoodLogEntry":      mustFoodLogEntryWithNeedle(t),
	}
}

// mustFoodLogEntryWithNeedle decodes one FoodLogEntry carrying a needle in
// every field UnmarshalJSON can populate. FoodLogEntry is the one nutrition
// model carrying a decoded log identifier, and it was previously missing from
// collectNutritionModels entirely, so the leak sweep never actually exercised
// its LogValue.
func mustFoodLogEntryWithNeedle(t *testing.T) api.FoodLogEntry {
	t.Helper()

	var entry api.FoodLogEntry
	raw := `{"logId":"LOGENTRY-SECRET-33119","mealId":60321,"mealDate":"MEALDATE-SECRET-71184"}`
	if err := entry.UnmarshalJSON([]byte(raw)); err != nil {
		t.Fatalf("FoodLogEntry.UnmarshalJSON(%q) = %v", raw, err)
	}
	return entry
}

// mustFoodLogDayWithNeedle round-trips a real FoodLog() call whose response
// carries a needle, so the opaque retained payload is not a zero value.
func mustFoodLogDayWithNeedle(t *testing.T) api.FoodLogDay {
	t.Helper()

	script := testkit.NewScript().With(nutritionFoodLogPath(), testkit.JSON(http.StatusOK,
		`[{"logId":"`+testLogIDValue+`","note":"LOGDAY-SECRET-55217"}]`))
	h := newHarness(t, script, client.Limits{})

	day, err := newNutrition(t, h).FoodLog(t.Context(), h.session, mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("FoodLog() = %v", err)
	}
	return day
}

// mustServingUnitsWithNeedle round-trips a real CustomFoodServingUnits() call
// whose response carries a needle, so the retained payload is not a zero
// value.
func mustServingUnitsWithNeedle(t *testing.T) api.ServingUnits {
	t.Helper()

	script := testkit.NewScript().With(client.PathNutritionCustomFoodServingUnits,
		testkit.JSON(http.StatusOK, `["G","ML","UNIT-CODE-QQXYZ99"]`))
	h := newHarness(t, script, client.Limits{})

	units, err := newNutrition(t, h).CustomFoodServingUnits(t.Context(), h.session)
	if err != nil {
		t.Fatalf("CustomFoodServingUnits() = %v", err)
	}
	return units
}

// mustFoodText decodes value into a client.Text the way a JSON response would,
// since FoodMetaData.FoodID has no exported constructor of its own.
func mustFoodText(t *testing.T, value string) client.Text {
	t.Helper()

	var text client.Text
	if err := text.UnmarshalJSON([]byte(`"` + value + `"`)); err != nil {
		t.Fatalf("UnmarshalJSON(%q) = %v", value, err)
	}
	return text
}

// mustNutritionContent builds a fixture nutrition content carrying the
// distinctive macro figures nutritionLogNeedles checks for.
func mustNutritionContent(t *testing.T) api.NutritionContent {
	t.Helper()

	return api.NutritionContent{
		ServingID: mustFoodText(t, "SERVING-88217"),
		Calories:  client.NewNumber(543),
		Carbs:     client.NewNumber(271),
	}
}

// TestNutritionModelsAreNotLoggable proves every nutrition model reports shape
// only when handed to slog, never a food name, brand, meal name or figure.
func TestNutritionModelsAreNotLoggable(t *testing.T) {
	t.Parallel()

	for name, value := range collectNutritionModels(t) {
		var logged strings.Builder
		slog.New(slog.NewTextHandler(&logged, leakLogOptions())).Info("garmin read", "model", value)

		rendered := logged.String()
		for _, needle := range nutritionLogNeedles() {
			if strings.Contains(rendered, needle) {
				t.Errorf("logging %s leaks %q: %s", name, needle, rendered)
			}
		}
		if !strings.Contains(rendered, "model") {
			t.Errorf("logging %s produced no model group: %s", name, rendered)
		}
	}
}
