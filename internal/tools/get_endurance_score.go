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

// ToolGetEnduranceScore is the upstream compatibility name of the endurance-score read.
const ToolGetEnduranceScore = "get_endurance_score"

// enduranceClassifications maps Garmin's classification code onto the label upstream
// gives it. Source: the classification_labels table of the pinned upstream tool. A
// code outside the table is reported as a code and never as an invented label.
func enduranceClassifications() map[int]string {
	return map[int]string{
		1: "recreational",
		2: "intermediate",
		3: "trained",
		4: "well_trained",
		5: "expert",
		6: "superior",
		7: "elite",
	}
}

// An EnduranceContribution is one activity type's share of the score.
//
// The activity type is reported as Garmin's numeric identifier. Upstream turns it into
// a name by reading the whole activity-type catalog first; this tool does not, because
// that would be a second Garmin call per read. get_activity_types returns the same
// mapping for a caller that wants the names.
type EnduranceContribution struct {
	ActivityTypeID      *int     `json:"activity_type_id,omitempty" jsonschema:"Garmin's activity type identifier"`
	Group               *int     `json:"group,omitempty" jsonschema:"the group code, sent without a type"`
	ContributionPercent *float64 `json:"contribution_percent,omitempty" jsonschema:"the share of the score, in percent"`
}

// EnduranceThresholds are the score limits of each classification.
type EnduranceThresholds struct {
	Intermediate *float64 `json:"intermediate,omitempty" jsonschema:"the lowest intermediate score"`
	Trained      *float64 `json:"trained,omitempty" jsonschema:"the lowest trained score"`
	WellTrained  *float64 `json:"well_trained,omitempty" jsonschema:"the lowest well-trained score"`
	Expert       *float64 `json:"expert,omitempty" jsonschema:"the lowest expert score"`
	Superior     *float64 `json:"superior,omitempty" jsonschema:"the lowest superior score"`
	Elite        *float64 `json:"elite,omitempty" jsonschema:"the lowest elite score"`
}

// An EnduranceWeek is one aggregation bucket of the window.
type EnduranceWeek struct {
	WeekStart    string                  `json:"week_start" jsonschema:"the first day of the bucket, YYYY-MM-DD"`
	AvgScore     *float64                `json:"avg_score,omitempty" jsonschema:"the bucket's average score"`
	MaxScore     *float64                `json:"max_score,omitempty" jsonschema:"the bucket's highest score"`
	Contributors []EnduranceContribution `json:"contributors" jsonschema:"what contributed to the bucket"`
}

// EnduranceScore is the endurance score over an inclusive window. It is health data —
// never log it, never cache it.
type EnduranceScore struct {
	StartDate string `json:"start_date" jsonschema:"the inclusive first day, YYYY-MM-DD"`
	EndDate   string `json:"end_date" jsonschema:"the inclusive last day, YYYY-MM-DD"`

	PeriodAvgScore *float64 `json:"period_avg_score,omitempty" jsonschema:"the window's average score"`
	PeriodMaxScore *float64 `json:"period_max_score,omitempty" jsonschema:"the window's highest score"`

	CurrentScore     *float64 `json:"current_score,omitempty" jsonschema:"the most recent overall score"`
	CurrentDate      *string  `json:"current_date,omitempty" jsonschema:"the day that score came from"`
	Classification   *string  `json:"classification,omitempty" jsonschema:"the label of a known code"`
	ClassificationID *int     `json:"classification_id,omitempty" jsonschema:"Garmin's classification code"`

	Thresholds   *EnduranceThresholds    `json:"thresholds,omitempty" jsonschema:"the limits of each class"`
	Contributors []EnduranceContribution `json:"contributors" jsonschema:"what contributed to the current score"`

	WeeklyBreakdown []EnduranceWeek `json:"weekly_breakdown" jsonschema:"the window's aggregation buckets, oldest first"`
	Truncated       bool            `json:"truncated" jsonschema:"whether a list was cut at this server's bound"`
}

// LogValue reports the shape of the answer, never a reading.
func (e EnduranceScore) LogValue() slog.Value {
	return shape("enduranceScore",
		slog.Int("weeks", len(e.WeeklyBreakdown)),
		slog.Int("contributors", len(e.Contributors)),
		slog.String("current", presence(e.CurrentScore != nil)),
		slog.Bool("truncated", e.Truncated),
	)
}

// enduranceScoreInput is the strict argument set: an inclusive date window.
type enduranceScoreInput struct {
	StartDate string `json:"start_date" jsonschema:"inclusive first day, YYYY-MM-DD"`
	EndDate   string `json:"end_date" jsonschema:"inclusive last day, YYYY-MM-DD"`
}

func getEnduranceScoreContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetEnduranceScore,
			Title: "Get endurance score",
			Description: "read the account's endurance score over an inclusive date window: " +
				"the window's average and highest score, the current score with its " +
				"classification and the score limits around it, what contributed to it, " +
				"and the weekly buckets Garmin aggregated. The window is bounded by this " +
				"server; narrow it if the call is refused",
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

