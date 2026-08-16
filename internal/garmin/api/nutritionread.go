package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// FoodLogDay is one day of logged food entries.
//
// Its per-entry shape is not decoded by upstream: neither python-garminconnect
// 0.3.10 nor the Taxuspt pinned curation ever parses it —
// get_nutrition_daily_food_log passes the response straight through as JSON
// (nutrition.py:43-46) — so no field name reaches this package with any
// evidence behind it. The bounded raw response is retained as before.
//
// Entries offers a best-effort, tolerant decoded view on top of that raw
// retention, because without a LogID from somewhere, delete_food_log can never
// be built on this layer at all — no other read exposes one. Every key
// FoodLogEntry tries is a defensive guess at the most plausible spelling,
// documented on FoodLogEntry itself.
type FoodLogDay struct {
	raw client.Payload
}

// Payload is the retained raw response.
func (f FoodLogDay) Payload() client.Payload { return f.raw }

// FoodLog reads the food-consumption log for one day.
//
// Source: get_nutrition_daily_food_log, GET
// "/nutrition-service/food/logs/{date}" (nutrition.py:42).
func (n *Nutrition) FoodLog(
	ctx context.Context, session client.Session, date client.Date,
) (FoodLogDay, error) {
	req := readRequest(client.OpGetNutritionDailyFoodLog, client.EndpointNutritionFoodLog, "", nil)
	path, err := datedNutritionPath(req, client.PathNutritionFoodLogPrefix, date)
	if err != nil {
		return FoodLogDay{}, err
	}
	req.Path = path

	// Decoded only to prove the body is valid JSON; no field of it is modeled.
	// See the FoodLogDay doc comment for why.
	var discard json.RawMessage
	payload, err := n.req.read(ctx, session, req, &discard)
	if err != nil {
		return FoodLogDay{}, err
	}
	return FoodLogDay{raw: payload}, nil
}

// Meal is one meal-level summary, keyed by the mealId a food-log write targets.
//
// Source: the m["mealId"], m.get("startTime"), m.get("endTime") and
// m.get("mealName") reads log_custom_food, log_food and upsert_and_log all
// perform against the meals document (nutrition.py:588-597, :671-681,
// :856-866). MealID is a bare integer in every one of those payload builds —
// never string-converted the way a food or serving id is — so it decodes as a
// number rather than as text.
type Meal struct {
	MealID    client.Number `json:"mealId"`
	MealName  client.Text   `json:"mealName"`
	StartTime client.Text   `json:"startTime"`
	EndTime   client.Text   `json:"endTime"`
}

// DailyMeals is one day's meal summaries.
//
// Source: get_nutrition_daily_meals, GET "/nutrition-service/meals/{date}"
// (nutrition.py:62), and the "meals" wrapper key every reader of this document
// takes it from: meals = (meals_data or {}).get("meals", []).
type DailyMeals struct {
	Meals client.List[Meal] `json:"meals"`

	raw client.Payload
}

// Payload is the retained raw response.
func (d DailyMeals) Payload() client.Payload { return d.raw }

// Meals reads the meal-level summaries for one day.
func (n *Nutrition) Meals(
	ctx context.Context, session client.Session, date client.Date,
) (DailyMeals, error) {
	req := readRequest(client.OpGetNutritionDailyMeals, client.EndpointNutritionMeals, "", nil)
	path, err := datedNutritionPath(req, client.PathNutritionMealsPrefix, date)
	if err != nil {
		return DailyMeals{}, err
	}
	req.Path = path

	var meals DailyMeals
	payload, err := n.req.read(ctx, session, req, &meals)
	if err != nil {
		return DailyMeals{}, err
	}
	meals.raw = payload
	return meals, nil
}

// NutritionSettings is the account's daily nutrition goal document.
//
// Only the four fields set_nutrition_daily_settings reads back after a write
// are modeled (nutrition.py:135-138): activeDailyCalories,
// activeDailyCarbohydrateGrams, activeDailyFatGrams and
// activeDailyProteinGrams. Every other field the document may carry — a plan
// id, a start date, per-meal targets — is unmodeled and preserved only through
// the retained raw payload, because upstream never names them either.
type NutritionSettings struct {
	CalorieGoal  client.Number `json:"activeDailyCalories"`
	CarbsGrams   client.Number `json:"activeDailyCarbohydrateGrams"`
	FatGrams     client.Number `json:"activeDailyFatGrams"`
	ProteinGrams client.Number `json:"activeDailyProteinGrams"`

	raw client.Payload
}

// Payload is the retained raw response, which is also the base document
// SetSettings patches to build its write.
func (s NutritionSettings) Payload() client.Payload { return s.raw }

