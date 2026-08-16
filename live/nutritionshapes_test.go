//go:build garminlive

package live

import "github.com/tamcore/garmin-mcp/internal/tools"

// The nutrition half of the shape table. Split from shapes_test.go only to stay
// inside the package's 400-line limit; the contract is the one stated there.

// nutritionShapes names, per nutrition tool, the result keys its answer always
// carries.
//
// get_nutrition_daily_settings declares only "date": every goal field is
// json:",omitempty" (internal/tools/nutritionsettings.go's NutritionSettingsResult),
// because the account decides which goals it has set, so requiring one here would
// pin this suite to that account's own configuration.
func nutritionShapes() map[string][]string {
	return map[string][]string{
		tools.ToolGetNutritionDailyFoodLog:  {argDate, keyEntries, keyCount, keyTruncated},
		tools.ToolGetNutritionDailyMeals:    {argDate, "meals", keyCount, keyTruncated},
		tools.ToolGetNutritionDailySettings: {argDate},
		tools.ToolSearchFoods:               {keyCount, "has_more", "results"},
		tools.ToolGetCustomFoods:            {"results", keyCount, "start", "limit"},
		tools.ToolGetCustomFoodServingUnits: {"units", keyCount},
	}
}
