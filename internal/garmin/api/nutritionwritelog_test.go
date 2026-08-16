package api_test

import (
	"errors"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// testLogIDValue is the 32-char hex log identifier shared by every nutrition
// test that needs one.
const testLogIDValue = "abcdef0123456789abcdef0123456789"

// testTrailMixName is the quick-add food name shared by this file's log_food
// fixtures.
const testTrailMixName = "Trail mix"

func mustLogID(t *testing.T) api.LogID {
	t.Helper()

	id, err := api.ParseLogID(testLogIDValue)
	if err != nil {
		t.Fatalf("ParseLogID() = %v", err)
	}
	return id
}

func mustMealTime(t *testing.T) api.MealTime {
	t.Helper()

	mealTime, err := api.ParseMealTime("12:30:00")
	if err != nil {
		t.Fatalf("ParseMealTime() = %v", err)
	}
	return mealTime
}

// TestLogCustomFoodSendsTheUpstreamPayload pins log_custom_food's PUT body and
// its fixed literal fields (nutrition.py:602-624).
func TestLogCustomFoodSendsTheUpstreamPayload(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathNutritionFoodLogs,
		testkit.JSON(http.StatusOK, `{}`))
	h := newHarness(t, script, client.Limits{})

	entry := api.LogCustomFoodEntry{
		MealDate: mustDate(t, testCalendarDate), MealTime: mustMealTime(t), MealID: 504,
		FoodID: mustFoodID(t), ServingID: mustServingID(t), ServingQty: 2,
		Source: api.SourceGarmin, LoggedAt: time.Date(2026, 1, 31, 8, 0, 0, 0, time.UTC),
	}
	if _, err := newNutrition(t, h).LogCustomFood(t.Context(), h.session, entry); err != nil {
		t.Fatalf("LogCustomFood() = %v", err)
	}

	requests := h.server.Requests()
	if requests[0].Method != http.MethodPut {
		t.Errorf("method = %q, want PUT", requests[0].Method)
	}
	body := decodeBody(t, requests[0].Body)
	if body["mealDate"] != testCalendarDate {
		t.Errorf("mealDate = %v, want %s", body["mealDate"], testCalendarDate)
	}
	items, ok := body["foodLogItems"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("foodLogItems = %v, want one entry", body["foodLogItems"])
	}
	item := items[0].(map[string]any)
	assertLogCustomFoodItemFields(t, item)
}

// assertLogCustomFoodItemFields checks every field of one decoded
// foodLogItems entry against the fixed literals and the entry's own values,
// factored out of TestLogCustomFoodSendsTheUpstreamPayload to keep that test
// under the complexity limit.
func assertLogCustomFoodItemFields(t *testing.T, item map[string]any) {
	t.Helper()

	want := map[string]any{
		"logSource":    "GCW",
		"logCategory":  "REGULAR_LOG",
		"action":       "ADD",
		"logTimestamp": "2026-01-31T08:00:00.000Z",
		"mealId":       float64(504),
		"servingQty":   float64(2),
		"foodId":       "1f3c9d2a00004000800000abcdef0123",
		"servingId":    "9001",
		"source":       "GARMIN",
		"regionCode":   client.RegionCodeUS,
		"languageCode": client.LanguageCodeEN,
		"mealTime":     "12:30:00",
	}
	for field, expected := range want {
		if item[field] != expected {
			t.Errorf("%s = %v, want %v", field, item[field], expected)
		}
	}
}

