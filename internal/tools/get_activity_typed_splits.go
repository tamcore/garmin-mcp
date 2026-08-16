package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetActivityTypedSplits is the upstream compatibility name of the split tool.
const ToolGetActivityTypedSplits = "get_activity_typed_splits"

// activityIDInput is the argument set both activity-detail tools take.
//
// ActivityID is typed any because the manifest declares anyOf integer or string, and
// the handler parses it strictly: only a positive whole number, as a JSON number or
// as decimal digits, becomes a validated identifier. Nothing else reaches a URL path.
type activityIDInput struct {
	ActivityID any `json:"activity_id" jsonschema:"the Garmin activity identifier, number or string"`
}

// activityIDProperty declares the identifier argument for both detail tools.
func activityIDProperty() Property {
	return Property{
		Name:        argActivityID,
		Types:       []string{typeInteger, typeString},
		Description: "the Garmin activity identifier, as a positive number or decimal string",
		Minimum:     bound(1),
		MaxLength:   new(maxIdentifierArgumentLen),
		Required:    true,
	}
}

// TypedSplit is one split, lap or interval of an activity.
type TypedSplit struct {
	Type             *string  `json:"type,omitempty" jsonschema:"the split type, for example INTERVAL_ACTIVE"`
	MessageIndex     *int     `json:"message_index,omitempty" jsonschema:"the split's ordinal in the activity"`
	DistanceMeters   *float64 `json:"distance_meters,omitempty" jsonschema:"the split distance in meters"`
	DurationSecs     *float64 `json:"duration_seconds,omitempty" jsonschema:"the timed duration in seconds"`
	ElapsedSecs      *float64 `json:"elapsed_seconds,omitempty" jsonschema:"the elapsed duration in seconds"`
	MovingSecs       *float64 `json:"moving_seconds,omitempty" jsonschema:"the moving duration in seconds"`
	AverageHeartRate *float64 `json:"average_heart_rate,omitempty" jsonschema:"the average heart rate in bpm"`
	MaxHeartRate     *float64 `json:"max_heart_rate,omitempty" jsonschema:"the maximum heart rate in bpm"`
	Calories         *float64 `json:"calories,omitempty" jsonschema:"the kilocalories burned"`
	MaxElevation     *float64 `json:"max_elevation,omitempty" jsonschema:"the maximum elevation in meters"`
	StartTimeGMT     *string  `json:"start_time_gmt,omitempty" jsonschema:"the split start time in UTC"`
}

// TypedSplitList is the normalized split collection.
//
// Garmin answers this endpoint with four different shapes; the domain client folds
// them into one list, and this result is always an array. That is the whole value of
// the tool: a caller never has to branch on which shape arrived.
type TypedSplitList struct {
	ActivityID int64        `json:"activity_id" jsonschema:"the activity these splits belong to"`
	Splits     []TypedSplit `json:"splits" jsonschema:"the splits, laps or intervals, in order"`
	Count      int          `json:"count" jsonschema:"how many splits this result carries"`
	Truncated  bool         `json:"truncated" jsonschema:"whether the list was cut at this server's bound"`
}

// LogValue reports the split count, never a split.
func (l TypedSplitList) LogValue() slog.Value {
	return shape("typedSplitList",
		slog.Int("splits", len(l.Splits)),
		slog.Bool("truncated", l.Truncated),
	)
}

func getActivityTypedSplitsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetActivityTypedSplits,
			Title: "Get activity typed splits",
			Description: "read one activity's splits, laps or intervals, normalized to a " +
				"single array whatever shape Garmin answers with. Use it to analyse pacing " +
				"and heart rate across an activity",
			Tier:        policy.TierReadOnly,
			Category:    categoryLocation,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(activityIDProperty()),
	}
}

// registerGetActivityTypedSplits registers the tool.
func registerGetActivityTypedSplits(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in activityIDInput) (
		*mcp.CallToolResult, TypedSplitList, error,
	) {
		id, err := parseActivityIdentifier(in.ActivityID)
		if err != nil {
			return nil, TypedSplitList{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, TypedSplitList{}, err
		}

		splits, err := svc.details.TypedSplits(ctx, session, id)
		if err != nil {
			return nil, TypedSplitList{}, fail(err)
		}
		return nil, newTypedSplitList(id.Int64(), splits.Splits(), svc.bounds.MaxSplits), nil
	}
	return mcpserver.AddTool(registry, getActivityTypedSplitsContract().Registration(), handler)
}

// newTypedSplitList maps the domain models onto the bounded result.
func newTypedSplitList(activityID int64, splits []api.TypedSplit, limit int) TypedSplitList {
	truncated := len(splits) > limit
	if truncated {
		splits = splits[:limit]
	}

	out := make([]TypedSplit, 0, len(splits))
	for _, split := range splits {
		out = append(out, TypedSplit{
			Type:             optionalText(split.Type),
			MessageIndex:     optionalInt(split.MessageIndex),
			DistanceMeters:   optionalFloat(split.Distance),
			DurationSecs:     optionalFloat(split.Duration),
			ElapsedSecs:      optionalFloat(split.ElapsedDuration),
			MovingSecs:       optionalFloat(split.MovingDuration),
			AverageHeartRate: optionalFloat(split.AverageHR),
			MaxHeartRate:     optionalFloat(split.MaxHR),
			Calories:         optionalFloat(split.Calories),
			MaxElevation:     optionalFloat(split.MaxElevation),
			StartTimeGMT:     split.StartTimeGMT,
		})
	}
	return TypedSplitList{
		ActivityID: activityID,
		Splits:     out,
		Count:      len(out),
		Truncated:  truncated,
	}
}
