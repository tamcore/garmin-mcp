package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// The upstream compatibility names of the activity-field writes.
const (
	ToolSetActivityName        = "set_activity_name"
	ToolSetActivityType        = "set_activity_type"
	ToolSetActivityEventType   = "set_activity_event_type"
	ToolSetActivityDescription = "set_activity_description"
	ToolSetActivityFeel        = "set_activity_feel"
	ToolSetPerceivedEffort     = "set_perceived_effort"
)

// setActivityNameInput is the rename argument set.
type setActivityNameInput struct {
	ActivityID   any    `json:"activity_id" jsonschema:"the Garmin activity identifier"`
	ActivityName string `json:"activity_name" jsonschema:"the new activity name"`
}

func setActivityNameContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolSetActivityName,
			Title: "Rename an activity",
			Description: "rename one activity. Repeating the call with the same name leaves " +
				"the same end state",
			Tier:        policy.TierWrite,
			Category:    categoryOrdinary,
			Annotations: writeAnnotations(true),
		},
		Schema: NewSchema(activityIDProperty(), Property{
			Name:        argNameActivityName,
			Types:       []string{typeString},
			Description: "the new activity name",
			MaxLength:   new(maxNameArgumentLen),
			Required:    true,
		}),
	}
}

func registerSetActivityName(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in setActivityNameInput) (
		*mcp.CallToolResult, ActivityUpdate, error,
	) {
		name, err := parseRequiredText(argNameActivityName, in.ActivityName, maxNameArgumentLen)
		if err != nil {
			return nil, ActivityUpdate{}, err
		}
		write, err := svc.resolveActivityWrite(ctx, in.ActivityID)
		if err != nil {
			return nil, ActivityUpdate{}, err
		}

		result, err := svc.writes.SetName(ctx, write.session, write.id, name)
		if err != nil {
			return nil, ActivityUpdate{}, fail(err)
		}
		return nil, newActivityUpdate(write.id, argNameActivityName, result), nil
	}
	return mcpserver.AddTool(registry, setActivityNameContract().Registration(), handler)
}

// setActivityTypeInput is the activity-type change argument set.
type setActivityTypeInput struct {
	ActivityID any    `json:"activity_id" jsonschema:"the Garmin activity identifier"`
	TypeKey    string `json:"type_key" jsonschema:"the target activity-type key"`
}

func setActivityTypeContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolSetActivityType,
			Title: "Set an activity's type",
			Description: "reclassify one activity to another sport. The type key is resolved " +
				"against Garmin's own catalog first, because Garmin stores the numeric type " +
				"and its parent as well as the key",
			Tier:        policy.TierWrite,
			Category:    categoryOrdinary,
			Annotations: writeAnnotations(true),
		},
		Schema: NewSchema(activityIDProperty(), Property{
			Name:  "type_key",
			Types: []string{typeString},
			Description: `the target activity-type key, for example "running", ` +
				"from get_activity_types",
			MaxLength: new(maxActivityTypeArgumentLen),
			Required:  true,
		}),
	}
}

func registerSetActivityType(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in setActivityTypeInput) (
		*mcp.CallToolResult, ActivityUpdate, error,
	) {
		filter, err := parseActivityTypeFilter(in.TypeKey)
		if err != nil {
			return nil, ActivityUpdate{}, err
		}
		if filter.IsZero() {
			return nil, ActivityUpdate{}, invalidArgument("type_key must not be empty")
		}
		write, err := svc.resolveActivityWrite(ctx, in.ActivityID)
		if err != nil {
			return nil, ActivityUpdate{}, err
		}
		change, err := svc.resolveTypeChange(ctx, write, filter.String())
		if err != nil {
			return nil, ActivityUpdate{}, err
		}

		result, err := svc.writes.SetType(ctx, write.session, write.id, change)
		if err != nil {
			return nil, ActivityUpdate{}, fail(err)
		}
		return nil, newActivityUpdate(write.id, "activity_type", result), nil
	}
	return mcpserver.AddTool(registry, setActivityTypeContract().Registration(), handler)
}