// TestLogCustomFoodRefusesInvalidInput covers the boundary validation.
func TestLogCustomFoodRefusesInvalidInput(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	n := newNutrition(t, h)
	base := api.LogCustomFoodEntry{
		MealDate: mustDate(t, testCalendarDate), MealTime: mustMealTime(t), MealID: 504,
		FoodID: mustFoodID(t), ServingID: mustServingID(t), ServingQty: 1,
		Source: api.SourceGarmin, LoggedAt: time.Date(2026, 1, 31, 8, 0, 0, 0, time.UTC),
	}

	withoutDate := base
	withoutDate.MealDate = client.Date{}
	withoutTime := base
	withoutTime.MealTime = api.MealTime{}
	withoutMealID := base
	withoutMealID.MealID = 0
	withoutFoodID := base
	withoutFoodID.FoodID = api.FoodID{}
	withoutServingID := base
	withoutServingID.ServingID = api.ServingID{}
	withoutServingQty := base
	withoutServingQty.ServingQty = 0
	badSource := base
	badSource.Source = "MYFITNESSPAL"
	withoutTimestamp := base
	withoutTimestamp.LoggedAt = time.Time{}
	nanQty := base
	nanQty.ServingQty = math.NaN()
	infQty := base
	infQty.ServingQty = math.Inf(1)
	hugeQty := base
	hugeQty.ServingQty = 1e300

	for name, entry := range map[string]api.LogCustomFoodEntry{
		"zero date":       withoutDate,
		"zero meal time":  withoutTime,
		"zero meal id":    withoutMealID,
		"zero food id":    withoutFoodID,
		"zero serving id": withoutServingID,
		"zero quantity":   withoutServingQty,
		"invalid source":  badSource,
		"zero timestamp":  withoutTimestamp,
		"NaN quantity":    nanQty,
		"+Inf quantity":   infQty,
		"huge quantity":   hugeQty,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := n.LogCustomFood(t.Context(), h.session, entry); !errors.Is(
				err, client.ErrValidation) {
				t.Errorf("LogCustomFood(%s) = %v, want ErrValidation", name, err)
			}
		})
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestLogFoodSendsStringEncodedMacros pins log_food's quick-add payload, whose
// macros are strings unlike log_custom_food's numeric servingQty
// (nutrition.py:685-703).
func TestLogFoodSendsStringEncodedMacros(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathNutritionFoodLogQuickAdd,
		testkit.JSON(http.StatusOK, `{}`))
	h := newHarness(t, script, client.Limits{})

	entry := api.LogFoodEntry{
		MealDate: mustDate(t, testCalendarDate), MealTime: mustMealTime(t), MealID: 504,
		Name: testTrailMixName, Calories: 210, Carbs: 20, Protein: 5, Fat: 12,
		LoggedAt: time.Date(2026, 1, 31, 8, 0, 0, 0, time.UTC),
	}
	if _, err := newNutrition(t, h).LogFood(t.Context(), h.session, entry); err != nil {
		t.Fatalf("LogFood() = %v", err)
	}

	requests := h.server.Requests()
	if requests[0].Method != http.MethodPut {
		t.Errorf("method = %q, want PUT", requests[0].Method)
	}
	body := decodeBody(t, requests[0].Body)
	items := body["quickAddItems"].([]any)
	item := items[0].(map[string]any)
	if item["logCategory"] != "QUICK_ADD" {
		t.Errorf("logCategory = %v, want QUICK_ADD", item["logCategory"])
	}
	if item["calories"] != "210" {
		t.Errorf("calories = %v, want the string \"210\"", item["calories"])
	}
	if item["carbs"] != "20" {
		t.Errorf("carbs = %v, want the string \"20\"", item["carbs"])
	}
	if item["protein"] != "5" {
		t.Errorf("protein = %v, want the string \"5\"", item["protein"])
	}
	if item["fat"] != "12" {
		t.Errorf("fat = %v, want the string \"12\"", item["fat"])
	}
	for _, field := range []string{"calories", "carbs", "protein", "fat"} {
		if _, isString := item[field].(string); !isString {
			t.Errorf("%s = %T, want a JSON string like every other quick-add macro", field, item[field])
		}
	}
	if item["name"] != testTrailMixName {
		t.Errorf("name = %v, want Trail mix", item["name"])
	}
	if _, present := item["logId"]; !present {
		t.Error("quickAddItems carries no logId key, want an explicit null")
	}
}

