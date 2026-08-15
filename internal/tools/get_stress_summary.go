package tools

import (
	"context"
	"log/slog"
	"math"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetStressSummary is the upstream compatibility name of the compact stress tool.
const ToolGetStressSummary = "get_stress_summary"

// StressSummary is one calendar day of stress without the series.
//
// It is health data — never log it, never cache it. It is the second of the three
// views of one read: the same URL as get_stress_data, reduced to the day's figures and
// the distribution across Garmin's four bands.
type StressSummary struct {
	Date    string `json:"date" jsonschema:"the calendar day, YYYY-MM-DD"`
	HasData bool   `json:"has_data" jsonschema:"whether Garmin held stress data for the day"`

	MaxStressLevel     *int `json:"max_stress_level,omitempty" jsonschema:"the day's highest stress level"`
	AverageStressLevel *int `json:"average_stress_level,omitempty" jsonschema:"the day's average stress level"`

	RestPercent         *float64 `json:"rest_percent,omitempty" jsonschema:"share of usable readings under 26"`
	LowStressPercent    *float64 `json:"low_stress_percent,omitempty" jsonschema:"share of usable readings from 26 to 50"`
	MediumStressPercent *float64 `json:"medium_stress_percent,omitempty" jsonschema:"share of readings from 51 to 75"`
	HighStressPercent   *float64 `json:"high_stress_percent,omitempty" jsonschema:"share of readings of 76 and above"`

	// DataPointsCount is how many readings the distribution was computed from. A gap
	// and an activity window are not readings and are excluded.
	DataPointsCount int `json:"data_points_count" jsonschema:"usable readings behind the distribution"`
}

// LogValue reports the shape of the summary, never a reading and never how many
// readings there were.
//
// DataPointsCount is deliberately absent. It is not the length of what was retained:
// it counts the elements that passed a value test, so a log carrying it would report
// how much of the day the account was actually measured — a coverage record built from
// the readings themselves. The presence of the distribution is the one bit an operator
// needs, and it is here.
func (s StressSummary) LogValue() slog.Value {
	return shape("stressSummary",
		slog.Bool("hasData", s.HasData),
		slog.String("distribution", presence(s.RestPercent != nil)),
	)
}

// getStressSummaryInput is the strict argument set: one calendar day.
type getStressSummaryInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getStressSummaryContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetStressSummary,
			Title: "Get stress summary",
			Description: "read one calendar day of the account's stress as a compact " +
				"summary: the highest and average level, and how the day's readings " +
				"split across Garmin's rest, low, medium and high bands. The series " +
				"itself is not returned; call get_stress_data for that",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetStressSummary registers the tool.
func registerGetStressSummary(registry *mcpserver.Registry, svc *service) error {
	stress, err := stressClient(svc)
	if err != nil {
		return err
	}
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getStressSummaryInput) (
		*mcp.CallToolResult, StressSummary, error,
	) {
		day, session, err := svc.resolveStressDay(ctx, in.Date)
		if err != nil {
			return nil, StressSummary{}, err
		}
		read, err := stress.DailyStress(ctx, session, day, api.StressViewSummary)
		if err != nil {
			return nil, StressSummary{}, fail(err)
		}
		return nil, newStressSummary(day.String(), read), nil
	}
	return mcpserver.AddTool(registry, getStressSummaryContract().Registration(), handler)
}

// newStressSummary reduces the day to its figures and its distribution.
//
// Source: get_stress_summary, which computes the four shares over the strictly
// positive readings only and omits the distribution when the day carries none.
func newStressSummary(date string, day api.DailyStress) StressSummary {
	out := StressSummary{
		Date:               date,
		HasData:            !day.IsEmpty(),
		MaxStressLevel:     optionalInt(day.MaxStressLevel),
		AverageStressLevel: optionalInt(day.AvgStressLevel),
	}

	distribution := day.Distribution()
	out.DataPointsCount = distribution.Valid
	if distribution.Valid == 0 {
		return out
	}
	out.RestPercent = sharePercent(distribution.Rest, distribution.Valid)
	out.LowStressPercent = sharePercent(distribution.Low, distribution.Valid)
	out.MediumStressPercent = sharePercent(distribution.Medium, distribution.Valid)
	out.HighStressPercent = sharePercent(distribution.High, distribution.Valid)
	return out
}

// sharePercent renders count as a percentage of total, rounded to one decimal the way
// upstream rounds it. A zero total yields no share rather than a division by zero.
func sharePercent(count, total int) *float64 {
	if total <= 0 {
		return nil
	}
	value := math.Round(float64(count)/float64(total)*1000) / 10
	return &value
}
