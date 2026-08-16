package client_test

import (
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

func TestNutritionConstantsHaveTheirPinnedValues(t *testing.T) {
	t.Parallel()

	paths := []struct {
		got  string
		want string
	}{
		{client.PathNutritionFoodLogPrefix, "/nutrition-service/food/logs"},
		{client.PathNutritionFoodLogs, "/nutrition-service/food/logs"},
		{client.PathNutritionFoodLogQuickAdd, "/nutrition-service/food/logs/quickAdd"},
		{client.PathNutritionMealsPrefix, "/nutrition-service/meals"},
		{client.PathNutritionSettingsPrefix, "/nutrition-service/settings"},
		{client.PathNutritionFoodSearch, "/nutrition-service/food/search"},
		{client.PathNutritionCustomFood, "/nutrition-service/customFood"},
		{client.PathNutritionCustomFoodServingUnits, "/nutrition-service/metadata/customFoodServingUnits"},
	}
	for _, tc := range paths {
		if tc.got != tc.want {
			t.Errorf("path = %q, want %q", tc.got, tc.want)
		}
	}

	queries := []struct {
		got  string
		want string
	}{
		{client.QuerySearchExpression, "searchExpression"},
		{client.QueryIncludeContent, "includeContent"},
	}
	for _, tc := range queries {
		if tc.got != tc.want {
			t.Errorf("query = %q, want %q", tc.got, tc.want)
		}
	}

	// literalTrue avoids a third bare "true" string literal alongside the two
	// already in this package's decode_test.go, which would otherwise trip
	// goconst.
	const literalTrue = "true"

	wireValues := []struct {
		got  string
		want string
	}{
		{client.IncludeContentTrue, literalTrue},
		{client.FoodTypeGeneric, "GENERIC"},
		{client.FoodSourceGarmin, "GARMIN"},
		{client.FoodSourceFatSecret, "FATSECRET"},
		{client.RegionCodeUS, "US"},
		{client.LanguageCodeEN, "en"},
		{client.LogSourceGCW, "GCW"},
		{client.LogCategoryRegular, "REGULAR_LOG"},
		{client.LogCategoryQuickAdd, "QUICK_ADD"},
		{client.FoodLogActionAdd, "ADD"},
	}
	for _, tc := range wireValues {
		if tc.got != tc.want {
			t.Errorf("wire value = %q, want %q", tc.got, tc.want)
		}
	}

	labels := []struct {
		got  client.Endpoint
		want string
	}{
		{client.EndpointNutritionFoodLog, "connectapi.nutrition.food_log"},
		{client.EndpointNutritionFoodLogQuickAdd, "connectapi.nutrition.food_log_quick_add"},
		{client.EndpointNutritionMeals, "connectapi.nutrition.meals"},
		{client.EndpointNutritionSettings, "connectapi.nutrition.settings"},
		{client.EndpointNutritionFoodSearch, "connectapi.nutrition.food_search"},
		{client.EndpointNutritionCustomFood, "connectapi.nutrition.custom_food"},
		{
			client.EndpointNutritionCustomFoodServingUnits,
			"connectapi.nutrition.custom_food_serving_units",
		},
	}
	for _, tc := range labels {
		if string(tc.got) != tc.want {
			t.Errorf("endpoint label = %q, want %q", tc.got, tc.want)
		}
	}

	operations := []struct {
		got  client.Op
		want string
	}{
		{client.OpGetNutritionDailyFoodLog, "get_nutrition_daily_food_log"},
		{client.OpGetNutritionDailyMeals, "get_nutrition_daily_meals"},
		{client.OpGetNutritionDailySettings, "get_nutrition_daily_settings"},
		{client.OpSetNutritionDailySettings, "set_nutrition_daily_settings"},
		{client.OpSearchFoods, "search_foods"},
		{client.OpGetCustomFoods, "get_custom_foods"},
		{client.OpGetCustomFoodServingUnits, "get_custom_food_serving_units"},
		{client.OpCreateCustomFood, "create_custom_food"},
		{client.OpUpdateCustomFood, "update_custom_food"},
		{client.OpDeleteCustomFood, "delete_custom_food"},
		{client.OpLogCustomFood, "log_custom_food"},
		{client.OpLogFood, "log_food"},
		{client.OpDeleteFoodLog, "delete_food_log"},
	}
	for _, tc := range operations {
		if string(tc.got) != tc.want {
			t.Errorf("op = %q, want %q", tc.got, tc.want)
		}
	}
}
