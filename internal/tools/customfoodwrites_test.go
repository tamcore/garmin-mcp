package tools

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

const customFoodHexID = "1f3c9d2a00004000800000abcdef0123"
const customFoodServingHexID = "2f3c9d2a00004000800000abcdef0456"
const testFoodNameCookies = "Homemade Cookies"
const testFoodNameUpdatedCookies = "Updated Cookies"

func customFoodWriteScript(behaviors ...testkit.Behavior) testkit.Script {
	return testkit.NewScript().With(client.PathNutritionCustomFood, behaviors...)
}

func customFoodDeleteScript(behaviors ...testkit.Behavior) testkit.Script {
	return testkit.NewScript().With(client.PathNutritionCustomFood+"/"+customFoodHexID, behaviors...)
}

func TestCreateCustomFoodDecodesTheCreatedRecord(t *testing.T) {
	t.Parallel()

	body := `{"foodMetaData":{"foodId":"` + customFoodHexID + `","foodName":"Homemade Cookies",` +
		`"foodType":"GENERIC","source":"GARMIN","brandName":"Three Bridges"},` +
		`"nutritionContents":[{"servingId":"` + customFoodServingHexID + `","servingUnit":"G",` +
		`"numberOfUnits":100,"calories":450}]}`
	h := newTrendHarness(t, customFoodWriteScript(testkit.JSON(http.StatusOK, body)))

	out, err := h.svc.createCustomFood(h.ctx, createCustomFoodInput{
		FoodName: testFoodNameCookies, Calories: 450,
	})
	if err != nil {
		t.Fatalf("createCustomFood() = %v", err)
	}
	if out.FoodID == nil || *out.FoodID != customFoodHexID {
		t.Errorf("food_id = %v, want %s", out.FoodID, customFoodHexID)
	}
	if out.ServingID == nil || *out.ServingID != customFoodServingHexID {
		t.Errorf("serving_id = %v, want %s", out.ServingID, customFoodServingHexID)
	}
	if out.BrandName == nil || *out.BrandName != "Three Bridges" {
		t.Errorf("brand_name = %v, want Three Bridges", out.BrandName)
	}
}

// TestCreateCustomFoodReportsNoIdentifiersOnAnEmptyResponse pins the documented gap:
// a 204 leaves food_id and serving_id absent rather than fabricated.
func TestCreateCustomFoodReportsNoIdentifiersOnAnEmptyResponse(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, customFoodWriteScript(testkit.Behavior{Status: http.StatusNoContent}))
	out, err := h.svc.createCustomFood(h.ctx, createCustomFoodInput{
		FoodName: testFoodNameCookies, Calories: 450,
	})
	if err != nil {
		t.Fatalf("createCustomFood() = %v", err)
	}
	if out.FoodID != nil || out.ServingID != nil {
		t.Errorf("result = %+v, want no identifiers for a 204", out)
	}
}

