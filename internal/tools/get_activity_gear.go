package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetActivityGear is the upstream compatibility name of the activity gear read.
const ToolGetActivityGear = "get_activity_gear"

// defaultMaxActivityGear bounds the gear list one activity may return. An activity
// carries a handful of items — shoes, a bike, a heart-rate strap — so a list past
// this bound is drift rather than a real kit.
const defaultMaxActivityGear = 32

// ActivityGear is one piece of gear linked to an activity.
//
// It is device material: it names a person's equipment, so it is returned to the
// authorized caller and never logged.
type ActivityGear struct {
	UUID            *string `json:"gear_uuid,omitempty" jsonschema:"the gear identifier the write tools take"`
	GearPk          *int64  `json:"gear_pk,omitempty" jsonschema:"the numeric gear key"`
	DisplayName     *string `json:"display_name,omitempty" jsonschema:"the name the account gave the gear"`
	CustomMakeModel *string `json:"custom_make_model,omitempty" jsonschema:"the make and model typed in"`
	MakeName        *string `json:"make_name,omitempty" jsonschema:"the manufacturer Garmin recorded"`
	ModelName       *string `json:"model_name,omitempty" jsonschema:"the model Garmin recorded"`
	TypeName        *string `json:"gear_type_name,omitempty" jsonschema:"the gear type, for example Shoes"`
	StatusName      *string `json:"gear_status_name,omitempty" jsonschema:"the gear status, for example active"`
	DateBegin       *string `json:"date_begin,omitempty" jsonschema:"when the gear entered service"`
	DateEnd         *string `json:"date_end,omitempty" jsonschema:"when the gear left service, when it has"`
}

// ActivityGearList is the bounded gear collection of one activity.
type ActivityGearList struct {
	ActivityID int64          `json:"activity_id" jsonschema:"the activity this gear was used on"`
	Gear       []ActivityGear `json:"gear" jsonschema:"the gear linked to the activity"`
	Count      int            `json:"count" jsonschema:"how many gear items this result carries"`
	Truncated  bool           `json:"truncated" jsonschema:"whether the list was cut at the bound"`
}

// LogValue reports the gear count, never an item.
func (l ActivityGearList) LogValue() slog.Value {
	return shape("activityGearList",
		slog.Int("gear", len(l.Gear)),
		slog.Bool("truncated", l.Truncated),
	)
}

func getActivityGearContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetActivityGear,
			Title: "Get activity gear",
			Description: "read the gear linked to one activity: shoes, bike, or any other " +
				"item the account tracks. An activity with no gear returns an empty list, " +
				"which is a normal state rather than a failure. The gear_uuid it returns is " +
				"the identifier the gear write tools take",
			Tier:        policy.TierReadOnly,
			Category:    categoryDevice,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(activityIDProperty()),
	}
}

// registerGetActivityGear registers the tool.
func registerGetActivityGear(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in activityIDInput) (
		*mcp.CallToolResult, ActivityGearList, error,
	) {
		id, session, err := svc.resolveActivityRead(ctx, in.ActivityID)
		if err != nil {
			return nil, ActivityGearList{}, err
		}

		items, err := svc.gear.ForActivity(ctx, session, id)
		if err != nil {
			return nil, ActivityGearList{}, fail(err)
		}
		return nil, newActivityGearList(id.Int64(), items), nil
	}
	return mcpserver.AddTool(registry, getActivityGearContract().Registration(), handler)
}

// newActivityGearList maps the domain models onto the bounded result.
//
// The list is truncated rather than refused, for the same reason the device list is:
// the first items are the useful ones and no history is lost by seeing fewer.
func newActivityGearList(activityID int64, items []api.GearItem) ActivityGearList {
	truncated := len(items) > defaultMaxActivityGear
	if truncated {
		items = items[:defaultMaxActivityGear]
	}

	out := make([]ActivityGear, 0, len(items))
	for _, item := range items {
		out = append(out, ActivityGear{
			UUID:            item.UUID,
			GearPk:          optionalInt64(item.GearPk),
			DisplayName:     item.DisplayName,
			CustomMakeModel: item.CustomMakeModel,
			MakeName:        item.GearMakeName,
			ModelName:       item.GearModelName,
			TypeName:        item.GearTypeName,
			StatusName:      item.GearStatusName,
			DateBegin:       item.DateBegin,
			DateEnd:         item.DateEnd,
		})
	}
	return ActivityGearList{
		ActivityID: activityID, Gear: out, Count: len(out), Truncated: truncated,
	}
}
