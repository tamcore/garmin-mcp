package tools

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

const logFoodHexID = "3f3c9d2a00004000800000abcdef0789"
const testFoodNameOatmeal = "Oatmeal"

// mealsWithSnacksBody is one BREAKFAST window and a SNACKS fallback meal, used
// across the log tests to exercise both the window match and the fallback.
const mealsWithSnacksBody = `{"meals":[{"mealId":5001,"mealName":"BREAKFAST",` +
	`"startTime":"06:00:00","endTime":"10:00:00"},{"mealId":5004,"mealName":"SNACKS"}]}`

func logCustomFoodScript(mealsBody string, logBehaviors ...testkit.Behavior) testkit.Script {
	return testkit.NewScript().
		With(client.PathNutritionMealsPrefix+"/"+nutritionTestDate, testkit.JSON(http.StatusOK, mealsBody)).
		With(client.PathNutritionFoodLogs, logBehaviors...)
}

func logFoodScript(mealsBody string, logBehaviors ...testkit.Behavior) testkit.Script {
	return testkit.NewScript().
		With(client.PathNutritionMealsPrefix+"/"+nutritionTestDate, testkit.JSON(http.StatusOK, mealsBody)).
		With(client.PathNutritionFoodLogQuickAdd, logBehaviors...)
}

func TestLogCustomFoodMatchesTheMealWindow(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, logCustomFoodScript(mealsWithSnacksBody, testkit.Behavior{Status: http.StatusOK}))
	out, err := h.svc.logCustomFood(h.ctx, logCustomFoodInput{
		MealDate: nutritionTestDate, MealTime: nutritionTestTime,
		FoodID: customFoodHexID, ServingID: customFoodServingHexID,
	})
	if err != nil {
		t.Fatalf("logCustomFood() = %v", err)
	}
	if out.MealID != 5001 {
		t.Errorf("meal_id = %d, want 5001 (the matched window)", out.MealID)
	}
	if !out.Logged {
		t.Error("logged = false, want true")
	}
}

func TestLogCustomFoodFallsBackToSnacks(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, logCustomFoodScript(mealsWithSnacksBody, testkit.Behavior{Status: http.StatusOK}))
	out, err := h.svc.logCustomFood(h.ctx, logCustomFoodInput{
		MealDate: nutritionTestDate, MealTime: "23:00:00",
		FoodID: customFoodHexID, ServingID: customFoodServingHexID,
	})
	if err != nil {
		t.Fatalf("logCustomFood() = %v", err)
	}
	if out.MealID != 5004 {
		t.Errorf("meal_id = %d, want 5004 (the SNACKS fallback)", out.MealID)
	}
}

func TestLogCustomFoodRefusesWhenNoMealMatchesAndNoSnacksExists(t *testing.T) {
	t.Parallel()

	noSnacks := `{"meals":[{"mealId":5001,"mealName":"BREAKFAST","startTime":"06:00:00","endTime":"10:00:00"}]}`
	h := newTrendHarness(t, logCustomFoodScript(noSnacks, testkit.Behavior{Status: http.StatusOK}))
	if _, err := h.svc.logCustomFood(h.ctx, logCustomFoodInput{
		MealDate: nutritionTestDate, MealTime: "23:00:00",
		FoodID: customFoodHexID, ServingID: customFoodServingHexID,
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("no matching meal = %v, want ErrInvalidArgument", err)
	}
}

func TestLogCustomFoodRefusesAnInvalidSource(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, logCustomFoodScript(mealsWithSnacksBody, testkit.Behavior{Status: http.StatusOK}))
	bad := "MYFITNESSPAL"
	if _, err := h.svc.logCustomFood(h.ctx, logCustomFoodInput{
		MealDate: nutritionTestDate, MealTime: nutritionTestTime,
		FoodID: customFoodHexID, ServingID: customFoodServingHexID, Source: &bad,
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("an invalid source = %v, want ErrInvalidArgument", err)
	}
}

func TestLogFoodMatchesTheMealWindowAndLogs(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, logFoodScript(mealsWithSnacksBody, testkit.Behavior{Status: http.StatusOK}))
	out, err := h.svc.logFood(h.ctx, logFoodInput{
		MealDate: nutritionTestDate, MealTime: nutritionTestTime, Name: testFoodNameOatmeal,
		Calories: 300, Carbs: 50, Protein: 10, Fat: 5,
	})
	if err != nil {
		t.Fatalf("logFood() = %v", err)
	}
	if out.MealID != 5001 || !out.Logged {
		t.Errorf("result = %+v, want meal 5001 logged", out)
	}
}

func TestLogFoodRefusesAnAnonymousRequest(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, logFoodScript(mealsWithSnacksBody, testkit.Behavior{Status: http.StatusOK}))
	if _, err := h.svc.logFood(t.Context(), logFoodInput{
		MealDate: nutritionTestDate, MealTime: nutritionTestTime, Name: testFoodNameOatmeal,
		Calories: 300, Carbs: 50, Protein: 10, Fat: 5,
	}); !errors.Is(err, identity.ErrNoPrincipal) {
		t.Errorf("an anonymous request = %v, want ErrNoPrincipal", err)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func deleteFoodLogScript(behaviors ...testkit.Behavior) testkit.Script {
	return testkit.NewScript().With(client.PathNutritionFoodLogPrefix+"/"+nutritionTestDate, behaviors...)
}

func TestDeleteFoodLogReportsTheRemoval(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, deleteFoodLogScript(testkit.Behavior{Status: http.StatusNoContent}))
	out, err := h.svc.deleteFoodLog(h.ctx, deleteFoodLogInput{
		LogID: logFoodHexID, MealDate: nutritionTestDate,
	})
	if err != nil {
		t.Fatalf("deleteFoodLog() = %v", err)
	}
	if out.LogID != logFoodHexID {
		t.Errorf("log_id = %q, want %q", out.LogID, logFoodHexID)
	}
	if out.Status != http.StatusNoContent {
		t.Errorf("status = %d, want 204", out.Status)
	}
}

func TestDeleteFoodLogRefusesAnInvalidLogID(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, deleteFoodLogScript(testkit.Behavior{Status: http.StatusNoContent}))
	if _, err := h.svc.deleteFoodLog(h.ctx, deleteFoodLogInput{
		LogID: "not valid", MealDate: nutritionTestDate,
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("an invalid log id = %v, want ErrInvalidArgument", err)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestFoodLogWriteResultNeverLogsAFigure is the redaction rule.
func TestFoodLogWriteResultNeverLogsAFigure(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, logFoodScript(mealsWithSnacksBody, testkit.Behavior{Status: http.StatusOK}))
	out, err := h.svc.logFood(h.ctx, logFoodInput{
		MealDate: nutritionTestDate, MealTime: nutritionTestTime, Name: testFoodNameOatmeal,
		Calories: 300, Carbs: 50, Protein: 10, Fat: 5,
	})
	if err != nil {
		t.Fatalf("logFood() = %v", err)
	}
	assertShapeOnly(t, "FoodLogWriteResult", out, "Oatmeal", "300")
}

// TestLogDeletionResultNeverLogsALogID is the redaction rule.
func TestLogDeletionResultNeverLogsALogID(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, deleteFoodLogScript(testkit.Behavior{Status: http.StatusNoContent}))
	out, err := h.svc.deleteFoodLog(h.ctx, deleteFoodLogInput{
		LogID: logFoodHexID, MealDate: nutritionTestDate,
	})
	if err != nil {
		t.Fatalf("deleteFoodLog() = %v", err)
	}
	assertShapeOnly(t, "LogDeletionResult", out, logFoodHexID)
}
