package tools

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetBodyBatteryEvents is the upstream compatibility name of the body-battery
// event tool.
const ToolGetBodyBatteryEvents = "get_body_battery_events"

// maxWellnessEvents bounds an event list this server renders. It applies to both
// uncurated event reads — the body-battery events and the daily wellness events.
const maxWellnessEvents = 200

// BodyBatteryEventList is one day of body-battery events.
//
// It is health data — never log it, never cache it. Upstream returns the document
// unchanged and curates no field, so the per-event shape is not established by the
// pinned source: no allowlist of event keys exists to prefer, and every event is passed
// through under Garmin's names rather than mapped onto invented ones.
//
// What is bounded is the list length, and what is removed is every key that identifies
// a person or a place — see sanitizeUntyped, which is the only thing between an
// unsourced event and an account identifier leaving this server.
type BodyBatteryEventList struct {
	Date string `json:"date" jsonschema:"the calendar day, YYYY-MM-DD"`

	Count int `json:"count" jsonschema:"how many events this result carries"`

	// Truncated reports that this result is not the whole document: either the list
	// was cut at the bound, or an event was too deep or too large to render whole.
	Truncated bool `json:"truncated" jsonschema:"whether this result is not the whole document"`

	// DroppedFields is how many identifying keys were removed, over the whole list.
	// It is a count and never a list of names.
	DroppedFields int `json:"dropped_fields" jsonschema:"identifying keys removed from the events"`

	Events []any `json:"events" jsonschema:"the day's body-battery events, as Garmin returned them"`
}

// LogValue reports the shape of the day, never an event.
func (l BodyBatteryEventList) LogValue() slog.Value {
	return shape("bodyBatteryEventList",
		slog.Int("events", l.Count),
		slog.Bool("truncated", l.Truncated),
	)
}

// getBodyBatteryEventsInput is the strict argument set: one calendar day.
type getBodyBatteryEventsInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getBodyBatteryEventsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetBodyBatteryEvents,
			Title: "Get body battery events",
			Description: "read one calendar day of the account's body-battery events — " +
				"sleep, recorded and auto-detected activities, and naps. Each event is " +
				"returned as Garmin sends it, and the list is bounded by this server",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetBodyBatteryEvents registers the tool.
func registerGetBodyBatteryEvents(registry *mcpserver.Registry, svc *service) error {
	stress, err := stressClient(svc)
	if err != nil {
		return err
	}
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getBodyBatteryEventsInput) (
		*mcp.CallToolResult, BodyBatteryEventList, error,
	) {
		date, bounded, err := svc.readEventList(ctx, in.Date, stress.BodyBatteryEvents)
		if err != nil {
			return nil, BodyBatteryEventList{}, err
		}
		return nil, BodyBatteryEventList{
			Date:          date,
			Count:         len(bounded.Values),
			Truncated:     bounded.Truncated,
			DroppedFields: bounded.Dropped,
			Events:        bounded.Values,
		}, nil
	}
	return mcpserver.AddTool(registry, getBodyBatteryEventsContract().Registration(), handler)
}

// eventReader is one uncurated day-keyed event read on the stress client. The two
// event tools differ only in which one they name.
type eventReader func(context.Context, client.Session, client.Date) ([]json.RawMessage, error)

// readEventList validates the day, performs the read, and bounds and sanitises what
// came back. Both uncurated event tools go through it, so the day validation, the list
// bound and the egress rule are written once.
func (s *service) readEventList(
	ctx context.Context, date string, read eventReader,
) (string, untypedList, error) {
	day, session, err := s.resolveStressDay(ctx, date)
	if err != nil {
		return "", untypedList{}, err
	}
	raw, err := read(ctx, session, day)
	if err != nil {
		return "", untypedList{}, fail(err)
	}
	return day.String(), boundedUntyped(raw, maxWellnessEvents), nil
}
