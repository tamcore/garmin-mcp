package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetSleepSummary is the upstream compatibility name of the compact sleep tool.
const ToolGetSleepSummary = "get_sleep_summary"

// SleepSummary is one night of sleep, narrower than get_sleep_data.
//
// It reads the same document through the same api.Wellness.DailySleep call and adds
// the fields that view does not carry, decoded from the payload the read already
// retained: there is one Garmin request behind both tools, never two. It is health
// data: never log it.
type SleepSummary struct {
	Date    string `json:"date" jsonschema:"the calendar day that was requested, YYYY-MM-DD"`
	HasData bool   `json:"has_data" jsonschema:"whether Garmin held a sleep summary for the day"`

	SleepSeconds      *float64 `json:"sleep_seconds,omitempty" jsonschema:"total measured sleep"`
	NapSeconds        *float64 `json:"nap_seconds,omitempty" jsonschema:"time slept outside the main window"`
	DeepSleepSeconds  *float64 `json:"deep_sleep_seconds,omitempty" jsonschema:"time in deep sleep"`
	LightSleepSeconds *float64 `json:"light_sleep_seconds,omitempty" jsonschema:"time in light sleep"`
	RemSleepSeconds   *float64 `json:"rem_sleep_seconds,omitempty" jsonschema:"time in REM sleep"`
	AwakeSeconds      *float64 `json:"awake_seconds,omitempty" jsonschema:"time awake in the window"`

	SleepStartGMT *float64 `json:"sleep_start_gmt,omitempty" jsonschema:"sleep start as a UTC epoch"`
	SleepEndGMT   *float64 `json:"sleep_end_gmt,omitempty" jsonschema:"sleep end as a UTC epoch"`

	SleepScore          *float64 `json:"sleep_score,omitempty" jsonschema:"Garmin's overall sleep score"`
	SleepScoreQualifier *string  `json:"sleep_score_qualifier,omitempty" jsonschema:"Garmin's label for the overall score"`

	AwakeCount           *float64 `json:"awake_count,omitempty" jsonschema:"how many times the night was interrupted"`
	RestlessMomentsCount *float64 `json:"restless_moments_count,omitempty" jsonschema:"restless moments recorded"`
	AvgSleepStress       *float64 `json:"avg_sleep_stress,omitempty" jsonschema:"the average stress level during sleep"`
	RestingBPM           *float64 `json:"resting_heart_rate_bpm,omitempty" jsonschema:"the resting rate for the night"`
	AvgOvernightHRV      *float64 `json:"avg_overnight_hrv,omitempty" jsonschema:"the average overnight HRV"`

	AvgSpO2Percent    *float64 `json:"avg_spo2_percent,omitempty" jsonschema:"the average blood oxygen during sleep"`
	LowestSpO2Percent *float64 `json:"lowest_spo2_percent,omitempty" jsonschema:"the lowest blood oxygen during sleep"`
}

// LogValue reports the shape of the night and never a reading.
func (s SleepSummary) LogValue() slog.Value {
	return shape("sleepSummary",
		slog.Bool("hasData", s.HasData),
		slog.String("score", presence(s.SleepScore != nil)),
		slog.String("spo2", presence(s.AvgSpO2Percent != nil)),
	)
}

// getSleepSummaryInput is the strict argument set: one calendar day.
type getSleepSummaryInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getSleepSummaryContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetSleepSummary,
			Title: "Get sleep summary",
			Description: "read one calendar day of the account's sleep as a compact " +
				"summary: the stage durations, the sleep window, Garmin's score and " +
				"qualifier, the interruption counts, and the overnight stress, heart-rate " +
				"and blood-oxygen averages. No per-minute detail is returned",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetSleepSummary registers the tool.
func registerGetSleepSummary(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getSleepSummaryInput) (
		*mcp.CallToolResult, SleepSummary, error,
	) {
		out, err := svc.readSleepSummary(ctx, in.Date)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getSleepSummaryContract().Registration(), handler)
}

// readSleepSummary performs the read behind the tool.
//
// It reuses the sleep read get_sleep_data already goes through and takes the extra
// summary fields out of the same payload, so the narrower tool costs no extra request.
func (s *service) readSleepSummary(ctx context.Context, date string) (SleepSummary, error) {
	read, err := s.resolveDailyRead(ctx, date)
	if err != nil {
		return SleepSummary{}, err
	}
	sleep, err := s.wellness.DailySleep(ctx, read.session, read.name, read.date)
	if err != nil {
		return SleepSummary{}, fail(err)
	}
	digest, err := api.NewSleepDigest(sleep)
	if err != nil {
		return SleepSummary{}, fail(err)
	}
	return newSleepSummary(read.date.String(), sleep, digest), nil
}

// newSleepSummary maps the two views of the one document onto the curated result.
func newSleepSummary(date string, sleep api.DailySleep, digest api.SleepDigest) SleepSummary {
	out := SleepSummary{Date: date}
	if summary := sleep.Summary; summary != nil {
		out.HasData = true
		out.SleepSeconds = optionalFloat(summary.SleepTimeSeconds)
		out.DeepSleepSeconds = optionalFloat(summary.DeepSleepSeconds)
		out.LightSleepSeconds = optionalFloat(summary.LightSleepSeconds)
		out.RemSleepSeconds = optionalFloat(summary.RemSleepSeconds)
		out.AwakeSeconds = optionalFloat(summary.AwakeSleepSeconds)
		out.SleepStartGMT = optionalFloat(summary.SleepStartTimestampGMT)
		out.SleepEndGMT = optionalFloat(summary.SleepEndTimestampGMT)
	}
	addSleepDigest(&out, digest)
	return out
}

// addSleepDigest folds in the fields the DailySleep view does not carry.
func addSleepDigest(out *SleepSummary, digest api.SleepDigest) {
	out.AvgOvernightHRV = optionalFloat(digest.AvgOvernightHRV)
	if daily := digest.Daily; daily != nil {
		out.HasData = true
		out.NapSeconds = optionalFloat(daily.NapTimeSeconds)
		out.AwakeCount = optionalFloat(daily.AwakeCount)
		out.RestlessMomentsCount = optionalFloat(daily.RestlessMomentsCount)
		out.AvgSleepStress = optionalFloat(daily.AvgSleepStress)
		out.RestingBPM = optionalFloat(daily.RestingHeartRate)
	}
	if overall, ok := digest.Overall(); ok {
		out.SleepScore = optionalFloat(overall.Value)
		out.SleepScoreQualifier = optionalText(overall.QualifierKey)
	}
	if spo2 := digest.SpO2; spo2 != nil {
		out.AvgSpO2Percent = optionalFloat(spo2.AverageSpO2)
		out.LowestSpO2Percent = optionalFloat(spo2.LowestSpO2)
	}
}
