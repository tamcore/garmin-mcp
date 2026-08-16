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

// The three near-identical badge-challenge listings — the account's joined
// challenges, the challenges available to join, and the joined-but-not-completed
// subset. The curation and pagination helpers all three share — and that
// get_adhoc_challenges (get_adhoc_challenges.go) and
// get_inprogress_virtual_challenges (get_inprogress_virtual_challenges.go) also
// call into — live in challengecuration.go.
//
// Source: Taxuspt/garmin_mcp's src/garmin_mcp/challenges.py at the commit
// docs/upstream-pins.md names. All three tools curate through the one shared
// _curate_badge_challenge (challenges.py:176-207), which this file ports as
// curateBadgeChallenge.

// The upstream compatibility names of the three badge-challenge listings.
const (
	ToolGetAvailableBadgeChallenges    = "get_available_badge_challenges"
	ToolGetBadgeChallenges             = "get_badge_challenges"
	ToolGetNonCompletedBadgeChallenges = "get_non_completed_badge_challenges"
)

// A BadgeChallenge is one badge challenge — joined, available or non-completed —
// curated the way _curate_badge_challenge curates all three (challenges.py:176-207).
// It is health data and identity material together: never log one.
type BadgeChallenge struct {
	Name            *string `json:"name,omitempty" jsonschema:"the challenge's display name"`
	UUID            *string `json:"uuid,omitempty" jsonschema:"the challenge's identifier"`
	Category        *string `json:"category,omitempty" jsonschema:"the challenge category label"`
	Status          *string `json:"status,omitempty" jsonschema:"the challenge status label"`
	Points          *int64  `json:"points,omitempty" jsonschema:"the badge points the challenge awards"`
	StartDate       *string `json:"start_date,omitempty" jsonschema:"the challenge's start date"`
	EndDate         *string `json:"end_date,omitempty" jsonschema:"the challenge's end date"`
	Joined          *bool   `json:"joined,omitempty" jsonschema:"whether the account has joined"`
	Progress        *string `json:"progress,omitempty" jsonschema:"the formatted progress value"`
	Target          *string `json:"target,omitempty" jsonschema:"the formatted target value"`
	ProgressPercent *string `json:"progress_percent,omitempty" jsonschema:"the progress as a percentage"`
	EarnedDate      *string `json:"earned_date,omitempty" jsonschema:"the date the challenge was completed"`
	Joinable        *bool   `json:"joinable,omitempty" jsonschema:"whether the account may still join"`
}

// LogValue reports which fields arrived, never a name, a date or a progress figure.
func (b BadgeChallenge) LogValue() slog.Value {
	return shape("badgeChallenge",
		slog.String("name", presence(b.Name != nil)),
		slog.String("status", presence(b.Status != nil)),
		slog.String("progress", presence(b.Progress != nil)),
		slog.String("earnedDate", presence(b.EarnedDate != nil)),
	)
}

// A BadgeChallengeList is one bounded page of badge challenges.
type BadgeChallengeList struct {
	Challenges []BadgeChallenge `json:"challenges" jsonschema:"the challenges on this page"`
	Total      int              `json:"total" jsonschema:"how many challenges this page carries"`

	// Truncated reports that Garmin returned more challenges than the requested
	// limit and this result was cut to it: Garmin does not reliably honor the
	// limit it is asked for.
	Truncated bool `json:"truncated" jsonschema:"whether this page was cut to the requested limit"`
}

// LogValue reports the page size, never a challenge.
func (l BadgeChallengeList) LogValue() slog.Value {
	return shape("badgeChallengeList",
		slog.Int("challenges", len(l.Challenges)),
		slog.Bool("truncated", l.Truncated),
	)
}

// curateBadgeChallenge ports _curate_badge_challenge (challenges.py:176-207).
func curateBadgeChallenge(item api.BadgeChallengeItem) BadgeChallenge {
	out := BadgeChallenge{
		Name:      optionalText(item.Name),
		UUID:      optionalText(item.UUID),
		Points:    optionalInt64(item.Points),
		StartDate: optionalISODate(item.StartDate),
		EndDate:   optionalISODate(item.EndDate),
		Joined:    item.UserJoined,
		Joinable:  item.Joinable,
	}
	if categoryID, ok := item.CategoryID.Int64(); ok {
		category := mappedLabel(challengeCategoryLabels(), categoryID, "category")
		out.Category = &category
	}
	if statusID, ok := item.StatusID.Int64(); ok {
		status := mappedLabel(challengeStatusLabels(), statusID, "status")
		out.Status = &status
	}
	applyBadgeChallengeProgress(&out, item)
	out.EarnedDate = optionalISODate(item.EarnedDate)
	return out
}

// applyBadgeChallengeProgress adds target only when a positive target is present,
// matching challenges.py:182 (`progress = challenge.get("badgeProgressValue")`),
// 197-200 (`if target is not None and target > 0`), and _format_badge_value's own
// `if value is None: return None` (challenges.py:146-149, 148). Progress and
// progress_percent are added only when progress itself is present: upstream still
// calls _format_badge_value(progress, ...) unconditionally once target qualifies,
// but that call returns None for a None progress, and this package represents an
// absent value as an absent field rather than fabricating a zero.
func applyBadgeChallengeProgress(out *BadgeChallenge, item api.BadgeChallengeItem) {
	target, ok := item.TargetValue.Float64()
	if !ok || target <= 0 {
		return
	}
	formattedTarget := formatBadgeValue(target, item.UnitID)
	out.Target = &formattedTarget

	progress, hasProgress := item.ProgressValue.Float64()
	if !hasProgress {
		return
	}
	formattedProgress := formatBadgeValue(progress, item.UnitID)
	percent := calculateProgressPercent(progress, target)
	out.Progress = &formattedProgress
	out.ProgressPercent = &percent
}

