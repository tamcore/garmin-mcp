package tools

import (
	"context"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// The upstream compatibility names of the weigh-in write tools.
const (
	ToolAddWeighIn               = "add_weigh_in"
	ToolAddWeighInWithTimestamps = "add_weigh_in_with_timestamps"
)

// defaultWeighInUnit is the manifest default for unit_key on both add tools
// (weight_management.py:156, :176-178): "kg".
const defaultWeighInUnit = "kg"

// argWeighInWeight is the wire argument name both add tools share.
const argWeighInWeight = "weight"

// argDateTimestamp and argGMTTimestamp are add_weigh_in_with_timestamps' two
// nullable timestamp argument names.
const (
	argDateTimestamp = "date_timestamp"
	argGMTTimestamp  = "gmt_timestamp"
)

// weighInTimestampEchoLayout is the caller-facing timestamp form both add tools
// accept and echo back: "YYYY-MM-DDThh:mm:ss", with no timezone suffix and no
// fractional seconds. Source: weight_management.py:187-188 (the documented
// date_timestamp/gmt_timestamp format) and :194-195 (now.strftime('%Y-%m-%dT%H:%M:%S')).
//
// This is deliberately not the RFC 3339 layout the rest of this package's
// absolute-instant arguments use (see writeargs.go's parseInstant): upstream's own
// format carries no offset and no fraction, and the wire DTO
// (weightwrite.go's weighInDTO) renders its own millisecond-fraction form
// independently, so this layout exists only for parsing and echoing the caller's
// argument, never for the wire body itself.
const weighInTimestampEchoLayout = "2006-01-02T15:04:05"

// maxWeighInTimestampLen bounds a date_timestamp/gmt_timestamp argument: exactly
// 19 characters in weighInTimestampEchoLayout.
const maxWeighInTimestampLen = 19

// weighInTimestampPattern anchors a date_timestamp/gmt_timestamp argument to
// weighInTimestampEchoLayout's shape before it ever reaches time.Parse.
const weighInTimestampPattern = `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}$`

// parseWeighInTimestamp validates a date_timestamp/gmt_timestamp argument.
func parseWeighInTimestamp(field, value string) (time.Time, error) {
	if len(value) > maxWeighInTimestampLen {
		return time.Time{}, invalidArgument(field + " must be exactly nineteen characters, YYYY-MM-DDThh:mm:ss")
	}
	parsed, err := time.Parse(weighInTimestampEchoLayout, value)
	if err != nil {
		return time.Time{}, invalidArgument(field + " must be a real instant in YYYY-MM-DDThh:mm:ss form")
	}
	return parsed, nil
}

// renderWeighInTimestampEcho renders the caller-facing echo of an instant: the
// local wall-clock digits it carries, formatted in weighInTimestampEchoLayout with
// no conversion — the same non-conversion rule weightwrite.go's
// renderLocalWeighInTimestamp documents for the local field.
func renderWeighInTimestampEcho(instant time.Time) string {
	return instant.Format(weighInTimestampEchoLayout)
}

// renderWeighInTimestampEchoGMT renders the caller-facing echo of an instant
// converted to UTC first, mirroring weightwrite.go's renderGMTWeighInTimestamp.
func renderWeighInTimestampEchoGMT(instant time.Time) string {
	return instant.UTC().Format(weighInTimestampEchoLayout)
}

// weighInUnitProperty declares the unit_key argument both add tools share.
//
// The manifest's own inputSchema declares no enum (it is a bare string with
// default "kg"), but this server accepts only "kg" or "lbs" and never silently
// coerces "lb" to "lbs": VALID_WEIGHT_UNITS in the pinned python-garminconnect
// release is {"kg", "lbs"} (see api.ParseWeightUnit), while the Taxuspt tool
// docstring loosely advertises "kg or lb". Declaring the closed enum here, beyond
// what the thin manifest states, tells a caller the accepted values up front
// instead of only at call time, the same way foodlogwrites.go's
// foodSourceProperty declares a closed enum for its own controlled vocabulary.
func weighInUnitProperty() Property {
	return Property{
		Name:  "unit_key",
		Types: []string{typeString},
		Description: "the unit the weight is expressed in: \"kg\" or \"lbs\". \"lb\" is " +
			"not accepted, because Garmin's wire format requires the full spelling \"lbs\"",
		Enum:    []any{api.WeightUnitKg, api.WeightUnitLbs},
		Default: defaultWeighInUnit,
	}
}

// parseWeighInUnitArg resolves an optional unit_key argument, applying the
// manifest default and rejecting anything but "kg" or "lbs" — never coercing.
func parseWeighInUnitArg(value *string) (api.WeightUnit, error) {
	unitValue := defaultWeighInUnit
	if value != nil {
		unitValue = *value
	}
	unit, err := api.ParseWeightUnit(unitValue)
	if err != nil {
		return "", invalidArgument("unit_key must be \"kg\" or \"lbs\"")
	}
	return unit, nil
}

// AddWeighInResult is what add_weigh_in reports, matching the manifest's
// staticTopLevelKeys (message, status, unit, weight) with one deliberate
// deviation: status is the HTTP status Garmin answered with, an int, rather than
// upstream's literal "success" string — this package's every other write result
// (FoodLogWriteResult, LogDeletionResult, WriteResult) reports status the same
// way, and a literal string would be the one write result in this package that
// does not.
type AddWeighInResult struct {
	Weight  float64 `json:"weight" jsonschema:"the weight value that was recorded"`
	Unit    string  `json:"unit" jsonschema:"the unit the weight was recorded in"`
	Status  int     `json:"status" jsonschema:"the HTTP status Garmin answered with"`
	Message string  `json:"message" jsonschema:"a human-readable confirmation"`
}

// LogValue reports that a write happened, never the weight or the unit.
func (r AddWeighInResult) LogValue() slog.Value {
	return shape("addWeighInResult", slog.Int("status", r.Status))
}

// addWeighInInput is the strict argument set.
type addWeighInInput struct {
	Weight  float64 `json:"weight" jsonschema:"the weight value to record"`
	UnitKey *string `json:"unit_key,omitempty" jsonschema:"the unit, kg or lbs, default kg"`
}

func addWeighInContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolAddWeighIn,
			Title: "Add a weigh-in",
			Description: "add a new weight measurement, recorded at the current instant. " +
				"Creates a new record every time it is called",
			Tier:        policy.TierWrite,
			Category:    categoryHealth,
			Annotations: writeAnnotations(false),
		},
		Schema: NewSchema(
			Property{
				Name: argWeighInWeight, Types: []string{typeNumber},
				Description: "the weight value to record", Required: true,
			},
			weighInUnitProperty(),
		),
	}
}

