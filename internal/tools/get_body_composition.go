package tools

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetBodyComposition is the upstream compatibility name of the body-composition
// tool.
const ToolGetBodyComposition = "get_body_composition"

// maxBodyCompositionEntries bounds the per-weigh-in list one call returns. It is
// above the widest window the request layer allows, so it cuts nothing a permitted
// window produces.
const maxBodyCompositionEntries = 512

// BodyComposition is the account's body-composition envelope for a date window.
//
// It is health data — never log it, never cache it. A weight, a body-fat share and a
// metabolic age are all readings.
type BodyComposition struct {
	// StartDate and EndDate are the window that was requested.
	StartDate string `json:"start_date" jsonschema:"the inclusive first day, YYYY-MM-DD"`
	EndDate   string `json:"end_date" jsonschema:"the inclusive last day, YYYY-MM-DD"`

	// ReportedStartDate and ReportedEndDate are Garmin's own echo of the window,
	// returned so a caller can see when the service answered a different one.
	ReportedStartDate *string `json:"reported_start_date,omitempty" jsonschema:"the window Garmin echoed"`
	ReportedEndDate   *string `json:"reported_end_date,omitempty" jsonschema:"the window Garmin echoed"`

	// HasEntryList reports that Garmin sent the per-weigh-in list at all. An
	// account with no weigh-in gets an empty list rather than no list, and the two
	// are different answers.
	HasEntryList bool `json:"has_entry_list" jsonschema:"whether Garmin sent the per-weigh-in list"`

	// Entries are Garmin's per-weigh-in records under Garmin's own names: the element
	// shape of a populated list has never been observed, so nothing here is renamed.
	// Every record is sanitised — see sanitizeUntyped — so keeping Garmin's names
	// does not mean forwarding Garmin's identifiers.
	Entries []any `json:"entries" jsonschema:"Garmin's per-weigh-in records"`

	// EntryCount is how many records this result carries.
	EntryCount int `json:"entry_count" jsonschema:"how many records this result carries"`

	// EntriesTruncated reports that this result is not the whole list: either it was
	// cut at this server's bound, or a record was too deep or too large to render.
	EntriesTruncated bool `json:"entries_truncated" jsonschema:"whether the list is incomplete"`

	// DroppedFields is how many identifying keys were removed, over the records and
	// the averaged metrics together. It is a count and never a list of names.
	DroppedFields int `json:"dropped_fields" jsonschema:"identifying keys removed from the result"`

	// Average is the window's averaged composition, absent only when Garmin sent
	// no such object.
	Average *BodyCompositionAverage `json:"average,omitempty" jsonschema:"the window's averaged composition"`
}

// BodyCompositionAverage is the averaged composition over the window.
//
// The two timestamps are epoch milliseconds, which is a different encoding from the
// calendar strings in the same envelope, and they are the only fields here with a
// confirmed type.
//
// The ten metrics are **unconfirmed**. The sampled account records no weight, so
// every metric arrived null and no sample has shown what a populated one looks like.
// Each is therefore passed on exactly as Garmin sent it, and omitted when Garmin sent
// nothing, rather than being declared a number this project has never seen.
type BodyCompositionAverage struct {
	FromEpochMS  *int64 `json:"from_epoch_ms,omitempty" jsonschema:"window start, epoch milliseconds"`
	UntilEpochMS *int64 `json:"until_epoch_ms,omitempty" jsonschema:"window end, epoch milliseconds"`

	Weight         any `json:"weight,omitempty" jsonschema:"average weight, as Garmin sent it"`
	BMI            any `json:"bmi,omitempty" jsonschema:"average BMI, as Garmin sent it"`
	BodyFat        any `json:"body_fat,omitempty" jsonschema:"average body fat, as Garmin sent it"`
	BodyWater      any `json:"body_water,omitempty" jsonschema:"average body water, as Garmin sent it"`
	BoneMass       any `json:"bone_mass,omitempty" jsonschema:"average bone mass, as Garmin sent it"`
	MuscleMass     any `json:"muscle_mass,omitempty" jsonschema:"average muscle mass, as Garmin sent it"`
	PhysiqueRating any `json:"physique_rating,omitempty" jsonschema:"physique rating, as Garmin sent it"`
	VisceralFat    any `json:"visceral_fat,omitempty" jsonschema:"visceral fat, as Garmin sent it"`
	MetabolicAge   any `json:"metabolic_age,omitempty" jsonschema:"metabolic age, as Garmin sent it"`
	Trend          any `json:"trend,omitempty" jsonschema:"trend, as Garmin sent it"`
}

// LogValue reports the shape of the result, never a weight or any other reading.
func (c BodyComposition) LogValue() slog.Value {
	return shape("bodyComposition",
		slog.Bool("hasEntryList", c.HasEntryList),
		slog.Int("entries", c.EntryCount),
		slog.Bool("truncated", c.EntriesTruncated),
		slog.String("average", presence(c.Average != nil)),
	)
}