// TestLogFoodTrimsName proves Name is trimmed before it reaches the wire, the
// same way CustomFoodFacts' FoodName and BrandName already are: an untrimmed
// name must not reach Garmin verbatim, and a name that is only whitespace or
// control characters must be refused rather than silently accepted, because
// requireText's control-character check exempts tab and newline.
func TestLogFoodTrimsName(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathNutritionFoodLogQuickAdd,
		testkit.JSON(http.StatusOK, `{}`))
	h := newHarness(t, script, client.Limits{})

	entry := api.LogFoodEntry{
		MealDate: mustDate(t, testCalendarDate), MealTime: mustMealTime(t), MealID: 504,
		Name: "  " + testTrailMixName + "  ", Calories: 210,
		LoggedAt: time.Date(2026, 1, 31, 8, 0, 0, 0, time.UTC),
	}
	if _, err := newNutrition(t, h).LogFood(t.Context(), h.session, entry); err != nil {
		t.Fatalf("LogFood() = %v", err)
	}
	body := decodeBody(t, h.server.Requests()[0].Body)
	item := body["quickAddItems"].([]any)[0].(map[string]any)
	if item["name"] != testTrailMixName {
		t.Errorf("name = %v, want the trimmed \"Trail mix\"", item["name"])
	}

	whitespaceOnly := entry
	whitespaceOnly.Name = "\n\n"
	if _, err := newNutrition(t, h).LogFood(t.Context(), h.session, whitespaceOnly); !errors.Is(
		err, client.ErrValidation) {
		t.Errorf("LogFood() with an all-whitespace name = %v, want ErrValidation", err)
	}
}

// TestLogFoodRefusesInvalidInput covers the boundary validation.
func TestLogFoodRefusesInvalidInput(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	n := newNutrition(t, h)
	base := api.LogFoodEntry{
		MealDate: mustDate(t, testCalendarDate), MealTime: mustMealTime(t), MealID: 504,
		Name: "x", Calories: 1, LoggedAt: time.Date(2026, 1, 31, 8, 0, 0, 0, time.UTC),
	}

	empty := base
	empty.Name = ""
	negative := base
	negative.Carbs = -1
	withoutDate := base
	withoutDate.MealDate = client.Date{}
	withoutTime := base
	withoutTime.MealTime = api.MealTime{}
	withoutMealID := base
	withoutMealID.MealID = 0
	withoutTimestamp := base
	withoutTimestamp.LoggedAt = time.Time{}
	nanCalories := base
	nanCalories.Calories = math.NaN()
	posInfCalories := base
	posInfCalories.Calories = math.Inf(1)
	negInfCarbs := base
	negInfCarbs.Carbs = math.Inf(-1)
	hugeProtein := base
	hugeProtein.Protein = 1e300

	cases := map[string]api.LogFoodEntry{
		"empty food name": empty, "negative macro": negative, "zero date": withoutDate,
		"zero meal time": withoutTime, "zero meal id": withoutMealID, "zero timestamp": withoutTimestamp,
		"NaN calories": nanCalories, "+Inf calories": posInfCalories, "-Inf carbs": negInfCarbs,
		"huge protein": hugeProtein,
	}
	for name, entry := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := n.LogFood(t.Context(), h.session, entry); !errors.Is(err, client.ErrValidation) {
				t.Errorf("LogFood(%s) = %v, want ErrValidation", name, err)
			}
		})
	}
}

// TestDeleteFoodLogSendsTheLogIDsBody pins delete_food_log's DELETE body
// (nutrition.py:733-734).
func TestDeleteFoodLogSendsTheLogIDsBody(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(nutritionFoodLogPath(), testkit.JSON(http.StatusOK, `{}`))
	h := newHarness(t, script, client.Limits{})

	if _, err := newNutrition(t, h).DeleteFoodLog(t.Context(), h.session,
		mustDate(t, testCalendarDate), mustLogID(t)); err != nil {
		t.Fatalf("DeleteFoodLog() = %v", err)
	}

	requests := h.server.Requests()
	if requests[0].Method != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", requests[0].Method)
	}
	body := decodeBody(t, requests[0].Body)
	ids, ok := body["logIds"].([]any)
	if !ok || len(ids) != 1 || ids[0] != testLogIDValue {
		t.Errorf("logIds = %v, want one matching id", body["logIds"])
	}

	if _, err := newNutrition(t, h).DeleteFoodLog(t.Context(), h.session,
		mustDate(t, testCalendarDate), api.LogID{}); !errors.Is(err, client.ErrValidation) {
		t.Errorf("DeleteFoodLog() without a log id = %v, want ErrValidation", err)
	}
	if _, err := newNutrition(t, h).DeleteFoodLog(t.Context(), h.session,
		client.Date{}, mustLogID(t)); !errors.Is(err, client.ErrValidation) {
		t.Errorf("DeleteFoodLog() without a date = %v, want ErrValidation", err)
	}
}
