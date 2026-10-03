package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetNutritionSummaryBetweenDates is the upstream compatibility name.
const ToolGetNutritionSummaryBetweenDates = "get_nutrition_summary_between_dates"

// MaxNutritionSummaryDays is Garmin's own window limit for the range endpoint.
// Source: nutrition.py:120.
const MaxNutritionSummaryDays = 61

// NutritionSummaryRange is the per-day intake totals over an inclusive window. It is
// health data: never log it.
type NutritionSummaryRange struct {
	StartDate string               `json:"start_date" jsonschema:"the inclusive first day, YYYY-MM-DD"`
	EndDate   string               `json:"end_date" jsonschema:"the inclusive last day, YYYY-MM-DD"`
	Days      []NutritionDayTotals `json:"days" jsonschema:"one entry per day Garmin returned"`
	Count     int                  `json:"count" jsonschema:"how many days this result carries"`
	Truncated bool                 `json:"truncated" jsonschema:"whether Garmin returned more days than the window holds"`
}

// NutritionDayTotals is one day's intake totals.
type NutritionDayTotals struct {
	Date      *string  `json:"date,omitempty" jsonschema:"the calendar day, YYYY-MM-DD"`
	Calories  *float64 `json:"calories,omitempty" jsonschema:"the day's logged calories"`
	Carbs     *float64 `json:"carbs,omitempty" jsonschema:"the day's logged carbohydrates in grams"`
	Protein   *float64 `json:"protein,omitempty" jsonschema:"the day's logged protein in grams"`
	Fat       *float64 `json:"fat,omitempty" jsonschema:"the day's logged fat in grams"`
	ItemCount int      `json:"item_count" jsonschema:"how many food items the day logged; 0 means nothing was logged"`
}

// LogValue reports the shape of the window, never a reading.
func (r NutritionSummaryRange) LogValue() slog.Value {
	return shape("nutritionSummaryRange", slog.Int("days", r.Count), slog.Bool("truncated", r.Truncated))
}

// nutritionSummaryRangeInput is the strict argument set: an inclusive window.
type nutritionSummaryRangeInput struct {
	StartDate string `json:"start_date" jsonschema:"the inclusive first day, YYYY-MM-DD"`
	EndDate   string `json:"end_date" jsonschema:"the inclusive last day, YYYY-MM-DD"`
}

func getNutritionSummaryBetweenDatesContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetNutritionSummaryBetweenDates,
			Title: "Get nutrition totals for a window",
			Description: "read the per-day nutrition totals (calories, carbs, protein, fat) " +
				"and the logged item count for every day of an inclusive window. A day " +
				"with item_count 0 logged nothing, and a low item_count can mean a " +
				"partly logged day: exclude such days before averaging intake",
			Tier:        policy.TierReadOnly,
			Category:    categoryNutrition,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(trendWindowProperties(MaxNutritionSummaryDays, reasonEndpointLimits)...),
	}
}

// registerGetNutritionSummaryBetweenDates registers the tool.
func registerGetNutritionSummaryBetweenDates(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in nutritionSummaryRangeInput) (
		*mcp.CallToolResult, NutritionSummaryRange, error,
	) {
		out, err := svc.readNutritionSummaryRange(ctx, in)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getNutritionSummaryBetweenDatesContract().Registration(), handler)
}

// readNutritionSummaryRange validates the window, reads it and bounds the list.
func (s *service) readNutritionSummaryRange(
	ctx context.Context, in nutritionSummaryRangeInput,
) (NutritionSummaryRange, error) {
	span, err := parseCappedWindow(in.StartDate, in.EndDate, s.limits, MaxNutritionSummaryDays)
	if err != nil {
		return NutritionSummaryRange{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return NutritionSummaryRange{}, err
	}
	summaries, err := s.nutrition.SummaryRange(ctx, session, span)
	if err != nil {
		return NutritionSummaryRange{}, fail(err)
	}

	truncated := len(summaries) > span.Days()
	if truncated {
		summaries = summaries[:span.Days()]
	}
	days := make([]NutritionDayTotals, 0, len(summaries))
	for _, day := range summaries {
		days = append(days, newNutritionDayTotals(day))
	}
	return NutritionSummaryRange{
		StartDate: span.Start().String(),
		EndDate:   span.End().String(),
		Days:      days,
		Count:     len(days),
		Truncated: truncated,
	}, nil
}

// newNutritionDayTotals maps one day of the summary onto the result.
func newNutritionDayTotals(day api.NutritionDaySummary) NutritionDayTotals {
	return NutritionDayTotals{
		Date:      optionalText(day.MealDate),
		Calories:  optionalFloat(day.Content.Calories),
		Carbs:     optionalFloat(day.Content.Carbs),
		Protein:   optionalFloat(day.Content.Protein),
		Fat:       optionalFloat(day.Content.Fat),
		ItemCount: day.ItemCount(),
	}
}
