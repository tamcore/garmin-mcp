package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetStats is the upstream compatibility name of the curated daily stats tool.
const ToolGetStats = "get_stats"

// DailyActivityStats is one day of the account's curated totals.
//
// The keys are upstream's curated names, not Garmin's wire names: get_stats renames
// every field it keeps, and a client written against the upstream server expects
// those names. Every field is optional, because a day with no wearable data, a new
// account, and an account whose device measures no stress, body battery or pulse ox
// are all normal states rather than failures.
//
// It is health data — never log it, never cache it.
type DailyActivityStats struct {
	// Date is the calendar day that was requested.
	Date string `json:"date" jsonschema:"the calendar day, YYYY-MM-DD"`

	TotalSteps      *int     `json:"total_steps,omitempty" jsonschema:"steps taken"`
	DailyStepGoal   *int     `json:"daily_step_goal,omitempty" jsonschema:"the day's step goal"`
	DistanceMeters  *float64 `json:"distance_meters,omitempty" jsonschema:"distance covered"`
	FloorsAscended  *float64 `json:"floors_ascended,omitempty" jsonschema:"floors climbed"`
	FloorsDescended *float64 `json:"floors_descended,omitempty" jsonschema:"floors descended"`

	TotalCalories  *float64 `json:"total_calories,omitempty" jsonschema:"total kilocalories"`
	ActiveCalories *float64 `json:"active_calories,omitempty" jsonschema:"active kilocalories"`
	BMRCalories    *float64 `json:"bmr_calories,omitempty" jsonschema:"resting kilocalories"`

	HighlyActiveSeconds *int `json:"highly_active_seconds,omitempty" jsonschema:"time highly active"`
	ActiveSeconds       *int `json:"active_seconds,omitempty" jsonschema:"time active"`
	SedentarySeconds    *int `json:"sedentary_seconds,omitempty" jsonschema:"time sedentary"`
	SleepingSeconds     *int `json:"sleeping_seconds,omitempty" jsonschema:"time asleep"`

	ModerateIntensityMinutes *int `json:"moderate_intensity_minutes,omitempty" jsonschema:"moderate intensity minutes"`
	VigorousIntensityMinutes *int `json:"vigorous_intensity_minutes,omitempty" jsonschema:"vigorous intensity minutes"`
	IntensityMinutesGoal     *int `json:"intensity_minutes_goal,omitempty" jsonschema:"the intensity minutes goal"`

	MinHeartRateBPM     *int `json:"min_heart_rate_bpm,omitempty" jsonschema:"minimum heart rate in bpm"`
	MaxHeartRateBPM     *int `json:"max_heart_rate_bpm,omitempty" jsonschema:"maximum heart rate in bpm"`
	RestingHeartRateBPM *int `json:"resting_heart_rate_bpm,omitempty" jsonschema:"resting heart rate in bpm"`
	LastSevenDaysAvgRHR *int `json:"last_7_days_avg_resting_hr,omitempty" jsonschema:"seven-day average resting hr"`

	AvgStressLevel  *int    `json:"avg_stress_level,omitempty" jsonschema:"average stress level"`
	MaxStressLevel  *int    `json:"max_stress_level,omitempty" jsonschema:"highest stress level"`
	StressQualifier *string `json:"stress_qualifier,omitempty" jsonschema:"Garmin's stress label"`

	BodyBatteryCharged *int `json:"body_battery_charged,omitempty" jsonschema:"body battery gained"`
	BodyBatteryDrained *int `json:"body_battery_drained,omitempty" jsonschema:"body battery spent"`
	BodyBatteryHighest *int `json:"body_battery_highest,omitempty" jsonschema:"highest body battery"`
	BodyBatteryLowest  *int `json:"body_battery_lowest,omitempty" jsonschema:"lowest body battery"`
	BodyBatteryCurrent *int `json:"body_battery_current,omitempty" jsonschema:"most recent body battery"`

	AvgSpO2Percent    *float64 `json:"avg_spo2_percent,omitempty" jsonschema:"average blood oxygen percentage"`
	LowestSpO2Percent *float64 `json:"lowest_spo2_percent,omitempty" jsonschema:"lowest blood oxygen percentage"`

	AvgWakingRespiration *float64 `json:"avg_waking_respiration,omitempty" jsonschema:"average waking breaths per minute"`
	HighestRespiration   *float64 `json:"highest_respiration,omitempty" jsonschema:"highest breaths per minute"`
	LowestRespiration    *float64 `json:"lowest_respiration,omitempty" jsonschema:"lowest breaths per minute"`
}

// LogValue reports the shape of the day, never a single reading. A step count, a
// stress level and a body-battery figure are all readings.
func (s DailyActivityStats) LogValue() slog.Value {
	return shape("dailyActivityStats",
		slog.String("steps", presence(s.TotalSteps != nil)),
		slog.String("heartRate", presence(s.RestingHeartRateBPM != nil)),
		slog.String("stress", presence(s.AvgStressLevel != nil)),
		slog.String("bodyBattery", presence(s.BodyBatteryCurrent != nil)),
		slog.String("respiration", presence(s.AvgWakingRespiration != nil)),
	)
}