// registerGetEnduranceScore registers the tool.
func registerGetEnduranceScore(registry *mcpserver.Registry, svc *service) error {
	scores, err := trainingScoresClient(svc)
	if err != nil {
		return err
	}
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in enduranceScoreInput) (
		*mcp.CallToolResult, EnduranceScore, error,
	) {
		span, err := parseWindow(in.StartDate, in.EndDate, svc.limits)
		if err != nil {
			return nil, EnduranceScore{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, EnduranceScore{}, err
		}
		window, err := scores.EnduranceScore(ctx, session, span)
		if err != nil {
			return nil, EnduranceScore{}, fail(err)
		}
		return nil, newEnduranceScore(span, window, svc.limits.MaxDateRangeDays), nil
	}
	return mcpserver.AddTool(registry, getEnduranceScoreContract().Registration(), handler)
}

// newEnduranceScore maps the window onto the bounded result.
//
// The bucket bound is the request layer's own date-window bound: a bucket covers at
// least a day, so a validated window can hold no more buckets than it holds days.
func newEnduranceScore(
	span client.DateRange, window api.EnduranceScoreWindow, maxDays int,
) EnduranceScore {
	out := EnduranceScore{
		StartDate:       span.Start().String(),
		EndDate:         span.End().String(),
		PeriodAvgScore:  optionalFloat(window.Avg),
		PeriodMaxScore:  optionalFloat(window.Max),
		Contributors:    []EnduranceContribution{},
		WeeklyBreakdown: []EnduranceWeek{},
	}
	applyEnduranceCurrent(&out, window.Score)
	appendEnduranceWeeks(&out, window.GroupMap, maxDays)
	return out
}

// applyEnduranceCurrent maps the current score, its classification and its contributors.
func applyEnduranceCurrent(out *EnduranceScore, score *api.EnduranceScoreDTO) {
	if score == nil {
		return
	}
	out.CurrentScore = optionalFloat(score.OverallScore)
	out.CurrentDate = score.CalendarDate
	out.ClassificationID = optionalInt(score.Classification)
	if out.ClassificationID != nil {
		if label, known := enduranceClassifications()[*out.ClassificationID]; known {
			out.Classification = &label
		}
	}

	thresholds := EnduranceThresholds{
		Intermediate: optionalFloat(score.ClassificationLowerIntermediate),
		Trained:      optionalFloat(score.ClassificationLowerTrained),
		WellTrained:  optionalFloat(score.ClassificationLowerWellTrained),
		Expert:       optionalFloat(score.ClassificationLowerExpert),
		Superior:     optionalFloat(score.ClassificationLowerSuperior),
		Elite:        optionalFloat(score.ClassificationLowerElite),
	}
	if thresholds != (EnduranceThresholds{}) {
		out.Thresholds = &thresholds
	}

	contributors, cut := enduranceContributions(score.Contributors.Items())
	out.Contributors = contributors
	out.Truncated = out.Truncated || cut
}

// appendEnduranceWeeks maps the aggregation buckets in calendar order, so the answer
// does not depend on Go's map iteration order.
func appendEnduranceWeeks(
	out *EnduranceScore, groups map[string]api.EnduranceGroup, maxWeeks int,
) {
	starts := slices.Sorted(maps.Keys(groups))
	if len(starts) > maxWeeks {
		starts = starts[:maxWeeks]
		out.Truncated = true
	}
	for _, start := range starts {
		group := groups[start]
		contributors, cut := enduranceContributions(group.Contributors.Items())
		out.Truncated = out.Truncated || cut
		out.WeeklyBreakdown = append(out.WeeklyBreakdown, EnduranceWeek{
			WeekStart:    start,
			AvgScore:     optionalFloat(group.GroupAverage),
			MaxScore:     optionalFloat(group.GroupMax),
			Contributors: contributors,
		})
	}
}

// enduranceContributions maps a contributor list, bounded by the same ceiling the
// activity-type catalog read uses: there is at most one contributor per Garmin
// activity type, and that read already bounds the catalog at defaultMaxActivityTypes.
func enduranceContributions(items []api.EnduranceContributor) ([]EnduranceContribution, bool) {
	truncated := false
	if len(items) > defaultMaxActivityTypes {
		items = items[:defaultMaxActivityTypes]
		truncated = true
	}

	out := make([]EnduranceContribution, 0, len(items))
	for _, item := range items {
		contribution := EnduranceContribution{
			ActivityTypeID: optionalInt(item.ActivityTypeID),
			Group:          optionalInt(item.Group),
		}
		if value, ok := item.Contribution.Float64(); ok {
			rounded := fitRound(value, placesTwo)
			contribution.ContributionPercent = &rounded
		}
		out = append(out, contribution)
	}
	return out, truncated
}
