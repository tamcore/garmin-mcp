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

// ToolAddBodyComposition is the upstream compatibility name of the tool.
const ToolAddBodyComposition = "add_body_composition"

// nullableNumberProperty declares a nullable optional number argument with
// no declared bound, the same representation weighInTimestampProperty uses
// for a manifest field whose own default is JSON null.
func nullableNumberProperty(name, description string) Property {
	return Property{
		Name: name, Types: []string{typeNumber}, Description: description, Nullable: true,
	}
}

// nullableBoundedIntegerProperty declares a nullable optional integer
// argument bounded to [minimum, maximum].
func nullableBoundedIntegerProperty(name, description string, minimum, maximum float64) Property {
	return Property{
		Name: name, Types: []string{typeInteger}, Description: description,
		Minimum: bound(minimum), Maximum: bound(maximum), Nullable: true,
	}
}

// nullableBoundedNumberProperty declares a nullable optional number argument
// bounded to [minimum, maximum]. Unlike nullableBoundedIntegerProperty, a
// fractional value is accepted: compat/tools.json types metabolic_age
// "number", not "integer".
func nullableBoundedNumberProperty(name, description string, minimum, maximum float64) Property {
	return Property{
		Name: name, Types: []string{typeNumber}, Description: description,
		Minimum: bound(minimum), Maximum: bound(maximum), Nullable: true,
	}
}

// AddBodyCompositionResult is what add_body_composition reports. Upstream's
// own outputShape carries no static top-level keys — it echoes back
// whatever the FIT upload's response happens to be — so this result matches
// every other write in this package that answers an otherwise uncurated
// acknowledgement: the caller's own date, plus the HTTP status.
//
// Weight is the weight the FIT message actually stored, not the caller's own
// input: internal/garmin/api's scaledUint16 truncates to the FIT format's
// two-decimal-place scale (fit.py:491's own scale-100 field, matching
// FitBaseType.pack's `int(value)` truncation), so a caller who writes 70.006
// must be told 70.00 was stored, never have 70.006 echoed back unchanged.
type AddBodyCompositionResult struct {
	Date    string  `json:"date" jsonschema:"the calendar day the reading was recorded for"`
	Weight  float64 `json:"weight" jsonschema:"the weight actually stored, in kg (at the FIT format's own precision)"`
	Status  int     `json:"status" jsonschema:"the HTTP status Garmin answered with"`
	Message string  `json:"message" jsonschema:"a human-readable confirmation"`
}

// LogValue reports that a write happened, never the weight, the date or any
// body-composition figure.
func (r AddBodyCompositionResult) LogValue() slog.Value {
	return shape("addBodyCompositionResult", slog.Int("status", r.Status))
}

// addBodyCompositionInput is the strict argument set, matching
// compat/tools.json's add_body_composition inputSchema.
type addBodyCompositionInput struct {
	Date             string   `json:"date" jsonschema:"the calendar day to record the reading for, YYYY-MM-DD"`
	Weight           float64  `json:"weight" jsonschema:"the weight to record, in kg"`
	PercentFat       *float64 `json:"percent_fat,omitempty" jsonschema:"body fat percentage"`
	PercentHydration *float64 `json:"percent_hydration,omitempty" jsonschema:"hydration percentage"`
	VisceralFatMass  *float64 `json:"visceral_fat_mass,omitempty" jsonschema:"visceral fat mass in kg"`
	BoneMass         *float64 `json:"bone_mass,omitempty" jsonschema:"bone mass in kg"`
	MuscleMass       *float64 `json:"muscle_mass,omitempty" jsonschema:"muscle mass in kg"`
	BasalMet         *float64 `json:"basal_met,omitempty" jsonschema:"basal metabolic rate in kcal/day"`
	ActiveMet        *float64 `json:"active_met,omitempty" jsonschema:"active metabolic rate in kcal/day"`
	PhysiqueRating   *int64   `json:"physique_rating,omitempty" jsonschema:"physique rating, 1-9"`
	// MetabolicAge is a number, not an integer: compat/tools.json types
	// metabolic_age "number", unlike physique_rating and
	// visceral_fat_rating, which really are integers.
	MetabolicAge      *float64 `json:"metabolic_age,omitempty" jsonschema:"metabolic age in years"`
	VisceralFatRating *int64   `json:"visceral_fat_rating,omitempty" jsonschema:"visceral fat rating"`
	BMI               *float64 `json:"bmi,omitempty" jsonschema:"body mass index"`
}

func addBodyCompositionContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolAddBodyComposition,
			Title: "Add body composition data",
			Description: "record a body-composition reading: weight and, optionally, body " +
				"fat, hydration, visceral and bone/muscle mass, metabolic rate, physique " +
				"rating, metabolic age and BMI. Creates a new record every time it is called",
			Tier:        policy.TierWrite,
			Category:    categoryHealth,
			Annotations: writeAnnotations(false),
		},
		Schema: NewSchema(
			dateProperty("date", "the calendar day to record the reading for"),
			Property{
				Name: argWeighInWeight, Types: []string{typeNumber},
				Description: "the weight to record, in kg", Required: true,
			},
			nullableNumberProperty("percent_fat", "body fat percentage"),
			nullableNumberProperty("percent_hydration", "hydration percentage"),
			nullableNumberProperty("visceral_fat_mass", "visceral fat mass in kg"),
			nullableNumberProperty("bone_mass", "bone mass in kg"),
			nullableNumberProperty("muscle_mass", "muscle mass in kg"),
			nullableNumberProperty("basal_met", "basal metabolic rate in kcal/day"),
			nullableNumberProperty("active_met", "active metabolic rate in kcal/day"),
			nullableBoundedIntegerProperty("physique_rating", "physique rating, 1-9", 1, 9),
			nullableBoundedNumberProperty("metabolic_age", "metabolic age in years", 1, 120),
			nullableBoundedIntegerProperty("visceral_fat_rating", "visceral fat rating", 1, 59),
			nullableNumberProperty("bmi", "body mass index"),
		),
	}
}

// registerAddBodyComposition registers the tool.
func registerAddBodyComposition(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in addBodyCompositionInput) (
		*mcp.CallToolResult, AddBodyCompositionResult, error,
	) {
		out, err := svc.addBodyComposition(ctx, in)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, addBodyCompositionContract().Registration(), handler)
}

// addBodyComposition performs the write behind the tool.
//
// The recorded instant is midnight UTC of the caller's date.
//
// Deliberate deviation: upstream's own FitEncoder.timestamp() converts a
// naive `datetime.fromisoformat(date)` through `time.mktime`, which
// interprets it in whatever timezone the Python process's host happens to
// carry (fit.py:410-416) — not the Garmin account's own timezone, which
// add_body_composition never receives. That host timezone is ambient state
// this server does not have and, per AGENTS.md, must not depend on: two
// deployments of the identical code, differing only in the host OS's zone
// file, would silently record a caller's reading on two different calendar
// days. This port fixes the instant at UTC midnight instead, which is
// deterministic across every deployment. The tradeoff is symmetric with
// upstream's own bug: an account whose real timezone is not UTC can still
// see the reading attributed to the adjacent calendar day. There is no
// timestamp or timezone argument on this tool's own manifest to resolve that
// correctly; see docs/parity.md's deliberate-deviations list.
func (s *service) addBodyComposition(
	ctx context.Context, in addBodyCompositionInput,
) (AddBodyCompositionResult, error) {
	date, err := parseCalendarDate("date", in.Date)
	if err != nil {
		return AddBodyCompositionResult{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return AddBodyCompositionResult{}, err
	}

	day := date.Time()
	at := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	result, err := s.dataManagement.AddBodyComposition(ctx, session, api.BodyCompositionEntry{
		At: at, Weight: in.Weight,
		PercentFat: in.PercentFat, PercentHydration: in.PercentHydration,
		VisceralFatMass: in.VisceralFatMass, BoneMass: in.BoneMass, MuscleMass: in.MuscleMass,
		BasalMet: in.BasalMet, ActiveMet: in.ActiveMet,
		PhysiqueRating: in.PhysiqueRating, MetabolicAge: in.MetabolicAge,
		VisceralFatRating: in.VisceralFatRating, BMI: in.BMI,
	})
	if err != nil {
		return AddBodyCompositionResult{}, fail(err)
	}
	return AddBodyCompositionResult{
		Date: date.String(), Weight: result.StoredWeightKG, Status: result.Status,
		Message: "Body composition data added successfully.",
	}, nil
}
