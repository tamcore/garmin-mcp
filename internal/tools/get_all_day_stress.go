package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetAllDayStress is the upstream compatibility name of the all-day stress tool.
const ToolGetAllDayStress = "get_all_day_stress"

// AllDayStress is the day-level view of the stress document.
//
// It is health data — never log it, never cache it. It is the third of the three views
// of one read: the same URL as get_stress_data and get_stress_summary, reduced to the
// day's own figures and to how much of the day was actually measured.
type AllDayStress struct {
	Date    string `json:"date" jsonschema:"the calendar day, YYYY-MM-DD"`
	HasData bool   `json:"has_data" jsonschema:"whether Garmin held stress data for the day"`

	MaxStressLevel     *int `json:"max_stress_level,omitempty" jsonschema:"the day's highest stress level"`
	AverageStressLevel *int `json:"average_stress_level,omitempty" jsonschema:"the day's average stress level"`

	// SampleCount is every element Garmin recorded for the day, gaps included.
	SampleCount int `json:"sample_count" jsonschema:"elements Garmin recorded for the day"`

	// UsableSampleCount is how many of those elements are readings. A gap and an
	// activity window are marked with a negative level and are not readings.
	UsableSampleCount int `json:"usable_sample_count" jsonschema:"how many of those elements are readings"`
}

// LogValue reports the shape of the day, never a reading.
//
// The two counts are not the same kind of fact. SampleCount is the retained length —
// every element Garmin sent, gaps included — which is shape and is logged.
// UsableSampleCount is derived by classifying each element's value, so it reports how
// much of the day was actually measured rather than how much came back, and it stays
// out of the log for the same reason a count of positive readings does.
func (s AllDayStress) LogValue() slog.Value {
	return shape("allDayStress",
		slog.Bool("hasData", s.HasData),
		slog.Int("samples", s.SampleCount),
		slog.String("maxLevel", presence(s.MaxStressLevel != nil)),
	)
}

// getAllDayStressInput is the strict argument set: one calendar day.
type getAllDayStressInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getAllDayStressContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetAllDayStress,
			Title: "Get all-day stress",
			Description: "read the day-level view of one calendar day of stress: the " +
				"highest and average level, and how much of the day Garmin actually " +
				"measured. No series is returned; call get_stress_data for the samples " +
				"or get_stress_summary for the distribution",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetAllDayStress registers the tool.
func registerGetAllDayStress(registry *mcpserver.Registry, svc *service) error {
	stress, err := stressClient(svc)
	if err != nil {
		return err
	}
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getAllDayStressInput) (
		*mcp.CallToolResult, AllDayStress, error,
	) {
		day, session, err := svc.resolveStressDay(ctx, in.Date)
		if err != nil {
			return nil, AllDayStress{}, err
		}
		read, err := stress.DailyStress(ctx, session, day, api.StressViewAllDay)
		if err != nil {
			return nil, AllDayStress{}, fail(err)
		}
		return nil, newAllDayStress(day.String(), read), nil
	}
	return mcpserver.AddTool(registry, getAllDayStressContract().Registration(), handler)
}

// newAllDayStress reduces the day to its own figures and its measured coverage.
func newAllDayStress(date string, day api.DailyStress) AllDayStress {
	distribution := day.Distribution()
	return AllDayStress{
		Date:               date,
		HasData:            !day.IsEmpty(),
		MaxStressLevel:     optionalInt(day.MaxStressLevel),
		AverageStressLevel: optionalInt(day.AvgStressLevel),
		SampleCount:        distribution.Samples,
		UsableSampleCount:  distribution.Valid,
	}
}
