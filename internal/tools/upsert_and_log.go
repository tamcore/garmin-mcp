package tools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolUpsertAndLog is the upstream compatibility name.
const ToolUpsertAndLog = "upsert_and_log"

// upsertAndLogSearchLimit matches upstream's own search page size for the
// find-or-create lookup (nutrition.py:778, :831: "start=0&limit=10").
const upsertAndLogSearchLimit = 10

// upsertAndLogInput is the strict argument set.
type upsertAndLogInput struct {
	MealDate      string   `json:"meal_date" jsonschema:"the day to log against, YYYY-MM-DD"`
	MealTime      string   `json:"meal_time" jsonschema:"the time to log against, HH:MM:SS"`
	FoodName      string   `json:"food_name" jsonschema:"the food to find or create"`
	Calories      float64  `json:"calories" jsonschema:"calories per serving"`
	Carbs         *float64 `json:"carbs,omitempty" jsonschema:"carbohydrates in grams per serving"`
	Protein       *float64 `json:"protein,omitempty" jsonschema:"protein in grams per serving"`
	Fat           *float64 `json:"fat,omitempty" jsonschema:"fat in grams per serving"`
	ServingUnit   *string  `json:"serving_unit,omitempty" jsonschema:"the serving-size unit, default G"`
	NumberOfUnits *float64 `json:"number_of_units,omitempty" jsonschema:"the serving size, default 100"`
	ServingQty    *float64 `json:"serving_qty,omitempty" jsonschema:"how many servings to log, default 1"`
}

func upsertAndLogContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolUpsertAndLog,
			Title: "Find or create a food, then log it",
			Description: "search the account's custom-food library for food_name; if found, " +
				"log it immediately, otherwise create it with the supplied nutrition facts " +
				"and then log it. A failed search is refused rather than treated as a miss, " +
				"so a transient search failure cannot create a second food with the same " +
				"name; two concurrent calls can still each pass the search before either " +
				"creates, and both may create a duplicate. The meal is chosen automatically " +
				"the same way log_custom_food chooses it",
			Tier:        policy.TierWrite,
			Category:    categoryNutrition,
			Annotations: writeAnnotations(false),
		},
		Schema: NewSchema(
			dateProperty("meal_date", "the day to log against"),
			mealTimeProperty(),
			Property{
				Name: "food_name", Types: []string{typeString},
				Description: "the food to find or create", Required: true,
			},
			Property{
				Name: argCalories, Types: []string{typeNumber},
				Description: "calories per serving", Required: true,
			},
			Property{
				Name: "carbs", Types: []string{typeNumber},
				Description: "carbohydrates in grams per serving", Nullable: true,
			},
			Property{
				Name: "protein", Types: []string{typeNumber},
				Description: "protein in grams per serving", Nullable: true,
			},
			Property{
				Name: "fat", Types: []string{typeNumber},
				Description: "total fat in grams per serving", Nullable: true,
			},
			Property{
				Name: "serving_unit", Types: []string{typeString},
				Description: "the serving-size unit, for example G, ML or OZ", Default: defaultServingUnit,
			},
			Property{
				Name: "number_of_units", Types: []string{typeNumber},
				Description: "the serving size in serving_unit", Default: defaultNumberOfUnits,
			},
			Property{
				Name: "serving_qty", Types: []string{typeNumber},
				Description: "how many servings to log", Minimum: bound(0), Default: defaultServingQty,
			},
		),
	}
}

// registerUpsertAndLog registers the tool.
func registerUpsertAndLog(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in upsertAndLogInput) (
		*mcp.CallToolResult, FoodLogWriteResult, error,
	) {
		out, err := svc.upsertAndLog(ctx, in)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, upsertAndLogContract().Registration(), handler)
}

// upsertAndLog performs the find-or-create-then-log composition behind the tool. It
// calls no method of its own on the domain client: every step is an existing
// Nutrition method, composed the way upstream's upsert_and_log composes them
// (nutrition.py:745-905).
func (s *service) upsertAndLog(ctx context.Context, in upsertAndLogInput) (FoodLogWriteResult, error) {
	date, err := parseCalendarDate("meal_date", in.MealDate)
	if err != nil {
		return FoodLogWriteResult{}, err
	}
	mealTime, err := api.ParseMealTime(in.MealTime)
	if err != nil {
		return FoodLogWriteResult{}, invalidArgument("meal_time must be in HH:MM:SS form")
	}
	trimmedName := strings.TrimSpace(in.FoodName)
	if trimmedName == "" {
		return FoodLogWriteResult{}, invalidArgument("food_name must not be empty")
	}

	session, err := s.session(ctx)
	if err != nil {
		return FoodLogWriteResult{}, err
	}

	// The meal is resolved before the food is found or created: if no meal window
	// matches and the day carries no SNACKS fallback, this fails before any food is
	// created, so a failing call never orphans a newly created food with no meal_id
	// in the result to find it by.
	mealID, err := s.resolveMealID(ctx, session, date, mealTime.String())
	if err != nil {
		return FoodLogWriteResult{}, err
	}

	foodID, servingID, err := s.findOrCreateCustomFood(ctx, session, trimmedName, in)
	if err != nil {
		return FoodLogWriteResult{}, err
	}

	qty := defaultServingQty
	if in.ServingQty != nil {
		qty = *in.ServingQty
	}
	result, err := s.nutrition.LogCustomFood(ctx, session, api.LogCustomFoodEntry{
		MealDate: date, MealTime: mealTime, MealID: mealID,
		FoodID: foodID, ServingID: servingID, ServingQty: qty, Source: api.SourceGarmin,
		LoggedAt: s.now(),
	})
	if err != nil {
		return FoodLogWriteResult{}, fail(err)
	}
	return FoodLogWriteResult{
		MealDate: date.String(), MealTime: mealTime.String(), MealID: mealID,
		Logged: true, Status: result.Status,
	}, nil
}

