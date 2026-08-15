package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetAllDayEvents is the upstream compatibility name of the daily wellness event
// tool.
const ToolGetAllDayEvents = "get_all_day_events"

// AllDayEventList is one day of wellness events.
//
// It is health data — never log it, never cache it. Upstream returns the document
// unchanged and curates no field, so the per-event shape is not established by the
// pinned source: no allowlist of event keys exists to prefer, and every event is passed
// through under Garmin's names rather than mapped onto invented ones.
//
// What is bounded is the list length, and what is removed is every key that identifies
// a person or a place — see sanitizeUntyped.
type AllDayEventList struct {
	Date string `json:"date" jsonschema:"the calendar day, YYYY-MM-DD"`

	Count int `json:"count" jsonschema:"how many events this result carries"`

	// Truncated reports that this result is not the whole document: either the list
	// was cut at the bound, or an event was too deep or too large to render whole.
	Truncated bool `json:"truncated" jsonschema:"whether this result is not the whole document"`

	// DroppedFields is how many identifying keys were removed, over the whole list.
	// It is a count and never a list of names.
	DroppedFields int `json:"dropped_fields" jsonschema:"identifying keys removed from the events"`

	Events []any `json:"events" jsonschema:"the day's wellness events, as Garmin returned them"`
}

// LogValue reports the shape of the day, never an event.
func (l AllDayEventList) LogValue() slog.Value {
	return shape("allDayEventList",
		slog.Int("events", l.Count),
		slog.Bool("truncated", l.Truncated),
	)
}

// getAllDayEventsInput is the strict argument set: one calendar day.
type getAllDayEventsInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getAllDayEventsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetAllDayEvents,
			Title: "Get all-day events",
			Description: "read one calendar day of the account's wellness events, " +
				"auto-detected activities included. Each event is returned as Garmin " +
				"sends it, and the list is bounded by this server",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetAllDayEvents registers the tool.
func registerGetAllDayEvents(registry *mcpserver.Registry, svc *service) error {
	stress, err := stressClient(svc)
	if err != nil {
		return err
	}
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getAllDayEventsInput) (
		*mcp.CallToolResult, AllDayEventList, error,
	) {
		date, bounded, err := svc.readEventList(ctx, in.Date, stress.AllDayEvents)
		if err != nil {
			return nil, AllDayEventList{}, err
		}
		return nil, AllDayEventList{
			Date:          date,
			Count:         len(bounded.Values),
			Truncated:     bounded.Truncated,
			DroppedFields: bounded.Dropped,
			Events:        bounded.Values,
		}, nil
	}
	return mcpserver.AddTool(registry, getAllDayEventsContract().Registration(), handler)
}
