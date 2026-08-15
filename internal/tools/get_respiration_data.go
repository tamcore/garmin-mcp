package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetRespirationData is the upstream compatibility name of the intraday
// respiration tool.
const ToolGetRespirationData = "get_respiration_data"

// Result bounds for the respiration day.
const (
	// DefaultMaxRespirationSamples bounds the retained per-sample series.
	// Respiration is sampled at least as densely as heart rate, so it carries the
	// same ceiling: one sample a minute for a whole day.
	DefaultMaxRespirationSamples = 1440

	// DefaultMaxRespirationHourlyAverages bounds the retained hourly series. A day
	// has 24 buckets, so the ceiling bounds a document that carries more than it
	// should without cutting an ordinary one.
	DefaultMaxRespirationHourlyAverages = 48
)

// A RespirationSample is one point of the per-sample series.
//
// Every field is optional. A point can carry no reading in two ways, and both are
// normal: the value can be null, and it can be one of Garmin's negative sentinels.
// NoReadingCode carries the sentinel that was sent, because -1 and -2 appear in
// different stretches and therefore mean two different things. A sentinel is never
// presented as a breath rate — a rate of minus one is not a measurement.
type RespirationSample struct {
	TimeGMTMillis    *int64   `json:"time_gmt_millis,omitempty" jsonschema:"the sample instant, a UTC epoch in ms"`
	BreathsPerMinute *float64 `json:"breaths_per_min,omitempty" jsonschema:"breaths per minute, absent for a gap"`
	NoReadingCode    *float64 `json:"no_reading_code,omitempty" jsonschema:"Garmin's marker for why there is no reading"`
}

// A RespirationHourlyAverage is one bucket of the hourly aggregate series.
//
// The high and the low are absent for a bucket Garmin marked as having no reading, so
// a bucket can carry a code and nothing else.
type RespirationHourlyAverage struct {
	TimeGMTMillis     *int64   `json:"time_gmt_millis,omitempty" jsonschema:"the bucket instant, a UTC epoch in ms"`
	AvgBreathsPerMin  *float64 `json:"avg_breaths_per_min,omitempty" jsonschema:"the bucket's average"`
	HighBreathsPerMin *float64 `json:"high_breaths_per_min,omitempty" jsonschema:"the bucket's highest reading"`
	LowBreathsPerMin  *float64 `json:"low_breaths_per_min,omitempty" jsonschema:"the bucket's lowest reading"`
	NoReadingCode     *float64 `json:"no_reading_code,omitempty" jsonschema:"Garmin's marker for why there is no reading"`
}

