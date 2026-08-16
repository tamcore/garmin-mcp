package api_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func nutritionFoodLogPath() string {
	return client.PathNutritionFoodLogPrefix + "/" + testCalendarDate
}

func nutritionMealsPath() string {
	return client.PathNutritionMealsPrefix + "/" + testCalendarDate
}

func nutritionSettingsPath() string {
	return client.PathNutritionSettingsPrefix + "/" + testCalendarDate
}

// TestFoodLogRetainsTheRawPayloadWithoutDecodingFields proves the
// deliberately-opaque model still bounds and retains the response.
func TestFoodLogRetainsTheRawPayloadWithoutDecodingFields(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(nutritionFoodLogPath(),
		testkit.JSON(http.StatusOK, `{"anything":"goes",   "here": 1}`))
	h := newHarness(t, script, client.Limits{})

	day, err := newNutrition(t, h).FoodLog(t.Context(), h.session, mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("FoodLog() = %v", err)
	}
	if day.Payload().Len() == 0 {
		t.Error("FoodLog() retained no raw payload")
	}

	if _, err := newNutrition(t, h).FoodLog(t.Context(), h.session, client.Date{}); !errors.Is(
		err, client.ErrValidation) {
		t.Errorf("FoodLog() without a date = %v, want ErrValidation", err)
	}
}

// TestMealsDecodesTheMealsWrapper pins the "meals" wrapper key and the field
// spellings log_custom_food, log_food and upsert_and_log all read
// (nutrition.py:588-597).
func TestMealsDecodesTheMealsWrapper(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(nutritionMealsPath(), testkit.JSON(http.StatusOK, `{"meals":[`+
		`{"mealId":501,"mealName":"BREAKFAST","startTime":"06:00:00","endTime":"10:59:59"},`+
		`{"mealId":504,"mealName":"SNACKS","startTime":"00:00:00","endTime":"23:59:59"}`+
		`]}`))
	h := newHarness(t, script, client.Limits{})

	meals, err := newNutrition(t, h).Meals(t.Context(), h.session, mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("Meals() = %v", err)
	}
	items := meals.Meals.Items()
	if len(items) != 2 {
		t.Fatalf("Meals() = %d meals, want 2", len(items))
	}
	if id, ok := items[0].MealID.Int64(); !ok || id != 501 {
		t.Errorf("first meal id = %v/%v, want 501", id, ok)
	}
	if name, ok := items[1].MealName.Value(); !ok || name != "SNACKS" {
		t.Errorf("second meal name = %v/%v, want SNACKS", name, ok)
	}
	if meals.Payload().Len() == 0 {
		t.Error("Meals() retained no raw payload")
	}
}

// TestSettingsDecodesTheFourKnownFields pins the field spellings
// set_nutrition_daily_settings reads back after a write (nutrition.py:135-138).
func TestSettingsDecodesTheFourKnownFields(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(nutritionSettingsPath(), testkit.JSON(http.StatusOK,
		`{"activeDailyCalories":2200,"activeDailyCarbohydrateGrams":250,`+
			`"activeDailyFatGrams":70,"activeDailyProteinGrams":140,"planId":"p-1"}`))
	h := newHarness(t, script, client.Limits{})

	settings, err := newNutrition(t, h).Settings(t.Context(), h.session, mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("Settings() = %v", err)
	}
	if calories, ok := settings.CalorieGoal.Int64(); !ok || calories != 2200 {
		t.Errorf("CalorieGoal = %v/%v, want 2200", calories, ok)
	}
	if carbs, ok := settings.CarbsGrams.Int64(); !ok || carbs != 250 {
		t.Errorf("CarbsGrams = %v/%v, want 250", carbs, ok)
	}
	if settings.Payload().Len() == 0 {
		t.Error("Settings() retained no raw payload")
	}
}

