package api

import "log/slog"

// The nutrition domain's LogValue implementations. Every model here carries a
// food name, a macro figure or a calorie count tied to a person, so each
// reports its shape only, following the same discipline sensitive.go documents
// for the rest of this package. Kept in a file of their own rather than folded
// into sensitive.go, which is already at its line-count ceiling.

// LogValue reports which parts of a food item arrived and how many servings it
// carries, never the name, brand or a single nutrition figure.
func (f FoodItem) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "foodItem"),
		slog.String("meta", presence(f.Meta != nil)),
		slog.Int("servings", f.Contents.Len()),
	)
}

// LogValue reports whether a food's identity arrived, never the name or brand
// it carries.
func (m FoodMetaData) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "foodMetaData"),
		slog.String("foodId", presence(m.FoodID.IsSet())),
		slog.String("foodName", presence(m.FoodName != nil)),
		slog.String("brandName", presence(m.BrandName != nil)),
	)
}

// LogValue reports which nutrition facts arrived, never a single figure.
func (c NutritionContent) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "nutritionContent"),
		slog.String("servingId", presence(c.ServingID.IsSet())),
		slog.String("calories", presence(c.Calories.IsSet())),
	)
}

// LogValue reports the result count, never the foods.
func (r FoodSearchResult) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "foodSearchResult"),
		slog.Int("results", r.Results.Len()),
		slog.String("moreDataAvailable", presence(r.MoreDataAvailable != nil)),
		slog.Any("payload", r.raw),
	)
}

// LogValue reports the custom-food count, never the foods.
func (p CustomFoodPage) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "customFoodPage"),
		slog.Int("customFoods", p.CustomFoods.Len()),
		slog.Any("payload", p.raw),
	)
}

// LogValue reports the meal count for one day, never a meal's name or window.
func (d DailyMeals) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "dailyMeals"),
		slog.Int("meals", d.Meals.Len()),
		slog.Any("payload", d.raw),
	)
}

// LogValue reports whether one meal's identity arrived, never its name or
// time window.
func (m Meal) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "meal"),
		slog.String("mealId", presence(m.MealID.IsSet())),
		slog.String("mealName", presence(m.MealName.IsSet())),
	)
}

// LogValue reports which nutrition goals arrived, never a target figure.
func (s NutritionSettings) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "nutritionSettings"),
		slog.String("calorieGoal", presence(s.CalorieGoal.IsSet())),
		slog.String("carbsGrams", presence(s.CarbsGrams.IsSet())),
		slog.String("fatGrams", presence(s.FatGrams.IsSet())),
		slog.String("proteinGrams", presence(s.ProteinGrams.IsSet())),
		slog.Any("payload", s.raw),
	)
}

// LogValue reports only that a food log was retained, never any entry in it:
// the per-entry shape is not decoded, so nothing but the bounded payload
// exists to report.
func (f FoodLogDay) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "foodLogDay"),
		slog.Any("payload", f.raw),
	)
}

// LogValue reports which fields of one tolerantly-decoded food-log entry
// arrived, never their content: a log id, a meal id and a date are all
// identity or health-adjacent material.
func (e FoodLogEntry) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "foodLogEntry"),
		slog.String("logId", presence(e.LogID.IsSet())),
		slog.String("mealId", presence(e.MealID.IsSet())),
		slog.String("mealDate", presence(e.MealDate.IsSet())),
	)
}

// LogValue reports the unit-code count, never the codes. A serving-unit code
// is not sensitive on its own, but the model is logged through the same
// discipline as every other, matching CatalogEntry's rationale.
func (u ServingUnits) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "servingUnits"),
		slog.Int("units", u.units.Len()),
		slog.Any("payload", u.raw),
	)
}
