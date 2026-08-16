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

// ToolGetAdhocChallenges is the upstream compatibility name of the user-created
// social/group challenge listing.
//
// Source: Taxuspt/garmin_mcp's get_adhoc_challenges tool (challenges.py:362-409).
// The shared pagination and formatting helpers this file uses are declared in
// badgechallengelists.go.
const ToolGetAdhocChallenges = "get_adhoc_challenges"

// adhocActivityTypeLabels maps an adhoc challenge's activity type id onto its
// display label. Source: ADHOC_ACTIVITY_TYPE_MAPPING (challenges.py:60-66).
func adhocActivityTypeLabels() map[int64]string {
	return map[int64]string{
		1: challengeLabelRunning, 2: challengeLabelCycling, 3: "Swimming",
		4: challengeLabelSteps, 5: "Walking",
	}
}

// An AdhocChallenge is one user-created social challenge, curated the way
// get_adhoc_challenges curates one (challenges.py:378-397). It is health data and
// identity material together — a ranking and a player count both ride along — so
// it must never be logged.
type AdhocChallenge struct {
	Name         *string `json:"name,omitempty" jsonschema:"the challenge's display name"`
	Description  *string `json:"description,omitempty" jsonschema:"the challenge's description"`
	UUID         *string `json:"uuid,omitempty" jsonschema:"the challenge's identifier"`
	ActivityType *string `json:"activity_type,omitempty" jsonschema:"the activity type label"`
	Status       *string `json:"status,omitempty" jsonschema:"the challenge status label"`
	StartDate    *string `json:"start_date,omitempty" jsonschema:"the challenge's start date"`
	EndDate      *string `json:"end_date,omitempty" jsonschema:"the challenge's end date"`
	YourRanking  *int64  `json:"your_ranking,omitempty" jsonschema:"the account's rank among players"`
	PlayerCount  *int64  `json:"player_count,omitempty" jsonschema:"how many players joined"`
}

// LogValue reports which fields arrived, never a name, a ranking or a date.
func (c AdhocChallenge) LogValue() slog.Value {
	return shape("adhocChallenge",
		slog.String("name", presence(c.Name != nil)),
		slog.String("status", presence(c.Status != nil)),
		slog.String("yourRanking", presence(c.YourRanking != nil)),
	)
}

// An AdhocChallengeList is one bounded page of adhoc challenges, newest start date
// first, matching challenges.py:400-402.
type AdhocChallengeList struct {
	Challenges []AdhocChallenge `json:"challenges" jsonschema:"the challenges on this page, most recently started first"`
	Total      int              `json:"total" jsonschema:"how many challenges this page carries"`

	// Truncated reports that Garmin returned more challenges than the requested
	// limit and this result was cut to it: Garmin does not reliably honor the
	// limit it is asked for.
	Truncated bool `json:"truncated" jsonschema:"whether this page was cut to the requested limit"`
}

// LogValue reports the page size, never a challenge.
func (l AdhocChallengeList) LogValue() slog.Value {
	return shape("adhocChallengeList",
		slog.Int("challenges", len(l.Challenges)),
		slog.Bool("truncated", l.Truncated),
	)
}

func getAdhocChallengesContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetAdhocChallenges,
			Title: "Get adhoc challenges",
			Description: "read the account's user-created social and group challenges, such " +
				"as step competitions with connections. These are separate from Garmin's own " +
				"badge challenges",
			Tier:        policy.TierReadOnly,
			Category:    categoryOrdinary,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(challengePageProperties(0, 0)...),
	}
}

func registerGetAdhocChallenges(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in challengePageInput) (
		*mcp.CallToolResult, AdhocChallengeList, error,
	) {
		page, err := resolveChallengePage(in.Start, in.Limit, 0, 0)
		if err != nil {
			return nil, AdhocChallengeList{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, AdhocChallengeList{}, err
		}
		result, err := svc.challenges.AdhocChallenges(ctx, session, page)
		if err != nil {
			return nil, AdhocChallengeList{}, fail(err)
		}
		return nil, newAdhocChallengeList(result.Challenges, page.Limit()), nil
	}
	return mcpserver.AddTool(registry, getAdhocChallengesContract().Registration(), handler)
}

// newAdhocChallengeList maps a page of decoded items, sorted by start date
// descending (challenges.py:400-402: `x.get("start_date") or ""`, reverse=True), and
// caps it at limit, the page's own requested limit (client.Page.Limit()): Garmin
// does not reliably honor the limit it was asked for.
func newAdhocChallengeList(items []api.AdhocChallengeItem, limit int) AdhocChallengeList {
	out := make([]AdhocChallenge, 0, len(items))
	for _, item := range items {
		out = append(out, curateAdhocChallenge(item))
	}
	slices.SortFunc(out, func(a, b AdhocChallenge) int {
		return cmp.Compare(dateKey(b.StartDate), dateKey(a.StartDate))
	})
	out, truncated := boundChallengePage(out, limit)
	return AdhocChallengeList{Challenges: out, Total: len(out), Truncated: truncated}
}

// curateAdhocChallenge ports the per-item curation of get_adhoc_challenges
// (challenges.py:378-397).
func curateAdhocChallenge(item api.AdhocChallengeItem) AdhocChallenge {
	out := AdhocChallenge{
		Name:        optionalText(item.Name),
		Description: optionalText(item.Description),
		UUID:        optionalText(item.UUID),
		StartDate:   optionalISODate(item.StartDate),
		EndDate:     optionalISODate(item.EndDate),
		YourRanking: optionalInt64(item.UserRanking),
		PlayerCount: optionalInt64(item.PlayerCount),
	}
	if typeID, ok := item.ActivityTypeID.Int64(); ok {
		activityType := mappedLabel(adhocActivityTypeLabels(), typeID, "type")
		out.ActivityType = &activityType
	}
	if statusID, ok := item.StatusID.Int64(); ok {
		status := mappedLabel(challengeStatusLabels(), statusID, "status")
		out.Status = &status
	}
	return out
}
