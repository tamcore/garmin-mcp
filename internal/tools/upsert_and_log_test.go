package tools

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

const testFoodNameNewSnack = "New Snack"

// upsertScript scripts the three endpoints upsertAndLog reaches. The meals fixture
// is always mealsWithSnacksBody: every test here cares about the find-or-create
// path, not meal-window selection, which foodlogwrites_test.go already covers.
func upsertScript(customFoodBehaviors []testkit.Behavior, logBehaviors ...testkit.Behavior) testkit.Script {
	return testkit.NewScript().
		With(client.PathNutritionCustomFood, customFoodBehaviors...).
		With(client.PathNutritionMealsPrefix+"/"+nutritionTestDate, testkit.JSON(http.StatusOK, mealsWithSnacksBody)).
		With(client.PathNutritionFoodLogs, logBehaviors...)
}

func TestUpsertAndLogLogsAnExistingFoodWithoutCreating(t *testing.T) {
	t.Parallel()

	existing := `{"customFoods":[{"foodMetaData":{"foodId":"` + customFoodHexID + `",` +
		`"foodName":"Homemade Granola","source":"GARMIN"},"nutritionContents":[{"servingId":"` +
		customFoodServingHexID + `"}]}]}`
	h := newTrendHarness(t, upsertScript(
		[]testkit.Behavior{testkit.JSON(http.StatusOK, existing)},
		testkit.Behavior{Status: http.StatusOK}))

	out, err := h.svc.upsertAndLog(h.ctx, upsertAndLogInput{
		MealDate: nutritionTestDate, MealTime: nutritionTestTime,
		FoodName: "Homemade Granola", Calories: 450,
	})
	if err != nil {
		t.Fatalf("upsertAndLog() = %v", err)
	}
	if out.MealID != 5001 || !out.Logged {
		t.Errorf("result = %+v, want meal 5001 logged", out)
	}

	// Only one customFood request (the search) was needed; the create path was
	// never taken because the search already found a match.
	requests := h.fake.Requests()
	customFoodRequests := 0
	for _, r := range requests {
		if r.Path == client.PathNutritionCustomFood {
			customFoodRequests++
		}
	}
	if customFoodRequests != 1 {
		t.Errorf("customFood requests = %d, want 1 (search only, no create)", customFoodRequests)
	}
}

func TestUpsertAndLogCreatesWhenNoExistingFoodMatches(t *testing.T) {
	t.Parallel()

	noMatch := `{"customFoods":[]}`
	created := `{"foodMetaData":{"foodId":"` + customFoodHexID + `","foodName":"` + testFoodNameNewSnack + `",` +
		`"source":"GARMIN"},"nutritionContents":[{"servingId":"` + customFoodServingHexID + `"}]}`
	h := newTrendHarness(t, upsertScript(
		[]testkit.Behavior{testkit.JSON(http.StatusOK, noMatch), testkit.JSON(http.StatusOK, created)},
		testkit.Behavior{Status: http.StatusOK}))

	out, err := h.svc.upsertAndLog(h.ctx, upsertAndLogInput{
		MealDate: nutritionTestDate, MealTime: nutritionTestTime,
		FoodName: testFoodNameNewSnack, Calories: 200,
	})
	if err != nil {
		t.Fatalf("upsertAndLog() = %v", err)
	}
	if !out.Logged {
		t.Error("logged = false, want true")
	}
}

