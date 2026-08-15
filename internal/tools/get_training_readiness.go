package tools

import (
	"context"
	"log/slog"
	"math"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetTrainingReadiness is the upstream compatibility name of the readiness tool.
const ToolGetTrainingReadiness = "get_training_readiness"

// Readiness bounds. Garmin records one or two snapshots a day; the bound exists so a
// drifted answer cannot turn one call into an unbounded list.
const (
	maxReadinessEntries = 32
	minutesPerHour      = 60.0
)

// ReadinessEntry is one training-readiness snapshot, curated.
//
// It is health data — never log it, never cache it. Every field is optional, because
// the set a device populates varies by model and by firmware. Source: the keys the
// pinned upstream tool curates from the readiness document.
type ReadinessEntry struct {
	Date      *string `json:"date,omitempty" jsonschema:"the day Garmin reported, YYYY-MM-DD"`
	Timestamp *string `json:"timestamp,omitempty" jsonschema:"the snapshot's local timestamp"`
	Context   *string `json:"context,omitempty" jsonschema:"what triggered the snapshot"`

	Level    *string `json:"level,omitempty" jsonschema:"Garmin's readiness level label"`
	Score    *int    `json:"score,omitempty" jsonschema:"the readiness score"`
	Feedback *string `json:"feedback,omitempty" jsonschema:"Garmin's short readiness feedback"`

	SleepScore          *int    `json:"sleep_score,omitempty" jsonschema:"last night's sleep score"`
	SleepFactorPercent  *int    `json:"sleep_factor_percent,omitempty" jsonschema:"sleep's share of the score"`
	SleepFactorFeedback *string `json:"sleep_factor_feedback,omitempty" jsonschema:"Garmin's sleep factor label"`

	RecoveryTimeHours      *float64 `json:"recovery_time_hours,omitempty" jsonschema:"recovery time still to run"`
	RecoveryFactorPercent  *int     `json:"recovery_factor_percent,omitempty" jsonschema:"recovery's share of the score"`
	RecoveryFactorFeedback *string  `json:"recovery_factor_feedback,omitempty" jsonschema:"Garmin's recovery factor label"`

	TrainingLoadFactorPercent *int    `json:"training_load_factor_percent,omitempty" jsonschema:"load's share"`
	TrainingLoadFeedback      *string `json:"training_load_feedback,omitempty" jsonschema:"Garmin's load factor label"`
	AcuteLoad                 *int    `json:"acute_load,omitempty" jsonschema:"the acute training load"`

	HRVFactorPercent  *int    `json:"hrv_factor_percent,omitempty" jsonschema:"HRV's share of the score"`
	HRVFactorFeedback *string `json:"hrv_factor_feedback,omitempty" jsonschema:"Garmin's HRV factor label"`
	HRVWeeklyAverage  *int    `json:"hrv_weekly_avg,omitempty" jsonschema:"the weekly average HRV"`

	StressHistoryFactorPercent *int    `json:"stress_history_factor_percent,omitempty" jsonschema:"stress history's share"`
	StressHistoryFeedback      *string `json:"stress_history_feedback,omitempty" jsonschema:"stress history label"`

	SleepHistoryFactorPercent *int    `json:"sleep_history_factor_percent,omitempty" jsonschema:"sleep history's share"`
	SleepHistoryFeedback      *string `json:"sleep_history_feedback,omitempty" jsonschema:"Garmin's sleep history label"`
}

// TrainingReadinessList is one day of readiness snapshots.
type TrainingReadinessList struct {
	Date string `json:"date" jsonschema:"the calendar day, YYYY-MM-DD"`

	Count     int  `json:"count" jsonschema:"how many snapshots this result carries"`
	Truncated bool `json:"truncated" jsonschema:"whether the list was cut at this server's bound"`

	Entries []ReadinessEntry `json:"entries" jsonschema:"the day's readiness snapshots"`
}

// LogValue reports the shape of the day, never a score.
func (l TrainingReadinessList) LogValue() slog.Value {
	return shape("trainingReadinessList",
		slog.Int("entries", l.Count),
		slog.Bool("truncated", l.Truncated),
	)
}

// getTrainingReadinessInput is the strict argument set: one calendar day.
type getTrainingReadinessInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getTrainingReadinessContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetTrainingReadiness,
			Title: "Get training readiness",
			Description: "read one calendar day of the account's training readiness: the " +
				"score and level, and the sleep, recovery, training-load, HRV and history " +
				"factors behind them. Garmin records one or two snapshots a day",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetTrainingReadiness registers the tool.
func registerGetTrainingReadiness(registry *mcpserver.Registry, svc *service) error {
	stress, err := stressClient(svc)
	if err != nil {
		return err
	}
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getTrainingReadinessInput) (
		*mcp.CallToolResult, TrainingReadinessList, error,
	) {
		day, session, err := svc.resolveStressDay(ctx, in.Date)
		if err != nil {
			return nil, TrainingReadinessList{}, err
		}
		read, err := stress.TrainingReadiness(ctx, session, day, api.ReadinessViewAll)
		if err != nil {
			return nil, TrainingReadinessList{}, fail(err)
		}
		return nil, newTrainingReadinessList(day.String(), read), nil
	}
	return mcpserver.AddTool(registry, getTrainingReadinessContract().Registration(), handler)
}