// Respiration is one day of breathing rate with both of its series.
//
// It is health data: never log it, never cache it.
type Respiration struct {
	Date    string `json:"date" jsonschema:"the calendar day that was requested, YYYY-MM-DD"`
	HasData bool   `json:"has_data" jsonschema:"whether Garmin held respiration data for the day"`

	LowestBreathsPerMin    *float64 `json:"lowest_breaths_per_min,omitempty" jsonschema:"the day's lowest reading"`
	HighestBreathsPerMin   *float64 `json:"highest_breaths_per_min,omitempty" jsonschema:"the day's highest reading"`
	AvgWakingBreathsPerMin *float64 `json:"avg_waking_breaths_per_min,omitempty" jsonschema:"the average while awake"`
	AvgSleepBreathsPerMin  *float64 `json:"avg_sleep_breaths_per_min,omitempty" jsonschema:"the average while asleep"`

	AvgNextNightBPM *float64 `json:"avg_tomorrow_sleep_breaths_per_min,omitempty" jsonschema:"the coming night's average"`

	SleepStartGMT   *string `json:"sleep_start_gmt,omitempty" jsonschema:"the start of last night's sleep"`
	SleepEndGMT     *string `json:"sleep_end_gmt,omitempty" jsonschema:"the end of last night's sleep"`
	SleepStartLocal *string `json:"sleep_start_local,omitempty" jsonschema:"the local start of last night's sleep"`
	SleepEndLocal   *string `json:"sleep_end_local,omitempty" jsonschema:"the local end of last night's sleep"`

	TomorrowSleepStartGMT   *string `json:"tomorrow_sleep_start_gmt,omitempty" jsonschema:"the start of the coming night"`
	TomorrowSleepEndGMT     *string `json:"tomorrow_sleep_end_gmt,omitempty" jsonschema:"the end of the coming night"`
	TomorrowSleepStartLocal *string `json:"tomorrow_sleep_start_local,omitempty" jsonschema:"local start of next night"`
	TomorrowSleepEndLocal   *string `json:"tomorrow_sleep_end_local,omitempty" jsonschema:"local end of next night"`

	RespirationVersion *int64 `json:"respiration_version,omitempty" jsonschema:"the document version Garmin reports"`

	Samples     []RespirationSample `json:"samples" jsonschema:"the per-sample series, oldest first"`
	SampleCount int                 `json:"sample_count" jsonschema:"how many samples this result carries"`
	Truncated   bool                `json:"truncated" jsonschema:"whether the series was cut at this server's bound"`

	HourlyAverages     []RespirationHourlyAverage `json:"hourly_averages" jsonschema:"the hourly aggregates, oldest first"`
	HourlyAverageCount int                        `json:"hourly_average_count" jsonschema:"how many buckets are carried"`
	HourlyTruncated    bool                       `json:"hourly_truncated" jsonschema:"whether the hourly series was cut"`
}

// LogValue reports the shape of the day and never a reading.
func (r Respiration) LogValue() slog.Value {
	return shape("respiration",
		slog.Bool("hasData", r.HasData),
		slog.Int("samples", r.SampleCount),
		slog.Bool("truncated", r.Truncated),
		slog.Int("hourlyAverages", r.HourlyAverageCount),
		slog.Bool("hourlyTruncated", r.HourlyTruncated),
	)
}

// getRespirationDataInput is the strict argument set: one calendar day.
type getRespirationDataInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getRespirationDataContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetRespirationData,
			Title: "Get respiration data",
			Description: "read one calendar day of the account's breathing rate: the day's " +
				"lowest and highest reading, the waking and sleeping averages, and the " +
				"intraday series. The series is bounded and a cut is reported",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetRespirationData registers the tool.
func registerGetRespirationData(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getRespirationDataInput) (
		*mcp.CallToolResult, Respiration, error,
	) {
		out, err := svc.readRespiration(ctx, in.Date)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getRespirationDataContract().Registration(), handler)
}

