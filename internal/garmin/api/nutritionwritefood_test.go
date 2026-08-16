package api_test

import (
	"errors"
	"math"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// testCookiesFoodName is the food name shared by every custom-food fixture in
// this file.
const testCookiesFoodName = "Cookies"

func mustFoodID(t *testing.T) api.FoodID {
	t.Helper()

	id, err := api.ParseFoodID("1f3c9d2a00004000800000abcdef0123")
	if err != nil {
		t.Fatalf("ParseFoodID() = %v", err)
	}
	return id
}

func mustServingID(t *testing.T) api.ServingID {
	t.Helper()

	id, err := api.ParseServingID("9001")
	if err != nil {
		t.Fatalf("ParseServingID() = %v", err)
	}
	return id
}

// TestCreateCustomFoodSendsStringEncodedNutrients pins the wire shape
// create_custom_food builds: every numeric nutrient is a JSON string, dropping
// ".0" for a whole number (nutrition.py:14-19, :321-359).
func TestCreateCustomFoodSendsStringEncodedNutrients(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathNutritionCustomFood, testkit.JSON(http.StatusOK,
		`{"foodMetaData":{"foodId":"`+"1f3c9d2a00004000800000abcdef0123"+`","foodName":"`+
			testCookiesFoodName+`"},`+
			`"nutritionContents":[{"servingId":"9001"}]}`))
	h := newHarness(t, script, client.Limits{})

	carbs := 30.5
	item, err := newNutrition(t, h).CreateCustomFood(t.Context(), h.session, api.CustomFoodFacts{
		FoodName: "Chocolate Cookies", ServingUnit: "G", NumberOfUnits: 100, Calories: 160,
		Carbs: &carbs,
	})
	if err != nil {
		t.Fatalf("CreateCustomFood() = %v", err)
	}
	if item.Meta == nil || item.Meta.FoodName == nil || *item.Meta.FoodName != testCookiesFoodName {
		t.Errorf("CreateCustomFood() = %+v, want the decoded response echoed back", item)
	}

	requests := h.server.Requests()
	if requests[0].Method != http.MethodPut {
		t.Errorf("method = %q, want PUT", requests[0].Method)
	}
	meta := nestedObject(t, requests[0].Body, "foodMetaData")
	if meta["foodType"] != client.FoodTypeGeneric || meta["source"] != client.FoodSourceGarmin {
		t.Errorf("foodMetaData = %v, want GENERIC/GARMIN", meta)
	}
	if _, present := meta["foodId"]; present {
		t.Error("create sent a foodId, want none for a new food")
	}

	body := decodeBody(t, requests[0].Body)
	contents, ok := body["nutritionContents"].([]any)
	if !ok || len(contents) != 1 {
		t.Fatalf("nutritionContents = %v, want one entry", body["nutritionContents"])
	}
	content := contents[0].(map[string]any)
	if content["calories"] != "160" {
		t.Errorf("calories = %v, want the string \"160\" (whole number, no .0)", content["calories"])
	}
	if content["carbs"] != "30.5" {
		t.Errorf("carbs = %v, want the string \"30.5\"", content["carbs"])
	}
	if _, present := content["protein"]; present {
		t.Error("carbs was set but protein was not; protein must be omitted, not zeroed")
	}
}

// TestUpdateCustomFoodCarriesTheFoodAndServingID pins update's payload, which
// differs from create only by including the existing identifiers.
func TestUpdateCustomFoodCarriesTheFoodAndServingID(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathNutritionCustomFood,
		testkit.JSON(http.StatusOK, `{"foodMetaData":{"foodName":"`+testCookiesFoodName+`"}}`))
	h := newHarness(t, script, client.Limits{})

	_, err := newNutrition(t, h).UpdateCustomFood(t.Context(), h.session, mustFoodID(t), mustServingID(t),
		api.CustomFoodFacts{FoodName: testCookiesFoodName, ServingUnit: "G", NumberOfUnits: 100, Calories: 160})
	if err != nil {
		t.Fatalf("UpdateCustomFood() = %v", err)
	}

	requests := h.server.Requests()
	meta := nestedObject(t, requests[0].Body, "foodMetaData")
	if meta["foodId"] != "1f3c9d2a00004000800000abcdef0123" {
		t.Errorf("foodMetaData.foodId = %v, want the update's food id", meta["foodId"])
	}
	body := decodeBody(t, requests[0].Body)
	contents := body["nutritionContents"].([]any)[0].(map[string]any)
	if contents["servingId"] != "9001" {
		t.Errorf("nutritionContents[0].servingId = %v, want 9001", contents["servingId"])
	}
}

