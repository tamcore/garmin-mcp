package tools

import (
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func TestGetNutritionSummaryBetweenDatesCuratesEachDay(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, testkit.NewScript().With(client.PathNutritionFoodLogRange,
		testkit.JSON(http.StatusOK, `{"dailyNutritionSummaries":[`+
			`{"mealDate":"2026-01-01","dailyNutritionContent":`+
			`{"calories":2100,"carbs":250,"protein":120,"fat":70,"fiber":30},`+
			`"mealDetails":[{"loggedFoods":[{"foodId":1},{"foodId":2}]},{"loggedFoods":[{}]}]},`+
			`{"mealDate":"2026-01-02","dailyNutritionContent":{},"mealDetails":[]}]}`)))

	got := h.call(t, ToolGetNutritionSummaryBetweenDates, map[string]any{
		argStartDate: scoresStartDate, argEndDate: "2026-01-02",
	})
	days, _ := got["days"].([]any)
	if len(days) != 2 || got["count"] != float64(2) || got["truncated"] != false {
		t.Fatalf("result = %v, want two untruncated days", got)
	}
	first, _ := days[0].(map[string]any)
	want := map[string]any{
		argDate: scoresStartDate, "calories": float64(2100), "carbs": float64(250),
		"protein": float64(120), "fat": float64(70), "item_count": float64(3),
	}
	for key, value := range want {
		if first[key] != value {
			t.Errorf("%s = %v, want %v", key, first[key], value)
		}
	}
	if _, present := first["fiber"]; present {
		t.Error("the day carries a field the tool does not curate")
	}
	empty, _ := days[1].(map[string]any)
	if empty["item_count"] != float64(0) {
		t.Errorf("item_count = %v, want 0 for a day that logged nothing", empty["item_count"])
	}
	if _, present := empty["calories"]; present {
		t.Error("a missing total must stay absent rather than read as zero")
	}
}

func TestGetNutritionSummaryBetweenDatesRefusesABadWindowBeforeCallingGarmin(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, testkit.NewScript())
	for name, args := range map[string]map[string]any{
		"reversed":    {argStartDate: "2026-01-02", argEndDate: scoresStartDate},
		"sixty-two":   {argStartDate: scoresStartDate, argEndDate: "2026-03-03"},
		"not a date":  {argStartDate: "soon", argEndDate: scoresStartDate},
		"missing end": {argStartDate: scoresStartDate},
	} {
		h.callError(t, ToolGetNutritionSummaryBetweenDates, args)
		if requests := h.fake.Requests(); len(requests) != 0 {
			t.Fatalf("%s: the fake received %d requests, want none", name, len(requests))
		}
	}

	ok := newToolHarness(t, testkit.NewScript().With(client.PathNutritionFoodLogRange,
		testkit.JSON(http.StatusOK, `{"dailyNutritionSummaries":[]}`)))
	ok.call(t, ToolGetNutritionSummaryBetweenDates, map[string]any{
		argStartDate: scoresStartDate, argEndDate: "2026-03-02",
	})
}