// TestSearchFoodsDecodesBothSourceNamespaces pins the results/moreDataAvailable
// wrapper and the foodMetaData/nutritionContents shape search_foods reads
// (nutrition.py:177-208).
func TestSearchFoodsDecodesBothSourceNamespaces(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathNutritionFoodSearch, testkit.JSON(http.StatusOK,
		`{"moreDataAvailable":true,"results":[{"foodMetaData":{"foodId":"4132350",`+
			`"foodName":"Cheerios","foodType":"BRANDED","source":"FATSECRET","regionCode":"US",`+
			`"languageCode":"en","brandName":"General Mills"},"nutritionContents":[`+
			`{"servingId":"9001","servingUnit":"G","numberOfUnits":28,"calories":110,`+
			`"carbs":22,"protein":3,"fat":2,"fiber":3,"sodium":140}]}]}`))
	h := newHarness(t, script, client.Limits{})

	page, err := client.NewPage(0, 20)
	if err != nil {
		t.Fatalf("NewPage() = %v", err)
	}
	result, err := newNutrition(t, h).SearchFoods(t.Context(), h.session, "Cheerios", page)
	if err != nil {
		t.Fatalf("SearchFoods() = %v", err)
	}
	if result.MoreDataAvailable == nil || !*result.MoreDataAvailable {
		t.Error("MoreDataAvailable = false/nil, want true")
	}
	items := result.Results.Items()
	if len(items) != 1 || items[0].Meta == nil {
		t.Fatalf("SearchFoods() = %+v, want one decoded item", items)
	}
	if name := items[0].Meta.FoodName; name == nil || *name != "Cheerios" {
		t.Errorf("foodName = %v, want Cheerios", name)
	}
	contents := items[0].Contents.Items()
	if len(contents) != 1 {
		t.Fatalf("nutritionContents = %d, want 1", len(contents))
	}
	if calories, ok := contents[0].Calories.Int64(); !ok || calories != 110 {
		t.Errorf("calories = %v/%v, want 110", calories, ok)
	}

	if got := h.server.Requests()[0].Query.Get(client.QuerySearchExpression); got != "Cheerios" {
		t.Errorf("searchExpression = %q, want Cheerios", got)
	}
	if result.Payload().Len() == 0 {
		t.Error("SearchFoods() retained no raw payload")
	}
}

// TestCustomFoodsIncludesContentAndBounds pins the "customFoods" wrapper and
// the always-true includeContent parameter (nutrition.py:239-244).
func TestCustomFoodsIncludesContentAndBounds(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathNutritionCustomFood, testkit.JSON(http.StatusOK,
		`{"customFoods":[{"foodMetaData":{"foodId":"`+
			`1f3c9d2a00004000800abcdef01234","foodName":"Protein Shake","foodType":"GENERIC",`+
			`"source":"GARMIN","regionCode":"US","languageCode":"en"},"nutritionContents":[`+
			`{"servingId":"1","servingUnit":"ML","numberOfUnits":250,"calories":180}]}]}`))
	h := newHarness(t, script, client.Limits{})

	page, err := client.NewPage(0, 20)
	if err != nil {
		t.Fatalf("NewPage() = %v", err)
	}
	result, err := newNutrition(t, h).CustomFoods(t.Context(), h.session, "Protein", page)
	if err != nil {
		t.Fatalf("CustomFoods() = %v", err)
	}
	if result.CustomFoods.Len() != 1 {
		t.Fatalf("CustomFoods() = %d items, want 1", result.CustomFoods.Len())
	}
	if result.Payload().Len() == 0 {
		t.Error("CustomFoods() retained no raw payload")
	}

	if got := h.server.Requests()[0].Query.Get(client.QueryIncludeContent); got != client.IncludeContentTrue {
		t.Errorf("includeContent = %q, want %q", got, client.IncludeContentTrue)
	}
}

// TestCustomFoodServingUnitsDecodesABareArray covers the best-effort shape:
// upstream never decodes this response (nutrition.py:259-264), so this package
// assumes only a bare array of unit-code strings.
func TestCustomFoodServingUnitsDecodesABareArray(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathNutritionCustomFoodServingUnits,
		testkit.JSON(http.StatusOK, `["G","ML","OZ"]`))
	h := newHarness(t, script, client.Limits{})

	units, err := newNutrition(t, h).CustomFoodServingUnits(t.Context(), h.session)
	if err != nil {
		t.Fatalf("CustomFoodServingUnits() = %v", err)
	}
	got := units.Units()
	if len(got) != 3 || got[0] != "G" || got[2] != "OZ" {
		t.Errorf("Units() = %v, want [G ML OZ]", got)
	}
	if units.Payload().Len() == 0 {
		t.Error("CustomFoodServingUnits() retained no raw payload")
	}
}

// TestCustomFoodServingUnitsDecodesAnObjectWrapper proves the read also
// tolerates a normal `{"servingUnits":[...]}` object wrapper: the doc comment
// on ServingUnits used to claim the shape was unevidenced and then modeled
// only a bare array, so this single-key object wrapper hard-failed the whole
// read with ErrMalformedPayload.
func TestCustomFoodServingUnitsDecodesAnObjectWrapper(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathNutritionCustomFoodServingUnits,
		testkit.JSON(http.StatusOK, `{"servingUnits":["G","ML","OZ"]}`))
	h := newHarness(t, script, client.Limits{})

	units, err := newNutrition(t, h).CustomFoodServingUnits(t.Context(), h.session)
	if err != nil {
		t.Fatalf("CustomFoodServingUnits() = %v", err)
	}
	got := units.Units()
	if len(got) != 3 || got[0] != "G" || got[2] != "OZ" {
		t.Errorf("Units() = %v, want [G ML OZ]", got)
	}
	if units.Payload().Len() == 0 {
		t.Error("CustomFoodServingUnits() retained no raw payload")
	}
}