// TestUpdateCustomFoodRefusesUnsetIdentifiers keeps validation ahead of
// dispatch: an update without a target identifier could otherwise be
// misread as a create.
func TestUpdateCustomFoodRefusesUnsetIdentifiers(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	facts := api.CustomFoodFacts{FoodName: "x", ServingUnit: "G", NumberOfUnits: 1, Calories: 1}

	if _, err := newNutrition(t, h).UpdateCustomFood(t.Context(), h.session, api.FoodID{}, mustServingID(t),
		facts); !errors.Is(err, client.ErrValidation) {
		t.Errorf("UpdateCustomFood() without a food id = %v, want ErrValidation", err)
	}
	if _, err := newNutrition(t, h).UpdateCustomFood(t.Context(), h.session, mustFoodID(t), api.ServingID{},
		facts); !errors.Is(err, client.ErrValidation) {
		t.Errorf("UpdateCustomFood() without a serving id = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestCustomFoodFactsRefuseInvalidInput covers the boundary validation shared
// by create and update.
func TestCustomFoodFactsRefuseInvalidInput(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	n := newNutrition(t, h)
	negative := -5.0

	nan := math.NaN()
	posInf := math.Inf(1)
	negInf := math.Inf(-1)
	huge := 1e300

	calls := map[string]api.CustomFoodFacts{
		"empty food name":    {FoodName: "", ServingUnit: "G", NumberOfUnits: 1, Calories: 1},
		"empty serving unit": {FoodName: "x", ServingUnit: "", NumberOfUnits: 1, Calories: 1},
		"zero units":         {FoodName: "x", ServingUnit: "G", NumberOfUnits: 0, Calories: 1},
		"negative calories":  {FoodName: "x", ServingUnit: "G", NumberOfUnits: 1, Calories: -1},
		"negative optional":  {FoodName: "x", ServingUnit: "G", NumberOfUnits: 1, Calories: 1, Carbs: &negative},
		"NaN calories":       {FoodName: "x", ServingUnit: "G", NumberOfUnits: 1, Calories: nan},
		"+Inf calories":      {FoodName: "x", ServingUnit: "G", NumberOfUnits: 1, Calories: posInf},
		"-Inf units":         {FoodName: "x", ServingUnit: "G", NumberOfUnits: negInf, Calories: 1},
		"huge optional":      {FoodName: "x", ServingUnit: "G", NumberOfUnits: 1, Calories: 1, Carbs: &huge},
		"control char brand": {
			FoodName: "x", ServingUnit: "G", NumberOfUnits: 1, Calories: 1,
			BrandName: new("bad\x00brand"),
		},
	}
	for name, facts := range calls {
		t.Run(name, func(t *testing.T) {
			if _, err := n.CreateCustomFood(t.Context(), h.session, facts); !errors.Is(
				err, client.ErrValidation) {
				t.Errorf("CreateCustomFood(%s) = %v, want ErrValidation", name, err)
			}
		})
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestCreateCustomFoodEffectIsNeverRepeated proves create's effect is
// EffectUnsafeWrite, not the EffectIdempotentWrite create and update used to
// share: compat/tools.json classifies create_custom_food as non-idempotent
// ("repeats create duplicates"), so a lost response must never be replayed by
// the retry layer, unlike update.
func TestCreateCustomFoodEffectIsNeverRepeated(t *testing.T) {
	t.Parallel()

	h := newHarness(t, serverErrors(client.PathNutritionCustomFood), retryLimits())
	_, err := newNutrition(t, h).CreateCustomFood(t.Context(), h.session, api.CustomFoodFacts{
		FoodName: "x", ServingUnit: "G", NumberOfUnits: 1, Calories: 1,
	})
	if !errors.Is(err, client.ErrServer) {
		t.Fatalf("CreateCustomFood() = %v, want ErrServer", err)
	}
	if got := len(h.server.Requests()); got != 1 {
		t.Errorf("the fake received %d requests, want 1 (create must never be repeated)", got)
	}
}

// TestUpdateCustomFoodEffectMayBeRepeated proves update keeps its idempotent
// effect: a PUT that fully replaces the identified record applies at most
// once for the same payload, so repeating it is safe, unlike create.
func TestUpdateCustomFoodEffectMayBeRepeated(t *testing.T) {
	t.Parallel()

	h := newHarness(t, serverErrors(client.PathNutritionCustomFood), retryLimits())
	_, err := newNutrition(t, h).UpdateCustomFood(t.Context(), h.session, mustFoodID(t), mustServingID(t),
		api.CustomFoodFacts{FoodName: "x", ServingUnit: "G", NumberOfUnits: 1, Calories: 1})
	if !errors.Is(err, client.ErrServer) {
		t.Fatalf("UpdateCustomFood() = %v, want ErrServer", err)
	}
	if got := len(h.server.Requests()); got != 3 {
		t.Errorf("the fake received %d requests, want 3 (update may be repeated)", got)
	}
}

// TestCreateCustomFoodValidatesAndWritesTrimmedBrandAndServingUnit proves
// BrandName and ServingUnit are both bound-and-control-char checked, and that
// the wire body carries the validated (trimmed) form rather than the caller's
// untrimmed original.
func TestCreateCustomFoodValidatesAndWritesTrimmedBrandAndServingUnit(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathNutritionCustomFood,
		testkit.JSON(http.StatusOK, `{}`))
	h := newHarness(t, script, client.Limits{})

	brand := "  Acme  "
	_, err := newNutrition(t, h).CreateCustomFood(t.Context(), h.session, api.CustomFoodFacts{
		FoodName: testCookiesFoodName, ServingUnit: "  G  ", NumberOfUnits: 1, Calories: 1, BrandName: &brand,
	})
	if err != nil {
		t.Fatalf("CreateCustomFood() = %v", err)
	}

	meta := nestedObject(t, h.server.Requests()[0].Body, "foodMetaData")
	if meta["brandName"] != "Acme" {
		t.Errorf("brandName = %v, want the trimmed \"Acme\"", meta["brandName"])
	}
	contents := decodeBody(t, h.server.Requests()[0].Body)["nutritionContents"].([]any)[0].(map[string]any)
	if contents["servingUnit"] != "G" {
		t.Errorf("servingUnit = %v, want the trimmed \"G\"", contents["servingUnit"])
	}
}

// TestCreateCustomFoodTrimsFoodName proves FoodName is trimmed the same way
// BrandName already is: an untrimmed name must not reach Garmin verbatim, and
// a name that is only whitespace or control characters must be refused the
// same way an all-whitespace BrandName is, rather than silently accepted
// because requireText's control-character check exempts tab and newline.
func TestCreateCustomFoodTrimsFoodName(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathNutritionCustomFood,
		testkit.JSON(http.StatusOK, `{}`))
	h := newHarness(t, script, client.Limits{})

	_, err := newNutrition(t, h).CreateCustomFood(t.Context(), h.session, api.CustomFoodFacts{
		FoodName: "  Cookies  ", ServingUnit: "G", NumberOfUnits: 1, Calories: 1,
	})
	if err != nil {
		t.Fatalf("CreateCustomFood() = %v", err)
	}
	meta := nestedObject(t, h.server.Requests()[0].Body, "foodMetaData")
	if meta["foodName"] != testCookiesFoodName {
		t.Errorf("foodName = %v, want the trimmed %q", meta["foodName"], testCookiesFoodName)
	}

	if _, err := newNutrition(t, h).CreateCustomFood(t.Context(), h.session, api.CustomFoodFacts{
		FoodName: "\n\n", ServingUnit: "G", NumberOfUnits: 1, Calories: 1,
	}); !errors.Is(err, client.ErrValidation) {
		t.Errorf("CreateCustomFood() with an all-whitespace food name = %v, want ErrValidation", err)
	}
}

// TestUpdateCustomFoodOmittedFieldsAreCleared pins the corrected
// documentation: update replaces the whole record, so a nutrient the caller
// omits is cleared on Garmin's side, not preserved from the existing record.
func TestUpdateCustomFoodOmittedFieldsAreCleared(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathNutritionCustomFood,
		testkit.JSON(http.StatusOK, `{"foodMetaData":{"foodName":"`+testCookiesFoodName+`"}}`))
	h := newHarness(t, script, client.Limits{})

	carbs := 10.0
	_, err := newNutrition(t, h).UpdateCustomFood(t.Context(), h.session, mustFoodID(t), mustServingID(t),
		api.CustomFoodFacts{FoodName: testCookiesFoodName, ServingUnit: "G", NumberOfUnits: 100, Calories: 160,
			Carbs: &carbs})
	if err != nil {
		t.Fatalf("UpdateCustomFood() = %v", err)
	}

	contents := decodeBody(t, h.server.Requests()[0].Body)["nutritionContents"].([]any)[0].(map[string]any)
	if contents["carbs"] != "10" {
		t.Errorf("carbs = %v, want the string \"10\"", contents["carbs"])
	}
	if _, present := contents["protein"]; present {
		t.Error("update omitted protein but the wire body carried a protein key; " +
			"update replaces the whole record rather than merging onto the existing one")
	}
}

// TestDeleteCustomFoodTargetsItsPath pins delete's path and method.
func TestDeleteCustomFoodTargetsItsPath(t *testing.T) {
	t.Parallel()

	path := client.PathNutritionCustomFood + "/1f3c9d2a00004000800000abcdef0123"
	script := testkit.NewScript().With(path, testkit.JSON(http.StatusOK, `{}`))
	h := newHarness(t, script, client.Limits{})

	if _, err := newNutrition(t, h).DeleteCustomFood(t.Context(), h.session, mustFoodID(t)); err != nil {
		t.Fatalf("DeleteCustomFood() = %v", err)
	}

	requests := h.server.Requests()
	if len(requests) != 1 || requests[0].Path != path || requests[0].Method != http.MethodDelete {
		t.Errorf("request = %+v, want one DELETE to %s", requests, path)
	}

	if _, err := newNutrition(t, h).DeleteCustomFood(t.Context(), h.session, api.FoodID{}); !errors.Is(
		err, client.ErrValidation) {
		t.Errorf("DeleteCustomFood() without a food id = %v, want ErrValidation", err)
	}
}