// registerAddWeighIn registers the tool.
func registerAddWeighIn(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in addWeighInInput) (
		*mcp.CallToolResult, AddWeighInResult, error,
	) {
		out, err := svc.addWeighIn(ctx, in)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, addWeighInContract().Registration(), handler)
}

// addWeighIn performs the write behind the tool.
//
// Unlike add_weigh_in_with_timestamps, this tool's schema carries no timestamp
// argument at all, so both the local and the GMT instant come from the server
// clock: api.AddWeighIn and api.AddWeighInWithTimestamps dispatch the identical
// payload shape, differing only in how the caller resolved the two instants
// (weight.go's AddWeighIn doc comment), and "use the current time" is the
// resolution this tool supplies.
func (s *service) addWeighIn(ctx context.Context, in addWeighInInput) (AddWeighInResult, error) {
	unit, err := parseWeighInUnitArg(in.UnitKey)
	if err != nil {
		return AddWeighInResult{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return AddWeighInResult{}, err
	}

	now := s.now()
	result, err := s.weight.AddWeighIn(ctx, session, api.WeighInEntry{
		Weight: in.Weight, Unit: unit, LocalAt: now, GMTAt: now,
	})
	if err != nil {
		return AddWeighInResult{}, fail(err)
	}
	return AddWeighInResult{
		Weight:  in.Weight,
		Unit:    string(unit),
		Status:  result.Status,
		Message: "Weight measurement added successfully.",
	}, nil
}

// AddWeighInWithTimestampsResult is what add_weigh_in_with_timestamps reports,
// matching the manifest's staticTopLevelKeys (message, status, timestamp_gmt,
// timestamp_local, unit, weight), with the same status-as-HTTP-int deviation
// AddWeighInResult documents.
type AddWeighInWithTimestampsResult struct {
	Weight         float64 `json:"weight" jsonschema:"the weight value that was recorded"`
	Unit           string  `json:"unit" jsonschema:"the unit the weight was recorded in"`
	TimestampLocal string  `json:"timestamp_local" jsonschema:"the local wall-clock timestamp that was recorded"`
	TimestampGMT   string  `json:"timestamp_gmt" jsonschema:"the UTC wall-clock timestamp that was recorded"`
	Status         int     `json:"status" jsonschema:"the HTTP status Garmin answered with"`
	Message        string  `json:"message" jsonschema:"a human-readable confirmation"`
}

// LogValue reports that a write happened, never the weight, the unit or either
// timestamp.
func (r AddWeighInWithTimestampsResult) LogValue() slog.Value {
	return shape("addWeighInWithTimestampsResult", slog.Int("status", r.Status))
}

// addWeighInWithTimestampsInput is the strict argument set. date_timestamp and
// gmt_timestamp are nullable, matching the manifest's "default": null.
type addWeighInWithTimestampsInput struct {
	Weight        float64 `json:"weight" jsonschema:"the weight value to record"`
	UnitKey       *string `json:"unit_key,omitempty" jsonschema:"the unit, kg or lbs, default kg"`
	DateTimestamp *string `json:"date_timestamp,omitempty" jsonschema:"the local timestamp, YYYY-MM-DDThh:mm:ss"`
	GMTTimestamp  *string `json:"gmt_timestamp,omitempty" jsonschema:"the GMT timestamp, YYYY-MM-DDThh:mm:ss"`
}

// weighInTimestampProperty declares date_timestamp/gmt_timestamp: nullable, no
// declared default beyond Nullable — the same representation
// nutritionsettings.go's nullableIntegerProperty uses for a manifest field whose
// own default is JSON null.
func weighInTimestampProperty(name, description string) Property {
	return Property{
		Name:        name,
		Types:       []string{typeString},
		Description: description + ", YYYY-MM-DDThh:mm:ss, no timezone",
		Pattern:     weighInTimestampPattern,
		MaxLength:   new(maxWeighInTimestampLen),
		Nullable:    true,
	}
}

func addWeighInWithTimestampsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolAddWeighInWithTimestamps,
			Title: "Add a weigh-in at a specific instant",
			Description: "add a new weight measurement at caller-supplied local and GMT " +
				"timestamps. If either timestamp is omitted or null, both are generated " +
				"fresh from the current instant instead — matching the pinned upstream " +
				"tool, which discards any single timestamp supplied alone rather than " +
				"guessing the other. Creates a new record every time it is called",
			Tier:        policy.TierWrite,
			Category:    categoryHealth,
			Annotations: writeAnnotations(false),
		},
		Schema: NewSchema(
			Property{
				Name: argWeighInWeight, Types: []string{typeNumber},
				Description: "the weight value to record", Required: true,
			},
			weighInUnitProperty(),
			weighInTimestampProperty(argDateTimestamp, "the local timestamp to record the weigh-in at"),
			weighInTimestampProperty(argGMTTimestamp, "the GMT timestamp to record the weigh-in at"),
		),
	}
}