// getBodyCompositionInput is the strict argument set. The end day is optional and
// defaults to the start day, matching get_body_composition.
type getBodyCompositionInput struct {
	StartDate string `json:"start_date" jsonschema:"the inclusive first day, YYYY-MM-DD"`
	EndDate   string `json:"end_date,omitempty" jsonschema:"the inclusive last day, YYYY-MM-DD"`
}

func getBodyCompositionContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetBodyComposition,
			Title: "Get body composition",
			Description: "read the account's body-composition series for one day or for an " +
				"inclusive date window. Omit end_date to read a single day. The window is " +
				"bounded by this server; narrow it if the call is refused",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(
			dateProperty("start_date", "the inclusive first day of the window"),
			optionalDateProperty("end_date",
				"the inclusive last day of the window, defaulting to start_date"),
		),
	}
}

// registerGetBodyComposition registers the tool.
func registerGetBodyComposition(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getBodyCompositionInput) (
		*mcp.CallToolResult, BodyComposition, error,
	) {
		composition, err := svc.bodyComposition(ctx, in)
		return nil, composition, err
	}
	return mcpserver.AddTool(registry, getBodyCompositionContract().Registration(), handler)
}

// bodyComposition validates the window, reads it and bounds the document.
func (s *service) bodyComposition(
	ctx context.Context, in getBodyCompositionInput,
) (BodyComposition, error) {
	span, err := s.resolveOptionalWindow(in.StartDate, in.EndDate)
	if err != nil {
		return BodyComposition{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return BodyComposition{}, err
	}

	composition, err := s.wellness.Daily().BodyComposition(ctx, session, span)
	if err != nil {
		return BodyComposition{}, fail(err)
	}
	return newBodyComposition(span, composition), nil
}

// newBodyComposition maps the envelope onto the bounded result.
func newBodyComposition(span client.DateRange, composition api.BodyComposition) BodyComposition {
	average, averageTally := newBodyCompositionAverage(composition.TotalAverage)
	out := BodyComposition{
		StartDate:         span.Start().String(),
		EndDate:           span.End().String(),
		ReportedStartDate: composition.StartDate,
		ReportedEndDate:   composition.EndDate,
		Entries:           []any{},
		Average:           average,
		DroppedFields:     averageTally.Dropped,
		EntriesTruncated:  averageTally.Truncated,
	}
	if list := composition.DateWeightList; list != nil {
		out.HasEntryList = true
		bounded := boundedUntyped(list.Items(), maxBodyCompositionEntries)
		out.Entries = bounded.Values
		out.EntriesTruncated = out.EntriesTruncated || bounded.Truncated
		out.DroppedFields += bounded.Dropped
	}
	out.EntryCount = len(out.Entries)
	return out
}

// newBodyCompositionAverage maps the averaged composition, keeping every metric under
// the name and in the form Garmin sent it, and reports how many identifying keys were
// removed from the ten of them together.
//
// The metrics accept arbitrary JSON because none of them has ever been observed
// populated. That is what makes them a drift surface: a metric that arrives as an
// object rather than a number would carry whatever that object holds, so each one goes
// through the shared sanitiser.
func newBodyCompositionAverage(
	average *api.BodyCompositionAverage,
) (*BodyCompositionAverage, untypedList) {
	if average == nil {
		return nil, untypedList{}
	}

	tally := untypedList{}
	clean := func(raw json.RawMessage) any {
		outcome := sanitizeRaw(raw)
		tally.Dropped += outcome.Dropped
		tally.Truncated = tally.Truncated || outcome.Truncated
		return outcome.Value
	}

	return &BodyCompositionAverage{
		FromEpochMS:    optionalInt64(average.From),
		UntilEpochMS:   optionalInt64(average.Until),
		Weight:         clean(average.Weight),
		BMI:            clean(average.BMI),
		BodyFat:        clean(average.BodyFat),
		BodyWater:      clean(average.BodyWater),
		BoneMass:       clean(average.BoneMass),
		MuscleMass:     clean(average.MuscleMass),
		PhysiqueRating: clean(average.PhysiqueRating),
		VisceralFat:    clean(average.VisceralFat),
		MetabolicAge:   clean(average.MetabolicAge),
		Trend:          clean(average.Trend),
	}, tally
}

// resolveOptionalWindow validates a window whose end day defaults to its start day.
func (s *service) resolveOptionalWindow(startValue, endValue string) (client.DateRange, error) {
	if endValue == "" {
		endValue = startValue
	}
	return parseWindow(startValue, endValue, s.limits)
}

// optionalDateProperty declares a calendar-date argument the caller may omit.
func optionalDateProperty(name, description string) Property {
	property := dateProperty(name, description)
	property.Required = false
	return property
}