// resolveDateOnlyRead validates the date argument first, so a malformed date costs no
// Garmin call at all, and only then resolves the session.
//
// It is the counterpart of resolveDailyRead for the wellness endpoints that key the
// day into the path and therefore need no display name — and so cost no profile read.
func (s *service) resolveDateOnlyRead(
	ctx context.Context, date string,
) (client.Date, client.Session, error) {
	day, err := parseCalendarDate("date", date)
	if err != nil {
		return client.Date{}, client.Session{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return client.Date{}, client.Session{}, err
	}
	return day, session, nil
}

// readRespiration performs the read behind the tool.
func (s *service) readRespiration(ctx context.Context, date string) (Respiration, error) {
	day, session, err := s.resolveDateOnlyRead(ctx, date)
	if err != nil {
		return Respiration{}, err
	}
	document, err := s.wellness.Cardio().Respiration(ctx, session, day)
	if err != nil {
		return Respiration{}, fail(err)
	}
	samples, err := document.Samples()
	if err != nil {
		return Respiration{}, fail(err)
	}
	hourly, err := document.HourlyAverages()
	if err != nil {
		return Respiration{}, fail(err)
	}
	return newRespiration(day.String(), document, samples, hourly), nil
}

// newRespiration maps the domain model onto the curated result.
func newRespiration(
	date string, day api.DailyRespiration, samples []api.Sample, hourly []api.AverageSample,
) Respiration {
	kept, truncated := api.BoundSamples(samples, DefaultMaxRespirationSamples)
	keptHourly, hourlyTruncated := api.BoundSamples(hourly, DefaultMaxRespirationHourlyAverages)

	out := Respiration{
		Date:                   date,
		LowestBreathsPerMin:    optionalFloat(day.LowestRespirationValue),
		HighestBreathsPerMin:   optionalFloat(day.HighestRespirationValue),
		AvgWakingBreathsPerMin: optionalFloat(day.AvgWakingRespirationValue),
		AvgSleepBreathsPerMin:  optionalFloat(day.AvgSleepRespirationValue),
		AvgNextNightBPM:        optionalFloat(day.AvgTomorrowSleepRespirationValue),
		RespirationVersion:     optionalInt64(day.RespirationVersion),
		Samples:                newRespirationSamples(kept),
		Truncated:              truncated,
		HourlyAverages:         newRespirationHourlyAverages(keptHourly),
		HourlyTruncated:        hourlyTruncated,
	}
	addRespirationWindows(&out, day)
	out.SampleCount = len(out.Samples)
	out.HourlyAverageCount = len(out.HourlyAverages)
	out.HasData = out.SampleCount > 0 || out.HourlyAverageCount > 0 ||
		out.LowestBreathsPerMin != nil || out.HighestBreathsPerMin != nil ||
		out.AvgWakingBreathsPerMin != nil || out.AvgSleepBreathsPerMin != nil
	return out
}

// addRespirationWindows carries the two sleep windows the document reports. They are
// passed through as Garmin renders them and are never parsed.
func addRespirationWindows(out *Respiration, day api.DailyRespiration) {
	out.SleepStartGMT = optionalText(day.SleepStartTimestampGMT)
	out.SleepEndGMT = optionalText(day.SleepEndTimestampGMT)
	out.SleepStartLocal = optionalText(day.SleepStartTimestampLocal)
	out.SleepEndLocal = optionalText(day.SleepEndTimestampLocal)
	out.TomorrowSleepStartGMT = optionalText(day.TomorrowSleepStartTimestampGMT)
	out.TomorrowSleepEndGMT = optionalText(day.TomorrowSleepEndTimestampGMT)
	out.TomorrowSleepStartLocal = optionalText(day.TomorrowSleepStartTimestampLocal)
	out.TomorrowSleepEndLocal = optionalText(day.TomorrowSleepEndTimestampLocal)
}

// newRespirationSamples renders the per-sample series, keeping a point whose reading
// is absent: a gap is information, and the sentinel says which kind of gap it was.
func newRespirationSamples(samples []api.Sample) []RespirationSample {
	out := make([]RespirationSample, 0, len(samples))
	for _, sample := range samples {
		out = append(out, RespirationSample{
			TimeGMTMillis:    optionalInt64(sample.TimeMillis),
			BreathsPerMinute: optionalFloat(sample.Value),
			NoReadingCode:    optionalFloat(sample.Sentinel),
		})
	}
	return out
}

// newRespirationHourlyAverages renders the hourly series.
func newRespirationHourlyAverages(samples []api.AverageSample) []RespirationHourlyAverage {
	out := make([]RespirationHourlyAverage, 0, len(samples))
	for _, sample := range samples {
		out = append(out, RespirationHourlyAverage{
			TimeGMTMillis:     optionalInt64(sample.TimeMillis),
			AvgBreathsPerMin:  optionalFloat(sample.Average),
			HighBreathsPerMin: optionalFloat(sample.High),
			LowBreathsPerMin:  optionalFloat(sample.Low),
			NoReadingCode:     optionalFloat(sample.Sentinel),
		})
	}
	return out
}
