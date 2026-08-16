package tools

import (
	"cmp"
	"context"
	"log/slog"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetEarnedBadges is the upstream compatibility name of the earned-badge read.
//
// Source: Taxuspt/garmin_mcp's get_earned_badges tool (challenges.py:296-360). The
// shared badge-value formatting this file uses is declared in
// badgechallengelists.go.
const ToolGetEarnedBadges = "get_earned_badges"

// defaultMaxEarnedBadges bounds the earned-badge collection this tool returns. A
// live account sample already carried 486 badges in one response with no
// pagination argument to narrow it, so this bound is set comfortably above that
// observed size rather than at it.
const defaultMaxEarnedBadges = 1000

// badgeCategoryLabels maps an earned badge's category id onto its display label.
// Source: BADGE_CATEGORY_MAPPING (challenges.py:14-21).
func badgeCategoryLabels() map[int64]string {
	return map[int64]string{
		1: "Activity", 2: challengeLabelRunning, 3: challengeLabelCycling, 4: "Challenge",
		5: challengeLabelSteps, 9: "Diving",
	}
}

// badgeDifficultyLabels maps an earned badge's difficulty id onto its display
// label. Source: BADGE_DIFFICULTY_MAPPING (challenges.py:24-28).
func badgeDifficultyLabels() map[int64]string {
	return map[int64]string{1: "Easy", 2: "Medium", 3: "Hard"}
}

// An EarnedBadge is one badge the account has earned, curated the way
// get_earned_badges curates one (challenges.py:306-350).
//
// Every field is optional. A live account sample of 486 earned badges carried
// category, difficulty, earned_date, name and points on every one of them, and
// challenge_period, progress, target, activity_id and series_id on a minority —
// respectively 405, 272, 272, 162 and 50 of the 486 — so none of the ten can be
// relied on to be present, and this type declares none of them required. It is
// health data and identity material together: never log one.
type EarnedBadge struct {
	Name            *string  `json:"name,omitempty" jsonschema:"the badge's display name"`
	Category        *string  `json:"category,omitempty" jsonschema:"the badge category label"`
	Difficulty      *string  `json:"difficulty,omitempty" jsonschema:"the badge difficulty label"`
	Points          *float64 `json:"points,omitempty" jsonschema:"the badge points earned"`
	EarnedDate      *string  `json:"earned_date,omitempty" jsonschema:"the date the badge was earned"`
	Progress        *string  `json:"progress,omitempty" jsonschema:"the formatted progress value"`
	Target          *string  `json:"target,omitempty" jsonschema:"the formatted target value"`
	ChallengePeriod *string  `json:"challenge_period,omitempty" jsonschema:"the challenge's date range, if any"`
	ActivityID      *int64   `json:"activity_id,omitempty" jsonschema:"the activity that earned the badge, if linked"`
	SeriesID        *int64   `json:"series_id,omitempty" jsonschema:"the badge series identifier, if part of one"`
}

// LogValue reports which fields arrived, never a name, a date or a point count.
func (b EarnedBadge) LogValue() slog.Value {
	return shape("earnedBadge",
		slog.String("name", presence(b.Name != nil)),
		slog.String("category", presence(b.Category != nil)),
		slog.String("earnedDate", presence(b.EarnedDate != nil)),
		slog.String("progress", presence(b.Progress != nil)),
	)
}

// An EarnedBadgeList is the whole bounded earned-badge collection, most recently
// earned first, matching challenges.py:353.
type EarnedBadgeList struct {
	Badges      []EarnedBadge `json:"badges" jsonschema:"the earned badges, most recently earned first"`
	TotalBadges int           `json:"total_badges" jsonschema:"how many badges this result carries"`

	// Truncated reports the collection was cut at defaultMaxEarnedBadges: this
	// tool takes no pagination argument, so a real account already past that bound
	// has no way to ask for a narrower slice.
	Truncated bool `json:"truncated" jsonschema:"whether the collection was cut at this server's bound"`
}

// LogValue reports the badge count, never a badge.
func (l EarnedBadgeList) LogValue() slog.Value {
	return shape("earnedBadgeList",
		slog.Int("badges", len(l.Badges)),
		slog.Bool("truncated", l.Truncated),
	)
}

func getEarnedBadgesContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:        ToolGetEarnedBadges,
			Title:       "Get earned badges",
			Description: "read every badge the account has earned",
			Tier:        policy.TierReadOnly,
			Category:    categoryOrdinary,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(),
	}
}