func TestCreateCustomFoodRefusesAnAnonymousRequest(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, customFoodWriteScript(testkit.JSON(http.StatusOK, `{}`)))
	if _, err := h.svc.createCustomFood(t.Context(), createCustomFoodInput{
		FoodName: testFoodNameCookies, Calories: 450,
	}); !errors.Is(err, identity.ErrNoPrincipal) {
		t.Errorf("an anonymous request = %v, want ErrNoPrincipal", err)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestUpdateCustomFoodRefusesAnInvalidFoodID(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, customFoodWriteScript(testkit.JSON(http.StatusOK, `{}`)))
	_, err := h.svc.updateCustomFood(h.ctx, updateCustomFoodInput{
		FoodID: "not valid", ServingID: customFoodServingHexID,
		FoodName: "Cookies", Calories: 450,
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("an invalid food id = %v, want ErrInvalidArgument", err)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0 before dispatch", got)
	}
}

func TestUpdateCustomFoodReplacesTheRecord(t *testing.T) {
	t.Parallel()

	body := `{"foodMetaData":{"foodId":"` + customFoodHexID + `","foodName":"` + testFoodNameUpdatedCookies + `",` +
		`"source":"GARMIN"},"nutritionContents":[{"servingId":"` + customFoodServingHexID + `"}]}`
	h := newTrendHarness(t, customFoodWriteScript(testkit.JSON(http.StatusOK, body)))

	out, err := h.svc.updateCustomFood(h.ctx, updateCustomFoodInput{
		FoodID: customFoodHexID, ServingID: customFoodServingHexID,
		FoodName: testFoodNameUpdatedCookies, Calories: 500,
	})
	if err != nil {
		t.Fatalf("updateCustomFood() = %v", err)
	}
	if out.FoodName == nil || *out.FoodName != testFoodNameUpdatedCookies {
		t.Errorf("food_name = %v, want Updated Cookies", out.FoodName)
	}
}

// existingNutritionBodyFixture is the food's current record, as CustomFoods'
// search-by-name lookup returns it: it carries every optional nutrient plus a
// brand name, so the merge tests can prove an omitted field survives an update.
const existingNutritionBodyFixture = `{"customFoods":[{"foodMetaData":{"foodId":"` + customFoodHexID + `",` +
	`"foodName":"` + testFoodNameUpdatedCookies + `","brandName":"Acme"},"nutritionContents":[{"servingId":"` +
	customFoodServingHexID + `","servingUnit":"G","numberOfUnits":100,"calories":450,"carbs":60,` +
	`"protein":10,"fat":18,"fiber":5,"sugar":20,"saturatedFat":6,"sodium":300,"cholesterol":15,` +
	`"potassium":250,"transFat":0,"calcium":40,"iron":2,"vitaminD":1}]}]}`

// putBodyOf decodes the nutritionContents object of the first PUT request in
// requests, failing the test if none is found.
func putBodyOf(t *testing.T, requests []testkit.RecordedRequest) map[string]any {
	t.Helper()
	for _, r := range requests {
		if r.Method != http.MethodPut {
			continue
		}
		var body struct {
			NutritionContents []map[string]any `json:"nutritionContents"`
		}
		if err := json.Unmarshal(r.Body, &body); err != nil {
			t.Fatalf("decoding the PUT body: %v", err)
		}
		if len(body.NutritionContents) != 1 {
			t.Fatalf("nutritionContents = %+v, want 1", body.NutritionContents)
		}
		return body.NutritionContents[0]
	}
	t.Fatal("no PUT request was recorded")
	return nil
}

// TestUpdateCustomFoodPreservesAnOmittedNutrient pins the read-modify-write fix:
// before it, update_custom_food replaced the whole record from only the fields
// the caller supplied, so an omitted nutrient like sugar or fiber was silently
// cleared on Garmin's side.
func TestUpdateCustomFoodPreservesAnOmittedNutrient(t *testing.T) {
	t.Parallel()

	updateResponse := `{"foodMetaData":{"foodId":"` + customFoodHexID + `","foodName":"` +
		testFoodNameUpdatedCookies + `"},` +
		`"nutritionContents":[{"servingId":"` + customFoodServingHexID + `"}]}`
	h := newTrendHarness(t, customFoodWriteScript(
		testkit.JSON(http.StatusOK, existingNutritionBodyFixture),
		testkit.JSON(http.StatusOK, updateResponse)))

	_, err := h.svc.updateCustomFood(h.ctx, updateCustomFoodInput{
		FoodID: customFoodHexID, ServingID: customFoodServingHexID,
		FoodName: testFoodNameUpdatedCookies, Calories: 500,
	})
	if err != nil {
		t.Fatalf("updateCustomFood() = %v", err)
	}

	content := putBodyOf(t, h.fake.Requests())
	if content["sugar"] != "20" {
		t.Errorf("sugar = %v, want the preserved existing value 20", content["sugar"])
	}
	if content["fiber"] != "5" {
		t.Errorf("fiber = %v, want the preserved existing value 5", content["fiber"])
	}
	if content["calories"] != "500" {
		t.Errorf("calories = %v, want the caller's new value 500", content["calories"])
	}
}

// TestUpdateCustomFoodOverridesASuppliedNutrientOverTheExisting is the opposite
// direction: a field the caller does supply wins over the existing record.
func TestUpdateCustomFoodOverridesASuppliedNutrientOverTheExisting(t *testing.T) {
	t.Parallel()

	updateResponse := `{"foodMetaData":{"foodId":"` + customFoodHexID + `","foodName":"` +
		testFoodNameUpdatedCookies + `"},` +
		`"nutritionContents":[{"servingId":"` + customFoodServingHexID + `"}]}`
	h := newTrendHarness(t, customFoodWriteScript(
		testkit.JSON(http.StatusOK, existingNutritionBodyFixture),
		testkit.JSON(http.StatusOK, updateResponse)))

	newSugar := 99.0
	_, err := h.svc.updateCustomFood(h.ctx, updateCustomFoodInput{
		FoodID: customFoodHexID, ServingID: customFoodServingHexID,
		FoodName: testFoodNameUpdatedCookies, Calories: 500, Sugar: &newSugar,
	})
	if err != nil {
		t.Fatalf("updateCustomFood() = %v", err)
	}

	content := putBodyOf(t, h.fake.Requests())
	if content["sugar"] != "99" {
		t.Errorf("sugar = %v, want the caller's supplied value 99, not the existing 20", content["sugar"])
	}
}

func TestDeleteCustomFoodReportsTheRemoval(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, customFoodDeleteScript(testkit.Behavior{Status: http.StatusNoContent}))
	out, err := h.svc.deleteCustomFood(h.ctx, deleteCustomFoodInput{FoodID: customFoodHexID})
	if err != nil {
		t.Fatalf("deleteCustomFood() = %v", err)
	}
	if out.FoodID != customFoodHexID {
		t.Errorf("food_id = %q, want %q", out.FoodID, customFoodHexID)
	}
	if out.Status != http.StatusNoContent {
		t.Errorf("status = %d, want 204", out.Status)
	}
	if out.Message == "" {
		t.Error("the deletion carries no confirmation message")
	}
}

