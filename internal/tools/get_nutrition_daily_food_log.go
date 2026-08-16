package tools

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetNutritionDailyFoodLog is the upstream compatibility name.
const ToolGetNutritionDailyFoodLog = "get_nutrition_daily_food_log"

// DefaultMaxFoodLogEntries bounds the food-log entries one day's read may report. It
// matches the bound internal/garmin/api's FoodLogDay already enforces while decoding,
// so this tool's own bound never disagrees with the one the domain client applied.
const DefaultMaxFoodLogEntries = 1000

// FoodLogEntryResult is one tolerantly-decoded food-log entry.
//
// Only the identifier fields internal/garmin/api/nutritionreadfoodlog.go decodes are
// modeled: the per-entry wire shape carries no evidence beyond what delete_food_log
// itself names — "Use get_nutrition_daily_food_log to find the logId and date"
// (nutrition.py:725) — so nothing else is invented here.
type FoodLogEntryResult struct {
	LogID    *string `json:"log_id,omitempty" jsonschema:"the food-log entry identifier, for delete_food_log"`
	MealID   *int64  `json:"meal_id,omitempty" jsonschema:"the meal this entry was logged against"`
	MealDate *string `json:"meal_date,omitempty" jsonschema:"the date this entry was logged for"`
}

// LogValue reports which fields arrived, never their content.
func (e FoodLogEntryResult) LogValue() slog.Value {
	return shape("foodLogEntry",
		slog.String("logId", presence(e.LogID != nil)),
		slog.String("mealId", presence(e.MealID != nil)),
	)
}

// FoodLogDayResult is one day of logged food entries, bounded.
//
// Upstream passes the day's food log straight through as JSON (nutrition.py:32-46,
// "Returns food items logged throughout the day including calories, macronutrients,
// and meal associations"). No source documents the per-entry field names beyond the
// identifiers FoodLogEntryResult carries, so those are not fabricated as typed
// fields; the calories, macros and meal associations the manifest promises reach the
// caller instead through Document, the sanitized whole-day document, the same way
// get_goals hands on a document no pinned source names a single field of.
type FoodLogDayResult struct {
	Date      string               `json:"date" jsonschema:"the day that was requested, YYYY-MM-DD"`
	Entries   []FoodLogEntryResult `json:"entries" jsonschema:"the day's tolerantly-decoded log entries"`
	Count     int                  `json:"count" jsonschema:"how many entries this result carries"`
	Truncated bool                 `json:"truncated" jsonschema:"whether the day carried more entries than this bound"`

	// Document is the day's whole food log, sanitized, under Garmin's own field
	// names — including the calories, macronutrients and meal associations the
	// manifest promises but no pinned source names as a stable field. It is health
	// data and identity material together: never log it.
	Document any `json:"document" jsonschema:"the day's food log, as Garmin returns it, sanitized"`

	// DroppedFields is how many identifying keys were removed from Document. It is
	// a count and never a list of names: see sanitizeUntyped for why naming them
	// would disclose what removing them hid.
	DroppedFields int `json:"dropped_fields" jsonschema:"identifying keys removed from document"`
}

// LogValue reports the entry count and truncation, never an entry.
func (d FoodLogDayResult) LogValue() slog.Value {
	return shape("foodLogDay", slog.Int("entries", d.Count), slog.Bool("truncated", d.Truncated))
}

// getNutritionDailyFoodLogInput is the strict argument set: one day.
type getNutritionDailyFoodLogInput struct {
	Date string `json:"date" jsonschema:"the day to read, YYYY-MM-DD"`
}

func getNutritionDailyFoodLogContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetNutritionDailyFoodLog,
			Title: "Get the daily food log",
			Description: "read the account's food-consumption log for one day: every logged " +
				"entry's identifier and meal association as typed fields, plus the whole " +
				"day's document — including calories, macronutrients and meal associations " +
				"— sanitized and passed through under Garmin's own field names. Use the log " +
				"id with delete_food_log to remove an entry",
			Tier:        policy.TierReadOnly,
			Category:    categoryNutrition,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the day to read")),
	}
}

// registerGetNutritionDailyFoodLog registers the tool.
func registerGetNutritionDailyFoodLog(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getNutritionDailyFoodLogInput) (
		*mcp.CallToolResult, FoodLogDayResult, error,
	) {
		out, err := svc.readNutritionDailyFoodLog(ctx, in.Date)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getNutritionDailyFoodLogContract().Registration(), handler)
}

// readNutritionDailyFoodLog performs the read behind the tool.
func (s *service) readNutritionDailyFoodLog(ctx context.Context, date string) (FoodLogDayResult, error) {
	day, err := parseCalendarDate("date", date)
	if err != nil {
		return FoodLogDayResult{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return FoodLogDayResult{}, err
	}
	read, err := s.nutrition.FoodLog(ctx, session, day)
	if err != nil {
		return FoodLogDayResult{}, fail(err)
	}
	out := newFoodLogDayResult(day.String(), read)
	document, dropped, err := sanitizedNutritionDocument(read.Payload().Bytes())
	if err != nil {
		return FoodLogDayResult{}, err
	}
	out.Document = document
	out.DroppedFields = dropped
	return out, nil
}

// sanitizedNutritionDocument sanitises one whole retained Garmin document, refusing
// rather than cutting when the walk cannot cover the whole document — the rule
// get_goals's own sanitizedGoalDocument applies to a goal document, applied here to
// a whole food-log or daily-meals document instead of a list of them.
func sanitizedNutritionDocument(raw []byte) (any, int, error) {
	if len(raw) == 0 {
		return nil, 0, nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, 0, tooLarge("this document is not readable JSON")
	}

	outcome := sanitizeUntyped(decoded)
	if outcome.Truncated {
		return nil, 0, tooLarge(
			"this document is nested deeper than " + strconv.Itoa(maxSanitizeDepth) +
				" levels or holds more than " + strconv.Itoa(maxSanitizeNodes) + " values")
	}
	return outcome.Value, outcome.Dropped, nil
}

// newFoodLogDayResult maps the domain model onto the bounded result.
func newFoodLogDayResult(date string, day api.FoodLogDay) FoodLogDayResult {
	entries := day.Entries()
	out := FoodLogDayResult{Date: date, Truncated: day.EntriesTruncated()}
	out.Entries = make([]FoodLogEntryResult, 0, len(entries))
	for _, entry := range entries {
		out.Entries = append(out.Entries, FoodLogEntryResult{
			LogID:    optionalText(entry.LogID),
			MealID:   optionalInt64(entry.MealID),
			MealDate: optionalText(entry.MealDate),
		})
	}
	out.Count = len(out.Entries)
	return out
}
