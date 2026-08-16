package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetHRVData is the upstream compatibility name.
const ToolGetHRVData = "get_hrv_data"

// DefaultMaxHRVReadings bounds the intraday HRV series one call may return.
//
// The basis is the series' own cadence: upstream describes hrvReadings as "detailed
// 5-minute HRV readings", so a full 24 hours is 288 readings and this bound is two
// full days of them. A night cannot exceed it, and a document that does is truncated
// with the flag set and the full count reported, never silently shortened.
const DefaultMaxHRVReadings = 576

// HRVReading is one five-minute reading of the intraday series.
type HRVReading struct {
	Time  string   `json:"time,omitempty" jsonschema:"the local reading time as Garmin sent it"`
	HRVMs *float64 `json:"hrv_ms,omitempty" jsonschema:"the reading in milliseconds"`
}

// LogValue reports that a reading exists, never the reading.
func (r HRVReading) LogValue() slog.Value {
	return shape("hrvReading", slog.String("hrvMs", presence(r.HRVMs != nil)))
}

// HRVData is one day of heart-rate variability.
//
// Every millisecond figure here is a health reading: never log it.
type HRVData struct {
	Date    string `json:"date" jsonschema:"the calendar day that was requested, YYYY-MM-DD"`
	HasData bool   `json:"has_data" jsonschema:"whether Garmin held HRV data for the day"`

	LastNightAvgHRVMs      *float64 `json:"last_night_avg_hrv_ms,omitempty" jsonschema:"last night's average"`
	LastNight5MinHighHRVMs *float64 `json:"last_night_5min_high_hrv_ms,omitempty" jsonschema:"the 5-minute high"`
	WeeklyAvgHRVMs         *float64 `json:"weekly_avg_hrv_ms,omitempty" jsonschema:"the seven-day average"`

	BaselineBalancedLowMs   *float64 `json:"baseline_balanced_low_ms,omitempty" jsonschema:"the balanced band's floor"`
	BaselineBalancedUpperMs *float64 `json:"baseline_balanced_upper_ms,omitempty" jsonschema:"the balanced band's ceiling"`
	BaselineLowUpperMs      *float64 `json:"baseline_low_upper_ms,omitempty" jsonschema:"the low band's ceiling"`

	Status   string `json:"status,omitempty" jsonschema:"Garmin's status for the night"`
	Feedback string `json:"feedback,omitempty" jsonschema:"Garmin's feedback phrase"`

	SleepStart string `json:"sleep_start,omitempty" jsonschema:"the local start of the sleep window"`
	SleepEnd   string `json:"sleep_end,omitempty" jsonschema:"the local end of the sleep window"`

	Readings          []HRVReading `json:"hrv_readings,omitempty" jsonschema:"the intraday series, when asked for"`
	ReadingsCount     int          `json:"readings_count,omitempty" jsonschema:"how many readings Garmin sent"`
	ReadingsTruncated bool         `json:"readings_truncated,omitempty" jsonschema:"whether the series was cut"`
}

// LogValue reports the shape of the day and never a reading.
func (d HRVData) LogValue() slog.Value {
	return shape("hrvData",
		slog.Bool("hasData", d.HasData),
		slog.Int("readings", len(d.Readings)),
		slog.Bool("truncated", d.ReadingsTruncated),
	)
}

// getHRVDataInput is the strict argument set.
type getHRVDataInput struct {
	Date             string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
	ReturnTimeseries *bool  `json:"return_timeseries,omitempty" jsonschema:"whether to include the five-minute series"`
}

func getHRVDataContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetHRVData,
			Title: "Get heart-rate variability",
			Description: "read one calendar day of the account's heart-rate variability: " +
				"last night's average and five-minute high, the seven-day average, the " +
				"personal baseline band and Garmin's status. The five-minute series is " +
				"returned only when it is asked for, and is bounded",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(
			dateProperty("date", "the calendar day to read"),
			Property{
				Name:        "return_timeseries",
				Types:       []string{typeBoolean},
				Description: "whether to include the detailed five-minute readings, which are large",
				Default:     false,
			},
		),
	}
}

// registerGetHRVData registers the tool.
func registerGetHRVData(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getHRVDataInput) (
		*mcp.CallToolResult, HRVData, error,
	) {
		withSeries := in.ReturnTimeseries != nil && *in.ReturnTimeseries
		out, err := svc.readHRVData(ctx, in.Date, withSeries)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getHRVDataContract().Registration(), handler)
}

// readHRVData performs the read behind the tool.
func (s *service) readHRVData(
	ctx context.Context, date string, withSeries bool,
) (HRVData, error) {
	day, err := parseCalendarDate("date", date)
	if err != nil {
		return HRVData{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return HRVData{}, err
	}

	document, err := s.trends().HRV(ctx, session, day)
	if err != nil {
		return HRVData{}, fail(err)
	}
	return newHRVData(day.String(), document, withSeries), nil
}

// newHRVData maps the domain model onto the bounded result.
func newHRVData(date string, document api.HRVDay, withSeries bool) HRVData {
	out := HRVData{
		Date:       date,
		SleepStart: textOrEmpty(document.SleepStartTimestampLocal),
		SleepEnd:   textOrEmpty(document.SleepEndTimestampLocal),
	}
	if summary := document.Summary; summary != nil {
		out.LastNightAvgHRVMs = optionalFloat(summary.NightAverage())
		out.LastNight5MinHighHRVMs = optionalFloat(summary.LastNight5MinHigh)
		out.WeeklyAvgHRVMs = optionalFloat(summary.WeeklyAvg)
		out.Status = textOrEmpty(summary.Status)
		out.Feedback = textOrEmpty(summary.FeedbackPhrase)
		if baseline := summary.Baseline; baseline != nil {
			out.BaselineBalancedLowMs = optionalFloat(baseline.BalancedLow)
			out.BaselineBalancedUpperMs = optionalFloat(baseline.BalancedUpper)
			out.BaselineLowUpperMs = optionalFloat(baseline.LowUpper)
		}
		if summary.CalendarDate != nil && *summary.CalendarDate != "" {
			out.Date = *summary.CalendarDate
		}
	}
	// has_data answers "did Garmin hold anything for this day", so every field this
	// result can carry counts. Testing only the three night averages reported false
	// beside a populated baseline, status or sleep window, which contradicted the
	// answer it was attached to.
	out.HasData = out.LastNightAvgHRVMs != nil || out.WeeklyAvgHRVMs != nil ||
		out.LastNight5MinHighHRVMs != nil ||
		out.BaselineBalancedLowMs != nil || out.BaselineBalancedUpperMs != nil ||
		out.BaselineLowUpperMs != nil ||
		out.Status != "" || out.SleepStart != "" || out.SleepEnd != ""

	if withSeries {
		out.Readings, out.ReadingsCount, out.ReadingsTruncated = hrvReadings(document.Readings)
	}
	return out
}

// hrvReadings renders the intraday series under its bound, reporting the full count
// Garmin sent and whether the list was cut.
func hrvReadings(readings []api.HRVReading) ([]HRVReading, int, bool) {
	count := len(readings)
	truncated := count > DefaultMaxHRVReadings
	if truncated {
		readings = readings[:DefaultMaxHRVReadings]
	}

	out := make([]HRVReading, 0, len(readings))
	for _, reading := range readings {
		out = append(out, HRVReading{
			Time:  textOrEmpty(reading.ReadingTimeLocal),
			HRVMs: optionalFloat(reading.HRVValue),
		})
	}
	return out, count, truncated
}
