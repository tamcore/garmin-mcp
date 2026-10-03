package tools

import (
	"context"
	"log/slog"
	"strconv"

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

// ToolGetSleepSummaryRange is the upstream compatibility name of the multi-night
// sleep read. It is an addition beyond the pinned manifest: upstream added it after
// the pinned commit.
const ToolGetSleepSummaryRange = "get_sleep_summary_range"

// MaxSleepSummaryRangeNights bounds the window the range read accepts. Garmin exposes
// no range endpoint for sleep, so the tool reads one night per request and the bound
// is what stops one MCP call from becoming an unbounded burst of Garmin reads.
//
// Source: MAX_DAYS = 90 in upstream's get_sleep_summary_range.
const MaxSleepSummaryRangeNights = 90

// SleepSummaryRange is every night of a window that carried a summary.
//
// A night the device was not worn carries nothing and is not returned;
// nights_requested and nights_returned together say how much of the window the
// account holds, so a short list is never mistaken for a complete one. It is health
// data: never log it.
type SleepSummaryRange struct {
	StartDate string `json:"start_date" jsonschema:"the inclusive first night, YYYY-MM-DD"`
	EndDate   string `json:"end_date" jsonschema:"the inclusive last night, YYYY-MM-DD"`

	NightsRequested int `json:"nights_requested" jsonschema:"how many nights the window spans"`
	NightsReturned  int `json:"nights_returned" jsonschema:"how many nights carried a summary"`

	Nights []SleepSummary `json:"nights" jsonschema:"the nights that carried a summary, oldest first"`
}

// LogValue reports the shape of the window and never a reading.
func (s SleepSummaryRange) LogValue() slog.Value {
	return shape("sleepSummaryRange",
		slog.Int("nightsReturned", len(s.Nights)),
	)
}

// sleepSummaryRangeInput is the strict argument set: an inclusive night window.
type sleepSummaryRangeInput struct {
	StartDate string `json:"start_date" jsonschema:"the inclusive first night, YYYY-MM-DD"`
	EndDate   string `json:"end_date" jsonschema:"the inclusive last night, YYYY-MM-DD"`
}

func getSleepSummaryRangeContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetSleepSummaryRange,
			Title: "Get sleep summaries for a window",
			Description: "read the same compact summary get_sleep_summary returns for " +
				"every night of an inclusive date window, oldest first. Garmin exposes no " +
				"range endpoint for sleep, so this reads one night per request under a " +
				"bounded fan-out: the window may span at most " +
				strconv.Itoa(MaxSleepSummaryRangeNights) + " nights. A night Garmin holds " +
				"nothing for is not returned, and nights_requested beside nights_returned " +
				"says how much of the window the account holds",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(trendWindowProperties(MaxSleepSummaryRangeNights, reasonReadsPerDay)...),
	}
}

// registerGetSleepSummaryRange registers the tool.
func registerGetSleepSummaryRange(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in sleepSummaryRangeInput) (
		*mcp.CallToolResult, SleepSummaryRange, error,
	) {
		out, err := svc.readSleepSummaryRange(ctx, in.StartDate, in.EndDate)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getSleepSummaryRangeContract().Registration(), handler)
}

// readSleepSummaryRange reads every night of the window through the domain client's
// bounded fan-out, and curates each one exactly as the single-night tool does.
//
// The display name the wellness paths take as a segment is resolved once for the whole
// window, so the window costs one profile read and one sleep read a night.
//
// A night that cannot be read fails the whole call, where upstream swallows the
// failure and continues. That is deliberate: a silently dropped night is
// indistinguishable from a night the account holds nothing for, which makes a partial
// window look complete. A caller that hits one bad night can narrow the window.
func (s *service) readSleepSummaryRange(
	ctx context.Context, start, end string,
) (SleepSummaryRange, error) {
	window, err := s.resolveTrendWindow(ctx, start, end, MaxSleepSummaryRangeNights)
	if err != nil {
		return SleepSummaryRange{}, err
	}
	name, err := s.displayName(ctx, window.session)
	if err != nil {
		return SleepSummaryRange{}, err
	}

	days, err := s.wellness.DailySleepRange(ctx, window.session, name, window.span)
	if err != nil {
		return SleepSummaryRange{}, fail(err)
	}

	nights := make([]SleepSummary, 0, len(days))
	for index, day := range days {
		digest, err := api.NewSleepDigest(day)
		if err != nil {
			return SleepSummaryRange{}, fail(err)
		}
		night := newSleepSummary(window.span.Start().AddDays(index).String(), day, digest)
		if !night.HasData {
			continue
		}
		nights = append(nights, night)
	}

	return SleepSummaryRange{
		StartDate:       window.span.Start().String(),
		EndDate:         window.span.End().String(),
		NightsRequested: window.span.Days(),
		NightsReturned:  len(nights),
		Nights:          nights,
	}, nil
}