// registerAddWeighInWithTimestamps registers the tool.
func registerAddWeighInWithTimestamps(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in addWeighInWithTimestampsInput) (
		*mcp.CallToolResult, AddWeighInWithTimestampsResult, error,
	) {
		out, err := svc.addWeighInWithTimestamps(ctx, in)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, addWeighInWithTimestampsContract().Registration(), handler)
}

// resolveWeighInInstants resolves the local and GMT instants a write dispatches.
//
// Matching weight_management.py:191-196 exactly: if either timestamp is absent,
// both are regenerated from the current instant, and any single timestamp the
// caller did supply is discarded rather than paired with a generated
// counterpart. That is upstream's own documented behavior, unusual as it is, and
// this tool follows it rather than inventing a more permissive merge.
func (s *service) resolveWeighInInstants(in addWeighInWithTimestampsInput) (local, gmt time.Time, err error) {
	if in.DateTimestamp == nil || in.GMTTimestamp == nil {
		now := s.now()
		return now, now, nil
	}
	local, err = parseWeighInTimestamp(argDateTimestamp, *in.DateTimestamp)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	gmt, err = parseWeighInTimestamp(argGMTTimestamp, *in.GMTTimestamp)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return local, gmt, nil
}

// addWeighInWithTimestamps performs the write behind the tool.
func (s *service) addWeighInWithTimestamps(
	ctx context.Context, in addWeighInWithTimestampsInput,
) (AddWeighInWithTimestampsResult, error) {
	unit, err := parseWeighInUnitArg(in.UnitKey)
	if err != nil {
		return AddWeighInWithTimestampsResult{}, err
	}
	local, gmt, err := s.resolveWeighInInstants(in)
	if err != nil {
		return AddWeighInWithTimestampsResult{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return AddWeighInWithTimestampsResult{}, err
	}

	result, err := s.weight.AddWeighInWithTimestamps(ctx, session, api.WeighInEntry{
		Weight: in.Weight, Unit: unit, LocalAt: local, GMTAt: gmt,
	})
	if err != nil {
		return AddWeighInWithTimestampsResult{}, fail(err)
	}
	return AddWeighInWithTimestampsResult{
		Weight:         in.Weight,
		Unit:           string(unit),
		TimestampLocal: renderWeighInTimestampEcho(local),
		TimestampGMT:   renderWeighInTimestampEchoGMT(gmt),
		Status:         result.Status,
		Message:        "Weight measurement added successfully.",
	}, nil
}
