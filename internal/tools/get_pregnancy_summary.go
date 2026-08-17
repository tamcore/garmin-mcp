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

// ToolGetPregnancySummary is the upstream compatibility name of the pregnancy-
// summary tool.
const ToolGetPregnancySummary = "get_pregnancy_summary"

// DefaultMaxPregnancySummaryBytes bounds the document this tool will return.
//
// The document is returned whole rather than field-mapped, so it is one result
// this server cannot reduce by selecting fields. A document past the bound is
// refused rather than cut: half a JSON document is not a JSON document, and this
// document is the most sensitive category this project handles.
const DefaultMaxPregnancySummaryBytes = 64 << 10

// PregnancySummary is the account's pregnancy snapshot.
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
// It is pregnancy health data — the most sensitive category this project handles:
// never log it, never cache it.
type PregnancySummary struct {
	HasData bool `json:"has_data" jsonschema:"whether Garmin held a pregnancy snapshot for this account"`

	// Document is the account's pregnancy snapshot, sanitized, under Garmin's own
	// field names. It is pregnancy health data: never log it.
	Document any `json:"document,omitempty" jsonschema:"Garmin's pregnancy-snapshot record, sanitized"`

	// DroppedFields is how many identifying keys were removed from Document. It is
	// a count and never a list of names: see sanitizeUntyped for why naming them
	// would disclose what removing them hid.
	DroppedFields int `json:"dropped_fields" jsonschema:"identifying keys removed from document"`
}

// LogValue reports whether data was found and never the document.
func (p PregnancySummary) LogValue() slog.Value {
	return shape("pregnancySummary",
		slog.Bool("hasData", p.HasData),
		slog.Int("droppedFields", p.DroppedFields),
	)
}

func getPregnancySummaryContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetPregnancySummary,
			Title: "Get pregnancy summary",
			Description: "read the account's pregnancy summary. Takes no arguments; the " +
				"account is the one this session is authenticated for. The document is " +
				"returned as JSON text because its field set is not pinned; a document over " +
				"this server's bound is refused rather than cut",
			Tier:        policy.TierReadOnly,
			Category:    categoryWomensHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(),
	}
}

// registerGetPregnancySummary registers the tool.
func registerGetPregnancySummary(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, _ noArguments) (
		*mcp.CallToolResult, PregnancySummary, error,
	) {
		out, err := svc.readPregnancySummary(ctx)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getPregnancySummaryContract().Registration(), handler)
}

// readPregnancySummary performs the read behind the tool.
func (s *service) readPregnancySummary(ctx context.Context) (PregnancySummary, error) {
	session, err := s.session(ctx)
	if err != nil {
		return PregnancySummary{}, err
	}
	document, err := s.womensHealth.PregnancySummary(ctx, session)
	if err != nil {
		return PregnancySummary{}, fail(err)
	}
	return newPregnancySummary(document)
}

// newPregnancySummary maps the domain model onto the result, refusing a document
// over the bound. The refusal names the bound, so a caller knows what it may ask
// for instead.
//
// The byte bound is checked before the document is parsed, so an oversized
// document costs no walk. What survives the bound is sanitised rather than
// forwarded: the field set is unsourced, so the only thing keeping an account
// identifier out of this passthrough is sanitizeUntyped.
func newPregnancySummary(document api.PregnancySummary) (PregnancySummary, error) {
	if !document.HasDocument() {
		return PregnancySummary{}, nil
	}
	if len(document.Document) > DefaultMaxPregnancySummaryBytes {
		return PregnancySummary{}, tooLarge(
			"the pregnancy-summary document is larger than " +
				strconv.Itoa(DefaultMaxPregnancySummaryBytes) + " bytes")
	}

	sanitized, dropped, err := sanitizedWomensHealthDocument(document.Document)
	if err != nil {
		return PregnancySummary{}, err
	}
	return PregnancySummary{
		HasData:       true,
		Document:      sanitized,
		DroppedFields: dropped,
	}, nil
}
