package tools

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetMenstrualDataForDate is the upstream compatibility name of the
// menstrual-cycle day-view tool.
const ToolGetMenstrualDataForDate = "get_menstrual_data_for_date"

// DefaultMaxMenstrualDayBytes bounds the document this tool will return.
//
// The document is returned whole rather than field-mapped, so it is one result
// this server cannot reduce by selecting fields. A document past the bound is
// refused rather than cut: half a JSON document is not a JSON document, and this
// document is the most sensitive category this project handles.
const DefaultMaxMenstrualDayBytes = 64 << 10

// MenstrualDay is one calendar day of the menstrual-cycle day view.
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
// Keeping Garmin's names is not the same as forwarding Garmin's document. The keys
// that identify a person or a place are removed by sanitizeUntyped before it is
// returned, and DroppedFields says how many.
//
// It is menstrual-cycle health data — the most sensitive category this project
// handles: never log it, never cache it.
type MenstrualDay struct {
	Date    string `json:"date" jsonschema:"the calendar day that was requested, YYYY-MM-DD"`
	HasData bool   `json:"has_data" jsonschema:"whether Garmin held a menstrual-cycle record for the day"`

	// Document is the day's menstrual-cycle record, sanitized, under Garmin's own
	// field names. It is menstrual-cycle health data: never log it.
	Document any `json:"document,omitempty" jsonschema:"Garmin's menstrual-cycle day-view record, sanitized"`

	// DroppedFields is how many identifying keys were removed from Document. It is
	// a count and never a list of names: see sanitizeUntyped for why naming them
	// would disclose what removing them hid.
	DroppedFields int `json:"dropped_fields" jsonschema:"identifying keys removed from document"`
}

// LogValue reports whether data was found and never the document.
func (d MenstrualDay) LogValue() slog.Value {
	return shape("menstrualDay",
		slog.Bool("hasData", d.HasData),
		slog.Int("droppedFields", d.DroppedFields),
	)
}

// getMenstrualDataForDateInput is the strict argument set: one calendar day.
type getMenstrualDataForDateInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getMenstrualDataForDateContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetMenstrualDataForDate,
			Title: "Get menstrual data for date",
			Description: "read one calendar day of the account's menstrual-cycle day view. " +
				"The document is returned as JSON text because its field set is not pinned; " +
				"a document over this server's bound is refused rather than cut",
			Tier:        policy.TierReadOnly,
			Category:    categoryWomensHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetMenstrualDataForDate registers the tool.
func registerGetMenstrualDataForDate(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getMenstrualDataForDateInput) (
		*mcp.CallToolResult, MenstrualDay, error,
	) {
		out, err := svc.readMenstrualDataForDate(ctx, in.Date)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getMenstrualDataForDateContract().Registration(), handler)
}

// readMenstrualDataForDate performs the read behind the tool.
func (s *service) readMenstrualDataForDate(ctx context.Context, date string) (MenstrualDay, error) {
	day, session, err := s.resolveDateOnlyRead(ctx, date)
	if err != nil {
		return MenstrualDay{}, err
	}
	document, err := s.womensHealth.DayView(ctx, session, day)
	if err != nil {
		return MenstrualDay{}, fail(err)
	}
	return newMenstrualDay(day.String(), document)
}

// newMenstrualDay maps the domain model onto the result, refusing a document over
// the bound. The refusal names the bound, so a caller knows what it may ask for
// instead.
//
// The byte bound is checked before the document is parsed, so an oversized
// document costs no walk. What survives the bound is sanitised rather than
// forwarded: the field set is unsourced, so the only thing keeping an account
// identifier out of this passthrough is sanitizeUntyped.
func newMenstrualDay(date string, document api.MenstrualDay) (MenstrualDay, error) {
	if !document.HasDocument() {
		return MenstrualDay{Date: date}, nil
	}
	if len(document.Document) > DefaultMaxMenstrualDayBytes {
		return MenstrualDay{}, tooLarge(
			"the menstrual-cycle document for this day is larger than " +
				strconv.Itoa(DefaultMaxMenstrualDayBytes) + " bytes")
	}

	sanitized, dropped, err := sanitizedWomensHealthDocument(document.Document)
	if err != nil {
		return MenstrualDay{}, err
	}
	return MenstrualDay{
		Date:          date,
		HasData:       true,
		Document:      sanitized,
		DroppedFields: dropped,
	}, nil
}

// sanitizedWomensHealthDocument sanitises one whole retained Garmin document,
// refusing rather than cutting when the walk cannot cover the whole document —
// the same rule get_goals's sanitizedGoalDocument and
// get_nutrition_daily_food_log's sanitizedNutritionDocument apply to a document no
// pinned source names a single field of. Shared by all three women's-health
// tools, since each carries exactly this shape.
func sanitizedWomensHealthDocument(raw json.RawMessage) (any, int, error) {
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, 0, tooLarge("this document is not readable JSON")
	}

	outcome := sanitizeUntyped(decoded)
	if outcome.Truncated {
		return nil, 0, tooLarge(
			"this document is nested deeper than " + strconv.Itoa(maxSanitizeDepth) +
				" levels or holds more than " + strconv.Itoa(maxSanitizeNodes) + " values")
	}
	return outcome.Value, outcome.Dropped, nil
}
