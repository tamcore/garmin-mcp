package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolCountActivities is the upstream compatibility name of the activity count.
const ToolCountActivities = "count_activities"

// countActivitiesNote is the advice upstream returns beside the count, restated for
// this server's own tool names.
const countActivitiesNote = "call get_activities with start and limit, or " +
	"get_activities_by_date, to read the activities themselves"

// ActivityCount is how many activities the account holds.
//
// It is ordinary data: one number about the size of a history, with nothing in it
// about what the history contains.
type ActivityCount struct {
	TotalActivities int64  `json:"total_activities" jsonschema:"how many activities the account holds"`
	Note            string `json:"note" jsonschema:"how to read the activities this count refers to"`
}

// LogValue reports that a count was read, never the count itself.
func (c ActivityCount) LogValue() slog.Value { return shape("activityCount") }

func countActivitiesContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolCountActivities,
			Title: "Count activities",
			Description: "read how many activities the authenticated account holds. Takes no " +
				"arguments. Use it to size a history before paging through it, rather than " +
				"walking every page to find out",
			Tier:        policy.TierReadOnly,
			Category:    categoryOrdinary,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(),
	}
}

// registerCountActivities registers the tool.
func registerCountActivities(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, _ noArguments) (
		*mcp.CallToolResult, ActivityCount, error,
	) {
		session, err := svc.session(ctx)
		if err != nil {
			return nil, ActivityCount{}, err
		}

		total, err := svc.activities.Count(ctx, session)
		if err != nil {
			return nil, ActivityCount{}, fail(err)
		}
		return nil, ActivityCount{TotalActivities: total, Note: countActivitiesNote}, nil
	}
	return mcpserver.AddTool(registry, countActivitiesContract().Registration(), handler)
}
