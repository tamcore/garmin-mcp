package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetActivityTypes is the upstream compatibility name of the type catalog read.
const ToolGetActivityTypes = "get_activity_types"

// defaultMaxActivityTypes bounds the returned catalog. Garmin's catalog holds a few
// hundred rows, so a longer answer is drift rather than a richer catalog.
const defaultMaxActivityTypes = 512

// ActivityTypeEntry is one row of Garmin's activity-type catalog.
type ActivityTypeEntry struct {
	TypeID       *int    `json:"type_id,omitempty" jsonschema:"the numeric type identifier"`
	TypeKey      *string `json:"type_key,omitempty" jsonschema:"the lowercase type key, for example running"`
	DisplayName  *string `json:"display_name,omitempty" jsonschema:"Garmin's own label for the type, for example Running"`
	ParentTypeID *int    `json:"parent_type_id,omitempty" jsonschema:"the identifier of the parent type"`
	IsHidden     *bool   `json:"is_hidden,omitempty" jsonschema:"whether Garmin hides the type"`
	SortOrder    *int    `json:"sort_order,omitempty" jsonschema:"Garmin's own ordering hint"`
}

// ActivityTypeList is the bounded activity-type catalog.
//
// It is ordinary data: the catalog is the same for every account and names nothing
// about the person who asked for it.
type ActivityTypeList struct {
	ActivityTypes []ActivityTypeEntry `json:"activity_types" jsonschema:"the catalog rows, in Garmin's order"`
	Count         int                 `json:"count" jsonschema:"how many rows this result carries"`
	Truncated     bool                `json:"truncated" jsonschema:"whether the catalog was cut at this server's bound"`
}

// LogValue reports the row count, never a row.
func (l ActivityTypeList) LogValue() slog.Value {
	return shape("activityTypeList",
		slog.Int("activityTypes", len(l.ActivityTypes)),
		slog.Bool("truncated", l.Truncated),
	)
}

func getActivityTypesContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetActivityTypes,
			Title: "Get activity types",
			Description: "read Garmin's catalog of activity types. Takes no arguments. Use it " +
				"to find the type_key that get_activities_by_date takes as activity_type, or " +
				"the key set_activity_type expects",
			Tier:        policy.TierReadOnly,
			Category:    categoryOrdinary,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(),
	}
}

// registerGetActivityTypes registers the tool.
func registerGetActivityTypes(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, _ noArguments) (
		*mcp.CallToolResult, ActivityTypeList, error,
	) {
		session, err := svc.session(ctx)
		if err != nil {
			return nil, ActivityTypeList{}, err
		}

		entries, err := svc.details.Types(ctx, session)
		if err != nil {
			return nil, ActivityTypeList{}, fail(err)
		}
		return nil, newActivityTypeList(entries), nil
	}
	return mcpserver.AddTool(registry, getActivityTypesContract().Registration(), handler)
}

// newActivityTypeList maps the catalog rows onto the bounded result.
func newActivityTypeList(entries []api.CatalogEntry) ActivityTypeList {
	truncated := len(entries) > defaultMaxActivityTypes
	if truncated {
		entries = entries[:defaultMaxActivityTypes]
	}

	out := make([]ActivityTypeEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, ActivityTypeEntry{
			TypeID:       optionalInt(entry.TypeID),
			TypeKey:      optionalText(entry.TypeKey),
			DisplayName:  optionalText(entry.DisplayName),
			ParentTypeID: optionalInt(entry.ParentTypeID),
			IsHidden:     entry.IsHidden,
			SortOrder:    optionalInt(entry.SortOrder),
		})
	}
	return ActivityTypeList{ActivityTypes: out, Count: len(out), Truncated: truncated}
}