func registerGetEarnedBadges(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, _ noArguments) (
		*mcp.CallToolResult, EarnedBadgeList, error,
	) {
		session, err := svc.session(ctx)
		if err != nil {
			return nil, EarnedBadgeList{}, err
		}
		badges, err := svc.challenges.EarnedBadges(ctx, session)
		if err != nil {
			return nil, EarnedBadgeList{}, fail(err)
		}
		return nil, newEarnedBadgeList(badges), nil
	}
	return mcpserver.AddTool(registry, getEarnedBadgesContract().Registration(), handler)
}

// newEarnedBadgeList maps the decoded badges, sorted by earned date descending
// (challenges.py:353: `x.get("earned_date") or ""`, reverse=True), and caps the
// collection at defaultMaxEarnedBadges.
func newEarnedBadgeList(badges []api.EarnedBadge) EarnedBadgeList {
	out := make([]EarnedBadge, 0, len(badges))
	for _, badge := range badges {
		out = append(out, curateEarnedBadge(badge))
	}
	slices.SortFunc(out, func(a, b EarnedBadge) int {
		return cmp.Compare(dateKey(b.EarnedDate), dateKey(a.EarnedDate))
	})
	out, truncated := boundChallengePage(out, defaultMaxEarnedBadges)
	return EarnedBadgeList{Badges: out, TotalBadges: len(out), Truncated: truncated}
}

// curateEarnedBadge ports the per-badge curation of get_earned_badges
// (challenges.py:306-350).
func curateEarnedBadge(badge api.EarnedBadge) EarnedBadge {
	out := EarnedBadge{
		Name:       optionalText(badge.BadgeName),
		Points:     optionalFloat(badge.BadgePoints),
		EarnedDate: optionalISODate(badge.BadgeEarnedDate),
		SeriesID:   optionalInt64(badge.BadgeSeriesID),
	}
	if categoryID, ok := badge.BadgeCategoryID.Int64(); ok {
		category := mappedLabel(badgeCategoryLabels(), categoryID, "category")
		out.Category = &category
	}
	if difficultyID, ok := badge.BadgeDifficultyID.Int64(); ok {
		difficulty := mappedLabel(badgeDifficultyLabels(), difficultyID, "level")
		out.Difficulty = &difficulty
	}
	applyEarnedBadgeProgress(&out, badge)
	applyEarnedBadgeChallengePeriod(&out, badge)
	applyEarnedBadgeActivityLink(&out, badge)
	return out
}

// applyEarnedBadgeProgress adds progress and target only when both are present,
// matching challenges.py:330-332 — unlike the badge-challenge families, a positive
// target is not additionally required here.
func applyEarnedBadgeProgress(out *EarnedBadge, badge api.EarnedBadge) {
	progress, hasProgress := badge.BadgeProgressValue.Float64()
	target, hasTarget := badge.BadgeTargetValue.Float64()
	if !hasProgress || !hasTarget {
		return
	}
	formattedProgress := formatBadgeValue(progress, badge.BadgeUnitID)
	formattedTarget := formatBadgeValue(target, badge.BadgeUnitID)
	out.Progress = &formattedProgress
	out.Target = &formattedTarget
}

// applyEarnedBadgeChallengePeriod adds the challenge_period range only when both
// dates parse to a non-empty value, matching challenges.py:335-338.
func applyEarnedBadgeChallengePeriod(out *EarnedBadge, badge api.EarnedBadge) {
	start := optionalISODate(badge.BadgeStartDate)
	end := optionalISODate(badge.BadgeEndDate)
	if start == nil || end == nil {
		return
	}
	period := *start + " to " + *end
	out.ChallengePeriod = &period
}

// applyEarnedBadgeActivityLink adds activity_id only when the badge is linked to
// one, matching challenges.py:341-344:
// `badge.get("badgeAssocType") == "activityId" and badge.get("badgeAssocDataId")`.
func applyEarnedBadgeActivityLink(out *EarnedBadge, badge api.EarnedBadge) {
	assocType, ok := badge.BadgeAssocType.Value()
	if !ok || assocType != "activityId" {
		return
	}
	out.ActivityID = optionalInt64(badge.BadgeAssocDataID)
}
