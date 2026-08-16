package client

// Nutrition paths. Source: python-garminconnect 0.3.10
// garminconnect/__init__.py (self.garmin_nutrition and the three date-keyed
// paths it builds), cross-checked against the Taxuspt pinned curation at
// src/garmin_mcp/nutrition.py, which is the only source for the food-search,
// custom-food and food-log-write paths — upstream 0.3.10 has no method for any
// of them.
const (
	// PathNutritionFoodLogPrefix precedes a calendar date in the daily food-log
	// read and delete paths. Source: garminconnect/__init__.py's
	// garmin_connect_nutrition_daily_food_logs (f"{garmin_nutrition}/food/logs"),
	// read by get_nutrition_daily_food_log; the same base is deleted from at
	// nutrition.py:733 (f"{...}/food/logs/{meal_date}") in delete_food_log.
	PathNutritionFoodLogPrefix = "/nutrition-service/food/logs"
	// PathNutritionFoodLogs is the bare food-log collection a regular log entry
	// is PUT to. It shares its literal path with PathNutritionFoodLogPrefix; the
	// two constants are kept separate because one is always followed by a date
	// segment and the other never is.
	// Source: nutrition.py:621, log_custom_food's url = "/nutrition-service/food/logs".
	PathNutritionFoodLogs = "/nutrition-service/food/logs"
	// PathNutritionFoodLogQuickAdd is the quick-add food-log write.
	// Source: nutrition.py:704, log_food's
	// url = "/nutrition-service/food/logs/quickAdd".
	PathNutritionFoodLogQuickAdd = "/nutrition-service/food/logs/quickAdd"
	// PathNutritionMealsPrefix precedes a calendar date in the daily meals path.
	// Source: garmin_connect_nutrition_daily_meals (f"{garmin_nutrition}/meals"),
	// read by get_nutrition_daily_meals.
	PathNutritionMealsPrefix = "/nutrition-service/meals"
	// PathNutritionSettingsPrefix precedes a calendar date in the daily nutrition
	// settings path, both read and written.
	// Source: garmin_connect_nutrition_daily_settings
	// (f"{garmin_nutrition}/settings"), read by get_nutrition_daily_settings and
	// written by set_nutrition_daily_settings (nutrition.py:118).
	PathNutritionSettingsPrefix = "/nutrition-service/settings"
	// PathNutritionFoodSearch is the general food-catalog search, filtered by
	// searchExpression, start and limit.
	// Source: nutrition.py:169, search_foods's
	// url = "/nutrition-service/food/search".
	PathNutritionFoodSearch = "/nutrition-service/food/search"
	// PathNutritionCustomFood is the user's custom-food library: filtered by
	// searchExpression, start, limit and includeContent for a read, and the
	// target of the create and update PUT. A delete appends the food id as one
	// more segment.
	// Source: nutrition.py:240 (get_custom_foods), :360 (create_custom_food),
	// :502 (update_custom_food) and :530 (f"{...}/customFood/{food_id}" in
	// delete_custom_food).
	PathNutritionCustomFood = "/nutrition-service/customFood"
	// PathNutritionCustomFoodServingUnits is the serving-unit catalog for a
	// custom food. Source: nutrition.py:260,
	// url = "/nutrition-service/metadata/customFoodServingUnits".
	PathNutritionCustomFoodServingUnits = "/nutrition-service/metadata/customFoodServingUnits"
)

// Query parameter names the nutrition reads add to the sets in endpoints.go,
// endpoints_health.go and endpoints_training.go. Source: the query strings
// built in nutrition.py's search_foods and get_custom_foods.
const (
	QuerySearchExpression = "searchExpression"
	QueryIncludeContent   = "includeContent"
)