// TestUpsertAndLogPropagatesASearchFailureWithoutCreating pins the fix for the
// duplicate-create defect: a failed search must not fall through to
// CreateCustomFood, since that would add a second food under the same name for a
// transient failure. Before the fix, findCustomFoodByName discarded the search
// error and reported found=false, so this scenario created a food despite the
// search never actually completing.
func TestUpsertAndLogPropagatesASearchFailureWithoutCreating(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, upsertScript(
		[]testkit.Behavior{testkit.JSON(http.StatusInternalServerError, `{"error":"synthetic"}`)},
		testkit.Behavior{Status: http.StatusOK}))

	if _, err := h.svc.upsertAndLog(h.ctx, upsertAndLogInput{
		MealDate: nutritionTestDate, MealTime: nutritionTestTime,
		FoodName: testFoodNameNewSnack, Calories: 200,
	}); err == nil {
		t.Fatal("upsertAndLog() = nil, want the search failure to propagate")
	}

	// The request layer's own bounded retry may repeat the failed GET search, but no
	// PUT (create) may ever be dispatched: that is the duplicate this fix prevents.
	for _, r := range h.fake.Requests() {
		if r.Path == client.PathNutritionCustomFood && r.Method == http.MethodPut {
			t.Error("a create (PUT) request was dispatched despite the search failure")
		}
	}
}

func TestUpsertAndLogFallsBackToALookupAfterAnEmptyCreateResponse(t *testing.T) {
	t.Parallel()

	noMatch := `{"customFoods":[]}`
	afterCreate := `{"customFoods":[{"foodMetaData":{"foodId":"` + customFoodHexID + `",` +
		`"foodName":"` + testFoodNameNewSnack + `","source":"GARMIN"},"nutritionContents":[{"servingId":"` +
		customFoodServingHexID + `"}]}]}`
	h := newTrendHarness(t, upsertScript(
		[]testkit.Behavior{
			testkit.JSON(http.StatusOK, noMatch),
			{Status: http.StatusNoContent},
			testkit.JSON(http.StatusOK, afterCreate),
		},
		testkit.Behavior{Status: http.StatusOK}))

	out, err := h.svc.upsertAndLog(h.ctx, upsertAndLogInput{
		MealDate: nutritionTestDate, MealTime: nutritionTestTime,
		FoodName: testFoodNameNewSnack, Calories: 200,
	})
	if err != nil {
		t.Fatalf("upsertAndLog() = %v", err)
	}
	if !out.Logged {
		t.Error("logged = false, want true")
	}
}

func TestUpsertAndLogRefusesAnAnonymousRequest(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, upsertScript(nil))
	if _, err := h.svc.upsertAndLog(t.Context(), upsertAndLogInput{
		MealDate: nutritionTestDate, MealTime: nutritionTestTime, FoodName: "Snack", Calories: 100,
	}); !errors.Is(err, identity.ErrNoPrincipal) {
		t.Errorf("an anonymous request = %v, want ErrNoPrincipal", err)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestUpsertAndLogRefusesAnEmptyFoodName(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, upsertScript(nil))
	if _, err := h.svc.upsertAndLog(h.ctx, upsertAndLogInput{
		MealDate: nutritionTestDate, MealTime: nutritionTestTime, FoodName: "   ", Calories: 100,
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("a blank food name = %v, want ErrInvalidArgument", err)
	}
}

// TestUpsertAndLogResultNeverLogsAFoodName is the redaction rule.
func TestUpsertAndLogResultNeverLogsAFoodName(t *testing.T) {
	t.Parallel()

	existing := `{"customFoods":[{"foodMetaData":{"foodId":"` + customFoodHexID + `",` +
		`"foodName":"Homemade Granola","source":"GARMIN"},"nutritionContents":[{"servingId":"` +
		customFoodServingHexID + `"}]}]}`
	h := newTrendHarness(t, upsertScript(
		[]testkit.Behavior{testkit.JSON(http.StatusOK, existing)},
		testkit.Behavior{Status: http.StatusOK}))
	out, err := h.svc.upsertAndLog(h.ctx, upsertAndLogInput{
		MealDate: nutritionTestDate, MealTime: nutritionTestTime,
		FoodName: "Homemade Granola", Calories: 450,
	})
	if err != nil {
		t.Fatalf("upsertAndLog() = %v", err)
	}
	assertShapeOnly(t, "FoodLogWriteResult", out, "Homemade Granola")
}
