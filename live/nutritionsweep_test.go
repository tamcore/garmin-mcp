//go:build garminlive

package live

import (
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/tools"
)

// The nutrition half of the read-only sweep. It obeys the same contract as
// surface_test.go and is split off only to stay inside the package's 400-line limit.

// argQuery is the free-text search argument search_foods takes.
const argQuery = "query"

// sweepFoodQuery is the query search_foods is asked with. It names a common, widely
// catalogued food rather than anything about the account, so the search has a
// reasonable chance of a non-empty page — though an empty page is still a pass, per
// assertToolAnswers' own contract.
const sweepFoodQuery = "banana"

// nutritionCalls are the nutrition tools whose argument is one calendar day, a food
// search, or nothing at all.
func nutritionCalls(now time.Time) []sweepCall {
	day := map[string]any{argDate: sweepDay(now)}

	return []sweepCall{
		{tools.ToolGetNutritionDailyFoodLog, day},
		{tools.ToolGetNutritionDailyMeals, day},
		{tools.ToolGetNutritionDailySettings, day},
		{tools.ToolSearchFoods, map[string]any{argQuery: sweepFoodQuery}},
		{tools.ToolGetCustomFoods, map[string]any{}},
		{tools.ToolGetCustomFoodServingUnits, map[string]any{}},
	}
}

// TestNutritionToolsAnswerOverTheLiveAccount drives every nutrition read-only tool
// against the real account. An empty log, meal list or search page is a pass: the
// account may carry no reading for the day or no match for the query, and the
// assertions are that a request was dispatched, the answer carries this tool's shape
// and bounds, and nothing leaked.
func TestNutritionToolsAnswerOverTheLiveAccount(t *testing.T) {
	e := liveEnv(t)

	for _, call := range nutritionCalls(e.now) {
		t.Run(call.tool, func(t *testing.T) { e.assertToolAnswers(t, call) })
	}
}
