package tools

import (
	"context"
	"log/slog"
	"math"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetBodyBattery is the upstream compatibility name of the body-battery tool.
const ToolGetBodyBattery = "get_body_battery"

// Body-battery result bounds. The window itself is bounded by the request layer's
// MaxDateRangeDays; these bound what one answer may render.
const (
	maxBodyBatteryDays      = 400
	maxBodyBatteryDayEvents = 64
	millisecondsPerMinute   = 60000.0
)

// BodyBatteryEvent is one event of a body-battery day. Every field is optional.
type BodyBatteryEvent struct {
	Type            *string  `json:"type,omitempty" jsonschema:"Garmin's event type, for example sleep"`
	StartTime       *string  `json:"start_time,omitempty" jsonschema:"the event start as Garmin reported it, UTC"`
	DurationMinutes *float64 `json:"duration_minutes,omitempty" jsonschema:"how long the event lasted"`
	Impact          *int     `json:"body_battery_impact,omitempty" jsonschema:"the event's effect on body battery"`
	Feedback        *string  `json:"feedback,omitempty" jsonschema:"Garmin's short feedback label"`
}

// BodyBatteryDay is one day of the body-battery report. It is health data — never log
// it, never cache it.
type BodyBatteryDay struct {
	Date    *string `json:"date,omitempty" jsonschema:"the day Garmin reported, YYYY-MM-DD"`
	Charged *int    `json:"charged,omitempty" jsonschema:"body battery gained over the day"`
	Drained *int    `json:"drained,omitempty" jsonschema:"body battery spent over the day"`

	CurrentFeedback  *string `json:"current_feedback,omitempty" jsonschema:"Garmin's dynamic feedback label"`
	BodyBatteryLevel *int    `json:"body_battery_level,omitempty" jsonschema:"the level the feedback was given at"`

	EventCount      int  `json:"event_count" jsonschema:"how many events this day carries"`
	EventsTruncated bool `json:"events_truncated" jsonschema:"whether the day's event list was cut"`

	Events []BodyBatteryEvent `json:"events" jsonschema:"the day's events"`
}

// BodyBatteryWindow is the body-battery report over an inclusive date window.
type BodyBatteryWindow struct {
	StartDate string `json:"start_date" jsonschema:"the inclusive first day, YYYY-MM-DD"`
	EndDate   string `json:"end_date" jsonschema:"the inclusive last day, YYYY-MM-DD"`

	Count     int  `json:"count" jsonschema:"how many days this result carries"`
	Truncated bool `json:"truncated" jsonschema:"whether the day list was cut at this server's bound"`

	Days []BodyBatteryDay `json:"days" jsonschema:"the days Garmin held data for"`
}

// LogValue reports the shape of the window, never a reading.
func (w BodyBatteryWindow) LogValue() slog.Value {
	return shape("bodyBatteryWindow",
		slog.Int("days", len(w.Days)),
		slog.Bool("truncated", w.Truncated),
	)
}

// getBodyBatteryInput is the strict argument set: an inclusive date window.
type getBodyBatteryInput struct {
	StartDate string `json:"start_date" jsonschema:"inclusive first day, YYYY-MM-DD"`
	EndDate   string `json:"end_date" jsonschema:"inclusive last day, YYYY-MM-DD"`
}

func getBodyBatteryContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetBodyBattery,
			Title: "Get body battery",
			Description: "read the account's body battery over an inclusive date window: " +
				"what each day charged and drained, the events that moved it, and " +
				"Garmin's own feedback. The window is bounded by this server; narrow it " +
				"if the call is refused",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(
			dateProperty("start_date", "inclusive first day of the window"),
			dateProperty("end_date", "inclusive last day of the window"),
		),
	}
}

// registerGetBodyBattery registers the tool.
func registerGetBodyBattery(registry *mcpserver.Registry, svc *service) error {
	stress, err := stressClient(svc)
	if err != nil {
		return err
	}
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getBodyBatteryInput) (
		*mcp.CallToolResult, BodyBatteryWindow, error,
	) {
		span, err := parseWindow(in.StartDate, in.EndDate, svc.limits)
		if err != nil {
			return nil, BodyBatteryWindow{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, BodyBatteryWindow{}, err
		}
		read, err := stress.BodyBattery(ctx, session, span)
		if err != nil {
			return nil, BodyBatteryWindow{}, fail(err)
		}
		return nil, newBodyBatteryWindow(span.Start().String(), span.End().String(), read), nil
	}
	return mcpserver.AddTool(registry, getBodyBatteryContract().Registration(), handler)
}

// newBodyBatteryWindow maps the days onto the bounded result.
func newBodyBatteryWindow(start, end string, days []api.BodyBatteryDay) BodyBatteryWindow {
	out := BodyBatteryWindow{StartDate: start, EndDate: end, Days: []BodyBatteryDay{}}
	if len(days) > maxBodyBatteryDays {
		days = days[:maxBodyBatteryDays]
		out.Truncated = true
	}
	for _, day := range days {
		out.Days = append(out.Days, newBodyBatteryDay(day))
	}
	out.Count = len(out.Days)
	return out
}

// newBodyBatteryDay maps one day, bounding its event list.
func newBodyBatteryDay(day api.BodyBatteryDay) BodyBatteryDay {
	out := BodyBatteryDay{
		Date:    day.Date,
		Charged: optionalInt(day.Charged),
		Drained: optionalInt(day.Drained),
		Events:  []BodyBatteryEvent{},
	}
	if day.DynamicFeedback != nil {
		out.CurrentFeedback = optionalText(day.DynamicFeedback.FeedbackShortType)
		out.BodyBatteryLevel = optionalInt(day.DynamicFeedback.BodyBatteryLevel)
	}

	events := day.ActivityEvents.Items()
	if len(events) > maxBodyBatteryDayEvents {
		events = events[:maxBodyBatteryDayEvents]
		out.EventsTruncated = true
	}
	for _, event := range events {
		out.Events = append(out.Events, BodyBatteryEvent{
			Type:            optionalText(event.EventType),
			StartTime:       optionalText(event.StartTimeGMT),
			DurationMinutes: durationMinutes(event.DurationMillis),
			Impact:          optionalInt(event.Impact),
			Feedback:        optionalText(event.ShortFeedback),
		})
	}
	out.EventCount = len(out.Events)
	return out
}

// durationMinutes renders a millisecond duration in minutes, rounded to one decimal the
// way upstream rounds it. An absent duration stays absent rather than becoming zero.
func durationMinutes(millis client.Number) *float64 {
	value, ok := millis.Float64()
	if !ok {
		return nil
	}
	minutes := math.Round(value/millisecondsPerMinute*10) / 10
	return &minutes
}
