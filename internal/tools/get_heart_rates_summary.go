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

// ToolGetHeartRatesSummary is the upstream compatibility name of the compact
// heart-rate tool.
const ToolGetHeartRatesSummary = "get_heart_rates_summary"

// HeartRateSummary is one day of heart rate without the series.
//
// It reads the same Garmin document as get_heart_rates and keeps the scalars, so the
// two tools are one read with two views rather than two requests. It is health data:
// never log it.
type HeartRateSummary struct {
	Date    string `json:"date" jsonschema:"the calendar day that was requested, YYYY-MM-DD"`
	HasData bool   `json:"has_data" jsonschema:"whether Garmin held heart-rate data for the day"`

	MaxBPM                 *float64 `json:"max_heart_rate_bpm,omitempty" jsonschema:"the day's highest reading"`
	MinBPM                 *float64 `json:"min_heart_rate_bpm,omitempty" jsonschema:"the day's lowest reading"`
	RestingBPM             *float64 `json:"resting_heart_rate_bpm,omitempty" jsonschema:"the day's resting heart rate"`
	Last7DaysAvgRestingBPM *float64 `json:"last_7_days_avg_resting_hr,omitempty" jsonschema:"the 7-day average resting"`

	AverageBPM *float64 `json:"avg_heart_rate_bpm,omitempty" jsonschema:"the mean of the day's readings"`
	DataPoints int      `json:"data_points_count" jsonschema:"how many readings the average was computed from"`
}

// LogValue reports the shape of the day and never a reading.
//
// DataPoints is deliberately absent. It counts the samples that were present and
// strictly positive, not the samples that came back, so it is a measure of how long the
// account wore the device rather than of what this server handled. The presence of the
// average already says whether any reading was usable.
func (h HeartRateSummary) LogValue() slog.Value {
	return shape("heartRateSummary",
		slog.Bool("hasData", h.HasData),
		slog.String("average", presence(h.AverageBPM != nil)),
	)
}

// getHeartRatesSummaryInput is the strict argument set: one calendar day.
type getHeartRatesSummaryInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getHeartRatesSummaryContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetHeartRatesSummary,
			Title: "Get heart rate summary",
			Description: "read one calendar day of the account's heart rate as a compact " +
				"summary: the day's maximum, minimum and resting rate, the seven-day " +
				"average resting rate, and the mean of the intraday readings. The series " +
				"itself is not returned",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetHeartRatesSummary registers the tool.
func registerGetHeartRatesSummary(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getHeartRatesSummaryInput) (
		*mcp.CallToolResult, HeartRateSummary, error,
	) {
		out, err := svc.readHeartRatesSummary(ctx, in.Date)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getHeartRatesSummaryContract().Registration(), handler)
}

// readHeartRatesSummary performs the read behind the tool.
func (s *service) readHeartRatesSummary(
	ctx context.Context, date string,
) (HeartRateSummary, error) {
	read, err := s.resolveDailyRead(ctx, date)
	if err != nil {
		return HeartRateSummary{}, err
	}
	day, err := s.wellness.Cardio().HeartRatesSummary(ctx, read.session, read.name, read.date)
	if err != nil {
		return HeartRateSummary{}, fail(err)
	}
	samples, err := day.Samples()
	if err != nil {
		return HeartRateSummary{}, fail(err)
	}
	return newHeartRateSummary(read.date.String(), day, samples), nil
}

// newHeartRateSummary maps the domain model onto the compact result.
func newHeartRateSummary(
	date string, day api.DailyHeartRate, samples []api.Sample,
) HeartRateSummary {
	out := HeartRateSummary{
		Date:                   date,
		MaxBPM:                 optionalFloat(day.MaxHeartRate),
		MinBPM:                 optionalFloat(day.MinHeartRate),
		RestingBPM:             optionalFloat(day.RestingHeartRate),
		Last7DaysAvgRestingBPM: optionalFloat(day.LastSevenDaysAvgRestingHeartRate),
	}
	out.AverageBPM, out.DataPoints = meanSampleValue(samples)
	out.HasData = out.DataPoints > 0 || out.MaxBPM != nil || out.MinBPM != nil ||
		out.RestingBPM != nil
	return out
}

// meanSampleValue averages the present, positive readings of a series and reports how
// many there were.
//
// A gap contributes nothing rather than a zero, which is what upstream does too: a
// mean that counted the minutes the watch was off the wrist would read as a resting
// day. The result is rounded to one decimal, which is the precision of the reading.
func meanSampleValue(samples []api.Sample) (*float64, int) {
	var total float64
	count := 0
	for _, sample := range samples {
		value, ok := sample.Value.Float64()
		if !ok || value <= 0 {
			continue
		}
		total += value
		count++
	}
	if count == 0 {
		return nil, 0
	}
	mean := math.Round(total/float64(count)*10) / 10
	return &mean, count
}
