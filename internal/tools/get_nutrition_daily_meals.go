package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetNutritionDailyMeals is the upstream compatibility name.
const ToolGetNutritionDailyMeals = "get_nutrition_daily_meals"

// DefaultMaxMeals bounds the meal summaries one day's read may report. Garmin's own
// meal structure is a handful of fixed slots (breakfast, lunch, dinner, snacks), so
// this is generous headroom rather than an observed count.
const DefaultMaxMeals = 64

// MealResult is one meal-level summary.
//
// Source: internal/garmin/api's Meal, whose fields are read from the meals document
// every logging tool in this package reads mealId, mealName, startTime and endTime
// from (nutrition.py:588-597, :671-681, :856-866).
type MealResult struct {
	MealID    *int64  `json:"meal_id,omitempty" jsonschema:"the meal identifier, needed to log food to this meal"`
	MealName  *string `json:"meal_name,omitempty" jsonschema:"Garmin's meal name, for example SNACKS"`
	StartTime *string `json:"start_time,omitempty" jsonschema:"the meal window's start, HH:MM:SS"`
	EndTime   *string `json:"end_time,omitempty" jsonschema:"the meal window's end, HH:MM:SS"`
}

// LogValue reports which fields arrived, never the meal's name or window.
func (m MealResult) LogValue() slog.Value {
	return shape("meal",
		slog.String("mealId", presence(m.MealID != nil)),
		slog.String("mealName", presence(m.MealName != nil)),
	)
}

// DailyMealsResult is one day's meal-level summaries, bounded.
type DailyMealsResult struct {
	Date      string       `json:"date" jsonschema:"the day that was requested, YYYY-MM-DD"`
	Meals     []MealResult `json:"meals" jsonschema:"the day's meal summaries"`
	Count     int          `json:"count" jsonschema:"how many meals this result carries"`
	Truncated bool         `json:"truncated" jsonschema:"whether the day carried more meals than this result's bound"`

	// Document is the day's whole meals document, sanitized, under Garmin's own
	// field names — including any meal total no pinned source names as a stable
	// field. It is health data and identity material together: never log it.
	Document any `json:"document" jsonschema:"the day's meals, as Garmin returns it, sanitized"`

	// DroppedFields is how many identifying keys were removed from Document.
	DroppedFields int `json:"dropped_fields" jsonschema:"identifying keys removed from document"`
}

// LogValue reports the meal count, never a meal's name or window.
func (d DailyMealsResult) LogValue() slog.Value {
	return shape("dailyMeals", slog.Int("meals", d.Count), slog.Bool("truncated", d.Truncated))
}

// getNutritionDailyMealsInput is the strict argument set: one day.
type getNutritionDailyMealsInput struct {
	Date string `json:"date" jsonschema:"the day to read, YYYY-MM-DD"`
}

func getNutritionDailyMealsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetNutritionDailyMeals,
			Title: "Get the daily meal summaries",
			Description: "read one day's meal-level summaries — breakfast, lunch, dinner and " +
				"snacks — each carrying the mealId that log_custom_food, log_food and " +
				"upsert_and_log log against, plus the whole day's meals document — " +
				"including any per-meal total — sanitized and passed through under " +
				"Garmin's own field names",
			Tier:        policy.TierReadOnly,
			Category:    categoryNutrition,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the day to read")),
	}
}

// registerGetNutritionDailyMeals registers the tool.
func registerGetNutritionDailyMeals(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getNutritionDailyMealsInput) (
		*mcp.CallToolResult, DailyMealsResult, error,
	) {
		out, err := svc.readNutritionDailyMeals(ctx, in.Date)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getNutritionDailyMealsContract().Registration(), handler)
}

// readNutritionDailyMeals performs the read behind the tool.
func (s *service) readNutritionDailyMeals(ctx context.Context, date string) (DailyMealsResult, error) {
	day, err := parseCalendarDate("date", date)
	if err != nil {
		return DailyMealsResult{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return DailyMealsResult{}, err
	}
	read, err := s.nutrition.Meals(ctx, session, day)
	if err != nil {
		return DailyMealsResult{}, fail(err)
	}
	out := newDailyMealsResult(day.String(), read)
	document, dropped, err := sanitizedNutritionDocument(read.Payload().Bytes())
	if err != nil {
		return DailyMealsResult{}, err
	}
	out.Document = document
	out.DroppedFields = dropped
	return out, nil
}

// newDailyMealsResult maps the domain model onto the bounded result.
func newDailyMealsResult(date string, document api.DailyMeals) DailyMealsResult {
	items := document.Meals.Items()
	truncated := len(items) > DefaultMaxMeals
	if truncated {
		items = items[:DefaultMaxMeals]
	}

	out := DailyMealsResult{Date: date, Truncated: truncated}
	out.Meals = make([]MealResult, 0, len(items))
	for _, meal := range items {
		out.Meals = append(out.Meals, MealResult{
			MealID:    optionalInt64(meal.MealID),
			MealName:  optionalText(meal.MealName),
			StartTime: optionalText(meal.StartTime),
			EndTime:   optionalText(meal.EndTime),
		})
	}
	out.Count = len(out.Meals)
	return out
}
