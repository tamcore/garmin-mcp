package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetSpO2Data is the upstream compatibility name of the pulse-ox tool.
const ToolGetSpO2Data = "get_spo2_data"

// DefaultMaxSpO2HourlyAverages bounds the retained hourly series. A day has 24 hours,
// so the ceiling is generous on purpose: it bounds a document that carries more than
// it should without cutting an ordinary one.
const DefaultMaxSpO2HourlyAverages = 48

// A SpO2HourlyAverage is one hour of the averaged pulse-ox series. Both fields are
// optional: an hour with no measurement carries no reading.
type SpO2HourlyAverage struct {
	TimeGMTMillis *int64   `json:"time_gmt_millis,omitempty" jsonschema:"the hour, a UTC epoch in ms"`
	SpO2Percent   *float64 `json:"spo2_percent,omitempty" jsonschema:"the hour's average percentage"`
}

// SpO2 is one day of pulse oximetry.
//
// It is health data: never log it, never cache it.
type SpO2 struct {
	Date    string `json:"date" jsonschema:"the calendar day that was requested, YYYY-MM-DD"`
	HasData bool   `json:"has_data" jsonschema:"whether Garmin held pulse-ox data for the day"`

	AveragePercent   *float64 `json:"avg_spo2_percent,omitempty" jsonschema:"the day's average blood-oxygen percentage"`
	LowestPercent    *float64 `json:"lowest_spo2_percent,omitempty" jsonschema:"the day's lowest reading"`
	LatestPercent    *float64 `json:"latest_spo2_percent,omitempty" jsonschema:"the most recent reading of the day"`
	LatestReadingGMT *string  `json:"latest_reading_gmt,omitempty" jsonschema:"when the latest reading was taken"`
	Last7DaysAvg     *float64 `json:"last_7_days_avg_spo2,omitempty" jsonschema:"the seven-day average"`
	AvgSleepPercent  *float64 `json:"avg_sleep_spo2_percent,omitempty" jsonschema:"the average measured during sleep"`

	HourlyAverages     []SpO2HourlyAverage `json:"hourly_averages" jsonschema:"the hourly averages, oldest first"`
	HourlyAverageCount int                 `json:"hourly_average_count" jsonschema:"how many hourly averages are carried"`
	Truncated          bool                `json:"truncated" jsonschema:"whether the hourly series was cut"`
}

// LogValue reports the shape of the day and never a reading.
func (s SpO2) LogValue() slog.Value {
	return shape("spo2",
		slog.Bool("hasData", s.HasData),
		slog.Int("hourlyAverages", s.HourlyAverageCount),
		slog.Bool("truncated", s.Truncated),
	)
}

// getSpO2DataInput is the strict argument set: one calendar day.
type getSpO2DataInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getSpO2DataContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetSpO2Data,
			Title: "Get SpO2 data",
			Description: "read one calendar day of the account's blood oxygen: the day's " +
				"average, lowest and latest reading, the seven-day average, the average " +
				"measured during sleep, and the hourly averages",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetSpO2Data registers the tool.
func registerGetSpO2Data(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getSpO2DataInput) (
		*mcp.CallToolResult, SpO2, error,
	) {
		out, err := svc.readSpO2(ctx, in.Date)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getSpO2DataContract().Registration(), handler)
}

// readSpO2 performs the read behind the tool.
func (s *service) readSpO2(ctx context.Context, date string) (SpO2, error) {
	day, session, err := s.resolveDateOnlyRead(ctx, date)
	if err != nil {
		return SpO2{}, err
	}
	document, err := s.wellness.Cardio().SpO2(ctx, session, day)
	if err != nil {
		return SpO2{}, fail(err)
	}
	hourly, err := document.HourlyAverages()
	if err != nil {
		return SpO2{}, fail(err)
	}
	return newSpO2(day.String(), document, hourly), nil
}

// newSpO2 maps the domain model onto the curated result.
func newSpO2(date string, day api.DailySpO2, hourly []api.Sample) SpO2 {
	kept, truncated := api.BoundSamples(hourly, DefaultMaxSpO2HourlyAverages)

	out := SpO2{
		Date:             date,
		AveragePercent:   optionalFloat(day.AverageSpO2),
		LowestPercent:    optionalFloat(day.LowestSpO2),
		LatestPercent:    optionalFloat(day.LatestSpO2),
		LatestReadingGMT: optionalText(day.LatestSpO2TimestampGMT),
		Last7DaysAvg:     optionalFloat(day.LastSevenDaysAvgSpO2),
		AvgSleepPercent:  optionalFloat(day.AvgSleepSpO2),
		HourlyAverages:   newSpO2HourlyAverages(kept),
		Truncated:        truncated,
	}
	out.HourlyAverageCount = len(out.HourlyAverages)
	out.HasData = out.HourlyAverageCount > 0 || out.AveragePercent != nil ||
		out.LowestPercent != nil || out.LatestPercent != nil || out.AvgSleepPercent != nil
	return out
}

// newSpO2HourlyAverages renders the hourly series, keeping an hour with no reading.
func newSpO2HourlyAverages(samples []api.Sample) []SpO2HourlyAverage {
	out := make([]SpO2HourlyAverage, 0, len(samples))
	for _, sample := range samples {
		out = append(out, SpO2HourlyAverage{
			TimeGMTMillis: optionalInt64(sample.TimeMillis),
			SpO2Percent:   optionalFloat(sample.Value),
		})
	}
	return out
}
