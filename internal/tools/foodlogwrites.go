package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// The upstream compatibility names of the food-log write tools.
const (
	ToolLogCustomFood = "log_custom_food"
	ToolLogFood       = "log_food"
	ToolDeleteFoodLog = "delete_food_log"

	mealNameSnacks    = "SNACKS"
	defaultServingQty = 1.0
	defaultFoodSource = "GARMIN"
)

// errNoMatchingMeal reports that neither a startTime/endTime window nor a SNACKS
// fallback meal matched the caller's meal_time, mirroring the plain-text failure
// upstream returns for the same case (nutrition.py:589-593, :671-679, :851-859).
func errNoMatchingMeal() error {
	return &ToolError{
		Advice: "No meal window matched the given meal time, and the day carries no SNACKS " +
			"meal to fall back to. Read get_nutrition_daily_meals for this date and pick a " +
			"meal_time inside a window.",
		Err: ErrInvalidArgument,
	}
}

// resolveMealID matches mealTime against the day's meal windows the way every
// logging tool in this package does: the first meal whose startTime/endTime window
// contains mealTime lexicographically, or the SNACKS meal when none matches
// (nutrition.py:589-596, :671-678, :850-859).
func (s *service) resolveMealID(
	ctx context.Context, session client.Session, date client.Date, mealTime string,
) (int64, error) {
	meals, err := s.nutrition.Meals(ctx, session, date)
	if err != nil {
		return 0, fail(err)
	}
	items := meals.Meals.Items()

	for _, meal := range items {
		start, startOK := meal.StartTime.Value()
		end, endOK := meal.EndTime.Value()
		if !startOK || !endOK || start == "" || end == "" {
			continue
		}
		if start <= mealTime && mealTime <= end {
			if id, ok := meal.MealID.Int64(); ok {
				return id, nil
			}
		}
	}
	for _, meal := range items {
		if name, ok := meal.MealName.Value(); ok && name == mealNameSnacks {
			if id, ok := meal.MealID.Int64(); ok {
				return id, nil
			}
		}
	}
	return 0, errNoMatchingMeal()
}

// FoodLogWriteResult is what log_custom_food, log_food and upsert_and_log report: an
// acknowledgement of the log write, never the logged figures themselves.
type FoodLogWriteResult struct {
	MealDate string `json:"meal_date" jsonschema:"the day the entry was logged for, YYYY-MM-DD"`
	MealTime string `json:"meal_time" jsonschema:"the time the entry was logged for, HH:MM:SS"`
	MealID   int64  `json:"meal_id" jsonschema:"the meal the entry was logged against"`
	Logged   bool   `json:"logged" jsonschema:"whether Garmin accepted the entry"`
	Status   int    `json:"status" jsonschema:"the HTTP status Garmin answered with"`
}

// LogValue reports that a log write happened, never its content.
func (r FoodLogWriteResult) LogValue() slog.Value {
	return shape("foodLogWrite", slog.Bool("logged", r.Logged), slog.Int("status", r.Status))
}

// logCustomFoodInput is the strict argument set.
type logCustomFoodInput struct {
	MealDate   string   `json:"meal_date" jsonschema:"the day to log against, YYYY-MM-DD"`
	MealTime   string   `json:"meal_time" jsonschema:"the time to log against, HH:MM:SS"`
	FoodID     string   `json:"food_id" jsonschema:"the food identifier, from get_custom_foods or search_foods"`
	ServingID  string   `json:"serving_id" jsonschema:"the serving identifier, from get_custom_foods or search_foods"`
	ServingQty *float64 `json:"serving_qty,omitempty" jsonschema:"how many servings, default 1"`
	Source     *string  `json:"source,omitempty" jsonschema:"GARMIN or FATSECRET, default GARMIN"`
}

// mealTimeProperty declares a meal_time argument in Garmin's HH:MM:SS form.
func mealTimeProperty() Property {
	return Property{
		Name:        "meal_time",
		Types:       []string{typeString},
		Description: "the time to log against, HH:MM:SS, account timezone",
		Pattern:     `^\d{2}:\d{2}:\d{2}$`,
		MaxLength:   new(8),
		Required:    true,
	}
}

// foodSourceProperty declares the GARMIN/FATSECRET namespace argument.
func foodSourceProperty() Property {
	return Property{
		Name:        "source",
		Types:       []string{typeString},
		Description: "the food namespace: GARMIN (the account's own library) or FATSECRET",
		Enum:        []any{defaultFoodSource, "FATSECRET"},
		Default:     defaultFoodSource,
	}
}

func logCustomFoodContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolLogCustomFood,
			Title: "Log a custom or catalog food",
			Description: "log a food item — from the account's own custom-food library or " +
				"the FatSecret catalog — to a meal on a date. The meal is chosen " +
				"automatically by matching meal_time against the day's meal windows, falling " +
				"back to SNACKS. Creates a new log entry every time it is called",
			Tier:        policy.TierWrite,
			Category:    categoryNutrition,
			Annotations: writeAnnotations(false),
		},
		Schema: NewSchema(
			dateProperty("meal_date", "the day to log against"),
			mealTimeProperty(),
			Property{
				Name: argFoodID, Types: []string{typeString},
				Description: "the food identifier, from get_custom_foods or search_foods", Required: true,
			},
			Property{
				Name: "serving_id", Types: []string{typeString},
				Description: "the serving identifier, from get_custom_foods or search_foods", Required: true,
			},
			Property{
				Name: "serving_qty", Types: []string{typeNumber},
				Description: "how many servings", Minimum: bound(0), Default: defaultServingQty,
			},
			foodSourceProperty(),
		),
	}
}

// registerLogCustomFood registers the tool.
func registerLogCustomFood(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in logCustomFoodInput) (
		*mcp.CallToolResult, FoodLogWriteResult, error,
	) {
		out, err := svc.logCustomFood(ctx, in)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, logCustomFoodContract().Registration(), handler)
}

// logCustomFood performs the write behind the tool.
func (s *service) logCustomFood(ctx context.Context, in logCustomFoodInput) (FoodLogWriteResult, error) {
	date, err := parseCalendarDate("meal_date", in.MealDate)
	if err != nil {
		return FoodLogWriteResult{}, err
	}
	mealTime, err := api.ParseMealTime(in.MealTime)
	if err != nil {
		return FoodLogWriteResult{}, invalidArgument("meal_time must be in HH:MM:SS form")
	}
	foodID, err := api.ParseFoodID(in.FoodID)
	if err != nil {
		return FoodLogWriteResult{}, invalidArgument("food_id must be a valid identifier")
	}
	servingID, err := api.ParseServingID(in.ServingID)
	if err != nil {
		return FoodLogWriteResult{}, invalidArgument("serving_id must be a valid identifier")
	}
	sourceValue := defaultFoodSource
	if in.Source != nil {
		sourceValue = *in.Source
	}
	source, err := api.ParseFoodSource(sourceValue)
	if err != nil {
		return FoodLogWriteResult{}, invalidArgument("source must be GARMIN or FATSECRET")
	}
	qty := defaultServingQty
	if in.ServingQty != nil {
		qty = *in.ServingQty
	}

	session, err := s.session(ctx)
	if err != nil {
		return FoodLogWriteResult{}, err
	}
	mealID, err := s.resolveMealID(ctx, session, date, mealTime.String())
	if err != nil {
		return FoodLogWriteResult{}, err
	}

	result, err := s.nutrition.LogCustomFood(ctx, session, api.LogCustomFoodEntry{
		MealDate: date, MealTime: mealTime, MealID: mealID,
		FoodID: foodID, ServingID: servingID, ServingQty: qty, Source: source,
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

// logFoodInput is the strict argument set. Every macro is required, matching
// log_food's signature (nutrition.py:637-644).
type logFoodInput struct {
	MealDate string  `json:"meal_date" jsonschema:"the day to log against, YYYY-MM-DD"`
	MealTime string  `json:"meal_time" jsonschema:"the time to log against, HH:MM:SS"`
	Name     string  `json:"name" jsonschema:"the display name for the food entry"`
	Calories float64 `json:"calories" jsonschema:"calories in kcal"`
	Carbs    float64 `json:"carbs" jsonschema:"carbohydrates in grams"`
	Protein  float64 `json:"protein" jsonschema:"protein in grams"`
	Fat      float64 `json:"fat" jsonschema:"fat in grams"`
}

func logFoodContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolLogFood,
			Title: "Quick-add a food entry",
			Description: "log a food entry by name and macros, without a food or serving " +
				"identifier, using Garmin's Quick Add feature. The meal is chosen " +
				"automatically the same way log_custom_food chooses it. Creates a new log " +
				"entry every time it is called",
			Tier:        policy.TierWrite,
			Category:    categoryNutrition,
			Annotations: writeAnnotations(false),
		},
		Schema: NewSchema(
			dateProperty("meal_date", "the day to log against"),
			mealTimeProperty(),
			Property{
				Name: "name", Types: []string{typeString},
				Description: "the display name for the food entry", Required: true,
			},
			Property{
				Name: argCalories, Types: []string{typeNumber},
				Description: "calories in kcal", Required: true,
			},
			Property{
				Name: "carbs", Types: []string{typeNumber},
				Description: "carbohydrates in grams", Required: true,
			},
			Property{
				Name: "protein", Types: []string{typeNumber},
				Description: "protein in grams", Required: true,
			},
			Property{
				Name: "fat", Types: []string{typeNumber},
				Description: "fat in grams", Required: true,
			},
		),
	}
}

