package tools

import (
	"context"
	"log/slog"
	"maps"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetFitnessAgeData is the upstream compatibility name of the fitness-age read.
const ToolGetFitnessAgeData = "get_fitnessage_data"

// maxFitnessAgeComponents bounds the component breakdown.
//
// It is a safety ceiling rather than a measured maximum: the pinned upstream tool
// names three components — BMI, resting heart rate and vigorous activity — and this
// project has sampled no document with more. Sixteen is five times the documented set,
// and a document that exceeds it is reported as truncated rather than cut silently.
const maxFitnessAgeComponents = 16

// argNameDetails is the wire name of the breakdown switch.
const argNameDetails = "details"

// A FitnessAgeComponent is one contributor to the fitness age.
type FitnessAgeComponent struct {
	Value                  *float64 `json:"value,omitempty" jsonschema:"the component's measured value"`
	Target                 *float64 `json:"target,omitempty" jsonschema:"the target Garmin sets"`
	ImprovementNeeded      *float64 `json:"improvement_needed,omitempty" jsonschema:"the distance to the target"`
	PotentialAgeIfImproved *float64 `json:"potential_age_if_improved,omitempty" jsonschema:"the age the target gives"`
	Priority               *int     `json:"priority,omitempty" jsonschema:"Garmin's improvement priority"`
	Stale                  *bool    `json:"stale,omitempty" jsonschema:"whether the measurement is stale"`
	LastMeasurement        *string  `json:"last_measurement,omitempty" jsonschema:"the day last measured"`
}

// FitnessAgeData is the fitness age of one day. It is health data — never log it,
// never cache it.
type FitnessAgeData struct {
	Date string `json:"date" jsonschema:"the day asked for, YYYY-MM-DD"`

	FitnessAgeYears           *float64 `json:"fitness_age_years,omitempty" jsonschema:"the fitness age Garmin computed"`
	ChronologicalAgeYears     *float64 `json:"chronological_age_years,omitempty" jsonschema:"the account's real age"`
	AgeDifferenceYears        *float64 `json:"age_difference_years,omitempty" jsonschema:"real age minus fitness age"`
	AchievableFitnessAgeYears *float64 `json:"achievable_fitness_age_years,omitempty" jsonschema:"the reachable age"`
	PreviousFitnessAgeYears   *float64 `json:"previous_fitness_age_years,omitempty" jsonschema:"the previous fitness age"`
	LastUpdated               *string  `json:"last_updated,omitempty" jsonschema:"when Garmin last recomputed it"`

	Reported bool `json:"reported" jsonschema:"whether Garmin held a fitness age for this day"`

	Components          map[string]FitnessAgeComponent `json:"components,omitempty" jsonschema:"the breakdown by name"`
	ComponentsTruncated bool                           `json:"components_truncated" jsonschema:"whether it was cut"`
}

// LogValue reports the shape of the answer, never a reading.
func (f FitnessAgeData) LogValue() slog.Value {
	return shape("fitnessAgeData",
		slog.Bool("reported", f.Reported),
		slog.Int("components", len(f.Components)),
		slog.Bool("truncated", f.ComponentsTruncated),
	)
}

// fitnessAgeInput is the strict argument set: one day, and whether to break it down.
type fitnessAgeInput struct {
	Date    string `json:"date" jsonschema:"the day to read, YYYY-MM-DD"`
	Details *bool  `json:"details" jsonschema:"whether to include the component breakdown"`
}

func getFitnessAgeDataContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetFitnessAgeData,
			Title: "Get fitness age",
			Description: "read the account's fitness age for one day: the fitness age " +
				"itself, the real age beside it, the difference between them, and what " +
				"Garmin says is achievable. Ask for details to add the component " +
				"breakdown with each component's target and the age it would give. A day " +
				"Garmin holds no fitness age for answers with reported false",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(
			dateProperty("date", "the day to read"),
			Property{
				Name:        argNameDetails,
				Types:       []string{typeBoolean},
				Description: "whether to include the component breakdown",
				Default:     false,
			},
		),
	}
}

// registerGetFitnessAgeData registers the tool.
func registerGetFitnessAgeData(registry *mcpserver.Registry, svc *service) error {
	scores, err := trainingScoresClient(svc)
	if err != nil {
		return err
	}
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in fitnessAgeInput) (
		*mcp.CallToolResult, FitnessAgeData, error,
	) {
		day, err := parseCalendarDate("date", in.Date)
		if err != nil {
			return nil, FitnessAgeData{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, FitnessAgeData{}, err
		}
		read, err := scores.FitnessAgeData(ctx, session, day)
		if err != nil {
			return nil, FitnessAgeData{}, fail(err)
		}
		details := in.Details != nil && *in.Details
		return nil, newFitnessAgeData(day.String(), read, details), nil
	}
	return mcpserver.AddTool(registry, getFitnessAgeDataContract().Registration(), handler)
}

// newFitnessAgeData maps the document onto the bounded result.
//
// Every age is rounded to one decimal, which is how the pinned upstream tool renders
// them.
func newFitnessAgeData(date string, read api.FitnessAge, details bool) FitnessAgeData {
	out := FitnessAgeData{
		Date:                      date,
		FitnessAgeYears:           roundedAge(read.FitnessAge),
		ChronologicalAgeYears:     roundedAge(read.ChronologicalAge),
		AchievableFitnessAgeYears: roundedAge(read.AchievableFitnessAge),
		PreviousFitnessAgeYears:   roundedAge(read.PreviousFitnessAge),
		LastUpdated:               optionalText(read.LastUpdated),
	}
	out.Reported = out.FitnessAgeYears != nil

	fitness, hasFitness := read.FitnessAge.Float64()
	chronological, hasChronological := read.ChronologicalAge.Float64()
	if hasFitness && hasChronological {
		difference := fitRound(chronological-fitness, placesOne)
		out.AgeDifferenceYears = &difference
	}

	if details {
		out.Components, out.ComponentsTruncated = fitnessAgeComponents(read.Components)
	}
	return out
}

// fitnessAgeComponents maps the breakdown, bounded and in a stable key order.
func fitnessAgeComponents(
	components map[string]api.FitnessAgeComponent,
) (map[string]FitnessAgeComponent, bool) {
	names := slices.Sorted(maps.Keys(components))
	truncated := false
	if len(names) > maxFitnessAgeComponents {
		names = names[:maxFitnessAgeComponents]
		truncated = true
	}

	out := make(map[string]FitnessAgeComponent, len(names))
	for _, name := range names {
		component := components[name]
		out[name] = FitnessAgeComponent{
			Value:                  optionalFloat(component.Value),
			Target:                 optionalFloat(component.TargetValue),
			ImprovementNeeded:      optionalFloat(component.ImprovementValue),
			PotentialAgeIfImproved: roundedAge(component.PotentialAge),
			Priority:               optionalInt(component.Priority),
			Stale:                  component.Stale,
			LastMeasurement:        component.LastMeasurementDate,
		}
	}
	return out, truncated
}

// roundedAge renders an optional age to one decimal, the way upstream renders one.
func roundedAge(value client.Number) *float64 {
	years, ok := value.Float64()
	if !ok {
		return nil
	}
	rounded := fitRound(years, placesOne)
	return &rounded
}
