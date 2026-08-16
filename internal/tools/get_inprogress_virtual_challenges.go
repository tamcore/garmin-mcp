package tools

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetInProgressVirtualChallenges is the upstream compatibility name of the
// in-progress virtual challenge / expedition listing.
//
// Source: Taxuspt/garmin_mcp's get_inprogress_virtual_challenges tool
// (challenges.py:552-623). The shared badge-value formatting and progress-percent
// calculation this file uses are declared in badgechallengelists.go.
const ToolGetInProgressVirtualChallenges = "get_inprogress_virtual_challenges"

// A VirtualChallenge is one in-progress virtual challenge or expedition, curated
// the way get_inprogress_virtual_challenges curates one (challenges.py:577-616).
//
// Unlike every other challenge family in this package, a distance challenge (no
// unit id, or unit id 1) reports its progress and target in meters and kilometers
// rather than through formatBadgeValue, matching challenges.py:602-608 exactly:
// that branch preserves the tool's original output contract from before the other
// units were added. It is health data and identity material together: never log
// it.
type VirtualChallenge struct {
	Name      *string `json:"name,omitempty" jsonschema:"the challenge's display name"`
	UUID      *string `json:"uuid,omitempty" jsonschema:"the challenge's identifier"`
	StartDate *string `json:"start_date,omitempty" jsonschema:"the challenge's start date"`
	EndDate   *string `json:"end_date,omitempty" jsonschema:"the challenge's end date"`

	ProgressMeters *float64 `json:"progress_meters,omitempty" jsonschema:"progress in meters, for a distance challenge"`
	TargetMeters   *float64 `json:"target_meters,omitempty" jsonschema:"target in meters, for a distance challenge"`
	ProgressKM     *string  `json:"progress_km,omitempty" jsonschema:"progress in kilometers, for a distance challenge"`
	TargetKM       *string  `json:"target_km,omitempty" jsonschema:"target in kilometers, for a distance challenge"`

	Progress *string `json:"progress,omitempty" jsonschema:"the formatted progress value, for a non-distance challenge"`
	Target   *string `json:"target,omitempty" jsonschema:"the formatted target value, for a non-distance challenge"`

	ProgressPercent *string `json:"progress_percent,omitempty" jsonschema:"the progress as a percentage"`
}

// LogValue reports which fields arrived, never a name, a date or a progress figure.
func (c VirtualChallenge) LogValue() slog.Value {
	return shape("virtualChallenge",
		slog.String("name", presence(c.Name != nil)),
		slog.String("progress", presence(c.Progress != nil || c.ProgressMeters != nil)),
	)
}

// A VirtualChallengeList is one bounded page of in-progress virtual challenges.
type VirtualChallengeList struct {
	Challenges []VirtualChallenge `json:"challenges" jsonschema:"the challenges on this page"`
	Total      int                `json:"total" jsonschema:"how many challenges this page carries"`

	// Truncated reports that Garmin returned more challenges than the requested
	// limit and this result was cut to it: Garmin does not reliably honor the
	// limit it is asked for.
	Truncated bool `json:"truncated" jsonschema:"whether this page was cut to the requested limit"`
}

// LogValue reports the page size, never a challenge.
func (l VirtualChallengeList) LogValue() slog.Value {
	return shape("virtualChallengeList",
		slog.Int("challenges", len(l.Challenges)),
		slog.Bool("truncated", l.Truncated),
	)
}

func getInProgressVirtualChallengesContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetInProgressVirtualChallenges,
			Title: "Get in-progress virtual challenges",
			Description: "read the account's in-progress virtual challenges and " +
				"expeditions, such as a walking route along a famous trail",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(challengePageProperties(1, 1)...),
	}
}

// registerGetInProgressVirtualChallenges registers the tool.
//
// The minimum start of 1 matches internal/garmin/api's own InProgressVirtualChallenges,
// which refuses start 0: garminconnect 0.3.2 rejects it for this endpoint alone, unlike
// every other challenge list in this package.
func registerGetInProgressVirtualChallenges(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in challengePageInput) (
		*mcp.CallToolResult, VirtualChallengeList, error,
	) {
		page, err := resolveChallengePage(in.Start, in.Limit, 1, 1)
		if err != nil {
			return nil, VirtualChallengeList{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, VirtualChallengeList{}, err
		}
		result, err := svc.challenges.InProgressVirtualChallenges(ctx, session, page)
		if err != nil {
			return nil, VirtualChallengeList{}, fail(err)
		}
		return nil, newVirtualChallengeList(result.Challenges, page.Limit()), nil
	}
	return mcpserver.AddTool(registry, getInProgressVirtualChallengesContract().Registration(), handler)
}

// newVirtualChallengeList maps a page of decoded items onto the bounded result and
// caps it at limit, the page's own requested limit (client.Page.Limit()): Garmin
// does not reliably honor the limit it was asked for.
func newVirtualChallengeList(items []api.VirtualChallengeItem, limit int) VirtualChallengeList {
	out := make([]VirtualChallenge, 0, len(items))
	for _, item := range items {
		out = append(out, curateVirtualChallenge(item))
	}
	out, truncated := boundChallengePage(out, limit)
	return VirtualChallengeList{Challenges: out, Total: len(out), Truncated: truncated}
}

// curateVirtualChallenge ports the per-item curation of
// get_inprogress_virtual_challenges (challenges.py:577-616).
func curateVirtualChallenge(item api.VirtualChallengeItem) VirtualChallenge {
	out := VirtualChallenge{
		StartDate: optionalISODate(item.StartDate),
		EndDate:   optionalISODate(item.EndDate),
	}
	if name, ok := item.Title(); ok {
		out.Name = &name
	}
	if uuid, ok := item.UUID.Value(); ok {
		out.UUID = &uuid
	}
	applyVirtualChallengeProgress(&out, item)
	return out
}

// applyVirtualChallengeProgress adds progress, target and progress_percent only
// when both are present and the target is positive, matching challenges.py:600.
func applyVirtualChallengeProgress(out *VirtualChallenge, item api.VirtualChallengeItem) {
	progress, hasProgress := item.Progress()
	target, hasTarget := item.Target()
	if !hasProgress || !hasTarget || target <= 0 {
		return
	}

	unitID, hasUnit := item.UnitID.Int64()
	if !hasUnit || unitID == 1 {
		progressKM := fmt.Sprintf("%.2f km", progress/1000)
		targetKM := fmt.Sprintf("%.2f km", target/1000)
		out.ProgressMeters = &progress
		out.TargetMeters = &target
		out.ProgressKM = &progressKM
		out.TargetKM = &targetKM
	} else {
		formattedProgress := formatBadgeValue(progress, item.UnitID)
		formattedTarget := formatBadgeValue(target, item.UnitID)
		out.Progress = &formattedProgress
		out.Target = &formattedTarget
	}

	percent := calculateProgressPercent(progress, target)
	out.ProgressPercent = &percent
}