// newTrainingReadinessList maps the snapshots onto the bounded result.
func newTrainingReadinessList(date string, entries []api.Readiness) TrainingReadinessList {
	out := TrainingReadinessList{Date: date, Entries: []ReadinessEntry{}}
	if len(entries) > maxReadinessEntries {
		entries = entries[:maxReadinessEntries]
		out.Truncated = true
	}
	for _, entry := range entries {
		out.Entries = append(out.Entries, newReadinessEntry(entry))
	}
	out.Count = len(out.Entries)
	return out
}

// newReadinessEntry maps one snapshot. It is shared with the morning readiness tool,
// which selects one of these entries out of the same read.
func newReadinessEntry(entry api.Readiness) ReadinessEntry {
	out := ReadinessEntry{
		Date:      entry.CalendarDate,
		Timestamp: optionalText(entry.TimestampLocal),
		Context:   optionalText(entry.InputContext),
		Level:     optionalText(entry.Level),
		Score:     optionalInt(entry.Score),
		Feedback:  optionalText(entry.FeedbackShort),

		SleepScore:          optionalInt(entry.SleepScore),
		SleepFactorPercent:  optionalInt(entry.SleepScoreFactorPercent),
		SleepFactorFeedback: optionalText(entry.SleepScoreFactorFeed),

		RecoveryTimeHours:      recoveryHours(entry.RecoveryTime),
		RecoveryFactorPercent:  optionalInt(entry.RecoveryTimeFactorPercent),
		RecoveryFactorFeedback: optionalText(entry.RecoveryTimeFactorFeed),

		TrainingLoadFactorPercent: optionalInt(entry.ACWRFactorPercent),
		TrainingLoadFeedback:      optionalText(entry.ACWRFactorFeed),
		AcuteLoad:                 optionalInt(entry.AcuteLoad),
	}
	out.HRVFactorPercent = optionalInt(entry.HRVFactorPercent)
	out.HRVFactorFeedback = optionalText(entry.HRVFactorFeed)
	out.HRVWeeklyAverage = optionalInt(entry.HRVWeeklyAverage)
	out.StressHistoryFactorPercent = optionalInt(entry.StressHistoryFactorPercent)
	out.StressHistoryFeedback = optionalText(entry.StressHistoryFactorFeed)
	out.SleepHistoryFactorPercent = optionalInt(entry.SleepHistoryFactorPercent)
	out.SleepHistoryFeedback = optionalText(entry.SleepHistoryFactorFeed)
	return out
}

// recoveryHours renders Garmin's recovery time, which it reports in minutes, as hours
// rounded to one decimal the way upstream rounds it. An absent time stays absent.
func recoveryHours(minutes client.Number) *float64 {
	value, ok := minutes.Float64()
	if !ok {
		return nil
	}
	hours := math.Round(value/minutesPerHour*10) / 10
	return &hours
}