// newBadgeChallengeList maps a page of decoded items, sorts it with less, and caps
// it at limit. limit is the page's own requested limit (client.Page.Limit()):
// Garmin does not reliably honor the limit it was asked for, so this is a defensive
// bound on top of the request, not a substitute for it.
func newBadgeChallengeList(
	items []api.BadgeChallengeItem, limit int, less func(a, b BadgeChallenge) int,
) BadgeChallengeList {
	out := make([]BadgeChallenge, 0, len(items))
	for _, item := range items {
		out = append(out, curateBadgeChallenge(item))
	}
	slices.SortFunc(out, less)
	out, truncated := boundChallengePage(out, limit)
	return BadgeChallengeList{Challenges: out, Total: len(out), Truncated: truncated}
}

// newAvailableBadgeChallengeList sorts by start date ascending, soonest first,
// matching challenges.py:435: `x.get("start_date") or ""`, no reverse.
func newAvailableBadgeChallengeList(items []api.BadgeChallengeItem, limit int) BadgeChallengeList {
	return newBadgeChallengeList(items, limit, func(a, b BadgeChallenge) int {
		return cmp.Compare(dateKey(a.StartDate), dateKey(b.StartDate))
	})
}

// newJoinedBadgeChallengeList sorts by start date descending, most recent first,
// matching challenges.py:466-468: `x.get("start_date") or ""`, reverse=True.
func newJoinedBadgeChallengeList(items []api.BadgeChallengeItem, limit int) BadgeChallengeList {
	return newBadgeChallengeList(items, limit, func(a, b BadgeChallenge) int {
		return cmp.Compare(dateKey(b.StartDate), dateKey(a.StartDate))
	})
}

// newNonCompletedBadgeChallengeList sorts by end date ascending, ending soonest
// first, matching challenges.py:503: `x.get("end_date") or ""`, no reverse.
func newNonCompletedBadgeChallengeList(items []api.BadgeChallengeItem, limit int) BadgeChallengeList {
	return newBadgeChallengeList(items, limit, func(a, b BadgeChallenge) int {
		return cmp.Compare(dateKey(a.EndDate), dateKey(b.EndDate))
	})
}

func getAvailableBadgeChallengesContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetAvailableBadgeChallenges,
			Title: "Get available badge challenges",
			Description: "read the official Garmin badge challenges the account may join: " +
				"monthly and seasonal challenges that award badges and points on completion",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(challengePageProperties(1, 0)...),
	}
}

func registerGetAvailableBadgeChallenges(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in challengePageInput) (
		*mcp.CallToolResult, BadgeChallengeList, error,
	) {
		page, err := resolveChallengePage(in.Start, in.Limit, 1, 0)
		if err != nil {
			return nil, BadgeChallengeList{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, BadgeChallengeList{}, err
		}
		result, err := svc.challenges.AvailableBadgeChallenges(ctx, session, page)
		if err != nil {
			return nil, BadgeChallengeList{}, fail(err)
		}
		return nil, newAvailableBadgeChallengeList(result.Challenges, page.Limit()), nil
	}
	return mcpserver.AddTool(registry, getAvailableBadgeChallengesContract().Registration(), handler)
}

func getBadgeChallengesContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetBadgeChallenges,
			Title: "Get joined badge challenges",
			Description: "read every badge challenge the account has joined, completed and " +
				"in-progress alike, with progress, completion status and earned dates",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(challengePageProperties(1, 0)...),
	}
}

func registerGetBadgeChallenges(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in challengePageInput) (
		*mcp.CallToolResult, BadgeChallengeList, error,
	) {
		page, err := resolveChallengePage(in.Start, in.Limit, 1, 0)
		if err != nil {
			return nil, BadgeChallengeList{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, BadgeChallengeList{}, err
		}
		result, err := svc.challenges.BadgeChallenges(ctx, session, page)
		if err != nil {
			return nil, BadgeChallengeList{}, fail(err)
		}
		return nil, newJoinedBadgeChallengeList(result.Challenges, page.Limit()), nil
	}
	return mcpserver.AddTool(registry, getBadgeChallengesContract().Registration(), handler)
}

func getNonCompletedBadgeChallengesContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetNonCompletedBadgeChallenges,
			Title: "Get in-progress badge challenges",
			Description: "read the badge challenges the account has joined but has not yet " +
				"completed, for tracking current progress toward a badge",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(challengePageProperties(1, 0)...),
	}
}

func registerGetNonCompletedBadgeChallenges(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in challengePageInput) (
		*mcp.CallToolResult, BadgeChallengeList, error,
	) {
		page, err := resolveChallengePage(in.Start, in.Limit, 1, 0)
		if err != nil {
			return nil, BadgeChallengeList{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, BadgeChallengeList{}, err
		}
		result, err := svc.challenges.NonCompletedBadgeChallenges(ctx, session, page)
		if err != nil {
			return nil, BadgeChallengeList{}, fail(err)
		}
		return nil, newNonCompletedBadgeChallengeList(result.Challenges, page.Limit()), nil
	}
	return mcpserver.AddTool(registry, getNonCompletedBadgeChallengesContract().Registration(), handler)
}
