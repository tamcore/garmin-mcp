package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetHeartRates is the upstream compatibility name of the intraday heart-rate
// tool.
const ToolGetHeartRates = "get_heart_rates"

// DefaultMaxHeartRateSamples bounds the retained intraday series.
//
// One observed day is roughly 700 pairs at about 120 s spacing, so this leaves room
// for a device that samples every minute and still refuses to hand a model an
// unbounded series. A series past the bound is cut and the cut is reported.
const DefaultMaxHeartRateSamples = 1440

// A HeartRateSample is one point of the intraday series.
//
// Both fields are optional because a real day carries points where the watch recorded
// nothing. Nothing may assume a fixed cadence or a contiguous range: the pair after a
// gap can jump by an hour.
type HeartRateSample struct {
	TimeGMTMillis *int64   `json:"time_gmt_millis,omitempty" jsonschema:"the sample instant, a UTC epoch in ms"`
	HeartRateBPM  *float64 `json:"heart_rate_bpm,omitempty" jsonschema:"beats per minute, absent for a gap"`
}

// HeartRates is one day of heart rate with its intraday series.
//
// It is among the most sensitive data this server returns. Never log it, never cache
// it. The account identifier the document carries is deliberately absent from this
// model.
type HeartRates struct {
	Date    string `json:"date" jsonschema:"the calendar day that was requested, YYYY-MM-DD"`
	HasData bool   `json:"has_data" jsonschema:"whether Garmin held heart-rate data for the day"`

	MaxBPM                 *float64 `json:"max_heart_rate_bpm,omitempty" jsonschema:"the day's highest reading"`
	MinBPM                 *float64 `json:"min_heart_rate_bpm,omitempty" jsonschema:"the day's lowest reading"`
	RestingBPM             *float64 `json:"resting_heart_rate_bpm,omitempty" jsonschema:"the day's resting heart rate"`
	Last7DaysAvgRestingBPM *float64 `json:"last_7_days_avg_resting_hr,omitempty" jsonschema:"the 7-day average resting"`

	StartGMT *string `json:"start_gmt,omitempty" jsonschema:"the start of the measured window"`
	EndGMT   *string `json:"end_gmt,omitempty" jsonschema:"the end of the measured window"`

	Samples     []HeartRateSample `json:"samples" jsonschema:"the intraday series, oldest first"`
	SampleCount int               `json:"sample_count" jsonschema:"how many samples this result carries"`
	Truncated   bool              `json:"truncated" jsonschema:"whether the series was cut at this server's bound"`
}

// LogValue reports the shape of the day and never a reading.
func (h HeartRates) LogValue() slog.Value {
	return shape("heartRates",
		slog.Bool("hasData", h.HasData),
		slog.Int("samples", h.SampleCount),
		slog.Bool("truncated", h.Truncated),
	)
}

// getHeartRatesInput is the strict argument set: one calendar day.
type getHeartRatesInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getHeartRatesContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetHeartRates,
			Title: "Get heart rates",
			Description: "read one calendar day of the account's heart rate: the day's " +
				"maximum, minimum and resting rate, the seven-day average resting rate, " +
				"and the intraday series. The series is bounded and a cut is reported",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetHeartRates registers the tool.
func registerGetHeartRates(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getHeartRatesInput) (
		*mcp.CallToolResult, HeartRates, error,
	) {
		out, err := svc.readHeartRates(ctx, in.Date)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getHeartRatesContract().Registration(), handler)
}

// readHeartRates performs the read behind the tool.
func (s *service) readHeartRates(ctx context.Context, date string) (HeartRates, error) {
	read, err := s.resolveDailyRead(ctx, date)
	if err != nil {
		return HeartRates{}, err
	}
	day, err := s.wellness.Cardio().HeartRates(ctx, read.session, read.name, read.date)
	if err != nil {
		return HeartRates{}, fail(err)
	}
	samples, err := day.Samples()
	if err != nil {
		return HeartRates{}, fail(err)
	}
	return newHeartRates(read.date.String(), day, samples), nil
}

// newHeartRates maps the domain model onto the curated result. The date is the day
// that was asked for, not the one the payload echoes, so a caller always knows what it
// got.
func newHeartRates(date string, day api.DailyHeartRate, samples []api.Sample) HeartRates {
	kept, truncated := api.BoundSamples(samples, DefaultMaxHeartRateSamples)

	out := HeartRates{
		Date:                   date,
		MaxBPM:                 optionalFloat(day.MaxHeartRate),
		MinBPM:                 optionalFloat(day.MinHeartRate),
		RestingBPM:             optionalFloat(day.RestingHeartRate),
		Last7DaysAvgRestingBPM: optionalFloat(day.LastSevenDaysAvgRestingHeartRate),
		StartGMT:               optionalText(day.StartTimestampGMT),
		EndGMT:                 optionalText(day.EndTimestampGMT),
		Samples:                newHeartRateSamples(kept),
		Truncated:              truncated,
	}
	out.SampleCount = len(out.Samples)
	out.HasData = out.SampleCount > 0 || out.MaxBPM != nil || out.MinBPM != nil ||
		out.RestingBPM != nil
	return out
}

// newHeartRateSamples renders the series, keeping a point whose reading is absent: a
// gap is information, and dropping it would imply a denser day than there was.
func newHeartRateSamples(samples []api.Sample) []HeartRateSample {
	out := make([]HeartRateSample, 0, len(samples))
	for _, sample := range samples {
		out = append(out, HeartRateSample{
			TimeGMTMillis: optionalInt64(sample.TimeMillis),
			HeartRateBPM:  optionalFloat(sample.Value),
		})
	}
	return out
}