// TestDeleteCustomFoodIsIdempotent proves a second delete of the same identifier
// still reports a clean removal, matching the manifest's idempotency note.
func TestDeleteCustomFoodIsIdempotent(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, customFoodDeleteScript(
		testkit.Behavior{Status: http.StatusNoContent}, testkit.Behavior{Status: http.StatusNoContent}))
	if _, err := h.svc.deleteCustomFood(h.ctx, deleteCustomFoodInput{FoodID: customFoodHexID}); err != nil {
		t.Fatalf("first deleteCustomFood() = %v", err)
	}
	if _, err := h.svc.deleteCustomFood(h.ctx, deleteCustomFoodInput{FoodID: customFoodHexID}); err != nil {
		t.Fatalf("second deleteCustomFood() = %v", err)
	}
}

func TestDeleteCustomFoodRefusesAnAnonymousRequest(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, customFoodDeleteScript(testkit.Behavior{Status: http.StatusNoContent}))
	if _, err := h.svc.deleteCustomFood(t.Context(), deleteCustomFoodInput{
		FoodID: customFoodHexID,
	}); !errors.Is(err, identity.ErrNoPrincipal) {
		t.Errorf("an anonymous request = %v, want ErrNoPrincipal", err)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestCustomFoodWriteResultNeverLogsAName is the redaction rule.
func TestCustomFoodWriteResultNeverLogsAName(t *testing.T) {
	t.Parallel()

	body := `{"foodMetaData":{"foodId":"` + customFoodHexID + `","foodName":"Homemade Cookies",` +
		`"brandName":"Three Bridges"},"nutritionContents":[{"servingId":"9001"}]}`
	h := newTrendHarness(t, customFoodWriteScript(testkit.JSON(http.StatusOK, body)))
	out, err := h.svc.createCustomFood(h.ctx, createCustomFoodInput{
		FoodName: testFoodNameCookies, Calories: 450,
	})
	if err != nil {
		t.Fatalf("createCustomFood() = %v", err)
	}
	assertShapeOnly(t, "CustomFoodWriteResult", out, "Homemade Cookies", "Three Bridges")
}

// TestFoodDeletionResultNeverLogsAFoodID is the redaction rule.
func TestFoodDeletionResultNeverLogsAFoodID(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, customFoodDeleteScript(testkit.Behavior{Status: http.StatusNoContent}))
	out, err := h.svc.deleteCustomFood(h.ctx, deleteCustomFoodInput{FoodID: customFoodHexID})
	if err != nil {
		t.Fatalf("deleteCustomFood() = %v", err)
	}
	assertShapeOnly(t, "FoodDeletionResult", out, customFoodHexID)
}
