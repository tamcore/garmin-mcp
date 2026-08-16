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

const customFoodsBodyFixture = `{"customFoods":[{"foodMetaData":{"foodId":"1f3c9d2a00004000800000abcdef0123",` +
	`"foodName":"Homemade Granola","foodType":"GENERIC","source":"GARMIN","regionCode":"US",` +
	`"languageCode":"en"},"nutritionContents":[{"servingId":"2f3c9d2a00004000800000abcdef0456",` +
	`"servingUnit":"G","numberOfUnits":100,"calories":450,"carbs":60,"protein":10,"fat":18}]}]}`

func customFoodsScript(body string) testkit.Script {
	return testkit.NewScript().With(client.PathNutritionCustomFood, testkit.JSON(http.StatusOK, body))
}

func TestGetCustomFoodsDecodesTheAccountsOwnLibrary(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, customFoodsScript(customFoodsBodyFixture))
	out, err := h.svc.getCustomFoods(h.ctx, getCustomFoodsInput{})
	if err != nil {
		t.Fatalf("getCustomFoods() = %v", err)
	}
	if out.Count != 1 || out.Start != 0 || out.Limit != 20 {
		t.Fatalf("result = %+v, want one result, start 0, limit 20", out)
	}
	item := out.Results[0]
	if item.FoodID == nil || *item.FoodID != "1f3c9d2a00004000800000abcdef0123" {
		t.Errorf("food_id = %v, want the fixture id", item.FoodID)
	}
	if item.Source == nil || *item.Source != defaultFoodSource {
		t.Errorf("source = %v, want GARMIN", item.Source)
	}
	if len(item.Servings) != 1 || item.Servings[0].ServingID == nil {
		t.Fatalf("servings = %+v, want one serving with an id", item.Servings)
	}
}

// TestGetCustomFoodsCapsResultsAtTheRequestedLimit pins the cardinality cap: before
// it, if Garmin ignored limit and sent more custom foods than requested, every one
// of them reached the caller.
func TestGetCustomFoodsCapsResultsAtTheRequestedLimit(t *testing.T) {
	t.Parallel()

	foods := make([]string, 0, defaultFoodPageLimit+5)
	for i := range defaultFoodPageLimit + 5 {
		foods = append(foods, `{"foodMetaData":{"foodId":"`+strconv.Itoa(4000000+i)+
			`","foodName":"Bulk Food"},"nutritionContents":[{"servingId":"9001"}]}`)
	}
	body := `{"customFoods":[` + strings.Join(foods, ",") + `]}`
	h := newTrendHarness(t, customFoodsScript(body))

	out, err := h.svc.getCustomFoods(h.ctx, getCustomFoodsInput{})
	if err != nil {
		t.Fatalf("getCustomFoods() = %v", err)
	}
	if out.Count != defaultFoodPageLimit || len(out.Results) != defaultFoodPageLimit {
		t.Errorf("count = %d, results = %d, want both %d", out.Count, len(out.Results), defaultFoodPageLimit)
	}
	if !out.Truncated {
		t.Error("more foods than the requested limit does not report itself truncated")
	}
}

func TestGetCustomFoodsRefusesAnOutOfRangeStartAndAnAnonymousRequest(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, customFoodsScript(customFoodsBodyFixture))
	badStart := -1
	if _, err := h.svc.getCustomFoods(h.ctx, getCustomFoodsInput{Start: &badStart}); !errors.Is(
		err, ErrInvalidArgument) {
		t.Errorf("a negative start = %v, want ErrInvalidArgument", err)
	}
	if _, err := h.svc.getCustomFoods(t.Context(), getCustomFoodsInput{}); !errors.Is(
		err, identity.ErrNoPrincipal) {
		t.Errorf("an anonymous request = %v, want ErrNoPrincipal", err)
	}
}

// TestGetCustomFoodsResultNeverLogsAName is the redaction rule.
func TestGetCustomFoodsResultNeverLogsAName(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, customFoodsScript(customFoodsBodyFixture))
	out, err := h.svc.getCustomFoods(h.ctx, getCustomFoodsInput{})
	if err != nil {
		t.Fatalf("getCustomFoods() = %v", err)
	}
	assertShapeOnly(t, "CustomFoodPageResult", out, "Homemade Granola", "450")
}