// TestCustomFoodServingUnitsDecodesAnArrayOfObjects proves an array of
// {"code":"G","name":"Gram"}-shaped objects — as plausible a catalog shape as
// a bare array of strings, and not evidenced against either way
// (nutrition.py:259-264 never decodes this response) — is decoded rather than
// failing the whole read with ErrMalformedPayload.
func TestCustomFoodServingUnitsDecodesAnArrayOfObjects(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathNutritionCustomFoodServingUnits,
		testkit.JSON(http.StatusOK, `[{"code":"G","name":"Gram"},{"code":"ML","name":"Milliliter"}]`))
	h := newHarness(t, script, client.Limits{})

	units, err := newNutrition(t, h).CustomFoodServingUnits(t.Context(), h.session)
	if err != nil {
		t.Fatalf("CustomFoodServingUnits() = %v", err)
	}
	got := units.Units()
	if len(got) != 2 || got[0] != "G" || got[1] != "ML" {
		t.Errorf("Units() = %v, want [G ML]", got)
	}
	if units.Payload().Len() == 0 {
		t.Error("CustomFoodServingUnits() retained no raw payload")
	}
}

// TestCustomFoodServingUnitsDecodesAMultiKeyWrapper proves a wrapper object
// carrying more than one top-level key — for example
// {"servingUnits":[...],"unitSystem":"metric"} — is tolerated the same
// direction nutritionreadfoodlog.go adopts for an unrecognized food-log
// shape: recognise what can be recognised, never fail the whole read over an
// extra sibling key.
func TestCustomFoodServingUnitsDecodesAMultiKeyWrapper(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathNutritionCustomFoodServingUnits,
		testkit.JSON(http.StatusOK, `{"servingUnits":["G","ML"],"unitSystem":"metric"}`))
	h := newHarness(t, script, client.Limits{})

	units, err := newNutrition(t, h).CustomFoodServingUnits(t.Context(), h.session)
	if err != nil {
		t.Fatalf("CustomFoodServingUnits() = %v", err)
	}
	got := units.Units()
	if len(got) != 2 || got[0] != "G" || got[1] != "ML" {
		t.Errorf("Units() = %v, want [G ML]", got)
	}
	if units.Payload().Len() == 0 {
		t.Error("CustomFoodServingUnits() retained no raw payload")
	}
}

// TestCustomFoodServingUnitsToleratesAnUnrecognizedShape proves a shape this
// package cannot recognise at all decodes to no units rather than failing the
// read, matching FoodLogDay.Entries' tolerance.
func TestCustomFoodServingUnitsToleratesAnUnrecognizedShape(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathNutritionCustomFoodServingUnits,
		testkit.JSON(http.StatusOK, `{"somethingElse":"goes here","andAnother":1}`))
	h := newHarness(t, script, client.Limits{})

	units, err := newNutrition(t, h).CustomFoodServingUnits(t.Context(), h.session)
	if err != nil {
		t.Fatalf("CustomFoodServingUnits() = %v, want no error", err)
	}
	if got := units.Units(); len(got) != 0 {
		t.Errorf("Units() over an unrecognized shape = %v, want none", got)
	}
	if units.Payload().Len() == 0 {
		t.Error("CustomFoodServingUnits() retained no raw payload")
	}
}

// TestNutritionReadsRefuseOversizedPages proves the page bound is enforced
// before dispatch for both paginated reads.
func TestNutritionReadsRefuseOversizedPages(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{MaxPageSize: 10})
	n := newNutrition(t, h)

	page, err := client.NewPage(0, client.MaxPageSizeCap)
	if err != nil {
		t.Fatalf("NewPage() = %v", err)
	}
	if _, err := n.SearchFoods(t.Context(), h.session, "x", page); !errors.Is(err, client.ErrValidation) {
		t.Errorf("SearchFoods() over the configured bound = %v, want ErrValidation", err)
	}
	if _, err := n.CustomFoods(t.Context(), h.session, "x", page); !errors.Is(err, client.ErrValidation) {
		t.Errorf("CustomFoods() over the configured bound = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestSearchFoodsBoundsTheQueryLength proves an unbounded free-text query
// cannot be dispatched: neither the search text nor the custom-food filter
// text carried a length bound before.
func TestSearchFoodsBoundsTheQueryLength(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	n := newNutrition(t, h)
	page, err := client.NewPage(0, 20)
	if err != nil {
		t.Fatalf("NewPage() = %v", err)
	}
	oversized := strings.Repeat("x", api.MaxSearchQueryLen+1)

	if _, err := n.SearchFoods(t.Context(), h.session, oversized, page); !errors.Is(
		err, client.ErrValidation) {
		t.Errorf("SearchFoods() over the query bound = %v, want ErrValidation", err)
	}
	if _, err := n.CustomFoods(t.Context(), h.session, oversized, page); !errors.Is(
		err, client.ErrValidation) {
		t.Errorf("CustomFoods() over the query bound = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}
