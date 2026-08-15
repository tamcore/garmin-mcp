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

// ToolGetLifestyleLoggingData is the upstream compatibility name of the lifestyle log
// tool.
const ToolGetLifestyleLoggingData = "get_lifestyle_logging_data"

// DefaultMaxLifestyleLogBytes bounds the document this tool will return.
//
// The document is returned whole rather than field-mapped, so it is the one result
// here whose size this server cannot reduce by selecting fields. A document past the
// bound is refused rather than cut: half a JSON document is not a JSON document.
const DefaultMaxLifestyleLogBytes = 64 << 10

// LifestyleLog is one day of the lifestyle log.
//
// The document is returned as text under Garmin's own field names, and that is
// deliberate. No pinned source names a single field inside it: python-garminconnect
// returns it whole and the upstream MCP tool serialises it unchanged, so any Go model
// of it would be invented. Give it a typed shape once a real document has been sampled.
//
// Keeping Garmin's names is not the same as forwarding Garmin's document. The keys that
// identify a person or a place are removed by sanitizeUntyped before the text is built,
// and DroppedFields says how many.
//
// It is health data: never log it, never cache it.
type LifestyleLog struct {
	Date    string `json:"date" jsonschema:"the calendar day that was requested, YYYY-MM-DD"`
	HasData bool   `json:"has_data" jsonschema:"whether Garmin held a lifestyle log for the day"`

	DocumentJSON  string `json:"document_json" jsonschema:"Garmin's lifestyle-log document for the day, as JSON text"`
	DocumentBytes int    `json:"document_bytes" jsonschema:"how many bytes the returned document occupies"`

	// DroppedFields is how many identifying keys were removed from the document
	// before it was returned. It is a count and never a list of names: see
	// sanitizeUntyped for why naming them would disclose what removing them hid.
	DroppedFields int `json:"dropped_fields" jsonschema:"identifying keys removed from the document"`
}

// LogValue reports the size of the document and never its content.
func (l LifestyleLog) LogValue() slog.Value {
	return shape("lifestyleLog",
		slog.Bool("hasData", l.HasData),
		slog.Int("bytes", l.DocumentBytes),
	)
}

// getLifestyleLoggingDataInput is the strict argument set: one calendar day.
type getLifestyleLoggingDataInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getLifestyleLoggingDataContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetLifestyleLoggingData,
			Title: "Get lifestyle logging data",
			Description: "read one calendar day of the account's lifestyle log, the record " +
				"of logged behaviours and their effect on health metrics. The document is " +
				"returned as JSON text because its field set is not pinned; a document " +
				"over this server's bound is refused rather than cut",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetLifestyleLoggingData registers the tool.
func registerGetLifestyleLoggingData(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getLifestyleLoggingDataInput) (
		*mcp.CallToolResult, LifestyleLog, error,
	) {
		out, err := svc.readLifestyleLog(ctx, in.Date)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getLifestyleLoggingDataContract().Registration(), handler)
}

// readLifestyleLog performs the read behind the tool.
func (s *service) readLifestyleLog(ctx context.Context, date string) (LifestyleLog, error) {
	day, session, err := s.resolveDateOnlyRead(ctx, date)
	if err != nil {
		return LifestyleLog{}, err
	}
	document, err := s.wellness.Cardio().LifestyleLogging(ctx, session, day)
	if err != nil {
		return LifestyleLog{}, fail(err)
	}
	return newLifestyleLog(day.String(), document)
}

// newLifestyleLog maps the domain model onto the result, refusing a document over the
// bound. The refusal names the bound, so a caller knows what it may ask for instead.
//
// The byte bound is checked before the document is parsed, so an oversized document
// costs no walk. What survives the bound is sanitised rather than forwarded: the field
// set is unsourced, so the only thing keeping an account identifier out of this
// passthrough is sanitizeUntyped.
func newLifestyleLog(date string, log api.LifestyleLog) (LifestyleLog, error) {
	if !log.HasDocument() {
		return LifestyleLog{Date: date}, nil
	}
	if len(log.Document) > DefaultMaxLifestyleLogBytes {
		return LifestyleLog{}, tooLarge(
			"the lifestyle log for this day is larger than " +
				strconv.Itoa(DefaultMaxLifestyleLogBytes) + " bytes")
	}

	document, dropped, err := sanitizedDocument(log.Document)
	if err != nil {
		return LifestyleLog{}, err
	}
	return LifestyleLog{
		Date:          date,
		HasData:       true,
		DocumentJSON:  document,
		DocumentBytes: len(document),
		DroppedFields: dropped,
	}, nil
}

// sanitizedDocument strips the identifying keys out of a whole Garmin document and
// renders what is left as JSON text.
//
// A document this server cannot walk whole is refused rather than returned with a
// silently missing subtree, for the same reason an oversized one is: half a JSON
// document is not a JSON document, and a caller cannot tell which half it holds. A
// document that does not parse is refused too, because a passthrough that cannot be
// read cannot be sanitised.
func sanitizedDocument(raw json.RawMessage) (string, int, error) {
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", 0, tooLarge("the lifestyle log for this day is not readable JSON")
	}

	outcome := sanitizeUntyped(decoded)
	if outcome.Truncated {
		return "", 0, tooLarge(
			"the lifestyle log for this day is nested deeper than " +
				strconv.Itoa(maxSanitizeDepth) + " levels or holds more than " +
				strconv.Itoa(maxSanitizeNodes) + " values")
	}

	encoded, err := json.Marshal(outcome.Value)
	if err != nil {
		return "", 0, tooLarge("the lifestyle log for this day cannot be rendered")
	}
	return string(encoded), outcome.Dropped, nil
}
