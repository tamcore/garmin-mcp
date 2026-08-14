package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetActivities is the upstream compatibility name of the activity-list tool.
const ToolGetActivities = "get_activities"

// ActivitySummary is one activity, bounded and stripped.
//
// The start coordinates Garmin sends are deliberately absent. They are the most
// sensitive field on the record — they name where a person lives — and no caller of
// this tool has asked for them.
type ActivitySummary struct {
	ActivityID     *int64   `json:"activity_id,omitempty" jsonschema:"the Garmin activity identifier"`
	Name           *string  `json:"activity_name,omitempty" jsonschema:"the activity name"`
	Description    *string  `json:"description,omitempty" jsonschema:"the activity description"`
	StartTimeLocal *string  `json:"start_time_local,omitempty" jsonschema:"the local start time"`
	StartTimeGMT   *string  `json:"start_time_gmt,omitempty" jsonschema:"the UTC start time"`
	ActivityType   *string  `json:"activity_type,omitempty" jsonschema:"the activity type key"`
	EventType      *string  `json:"event_type,omitempty" jsonschema:"the event type key, for example race"`
	DistanceMeters *float64 `json:"distance_meters,omitempty" jsonschema:"the distance in meters"`
	DurationSecs   *float64 `json:"duration_seconds,omitempty" jsonschema:"the timed duration in seconds"`
	ElapsedSecs    *float64 `json:"elapsed_seconds,omitempty" jsonschema:"the elapsed duration in seconds"`
	MovingSecs     *float64 `json:"moving_seconds,omitempty" jsonschema:"the moving duration in seconds"`
	Calories       *float64 `json:"calories,omitempty" jsonschema:"the kilocalories burned"`
	AverageHR      *float64 `json:"average_heart_rate,omitempty" jsonschema:"the average heart rate in bpm"`
	MaxHR          *float64 `json:"max_heart_rate,omitempty" jsonschema:"the maximum heart rate in bpm"`
	Favorite       *bool    `json:"favorite,omitempty" jsonschema:"whether the account marked it a favorite"`
}

// ActivityList is one page of activities plus the window it came from.
type ActivityList struct {
	Activities []ActivitySummary `json:"activities" jsonschema:"the activities on this page, newest first"`
	Count      int               `json:"count" jsonschema:"how many activities this page carries"`
	Start      int               `json:"start" jsonschema:"the record offset this page started at"`
	Limit      int               `json:"limit" jsonschema:"the effective page size, after this server's bound"`
}

// LogValue reports the page size, never the activities.
func (l ActivityList) LogValue() slog.Value {
	return shape("activityList",
		slog.Int("activities", len(l.Activities)),
		slog.Int("start", l.Start),
		slog.Int("limit", l.Limit),
	)
}

// getActivitiesInput is the strict argument set. Both fields are pointers so an
// absent argument takes the manifest default while an explicit out-of-range value is
// still refused.
type getActivitiesInput struct {
	Start *int `json:"start,omitempty" jsonschema:"zero-based record offset, default 0"`
	Limit *int `json:"limit,omitempty" jsonschema:"activities per page, 1 to 100, default 20"`
}

func getActivitiesContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetActivities,
			Title: "Get activities",
			Description: "read a page of the account's activities, newest first. Use this " +
				"to browse a large history without a date filter; use get_activities_by_date " +
				"when the question names a period. Start coordinates are not returned",
			Tier:        policy.TierReadOnly,
			Category:    categoryLocation,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(
			Property{
				Name:        "start",
				Types:       []string{typeInteger},
				Description: "zero-based record offset",
				Minimum:     bound(0),
				Maximum:     bound(2_000_000),
				Default:     defaultActivityStart,
			},
			Property{
				Name:        "limit",
				Types:       []string{typeInteger},
				Description: "how many activities to return; the server may lower it",
				Minimum:     bound(1),
				Maximum:     bound(DefaultMaxActivityPageSize),
				Default:     defaultActivityLimit,
			},
		),
	}
}

// registerGetActivities registers the tool.
func registerGetActivities(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getActivitiesInput) (
		*mcp.CallToolResult, ActivityList, error,
	) {
		page, err := resolveActivityPage(in.Start, in.Limit, svc.limits)
		if err != nil {
			return nil, ActivityList{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, ActivityList{}, err
		}

		result, err := svc.activities.List(ctx, session, api.ListQuery{Page: page})
		if err != nil {
			return nil, ActivityList{}, fail(err)
		}
		return nil, ActivityList{
			Activities: newActivitySummaries(result.Activities),
			Count:      len(result.Activities),
			Start:      page.Start(),
			Limit:      page.Limit(),
		}, nil
	}
	return mcpserver.AddTool(registry, getActivitiesContract().Registration(), handler)
}

// newActivitySummaries maps the domain models onto the bounded results.
func newActivitySummaries(activities []api.Activity) []ActivitySummary {
	out := make([]ActivitySummary, 0, len(activities))
	for _, activity := range activities {
		out = append(out, newActivitySummary(activity))
	}
	return out
}

func newActivitySummary(activity api.Activity) ActivitySummary {
	return ActivitySummary{
		ActivityID:     activity.ActivityID,
		Name:           activity.ActivityName,
		Description:    activity.Description,
		StartTimeLocal: activity.StartTimeLocal,
		StartTimeGMT:   activity.StartTimeGMT,
		ActivityType:   typeKeyOf(activity.ActivityType),
		EventType:      typeKeyOf(activity.EventType),
		DistanceMeters: optionalFloat(activity.Distance),
		DurationSecs:   optionalFloat(activity.Duration),
		ElapsedSecs:    optionalFloat(activity.ElapsedTime),
		MovingSecs:     optionalFloat(activity.MovingTime),
		Calories:       optionalFloat(activity.Calories),
		AverageHR:      optionalFloat(activity.AverageHR),
		MaxHR:          optionalFloat(activity.MaxHR),
		Favorite:       activity.Favorite,
	}
}