// resolveTypeChange reads Garmin's activity-type catalog and builds the whole triple
// the write needs. The key alone does not identify the row in Garmin's own catalog,
// so sending it without the numeric ids is a guaranteed rejection.
func (s *service) resolveTypeChange(
	ctx context.Context, write activityWrite, key string,
) (api.TypeChange, error) {
	entries, err := s.details.Types(ctx, write.session)
	if err != nil {
		return api.TypeChange{}, fail(err)
	}
	for _, entry := range entries {
		name, ok := entry.TypeKey.Value()
		if !ok || name != key {
			continue
		}
		id, _ := entry.TypeID.Int64()
		parent, _ := entry.ParentTypeID.Int64()
		return api.TypeChange{TypeID: id, TypeKey: name, ParentTypeID: parent}, nil
	}
	return api.TypeChange{}, invalidArgument(
		"type_key names no activity type in Garmin's catalog; call get_activity_types")
}

// setActivityEventTypeInput is the event-type change argument set.
type setActivityEventTypeInput struct {
	ActivityID any    `json:"activity_id" jsonschema:"the Garmin activity identifier"`
	EventType  string `json:"event_type" jsonschema:"the target event-type key"`
}

func setActivityEventTypeContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolSetActivityEventType,
			Title: "Set an activity's event type",
			Description: "categorise one activity's purpose, for example race or training. " +
				"Repeating the call with the same key leaves the same end state",
			Tier:        policy.TierWrite,
			Category:    categoryOrdinary,
			Annotations: writeAnnotations(true),
		},
		Schema: NewSchema(activityIDProperty(), Property{
			Name:        "event_type",
			Types:       []string{typeString},
			Description: "the target event-type key",
			MaxLength:   new(maxEventTypeArgumentLen),
			Enum:        eventTypeEnum(),
			Required:    true,
		}),
	}
}

// eventTypeEnum renders the closed event-type set the API layer validates against.
func eventTypeEnum() []any {
	keys := api.EventTypeKeys()
	out := make([]any, 0, len(keys))
	for _, key := range keys {
		out = append(out, key)
	}
	return out
}

func registerSetActivityEventType(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in setActivityEventTypeInput) (
		*mcp.CallToolResult, ActivityUpdate, error,
	) {
		if len(in.EventType) > maxEventTypeArgumentLen {
			return nil, ActivityUpdate{}, invalidArgument("event_type is too long")
		}
		key, err := api.ParseEventTypeKey(in.EventType)
		if err != nil {
			return nil, ActivityUpdate{}, invalidArgument(
				"event_type must be one of the keys this tool declares")
		}
		write, err := svc.resolveActivityWrite(ctx, in.ActivityID)
		if err != nil {
			return nil, ActivityUpdate{}, err
		}

		result, err := svc.writes.SetEventType(
			ctx, write.session, write.id, api.EventTypeChange{TypeKey: key})
		if err != nil {
			return nil, ActivityUpdate{}, fail(err)
		}
		return nil, newActivityUpdate(write.id, "event_type", result), nil
	}
	return mcpserver.AddTool(registry, setActivityEventTypeContract().Registration(), handler)
}

// setActivityDescriptionInput is the description argument set.
type setActivityDescriptionInput struct {
	ActivityID  any    `json:"activity_id" jsonschema:"the Garmin activity identifier"`
	Description string `json:"description" jsonschema:"the new description; empty clears it"`
}

func setActivityDescriptionContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolSetActivityDescription,
			Title: "Set an activity's description",
			Description: "write the free-text notes of one activity. Upstream documents an " +
				"empty string as the way to clear a description; the request layer refuses " +
				"an empty write field, so send a single space to blank the notes instead",
			Tier:        policy.TierWrite,
			Category:    categoryOrdinary,
			Annotations: writeAnnotations(true),
		},
		Schema: NewSchema(activityIDProperty(), Property{
			Name:        "description",
			Types:       []string{typeString},
			Description: "the new description text; it must not be empty",
			MaxLength:   new(maxDescriptionArgumentLen),
			Required:    true,
		}),
	}
}

