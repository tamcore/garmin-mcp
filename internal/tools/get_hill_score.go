package tools

import (
	"context"
	"log/slog"
	"maps"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetHillScore is the upstream compatibility name of the hill-score read.
const ToolGetHillScore = "get_hill_score"

// A HillScoreDaily is one day of the hill score.
type HillScoreDaily struct {
	Date      *string  `json:"date,omitempty" jsonschema:"the day Garmin scored, YYYY-MM-DD"`
	Overall   *float64 `json:"overall,omitempty" jsonschema:"the day's overall hill score"`
	Strength  *float64 `json:"strength,omitempty" jsonschema:"the day's hill strength score"`
	Endurance *float64 `json:"endurance,omitempty" jsonschema:"the day's hill endurance score"`
}

// HillScore is the hill score over an inclusive window. It is health data — never log
// it, never cache it.
type HillScore struct {
	StartDate string `json:"start_date" jsonschema:"the inclusive first day, YYYY-MM-DD"`
	EndDate   string `json:"end_date" jsonschema:"the inclusive last day, YYYY-MM-DD"`

	PeriodAvgScore *float64 `json:"period_avg_score,omitempty" jsonschema:"the window's average overall score"`
	MaxScore       *float64 `json:"max_score,omitempty" jsonschema:"the window's highest overall score"`

	LatestDate             *string  `json:"latest_date,omitempty" jsonschema:"the day the most recent score came from"`
	LatestOverallScore     *float64 `json:"latest_overall_score,omitempty" jsonschema:"the most recent overall score"`
	LatestStrengthScore    *float64 `json:"latest_strength_score,omitempty" jsonschema:"the most recent strength score"`
	LatestEnduranceScore   *float64 `json:"latest_endurance_score,omitempty" jsonschema:"the newest endurance score"`
	LatestClassificationID *int     `json:"latest_classification_id,omitempty" jsonschema:"the newest score's code"`

	Count     int  `json:"count" jsonschema:"how many days this result carries"`
	Truncated bool `json:"truncated" jsonschema:"whether the day list was cut at this server's bound"`

	DailyScores []HillScoreDaily `json:"daily_scores" jsonschema:"the scored days of the window"`
}

// LogValue reports the shape of the answer, never a reading.
func (h HillScore) LogValue() slog.Value {
	return shape("hillScore",
		slog.Int("days", len(h.DailyScores)),
		slog.String("periodAverage", presence(h.PeriodAvgScore != nil)),
		slog.Bool("truncated", h.Truncated),
	)
}

// hillScoreInput is the strict argument set: an inclusive date window.
type hillScoreInput struct {
	StartDate string `json:"start_date" jsonschema:"inclusive first day, YYYY-MM-DD"`
	EndDate   string `json:"end_date" jsonschema:"inclusive last day, YYYY-MM-DD"`
}

func getHillScoreContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetHillScore,
			Title: "Get hill score",
			Description: "read the account's hill score over an inclusive date window: the " +
				"window's average and highest score, the most recent day's overall, " +
				"strength and endurance scores, and the score of each day Garmin holds " +
				"one for. The window is bounded by this server; narrow it if the call is " +
				"refused",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(
			dateProperty("start_date", "inclusive first day of the window"),
			dateProperty("end_date", "inclusive last day of the window"),
		),
	}
}

// registerGetHillScore registers the tool.
func registerGetHillScore(registry *mcpserver.Registry, svc *service) error {
	scores, err := trainingScoresClient(svc)
	if err != nil {
		return err
	}
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in hillScoreInput) (
		*mcp.CallToolResult, HillScore, error,
	) {
		span, err := parseWindow(in.StartDate, in.EndDate, svc.limits)
		if err != nil {
			return nil, HillScore{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, HillScore{}, err
		}
		window, err := scores.HillScore(ctx, session, span)
		if err != nil {
			return nil, HillScore{}, fail(err)
		}
		return nil, newHillScore(span, window, svc.limits.MaxDateRangeDays), nil
	}
	return mcpserver.AddTool(registry, getHillScoreContract().Registration(), handler)
}

// newHillScore maps the window onto the bounded result.
//
// The day bound is the request layer's own date-window bound: the read is aggregated
// daily, so a window that survived validation can hold at most one score per day, and
// anything past that is drift rather than data.
func newHillScore(span client.DateRange, window api.HillScoreWindow, maxDays int) HillScore {
	out := HillScore{
		StartDate:      span.Start().String(),
		EndDate:        span.End().String(),
		PeriodAvgScore: firstKeyedValue(window.PeriodAvgScore),
		MaxScore:       optionalFloat(window.MaxScore),
		DailyScores:    []HillScoreDaily{},
	}

	days := window.Days.Items()
	if len(days) > maxDays {
		days = days[:maxDays]
		out.Truncated = true
	}
	for _, day := range days {
		out.DailyScores = append(out.DailyScores, HillScoreDaily{
			Date:      day.CalendarDate,
			Overall:   optionalFloat(day.OverallScore),
			Strength:  optionalFloat(day.StrengthScore),
			Endurance: optionalFloat(day.EnduranceScore),
		})
	}
	out.Count = len(out.DailyScores)
	applyLatestHillScore(&out, days)
	return out
}

// applyLatestHillScore copies the first listed day into the latest-score fields, which
// is the day upstream reports as the latest.
func applyLatestHillScore(out *HillScore, days []api.HillScoreDay) {
	if len(days) == 0 {
		return
	}
	latest := days[0]
	out.LatestDate = latest.CalendarDate
	out.LatestOverallScore = optionalFloat(latest.OverallScore)
	out.LatestStrengthScore = optionalFloat(latest.StrengthScore)
	out.LatestEnduranceScore = optionalFloat(latest.EnduranceScore)
	out.LatestClassificationID = optionalInt(latest.ClassificationID)
}

// firstKeyedValue reads one value out of a keyed Garmin object without ever exposing
// the key, which this project has not sourced and which may be an account identifier.
// The keys are visited in sorted order so the answer is the same on every call.
func firstKeyedValue(values map[string]client.Number) *float64 {
	for _, key := range slices.Sorted(maps.Keys(values)) {
		if value := optionalFloat(values[key]); value != nil {
			return value
		}
	}
	return nil
}

// trainingScoresClient builds the training-scores domain client beside the wellness
// client the service already holds, which is where the shared request layer lives.
func trainingScoresClient(svc *service) (*api.TrainingScores, error) {
	if svc == nil {
		return nil, fail(ErrMissingDependency)
	}
	scores, err := api.NewTrainingScoresFrom(svc.wellness)
	if err != nil {
		return nil, fail(err)
	}
	return scores, nil
}