// registerLogFood registers the tool.
func registerLogFood(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in logFoodInput) (
		*mcp.CallToolResult, FoodLogWriteResult, error,
	) {
		out, err := svc.logFood(ctx, in)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, logFoodContract().Registration(), handler)
}

// logFood performs the write behind the tool.
func (s *service) logFood(ctx context.Context, in logFoodInput) (FoodLogWriteResult, error) {
	date, err := parseCalendarDate("meal_date", in.MealDate)
	if err != nil {
		return FoodLogWriteResult{}, err
	}
	mealTime, err := api.ParseMealTime(in.MealTime)
	if err != nil {
		return FoodLogWriteResult{}, invalidArgument("meal_time must be in HH:MM:SS form")
	}

	session, err := s.session(ctx)
	if err != nil {
		return FoodLogWriteResult{}, err
	}
	mealID, err := s.resolveMealID(ctx, session, date, mealTime.String())
	if err != nil {
		return FoodLogWriteResult{}, err
	}

	result, err := s.nutrition.LogFood(ctx, session, api.LogFoodEntry{
		MealDate: date, MealTime: mealTime, MealID: mealID, Name: in.Name,
		Calories: in.Calories, Carbs: in.Carbs, Protein: in.Protein, Fat: in.Fat,
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

// deleteFoodLogInput is the strict argument set.
type deleteFoodLogInput struct {
	LogID    string `json:"log_id" jsonschema:"the food-log entry to delete, from get_nutrition_daily_food_log"`
	MealDate string `json:"meal_date" jsonschema:"the day the entry was logged for, YYYY-MM-DD"`
}

// LogDeletionResult reports one food-log removal, matching the manifest's
// staticTopLevelKeys (log_id, message, status).
type LogDeletionResult struct {
	LogID   string `json:"log_id" jsonschema:"the log entry that was removed"`
	Message string `json:"message" jsonschema:"a human-readable confirmation"`
	Status  int    `json:"status" jsonschema:"the HTTP status Garmin answered with"`
}

// LogValue reports that a removal happened, never which entry it named.
func (r LogDeletionResult) LogValue() slog.Value {
	return shape("logDeletion", slog.Int("status", r.Status))
}

func deleteFoodLogContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolDeleteFoodLog,
			Title: "Delete a food-log entry",
			Description: "permanently remove one logged food item, whether it was a quick-add " +
				"or a regular log entry. It cannot be undone and it requires confirmation",
			Tier:        policy.TierDestructive,
			Category:    categoryNutrition,
			Annotations: destructiveAnnotations(),
		},
		Schema: NewSchema(
			Property{
				Name: "log_id", Types: []string{typeString},
				Description: "the food-log entry to delete, from get_nutrition_daily_food_log", Required: true,
			},
			dateProperty("meal_date", "the day the entry was logged for"),
		),
	}
}

// registerDeleteFoodLog registers the tool.
func registerDeleteFoodLog(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in deleteFoodLogInput) (
		*mcp.CallToolResult, LogDeletionResult, error,
	) {
		out, err := svc.deleteFoodLog(ctx, in)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, deleteFoodLogContract().Registration(), handler)
}

// deleteFoodLog performs the removal behind the tool.
func (s *service) deleteFoodLog(ctx context.Context, in deleteFoodLogInput) (LogDeletionResult, error) {
	logID, err := api.ParseLogID(in.LogID)
	if err != nil {
		return LogDeletionResult{}, invalidArgument("log_id must be a valid identifier")
	}
	date, err := parseCalendarDate("meal_date", in.MealDate)
	if err != nil {
		return LogDeletionResult{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return LogDeletionResult{}, err
	}
	result, err := s.nutrition.DeleteFoodLog(ctx, session, date, logID)
	if err != nil {
		return LogDeletionResult{}, fail(err)
	}
	return LogDeletionResult{
		LogID:   logID.String(),
		Message: "Food log entry deleted successfully.",
		Status:  result.Status,
	}, nil
}