func registerSetActivityDescription(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in setActivityDescriptionInput) (
		*mcp.CallToolResult, ActivityUpdate, error,
	) {
		text, err := parseRequiredText("description", in.Description, maxDescriptionArgumentLen)
		if err != nil {
			return nil, ActivityUpdate{}, err
		}
		write, err := svc.resolveActivityWrite(ctx, in.ActivityID)
		if err != nil {
			return nil, ActivityUpdate{}, err
		}

		result, err := svc.writes.SetDescription(ctx, write.session, write.id, text)
		if err != nil {
			return nil, ActivityUpdate{}, fail(err)
		}
		return nil, newActivityUpdate(write.id, "description", result), nil
	}
	return mcpserver.AddTool(registry, setActivityDescriptionContract().Registration(), handler)
}

// setActivityFeelInput is the feel-rating argument set. The rating is health data.
type setActivityFeelInput struct {
	ActivityID any `json:"activity_id" jsonschema:"the Garmin activity identifier"`
	Feel       int `json:"feel" jsonschema:"the feel rating: 0, 25, 50, 75 or 100"`
}

func setActivityFeelContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolSetActivityFeel,
			Title: "Set how an activity felt",
			Description: "write Garmin's five-point feel rating for one activity, where " +
				"higher is better. The rating is health data and is never logged",
			Tier:        policy.TierWrite,
			Category:    categoryHealth,
			Annotations: writeAnnotations(true),
		},
		Schema: NewSchema(activityIDProperty(), Property{
			Name:  "feel",
			Types: []string{typeInteger},
			Description: "the feel rating: 0 very tired, 25 tired, 50 normal, 75 good, " +
				"100 strong",
			Minimum:  bound(0),
			Maximum:  bound(100),
			Enum:     []any{0, 25, 50, 75, 100},
			Required: true,
		}),
	}
}

func registerSetActivityFeel(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in setActivityFeelInput) (
		*mcp.CallToolResult, ActivityUpdate, error,
	) {
		feel, err := api.ParseFeel(in.Feel)
		if err != nil {
			return nil, ActivityUpdate{}, invalidArgument("feel must be 0, 25, 50, 75 or 100")
		}
		write, err := svc.resolveActivityWrite(ctx, in.ActivityID)
		if err != nil {
			return nil, ActivityUpdate{}, err
		}

		result, err := svc.writes.SetFeel(ctx, write.session, write.id, feel)
		if err != nil {
			return nil, ActivityUpdate{}, fail(err)
		}
		return nil, newActivityUpdate(write.id, "feel", result), nil
	}
	return mcpserver.AddTool(registry, setActivityFeelContract().Registration(), handler)
}

// setPerceivedEffortInput is the RPE argument set. The rating is health data.
type setPerceivedEffortInput struct {
	ActivityID any     `json:"activity_id" jsonschema:"the Garmin activity identifier"`
	RPE        float64 `json:"rpe" jsonschema:"the perceived effort from 0 to 10; 0 clears it"`
}

func setPerceivedEffortContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolSetPerceivedEffort,
			Title: "Set an activity's perceived effort",
			Description: "write the perceived effort of one activity on Garmin's 0 to 10 " +
				"scale, where 0 clears the rating. The rating is health data and is never " +
				"logged",
			Tier:        policy.TierWrite,
			Category:    categoryHealth,
			Annotations: writeAnnotations(true),
		},
		Schema: NewSchema(activityIDProperty(), Property{
			Name:        "rpe",
			Types:       []string{typeNumber},
			Description: "the perceived effort from 0 to 10, where 0 clears the rating",
			Minimum:     bound(0),
			Maximum:     bound(api.MaxPerceivedEffort),
			Required:    true,
		}),
	}
}

func registerSetPerceivedEffort(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in setPerceivedEffortInput) (
		*mcp.CallToolResult, ActivityUpdate, error,
	) {
		if err := inRange("rpe", in.RPE, 0, api.MaxPerceivedEffort); err != nil {
			return nil, ActivityUpdate{}, err
		}
		write, err := svc.resolveActivityWrite(ctx, in.ActivityID)
		if err != nil {
			return nil, ActivityUpdate{}, err
		}

		result, err := svc.writes.SetPerceivedEffort(ctx, write.session, write.id, in.RPE)
		if err != nil {
			return nil, ActivityUpdate{}, fail(err)
		}
		return nil, newActivityUpdate(write.id, "perceived_effort", result), nil
	}
	return mcpserver.AddTool(registry, setPerceivedEffortContract().Registration(), handler)
}
