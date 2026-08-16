package tools

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func dailyMealsScript(body string) testkit.Script {
	return testkit.NewScript().With(client.PathNutritionMealsPrefix+"/"+nutritionTestDate,
		testkit.JSON(http.StatusOK, body))
}

func TestDailyMealsDecodesEachMeal(t *testing.T) {
	t.Parallel()

	body := `{"meals":[{"mealId":5001,"mealName":"BREAKFAST","startTime":"06:00:00",` +
		`"endTime":"10:00:00"},{"mealId":5004,"mealName":"SNACKS"}]}`
	h := newTrendHarness(t, dailyMealsScript(body))

	out, err := h.svc.readNutritionDailyMeals(h.ctx, nutritionTestDate)
	if err != nil {
		t.Fatalf("readNutritionDailyMeals() = %v", err)
	}
	if out.Count != 2 || len(out.Meals) != 2 {
		t.Fatalf("meals = %+v, want 2", out.Meals)
	}
	if out.Meals[0].MealID == nil || *out.Meals[0].MealID != 5001 {
		t.Errorf("first meal id = %v, want 5001", out.Meals[0].MealID)
	}
	if out.Meals[0].StartTime == nil || *out.Meals[0].StartTime != "06:00:00" {
		t.Errorf("first meal start = %v, want 06:00:00", out.Meals[0].StartTime)
	}
	if out.Meals[1].MealName == nil || *out.Meals[1].MealName != "SNACKS" {
		t.Errorf("second meal name = %v, want SNACKS", out.Meals[1].MealName)
	}
	if out.Truncated {
		t.Error("two meals must not report truncation")
	}
}

// TestDailyMealsResultCarriesTheSanitizedDocument pins the fix: before it, only
// mealId, mealName and the window reached the caller, even though the manifest
// promises meal totals no pinned source names as a typed field.
func TestDailyMealsResultCarriesTheSanitizedDocument(t *testing.T) {
	t.Parallel()

	body := `{"meals":[{"mealId":5001,"mealName":"BREAKFAST","totalCalories":420,"userId":42}]}`
	h := newTrendHarness(t, dailyMealsScript(body))

	out, err := h.svc.readNutritionDailyMeals(h.ctx, nutritionTestDate)
	if err != nil {
		t.Fatalf("readNutritionDailyMeals() = %v", err)
	}
	if out.Document == nil {
		t.Fatal("document = nil, want the sanitized meals document")
	}
	encoded, err := json.Marshal(out.Document)
	if err != nil {
		t.Fatalf("marshaling document: %v", err)
	}
	if !strings.Contains(string(encoded), `"totalCalories":420`) {
		t.Errorf("document = %s, want it to carry the totalCalories field the manifest promises", encoded)
	}
	if strings.Contains(string(encoded), "userId") {
		t.Errorf("document = %s, want the identifying userId key dropped", encoded)
	}
	if out.DroppedFields == 0 {
		t.Error("dropped_fields = 0, want at least the removed userId key")
	}
}

func TestDailyMealsTruncatesAnOversizedDay(t *testing.T) {
	t.Parallel()

	oversized := DefaultMaxMeals + 3
	var body strings.Builder
	body.WriteString(`{"meals":[`)
	for i := range oversized {
		if i > 0 {
			body.WriteString(",")
		}
		body.WriteString(`{"mealId":` + strconv.Itoa(5000+i) + `,"mealName":"SNACKS"}`)
	}
	body.WriteString(`]}`)

	h := newTrendHarness(t, dailyMealsScript(body.String()))
	out, err := h.svc.readNutritionDailyMeals(h.ctx, nutritionTestDate)
	if err != nil {
		t.Fatalf("readNutritionDailyMeals() = %v", err)
	}
	if len(out.Meals) != DefaultMaxMeals {
		t.Errorf("meals = %d, want the bound %d", len(out.Meals), DefaultMaxMeals)
	}
	if !out.Truncated {
		t.Error("the cut list does not report itself truncated")
	}
}

func TestGetNutritionDailyMealsRefusesABadDateAndAnAnonymousRequest(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, dailyMealsScript(`{"meals":[]}`))
	if _, err := h.svc.readNutritionDailyMeals(h.ctx, "not-a-date"); !errors.Is(
		err, ErrInvalidArgument) {
		t.Errorf("a malformed date = %v, want ErrInvalidArgument", err)
	}
	if _, err := h.svc.readNutritionDailyMeals(t.Context(), nutritionTestDate); !errors.Is(
		err, identity.ErrNoPrincipal) {
		t.Errorf("an anonymous request = %v, want ErrNoPrincipal", err)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestDailyMealsResultNeverLogsAMealName is the redaction rule.
func TestDailyMealsResultNeverLogsAMealName(t *testing.T) {
	t.Parallel()

	body := `{"meals":[{"mealId":5001,"mealName":"BREAKFAST"}]}`
	h := newTrendHarness(t, dailyMealsScript(body))
	out, err := h.svc.readNutritionDailyMeals(h.ctx, nutritionTestDate)
	if err != nil {
		t.Fatalf("readNutritionDailyMeals() = %v", err)
	}
	assertShapeOnly(t, "DailyMealsResult", out, "BREAKFAST")
}
