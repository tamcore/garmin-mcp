package tools

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

const testFoodQueryCheerios = "Cheerios"

const foodSearchBodyFixture = `{"results":[{"foodMetaData":{"foodId":"4132350","foodName":"Cheerios",` +
	`"foodType":"BRANDED","source":"FATSECRET","regionCode":"US","languageCode":"en",` +
	`"brandName":"General Mills"},"nutritionContents":[{"servingId":"9001","servingUnit":"CUP",` +
	`"numberOfUnits":1,"calories":100,"carbs":20,"protein":3,"fat":2,"fiber":3,"sodium":210}]}],` +
	`"moreDataAvailable":true}`

func foodSearchScript(body string) testkit.Script {
	return testkit.NewScript().With(client.PathNutritionFoodSearch, testkit.JSON(http.StatusOK, body))
}

func TestSearchFoodsDecodesTheCatalogItem(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, foodSearchScript(foodSearchBodyFixture))
	out, err := h.svc.searchFoods(h.ctx, searchFoodsInput{Query: testFoodQueryCheerios})
	if err != nil {
		t.Fatalf("searchFoods() = %v", err)
	}
	if out.Count != 1 || !out.HasMore {
		t.Fatalf("result = %+v, want one result with more available", out)
	}
	item := out.Results[0]
	if item.FoodID == nil || *item.FoodID != "4132350" {
		t.Errorf("food_id = %v, want 4132350", item.FoodID)
	}
	if item.Source == nil || *item.Source != "FATSECRET" {
		t.Errorf("source = %v, want FATSECRET", item.Source)
	}
	if item.Brand == nil || *item.Brand != "General Mills" {
		t.Errorf("brand = %v, want General Mills", item.Brand)
	}
	if len(item.Servings) != 1 {
		t.Fatalf("servings = %+v, want 1", item.Servings)
	}
	if item.Servings[0].Calories == nil || *item.Servings[0].Calories != 100 {
		t.Errorf("calories = %v, want 100", item.Servings[0].Calories)
	}
}

// oneServingFoodJSON renders a food item carrying count servings, each a distinct
// serving id, for the truncation tests below.
func oneServingFoodJSON(foodID string, count int) string {
	servings := make([]string, 0, count)
	for i := range count {
		servings = append(servings, `{"servingId":"`+strconv.Itoa(9000+i)+`","calories":`+strconv.Itoa(100+i)+`}`)
	}
	return `{"foodMetaData":{"foodId":"` + foodID + `","foodName":"Bulk Food"},` +
		`"nutritionContents":[` + strings.Join(servings, ",") + `]}`
}

// TestNewFoodCatalogItemTruncatesServingsAtTheBound pins the truncation flag: before
// it, newFoodCatalogItem cut the serving list at DefaultMaxServingsPerFood with no
// way for a caller to tell the list was cut rather than complete — a bound that
// could silently drop to 1 with no test catching it.
func TestNewFoodCatalogItemTruncatesServingsAtTheBound(t *testing.T) {
	t.Parallel()

	oversized := DefaultMaxServingsPerFood + 5
	body := `{"results":[` + oneServingFoodJSON("4132350", oversized) + `],"moreDataAvailable":false}`
	h := newTrendHarness(t, foodSearchScript(body))

	out, err := h.svc.searchFoods(h.ctx, searchFoodsInput{Query: testFoodQueryCheerios})
	if err != nil {
		t.Fatalf("searchFoods() = %v", err)
	}
	if len(out.Results) != 1 {
		t.Fatalf("results = %+v, want 1 food", out.Results)
	}
	item := out.Results[0]
	if len(item.Servings) != DefaultMaxServingsPerFood {
		t.Errorf("servings = %d, want the bound %d", len(item.Servings), DefaultMaxServingsPerFood)
	}
	if !item.ServingsTruncated {
		t.Error("an oversized serving list does not report itself truncated")
	}
}

// TestNewFoodCatalogItemReportsNoTruncationUnderTheBound proves the flag is not
// simply always true.
func TestNewFoodCatalogItemReportsNoTruncationUnderTheBound(t *testing.T) {
	t.Parallel()

	body := `{"results":[` + oneServingFoodJSON("4132350", 2) + `],"moreDataAvailable":false}`
	h := newTrendHarness(t, foodSearchScript(body))

	out, err := h.svc.searchFoods(h.ctx, searchFoodsInput{Query: testFoodQueryCheerios})
	if err != nil {
		t.Fatalf("searchFoods() = %v", err)
	}
	if out.Results[0].ServingsTruncated {
		t.Error("a two-serving food reports itself truncated, want false")
	}
}

// TestSearchFoodsCapsResultsAtTheRequestedLimit pins the cardinality cap: before
// it, if Garmin ignored limit and sent more foods than requested, every one of
// them reached the caller.
func TestSearchFoodsCapsResultsAtTheRequestedLimit(t *testing.T) {
	t.Parallel()

	foods := make([]string, 0, defaultFoodPageLimit+5)
	for i := range defaultFoodPageLimit + 5 {
		foods = append(foods, oneServingFoodJSON(strconv.Itoa(4000000+i), 1))
	}
	body := `{"results":[` + strings.Join(foods, ",") + `],"moreDataAvailable":false}`
	h := newTrendHarness(t, foodSearchScript(body))

	out, err := h.svc.searchFoods(h.ctx, searchFoodsInput{Query: testFoodQueryCheerios})
	if err != nil {
		t.Fatalf("searchFoods() = %v", err)
	}
	if out.Count != defaultFoodPageLimit || len(out.Results) != defaultFoodPageLimit {
		t.Errorf("count = %d, results = %d, want both %d", out.Count, len(out.Results), defaultFoodPageLimit)
	}
	if !out.Truncated {
		t.Error("more foods than the requested limit does not report itself truncated")
	}
}

func TestSearchFoodsRefusesAnOutOfRangeLimitAndAnAnonymousRequest(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, foodSearchScript(foodSearchBodyFixture))
	tooLarge := DefaultMaxFoodResultsPage + 1
	if _, err := h.svc.searchFoods(h.ctx, searchFoodsInput{
		Query: testFoodQueryCheerios, Limit: &tooLarge,
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("an over-limit request = %v, want ErrInvalidArgument", err)
	}
	if _, err := h.svc.searchFoods(t.Context(), searchFoodsInput{Query: testFoodQueryCheerios}); !errors.Is(
		err, identity.ErrNoPrincipal) {
		t.Errorf("an anonymous request = %v, want ErrNoPrincipal", err)
	}
}

// TestSearchFoodsResultNeverLogsAName is the redaction rule: a food name and brand
// are the caller's dietary data.
func TestSearchFoodsResultNeverLogsAName(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, foodSearchScript(foodSearchBodyFixture))
	out, err := h.svc.searchFoods(h.ctx, searchFoodsInput{Query: testFoodQueryCheerios})
	if err != nil {
		t.Fatalf("searchFoods() = %v", err)
	}
	assertShapeOnly(t, "FoodSearchResultOut", out, "Cheerios", "General Mills", "100")
}
