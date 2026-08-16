package tools

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// Bounds on a nutrition-goal write. Garmin's own goal UI does not accept a
// negative or an absurd target, so these are headroom over any real diet, chosen
// to keep a hostile or malformed goal like -9000000000 from ever reaching the
// account rather than pinned to an observed Garmin limit. Every goal shares the
// same floor of zero: none of the four is meaningfully negative.
const (
	minNutritionGoal = 0
	maxCalorieGoal   = 20000
	maxMacroGrams    = 2000
)

// The upstream compatibility names of the two nutrition-settings tools.
const (
	ToolGetNutritionDailySettings = "get_nutrition_daily_settings"
	ToolSetNutritionDailySettings = "set_nutrition_daily_settings"
)

// NutritionSettingsResult is one day's nutrition-goal document.
//
// Only the four fields internal/garmin/api's NutritionSettings models are carried:
// activeDailyCalories, activeDailyCarbohydrateGrams, activeDailyFatGrams and
// activeDailyProteinGrams (nutrition.py:135-138). Every other field the document may
// carry is unmodeled upstream too, so nothing else is invented here.
type NutritionSettingsResult struct {
	Date         string `json:"date" jsonschema:"the day this goal document applies to, YYYY-MM-DD"`
	CalorieGoal  *int64 `json:"calorie_goal,omitempty" jsonschema:"the daily calorie target in kcal"`
	CarbsGrams   *int64 `json:"carbs_grams,omitempty" jsonschema:"the daily carbohydrate target in grams"`
	FatGrams     *int64 `json:"fat_grams,omitempty" jsonschema:"the daily fat target in grams"`
	ProteinGrams *int64 `json:"protein_grams,omitempty" jsonschema:"the daily protein target in grams"`
}

// LogValue reports which goals arrived, never a target figure.
func (r NutritionSettingsResult) LogValue() slog.Value {
	return shape("nutritionSettings",
		slog.String("calorieGoal", presence(r.CalorieGoal != nil)),
		slog.String("carbsGrams", presence(r.CarbsGrams != nil)),
		slog.String("fatGrams", presence(r.FatGrams != nil)),
		slog.String("proteinGrams", presence(r.ProteinGrams != nil)),
	)
}

// newNutritionSettingsResult maps the domain model onto the result.
func newNutritionSettingsResult(date string, settings api.NutritionSettings) NutritionSettingsResult {
	return NutritionSettingsResult{
		Date:         date,
		CalorieGoal:  optionalInt64(settings.CalorieGoal),
		CarbsGrams:   optionalInt64(settings.CarbsGrams),
		FatGrams:     optionalInt64(settings.FatGrams),
		ProteinGrams: optionalInt64(settings.ProteinGrams),
	}
}

// getNutritionDailySettingsInput is the strict argument set: one day.
type getNutritionDailySettingsInput struct {
	Date string `json:"date" jsonschema:"the day to read, YYYY-MM-DD"`
}

func getNutritionDailySettingsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetNutritionDailySettings,
			Title: "Get the daily nutrition goals",
			Description: "read the account's nutrition goals and targets for one day: the " +
				"calorie target and the carbohydrate, fat and protein targets in grams",
			Tier:        policy.TierReadOnly,
			Category:    categoryNutrition,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the day to read")),
	}
}

// registerGetNutritionDailySettings registers the tool.
func registerGetNutritionDailySettings(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getNutritionDailySettingsInput) (
		*mcp.CallToolResult, NutritionSettingsResult, error,
	) {
		out, err := svc.readNutritionDailySettings(ctx, in.Date)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getNutritionDailySettingsContract().Registration(), handler)
}