// Settings reads the nutrition goal document for one day.
//
// Source: get_nutrition_daily_settings, GET
// "/nutrition-service/settings/{date}" (nutrition.py:81).
func (n *Nutrition) Settings(
	ctx context.Context, session client.Session, date client.Date,
) (NutritionSettings, error) {
	req := readRequest(client.OpGetNutritionDailySettings, client.EndpointNutritionSettings, "", nil)
	path, err := datedNutritionPath(req, client.PathNutritionSettingsPrefix, date)
	if err != nil {
		return NutritionSettings{}, err
	}
	req.Path = path

	var settings NutritionSettings
	payload, err := n.req.read(ctx, session, req, &settings)
	if err != nil {
		return NutritionSettings{}, err
	}
	settings.raw = payload
	return settings, nil
}

// FoodSearchResult is one page of the general food-catalog search, spanning
// both FatSecret-sourced and Garmin custom foods.
//
// Source: search_foods, GET "/nutrition-service/food/search"
// (nutrition.py:169), and the "results"/"moreDataAvailable" keys it reads
// (nutrition.py:177-178).
type FoodSearchResult struct {
	Results           client.List[FoodItem] `json:"results"`
	MoreDataAvailable *bool                 `json:"moreDataAvailable"`

	raw client.Payload
}

// Payload is the retained raw response.
func (r FoodSearchResult) Payload() client.Payload { return r.raw }

// MaxSearchQueryLen bounds a caller-supplied food-search or custom-food-filter
// query. Garmin's own search box does not take an unbounded string, and an
// unbounded query is a request-size and log hazard before it ever reaches
// Garmin; this is headroom over any real search phrase, not an observed
// Garmin limit.
const MaxSearchQueryLen = 256

// requireBoundedQuery refuses a free-text search query over MaxSearchQueryLen.
// An empty query is valid — CustomFoods lists everything when search is "" —
// so only the upper bound is enforced here.
func requireBoundedQuery(req client.Request, query, field string) error {
	if len(query) > MaxSearchQueryLen {
		return invalid(req, fmt.Errorf("%w: %s is too long", client.ErrValidation, field))
	}
	return nil
}

// SearchFoods searches the general food catalog, filtered by a free-text query
// and bounded by page.
//
// Source: search_foods, whose query string carries searchExpression, start and
// limit (nutrition.py:168-172).
func (n *Nutrition) SearchFoods(
	ctx context.Context, session client.Session, query string, page client.Page,
) (FoodSearchResult, error) {
	values := url.Values{}
	values.Set(client.QuerySearchExpression, query)
	values.Set(client.QueryStart, strconv.Itoa(page.Start()))
	values.Set(client.QueryLimit, strconv.Itoa(page.Limit()))

	req := readRequest(client.OpSearchFoods, client.EndpointNutritionFoodSearch,
		client.PathNutritionFoodSearch, values)
	if err := requireBoundedQuery(req, query, "search query"); err != nil {
		return FoodSearchResult{}, err
	}
	if err := n.req.limits().ValidatePage(page); err != nil {
		return FoodSearchResult{}, invalid(req, err)
	}

	var result FoodSearchResult
	payload, err := n.req.read(ctx, session, req, &result)
	if err != nil {
		return FoodSearchResult{}, err
	}
	result.raw = payload
	return result, nil
}

// CustomFoodPage is one page of the user's own custom-food library.
//
// Source: get_custom_foods, GET "/nutrition-service/customFood"
// (nutrition.py:239-244), and the "customFoods" wrapper key both it and
// update_custom_food's existing-record lookup read
// (foods = search_data.get("customFoods", []), nutrition.py:444, :787).
type CustomFoodPage struct {
	CustomFoods client.List[FoodItem] `json:"customFoods"`

	raw client.Payload
}

// Payload is the retained raw response.
func (p CustomFoodPage) Payload() client.Payload { return p.raw }

// CustomFoods searches or lists the user's custom foods, filtered by an
// optional free-text search and bounded by page. An empty search lists every
// custom food, matching upstream's default parameter (nutrition.py:221).
//
// includeContent is always sent true, matching upstream: the tool has no
// caller-facing switch for it (nutrition.py:243).
func (n *Nutrition) CustomFoods(
	ctx context.Context, session client.Session, search string, page client.Page,
) (CustomFoodPage, error) {
	values := url.Values{}
	values.Set(client.QuerySearchExpression, search)
	values.Set(client.QueryStart, strconv.Itoa(page.Start()))
	values.Set(client.QueryLimit, strconv.Itoa(page.Limit()))
	values.Set(client.QueryIncludeContent, client.IncludeContentTrue)

	req := readRequest(client.OpGetCustomFoods, client.EndpointNutritionCustomFood,
		client.PathNutritionCustomFood, values)
	if err := requireBoundedQuery(req, search, "search query"); err != nil {
		return CustomFoodPage{}, err
	}
	if err := n.req.limits().ValidatePage(page); err != nil {
		return CustomFoodPage{}, invalid(req, err)
	}

	var result CustomFoodPage
	payload, err := n.req.read(ctx, session, req, &result)
	if err != nil {
		return CustomFoodPage{}, err
	}
	result.raw = payload
	return result, nil
}