// getStatsInput is the strict argument set: one calendar day.
type getStatsInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getStatsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetStats,
			Title: "Get daily stats",
			Description: "read one calendar day of the account's curated totals: steps and " +
				"the step goal, distance, floors, calories, activity durations, intensity " +
				"minutes, heart rates, stress, body battery, pulse ox and respiration. " +
				"A metric the account holds no data for is omitted",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetStats registers the tool.
func registerGetStats(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getStatsInput) (
		*mcp.CallToolResult, DailyActivityStats, error,
	) {
		stats, err := svc.dailyStats(ctx, in)
		return nil, stats, err
	}
	return mcpserver.AddTool(registry, getStatsContract().Registration(), handler)
}

// dailyStats validates the day, reads it and curates it.
func (s *service) dailyStats(ctx context.Context, in getStatsInput) (DailyActivityStats, error) {
	read, err := s.resolveDailyRead(ctx, in.Date)
	if err != nil {
		return DailyActivityStats{}, err
	}
	stats, err := s.wellness.Daily().Stats(ctx, read.session, read.name, read.date)
	if err != nil {
		return DailyActivityStats{}, fail(err)
	}
	return newDailyActivityStats(read.date.String(), stats), nil
}

// newDailyActivityStats maps the domain model onto the curated result. The date is
// the day that was asked for rather than the one the payload echoes, so a caller
// always knows what it got.
func newDailyActivityStats(date string, stats api.DailyStats) DailyActivityStats {
	out := DailyActivityStats{
		Date:            date,
		TotalSteps:      optionalInt(stats.TotalSteps),
		DailyStepGoal:   optionalInt(stats.DailyStepGoal),
		DistanceMeters:  optionalFloat(stats.TotalDistanceMeters),
		FloorsAscended:  optionalFloat(stats.FloorsAscended),
		FloorsDescended: optionalFloat(stats.FloorsDescended),
		TotalCalories:   optionalFloat(stats.TotalKilocalories),
		ActiveCalories:  optionalFloat(stats.ActiveKilocalories),
		BMRCalories:     optionalFloat(stats.BMRKilocalories),
		StressQualifier: optionalText(stats.StressQualifier),
	}
	addStatsDurations(&out, stats)
	addStatsVitals(&out, stats)
	return out
}

// addStatsDurations fills the activity-duration and intensity-minute fields.
func addStatsDurations(out *DailyActivityStats, stats api.DailyStats) {
	out.HighlyActiveSeconds = optionalInt(stats.HighlyActiveSeconds)
	out.ActiveSeconds = optionalInt(stats.ActiveSeconds)
	out.SedentarySeconds = optionalInt(stats.SedentarySeconds)
	out.SleepingSeconds = optionalInt(stats.SleepingSeconds)
	out.ModerateIntensityMinutes = optionalInt(stats.ModerateIntensityMinutes)
	out.VigorousIntensityMinutes = optionalInt(stats.VigorousIntensityMinutes)
	out.IntensityMinutesGoal = optionalInt(stats.IntensityMinutesGoal)
}

// addStatsVitals fills the heart-rate, stress, body-battery, pulse-ox and
// respiration fields.
func addStatsVitals(out *DailyActivityStats, stats api.DailyStats) {
	out.MinHeartRateBPM = optionalInt(stats.MinHeartRate)
	out.MaxHeartRateBPM = optionalInt(stats.MaxHeartRate)
	out.RestingHeartRateBPM = optionalInt(stats.RestingHeartRate)
	out.LastSevenDaysAvgRHR = optionalInt(stats.LastSevenDaysAvgRestingHeartRate)
	out.AvgStressLevel = optionalInt(stats.AverageStressLevel)
	out.MaxStressLevel = optionalInt(stats.MaxStressLevel)
	out.BodyBatteryCharged = optionalInt(stats.BodyBatteryCharged)
	out.BodyBatteryDrained = optionalInt(stats.BodyBatteryDrained)
	out.BodyBatteryHighest = optionalInt(stats.BodyBatteryHighest)
	out.BodyBatteryLowest = optionalInt(stats.BodyBatteryLowest)
	out.BodyBatteryCurrent = optionalInt(stats.BodyBatteryMostRecent)
	out.AvgSpO2Percent = optionalFloat(stats.AverageSpo2)
	out.LowestSpO2Percent = optionalFloat(stats.LowestSpo2)
	out.AvgWakingRespiration = optionalFloat(stats.AvgWakingRespiration)
	out.HighestRespiration = optionalFloat(stats.HighestRespiration)
	out.LowestRespiration = optionalFloat(stats.LowestRespiration)
}
