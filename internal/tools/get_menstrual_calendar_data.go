package tools

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetMenstrualCalendarData is the upstream compatibility name of the
// menstrual-cycle calendar tool.
const ToolGetMenstrualCalendarData = "get_menstrual_calendar_data"

// DefaultMaxMenstrualCalendarBytes bounds the document this tool will return.
//
// The document is returned whole rather than field-mapped, so it is one result
// this server cannot reduce by selecting fields. A document past the bound is
// refused rather than cut: half a JSON document is not a JSON document, and this
// document is the most sensitive category this project handles.
const DefaultMaxMenstrualCalendarBytes = 64 << 10

// MenstrualCalendar is the menstrual-cycle calendar summary over an inclusive
// date window.
//
// The document is returned as structured JSON under Garmin's own field names, and
// that is deliberate. No pinned source names a single field inside it:
// python-garminconnect returns it whole and Taxuspt/garmin_mcp's curation
// serialises it unchanged (see api.WomensHealth's doc comment), so it is passed
// through as sanitized structured data — never as a JSON-encoded string, which
// would be invisible to a caller walking decoded keys — the same discipline
// get_goals and get_nutrition_daily_food_log already apply to a document no
// pinned source names a single field of. Give it a typed shape once a real
// document has been sampled.
//
// The window is bounded by this server's configured date-range limit rather than
// chunked into successive requests: see api.WomensHealth.Calendar's doc comment
// for why upstream's client-side 92-day chunking is not ported. A caller that
// needs a wider span asks again with a narrower one.
//
// Keeping Garmin's names is not the same as forwarding Garmin's document. The keys
// that identify a person or a place are removed by sanitizeUntyped before it is
// returned, and DroppedFields says how many.
//
// It is menstrual-cycle health data — the most sensitive category this project
// handles: never log it, never cache it.
type MenstrualCalendar struct {
	StartDate string `json:"start_date" jsonschema:"the first day of the window that was requested, YYYY-MM-DD"`
	EndDate   string `json:"end_date" jsonschema:"the last day of the window that was requested, YYYY-MM-DD"`
	HasData   bool   `json:"has_data" jsonschema:"whether Garmin held a menstrual-cycle calendar for the window"`

	// Document is the window's menstrual-cycle calendar record, sanitized, under
	// Garmin's own field names. It is menstrual-cycle health data: never log it.
	Document any `json:"document,omitempty" jsonschema:"Garmin's menstrual-cycle calendar record, sanitized"`

	// DroppedFields is how many identifying keys were removed from Document. It is
	// a count and never a list of names: see sanitizeUntyped for why naming them
	// would disclose what removing them hid.
	DroppedFields int `json:"dropped_fields" jsonschema:"identifying keys removed from document"`
}

// LogValue reports whether data was found and never the document.
func (c MenstrualCalendar) LogValue() slog.Value {
	return shape("menstrualCalendar",
		slog.Bool("hasData", c.HasData),
		slog.Int("droppedFields", c.DroppedFields),
	)
}

// getMenstrualCalendarDataInput is the strict argument set: one inclusive date
// window.
type getMenstrualCalendarDataInput struct {
	StartDate string `json:"start_date" jsonschema:"the first day of the window, YYYY-MM-DD"`
	EndDate   string `json:"end_date" jsonschema:"the last day of the window, YYYY-MM-DD"`
}

func getMenstrualCalendarDataContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetMenstrualCalendarData,
			Title: "Get menstrual calendar data",
			Description: "read the account's menstrual-cycle calendar summary over an " +
				"inclusive date window. The document is returned as JSON text because its " +
				"field set is not pinned; the window and the document are both bounded",
			Tier:        policy.TierReadOnly,
			Category:    categoryWomensHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(
			dateProperty("start_date", "the first day of the window"),
			dateProperty("end_date", "the last day of the window"),
		),
	}
}

// registerGetMenstrualCalendarData registers the tool.
func registerGetMenstrualCalendarData(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getMenstrualCalendarDataInput) (
		*mcp.CallToolResult, MenstrualCalendar, error,
	) {
		out, err := svc.readMenstrualCalendarData(ctx, in)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getMenstrualCalendarDataContract().Registration(), handler)
}

// readMenstrualCalendarData performs the read behind the tool.
//
// The window is validated before the session is resolved, so a window this server
// refuses costs no Garmin call at all.
func (s *service) readMenstrualCalendarData(
	ctx context.Context, in getMenstrualCalendarDataInput,
) (MenstrualCalendar, error) {
	span, err := parseWindow(in.StartDate, in.EndDate, s.limits)
	if err != nil {
		return MenstrualCalendar{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return MenstrualCalendar{}, err
	}
	document, err := s.womensHealth.Calendar(ctx, session, span)
	if err != nil {
		return MenstrualCalendar{}, fail(err)
	}
	return newMenstrualCalendar(span.Start().String(), span.End().String(), document)
}

// newMenstrualCalendar maps the domain model onto the result, refusing a document
// over the bound. The refusal names the bound, so a caller knows what it may ask
// for instead.
//
// The byte bound is checked before the document is parsed, so an oversized
// document costs no walk. What survives the bound is sanitised rather than
// forwarded: the field set is unsourced, so the only thing keeping an account
// identifier out of this passthrough is sanitizeUntyped.
func newMenstrualCalendar(start, end string, document api.MenstrualCalendar) (MenstrualCalendar, error) {
	if !document.HasDocument() {
		return MenstrualCalendar{StartDate: start, EndDate: end}, nil
	}
	if len(document.Document) > DefaultMaxMenstrualCalendarBytes {
		return MenstrualCalendar{}, tooLarge(
			"the menstrual-cycle calendar document for this window is larger than " +
				strconv.Itoa(DefaultMaxMenstrualCalendarBytes) + " bytes")
	}

	sanitized, dropped, err := sanitizedWomensHealthDocument(document.Document)
	if err != nil {
		return MenstrualCalendar{}, err
	}
	return MenstrualCalendar{
		StartDate:     start,
		EndDate:       end,
		HasData:       true,
		Document:      sanitized,
		DroppedFields: dropped,
	}, nil
}