// findOrCreateCustomFood searches the account's custom-food library for an exact
// case-insensitive name match; if none exists, it creates one and re-reads it when
// the create acknowledged with no body, matching upstream's own two-step fallback
// (nutrition.py:774-841).
func (s *service) findOrCreateCustomFood(
	ctx context.Context, session client.Session, name string, in upsertAndLogInput,
) (api.FoodID, api.ServingID, error) {
	page, err := client.NewPage(0, upsertAndLogSearchLimit)
	if err != nil {
		return api.FoodID{}, api.ServingID{}, fail(err)
	}

	id, serving, found, err := s.findCustomFoodByName(ctx, session, name, page)
	if err != nil {
		return api.FoodID{}, api.ServingID{}, err
	}
	if found {
		return id, serving, nil
	}

	facts := customFoodFactsFields{
		FoodName: name, Calories: in.Calories, ServingUnit: in.ServingUnit,
		NumberOfUnits: in.NumberOfUnits, Carbs: in.Carbs, Protein: in.Protein, Fat: in.Fat,
	}.toFacts()
	created, err := s.nutrition.CreateCustomFood(ctx, session, facts)
	if err != nil {
		return api.FoodID{}, api.ServingID{}, fail(err)
	}
	if id, serving, ok := identifiersOf(created); ok {
		return id, serving, nil
	}

	// A 204 create carries no body: look the food up by name, the same fallback
	// upstream performs (nutrition.py:829-841).
	id, serving, found, err = s.findCustomFoodByName(ctx, session, name, page)
	if err != nil {
		return api.FoodID{}, api.ServingID{}, err
	}
	if found {
		return id, serving, nil
	}
	return api.FoodID{}, api.ServingID{}, &ToolError{
		Advice: "The custom food was created but could not be read back by name. Read " +
			"get_custom_foods to find its id and log it with log_custom_food instead.",
		Err: ErrInvalidArgument,
	}
}

// findCustomFoodByName searches the account's custom-food library for an exact
// case-insensitive name match and returns its identifiers.
//
// A search failure is returned rather than reported as a miss: treating it as "not
// found" would fall through to CreateCustomFood and add a second food under the same
// name for a transient failure — a 429, a 500, a timeout or a decode error — that
// upstream's own search would have surfaced as a call failure instead.
func (s *service) findCustomFoodByName(
	ctx context.Context, session client.Session, name string, page client.Page,
) (api.FoodID, api.ServingID, bool, error) {
	result, err := s.nutrition.CustomFoods(ctx, session, name, page)
	if err != nil {
		return api.FoodID{}, api.ServingID{}, false, fail(err)
	}
	for _, item := range result.CustomFoods.Items() {
		if item.Meta == nil || item.Meta.FoodName == nil {
			continue
		}
		if !strings.EqualFold(*item.Meta.FoodName, name) {
			continue
		}
		if id, serving, ok := identifiersOf(item); ok {
			return id, serving, true, nil
		}
	}
	return api.FoodID{}, api.ServingID{}, false, nil
}

// identifiersOf extracts a usable food and serving identifier from a decoded food
// item, reporting false when either is absent or does not parse as a valid
// identifier.
func identifiersOf(item api.FoodItem) (api.FoodID, api.ServingID, bool) {
	if item.Meta == nil {
		return api.FoodID{}, api.ServingID{}, false
	}
	rawID, ok := item.Meta.FoodID.Value()
	if !ok || rawID == "" {
		return api.FoodID{}, api.ServingID{}, false
	}
	contents := item.Contents.Items()
	if len(contents) == 0 {
		return api.FoodID{}, api.ServingID{}, false
	}
	rawServing, ok := contents[0].ServingID.Value()
	if !ok || rawServing == "" {
		return api.FoodID{}, api.ServingID{}, false
	}

	id, err := api.ParseFoodID(rawID)
	if err != nil {
		return api.FoodID{}, api.ServingID{}, false
	}
	serving, err := api.ParseServingID(rawServing)
	if err != nil {
		return api.FoodID{}, api.ServingID{}, false
	}
	return id, serving, true
}