// readNutritionDailySettings performs the read behind the tool.
func (s *service) readNutritionDailySettings(ctx context.Context, date string) (NutritionSettingsResult, error) {
	day, err := parseCalendarDate("date", date)
	if err != nil {
		return NutritionSettingsResult{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return NutritionSettingsResult{}, err
	}
	read, err := s.nutrition.Settings(ctx, session, day)
	if err != nil {
		return NutritionSettingsResult{}, fail(err)
	}
	return newNutritionSettingsResult(day.String(), read), nil
}

// setNutritionDailySettingsInput is the strict argument set. Every goal is optional
// and nullable: an absent field leaves Garmin's existing value untouched, matching
// set_nutrition_daily_settings' read-modify-write description (nutrition.py:90-139).
type setNutritionDailySettingsInput struct {
	Date         string `json:"date" jsonschema:"the day to update, YYYY-MM-DD"`
	CalorieGoal  *int64 `json:"calorie_goal,omitempty" jsonschema:"the daily calorie target in kcal"`
	CarbsGrams   *int64 `json:"carbs_grams,omitempty" jsonschema:"the daily carbohydrate target in grams"`
	FatGrams     *int64 `json:"fat_grams,omitempty" jsonschema:"the daily fat target in grams"`
	ProteinGrams *int64 `json:"protein_grams,omitempty" jsonschema:"the daily protein target in grams"`
}

// SetNutritionSettingsResult is what set_nutrition_daily_settings reports: the
// updated goals plus a status word, matching the manifest's staticTopLevelKeys
// (calorie_goal, carbs_grams, date, fat_grams, protein_grams, status).
type SetNutritionSettingsResult struct {
	Status       string `json:"status" jsonschema:"always updated, once the write is accepted"`
	Date         string `json:"date" jsonschema:"the day that was updated, YYYY-MM-DD"`
	CalorieGoal  *int64 `json:"calorie_goal,omitempty" jsonschema:"the daily calorie target in kcal, updated"`
	CarbsGrams   *int64 `json:"carbs_grams,omitempty" jsonschema:"the daily carbohydrate target in grams, updated"`
	FatGrams     *int64 `json:"fat_grams,omitempty" jsonschema:"the daily fat target in grams, updated"`
	ProteinGrams *int64 `json:"protein_grams,omitempty" jsonschema:"the daily protein target in grams, updated"`
}

// LogValue reports which goals arrived, never a target figure.
func (r SetNutritionSettingsResult) LogValue() slog.Value {
	return shape("setNutritionSettings",
		slog.String("calorieGoal", presence(r.CalorieGoal != nil)),
		slog.String("carbsGrams", presence(r.CarbsGrams != nil)),
		slog.String("fatGrams", presence(r.FatGrams != nil)),
		slog.String("proteinGrams", presence(r.ProteinGrams != nil)),
	)
}

// nullableIntegerProperty declares a nullable optional integer argument, bounded to
// [minNutritionGoal, maximum]: absent or null leaves the current value untouched.
func nullableIntegerProperty(name, description string, maximum float64) Property {
	return Property{
		Name:        name,
		Types:       []string{typeInteger},
		Description: description,
		Minimum:     bound(minNutritionGoal),
		Maximum:     bound(maximum),
		Nullable:    true,
	}
}

func setNutritionDailySettingsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolSetNutritionDailySettings,
			Title: "Set the daily nutrition goals",
			Description: "update one or more of the account's daily nutrition goals: the " +
				"calorie target and the carbohydrate, fat and protein targets in grams. " +
				"Reads the current goals, applies only the supplied overrides, and writes " +
				"the merged document back — an omitted field keeps its existing value",
			Tier:        policy.TierWrite,
			Category:    categoryNutrition,
			Annotations: writeAnnotations(true),
		},
		Schema: NewSchema(
			dateProperty("date", "the day to update"),
			nullableIntegerProperty("calorie_goal", "the daily calorie target in kcal", maxCalorieGoal),
			nullableIntegerProperty("carbs_grams", "the daily carbohydrate target in grams", maxMacroGrams),
			nullableIntegerProperty("fat_grams", "the daily fat target in grams", maxMacroGrams),
			nullableIntegerProperty("protein_grams", "the daily protein target in grams", maxMacroGrams),
		),
	}
}

// registerSetNutritionDailySettings registers the tool.
func registerSetNutritionDailySettings(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in setNutritionDailySettingsInput) (
		*mcp.CallToolResult, SetNutritionSettingsResult, error,
	) {
		out, err := svc.setNutritionDailySettings(ctx, in)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, setNutritionDailySettingsContract().Registration(), handler)
}

// validateNutritionGoal refuses a supplied goal outside [minNutritionGoal, maximum].
// A nil value is left untouched: this only bounds a value the caller actually
// supplied.
func validateNutritionGoal(name string, value *int64, maximum int64) error {
	if value == nil {
		return nil
	}
	if *value < minNutritionGoal || *value > maximum {
		return invalidArgument(name + " must be between " + strconv.FormatInt(minNutritionGoal, 10) +
			" and " + strconv.FormatInt(maximum, 10))
	}
	return nil
}

// setNutritionDailySettings performs the write behind the tool.
func (s *service) setNutritionDailySettings(
	ctx context.Context, in setNutritionDailySettingsInput,
) (SetNutritionSettingsResult, error) {
	day, err := parseCalendarDate("date", in.Date)
	if err != nil {
		return SetNutritionSettingsResult{}, err
	}
	if err := validateNutritionGoal("calorie_goal", in.CalorieGoal, maxCalorieGoal); err != nil {
		return SetNutritionSettingsResult{}, err
	}
	if err := validateNutritionGoal("carbs_grams", in.CarbsGrams, maxMacroGrams); err != nil {
		return SetNutritionSettingsResult{}, err
	}
	if err := validateNutritionGoal("fat_grams", in.FatGrams, maxMacroGrams); err != nil {
		return SetNutritionSettingsResult{}, err
	}
	if err := validateNutritionGoal("protein_grams", in.ProteinGrams, maxMacroGrams); err != nil {
		return SetNutritionSettingsResult{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return SetNutritionSettingsResult{}, err
	}

	update := api.NutritionSettingsUpdate{
		CalorieGoal:  in.CalorieGoal,
		CarbsGrams:   in.CarbsGrams,
		FatGrams:     in.FatGrams,
		ProteinGrams: in.ProteinGrams,
	}
	settings, err := s.nutrition.SetSettings(ctx, session, day, update)
	if err != nil {
		return SetNutritionSettingsResult{}, fail(err)
	}

	mapped := newNutritionSettingsResult(day.String(), settings)
	return SetNutritionSettingsResult{
		Status:       "updated",
		Date:         mapped.Date,
		CalorieGoal:  mapped.CalorieGoal,
		CarbsGrams:   mapped.CarbsGrams,
		FatGrams:     mapped.FatGrams,
		ProteinGrams: mapped.ProteinGrams,
	}, nil
}
