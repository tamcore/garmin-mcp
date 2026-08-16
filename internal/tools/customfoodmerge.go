package tools

import (
	"context"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// The read-modify-write merge behind update_custom_food.
//
// Garmin's custom-food PUT replaces the whole record, so a caller correcting one
// figure would clear every nutrient it did not resend. Upstream reads the existing
// record and carries the omitted fields forward (nutrition.py:433-448); this does
// the same, and differs in one deliberate way: a failed lookup is propagated rather
// than swallowed, because falling back to a bare replace is exactly the data loss
// the merge exists to prevent.

// mergeCustomFoodFacts reads the food's current record and overlays the caller's
// supplied fields onto it, so an omitted optional nutrient survives Garmin's
// whole-record replace instead of being cleared — the read-modify-write dance
// upstream's own update_custom_food performs by searching food_name and matching
// food_id (nutrition.py:433-448).
//
// The lookup is by name because that is the only search this domain client
// exposes; a lookup that fails to find a match — a rename, or a different account
// state — is not an error: nothing is merged, and every field is taken from what
// the caller supplied, the same as create_custom_food. A lookup that fails
// outright (a transient Garmin error) is propagated rather than swallowed: falling
// back silently would reintroduce exactly the field-clearing bug this merge
// exists to close.
func (s *service) mergeCustomFoodFacts(
	ctx context.Context, session client.Session, id api.FoodID, fields customFoodFactsFields,
) (api.CustomFoodFacts, error) {
	page, err := client.NewPage(0, updateCustomFoodLookupLimit)
	if err != nil {
		return api.CustomFoodFacts{}, fail(err)
	}
	result, err := s.nutrition.CustomFoods(ctx, session, fields.FoodName, page)
	if err != nil {
		return api.CustomFoodFacts{}, fail(err)
	}

	var existing *api.NutritionContent
	var existingBrand *string
	for _, item := range result.CustomFoods.Items() {
		if item.Meta == nil {
			continue
		}
		rawID, ok := item.Meta.FoodID.Value()
		if !ok || rawID != id.String() {
			continue
		}
		if contents := item.Contents.Items(); len(contents) > 0 {
			existing = &contents[0]
		}
		existingBrand = item.Meta.BrandName
		break
	}

	merged := fields
	if merged.BrandName == nil {
		merged.BrandName = existingBrand
	}
	if existing != nil {
		merged.Carbs = mergeCustomFoodNutrient(merged.Carbs, existing.Carbs)
		merged.Protein = mergeCustomFoodNutrient(merged.Protein, existing.Protein)
		merged.Fat = mergeCustomFoodNutrient(merged.Fat, existing.Fat)
		merged.Fiber = mergeCustomFoodNutrient(merged.Fiber, existing.Fiber)
		merged.Sugar = mergeCustomFoodNutrient(merged.Sugar, existing.Sugar)
		merged.SaturatedFat = mergeCustomFoodNutrient(merged.SaturatedFat, existing.SaturatedFat)
		merged.Sodium = mergeCustomFoodNutrient(merged.Sodium, existing.Sodium)
		merged.Cholesterol = mergeCustomFoodNutrient(merged.Cholesterol, existing.Cholesterol)
		merged.Potassium = mergeCustomFoodNutrient(merged.Potassium, existing.Potassium)
		merged.TransFat = mergeCustomFoodNutrient(merged.TransFat, existing.TransFat)
		merged.Calcium = mergeCustomFoodNutrient(merged.Calcium, existing.Calcium)
		merged.Iron = mergeCustomFoodNutrient(merged.Iron, existing.Iron)
		merged.VitaminD = mergeCustomFoodNutrient(merged.VitaminD, existing.VitaminD)
	}
	return merged.toFacts(), nil
}

// mergeCustomFoodNutrient keeps the caller's supplied value; when the caller left
// the field nil, it is filled from the existing record's value, when the existing
// record carries one.
func mergeCustomFoodNutrient(supplied *float64, existing client.Number) *float64 {
	if supplied != nil {
		return supplied
	}
	if value, ok := existing.Float64(); ok {
		return &value
	}
	return nil
}