// Fixed Garmin wire values the nutrition writes send. They are values Garmin's
// API expects verbatim, not runtime settings, so they belong here rather than
// in a handler. Source: the literal fields nutrition.py sets on every write.
const (
	// IncludeContentTrue is the literal value get_custom_foods and the
	// existing-record lookups inside update_custom_food and upsert_and_log send
	// for includeContent. Source: nutrition.py:243, "&includeContent=true".
	IncludeContentTrue = "true"
	// FoodTypeGeneric is the foodType every custom food is created and updated
	// with. Source: nutrition.py:348 and :490, "foodType": "GENERIC".
	FoodTypeGeneric = "GENERIC"
	// FoodSourceGarmin is the source namespace of the user's own custom-food
	// library, both the foodMetaData.source a create or update writes and one of
	// the two values log_custom_food's source parameter accepts.
	// Source: nutrition.py:349, :491 and :552.
	FoodSourceGarmin = "GARMIN"
	// FoodSourceFatSecret is the other source namespace log_custom_food accepts,
	// for a food found through search_foods rather than the custom library.
	// Source: nutrition.py:552 and the source field search_foods copies out of
	// foodMetaData (:202).
	FoodSourceFatSecret = "FATSECRET"
	// RegionCodeUS is the region code every custom food is created and updated
	// with. Source: nutrition.py:350 and :492, "regionCode": "US".
	RegionCodeUS = "US"
	// LanguageCodeEN is the language code every custom food is created and
	// updated with. Source: nutrition.py:351 and :493, "languageCode": "en".
	LanguageCodeEN = "en"
	// LogSourceGCW is the logSource every food-log write carries.
	// Source: nutrition.py:607 and :692, "logSource": "GCW".
	LogSourceGCW = "GCW"
	// LogCategoryRegular marks a regular (non-quick-add) food-log entry.
	// Source: nutrition.py:608, "logCategory": "REGULAR_LOG".
	LogCategoryRegular = "REGULAR_LOG"
	// LogCategoryQuickAdd marks a quick-add food-log entry.
	// Source: nutrition.py:693, "logCategory": "QUICK_ADD".
	LogCategoryQuickAdd = "QUICK_ADD"
	// FoodLogActionAdd is the action every food-log write carries. Upstream
	// never sends another value.
	// Source: nutrition.py:610 and :696, "action": "ADD".
	FoodLogActionAdd = "ADD"
)

// Sanitized endpoint labels for the nutrition tier. They never contain a host,
// a credential or a query string.
const (
	EndpointNutritionFoodLog                = Endpoint("connectapi.nutrition.food_log")
	EndpointNutritionFoodLogQuickAdd        = Endpoint("connectapi.nutrition.food_log_quick_add")
	EndpointNutritionMeals                  = Endpoint("connectapi.nutrition.meals")
	EndpointNutritionSettings               = Endpoint("connectapi.nutrition.settings")
	EndpointNutritionFoodSearch             = Endpoint("connectapi.nutrition.food_search")
	EndpointNutritionCustomFood             = Endpoint("connectapi.nutrition.custom_food")
	EndpointNutritionCustomFoodServingUnits = Endpoint("connectapi.nutrition.custom_food_serving_units")
)

// nutritionEndpoints returns the nutrition labels. A function, not a var:
// AGENTS.md allows no package-level mutable state, and a constant that cannot
// be a const is a function, never a var.
func nutritionEndpoints() []Endpoint {
	return []Endpoint{
		EndpointNutritionFoodLog,
		EndpointNutritionFoodLogQuickAdd,
		EndpointNutritionMeals,
		EndpointNutritionSettings,
		EndpointNutritionFoodSearch,
		EndpointNutritionCustomFood,
		EndpointNutritionCustomFoodServingUnits,
	}
}

// Sanitized operation labels, one per nutrition tool. delete_food_log and the
// two log writes share their endpoint's path family but never its label, so an
// operation, not an endpoint, identifies a read or write in a log line.
//
// upsert_and_log carries no operation of its own: it is a tool-layer
// composition of get_custom_foods, create_custom_food, get_nutrition_daily_meals
// and log_custom_food, each already labeled, and it dispatches no request this
// package does not already have a label for.
const (
	OpGetNutritionDailyFoodLog  = Op("get_nutrition_daily_food_log")
	OpGetNutritionDailyMeals    = Op("get_nutrition_daily_meals")
	OpGetNutritionDailySettings = Op("get_nutrition_daily_settings")
	OpSetNutritionDailySettings = Op("set_nutrition_daily_settings")
	OpSearchFoods               = Op("search_foods")
	OpGetCustomFoods            = Op("get_custom_foods")
	OpGetCustomFoodServingUnits = Op("get_custom_food_serving_units")
	OpCreateCustomFood          = Op("create_custom_food")
	OpUpdateCustomFood          = Op("update_custom_food")
	OpDeleteCustomFood          = Op("delete_custom_food")
	OpLogCustomFood             = Op("log_custom_food")
	OpLogFood                   = Op("log_food")
	OpDeleteFoodLog             = Op("delete_food_log")
)

// nutritionOps returns the nutrition operations. A function for the same
// reason as nutritionEndpoints.
func nutritionOps() []Op {
	return []Op{
		OpGetNutritionDailyFoodLog,
		OpGetNutritionDailyMeals,
		OpGetNutritionDailySettings,
		OpSetNutritionDailySettings,
		OpSearchFoods,
		OpGetCustomFoods,
		OpGetCustomFoodServingUnits,
		OpCreateCustomFood,
		OpUpdateCustomFood,
		OpDeleteCustomFood,
		OpLogCustomFood,
		OpLogFood,
		OpDeleteFoodLog,
	}
}
