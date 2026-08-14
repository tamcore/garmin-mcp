package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetUserSummary is the upstream compatibility name of the daily summary tool.
const ToolGetUserSummary = "get_user_summary"

// DailySummary is one day of activity and wellness totals.
//
// It is health data — never log it, never cache it. Garmin can withhold the document
// for a session it does not trust; that arrives as an authentication failure, and the
// tool reports it as one rather than as an empty day.
type DailySummary struct {
	// Date is the calendar day that was requested.
	Date string `json:"date" jsonschema:"the calendar day, YYYY-MM-DD"`

	TotalSteps          *int     `json:"total_steps,omitempty" jsonschema:"steps taken"`
	TotalDistanceMeters *float64 `json:"total_distance_meters,omitempty" jsonschema:"distance covered"`
	TotalCalories       *float64 `json:"total_calories,omitempty" jsonschema:"total kilocalories"`
	ActiveCalories      *float64 `json:"active_calories,omitempty" jsonschema:"active kilocalories"`
	RestingHeartRate    *int     `json:"resting_heart_rate,omitempty" jsonschema:"resting heart rate in bpm"`
	MinHeartRate        *int     `json:"min_heart_rate,omitempty" jsonschema:"minimum heart rate in bpm"`
	MaxHeartRate        *int     `json:"max_heart_rate,omitempty" jsonschema:"maximum heart rate in bpm"`
	AverageStressLevel  *int     `json:"average_stress_level,omitempty" jsonschema:"average stress level"`
	BodyBatteryHighest  *int     `json:"body_battery_highest,omitempty" jsonschema:"highest body battery"`
	BodyBatteryLowest   *int     `json:"body_battery_lowest,omitempty" jsonschema:"lowest body battery"`
	FloorsAscended      *float64 `json:"floors_ascended,omitempty" jsonschema:"floors climbed"`
}

// LogValue reports the shape of the day, never a single health value.
func (s DailySummary) LogValue() slog.Value {
	return shape("dailySummary",
		slog.String("totalSteps", presence(s.TotalSteps != nil)),
		slog.String("restingHeartRate", presence(s.RestingHeartRate != nil)),
		slog.String("stress", presence(s.AverageStressLevel != nil)),
	)
}

// getUserSummaryInput is the strict argument set: one calendar day.
type getUserSummaryInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getUserSummaryContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetUserSummary,
			Title: "Get daily summary",
			Description: "read one calendar day of the account's totals: steps, distance, " +
				"calories, resting and extreme heart rates, average stress and body battery",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetUserSummary registers the tool.
func registerGetUserSummary(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getUserSummaryInput) (
		*mcp.CallToolResult, DailySummary, error,
	) {
		read, err := svc.resolveDailyRead(ctx, in.Date)
		if err != nil {
			return nil, DailySummary{}, err
		}
		summary, err := svc.wellness.UserSummary(ctx, read.session, read.name, read.date)
		if err != nil {
			return nil, DailySummary{}, fail(err)
		}
		return nil, newDailySummary(read.date.String(), summary), nil
	}
	return mcpserver.AddTool(registry, getUserSummaryContract().Registration(), handler)
}

// newDailySummary maps the domain model onto the bounded result. The profile
// identifier the payload carries is dropped: the caller already knows whose account
// this is, and repeating it only widens what a leak would expose.
func newDailySummary(date string, summary api.UserSummary) DailySummary {
	return DailySummary{
		Date:                date,
		TotalSteps:          optionalInt(summary.TotalSteps),
		TotalDistanceMeters: optionalFloat(summary.TotalDistanceMeters),
		TotalCalories:       optionalFloat(summary.TotalKilocalories),
		ActiveCalories:      optionalFloat(summary.ActiveKilocalories),
		RestingHeartRate:    optionalInt(summary.RestingHeartRate),
		MinHeartRate:        optionalInt(summary.MinHeartRate),
		MaxHeartRate:        optionalInt(summary.MaxHeartRate),
		AverageStressLevel:  optionalInt(summary.AverageStressLevel),
		BodyBatteryHighest:  optionalInt(summary.BodyBatteryHighest),
		BodyBatteryLowest:   optionalInt(summary.BodyBatteryLowest),
		FloorsAscended:      optionalFloat(summary.FloorsAscended),
	}
}
