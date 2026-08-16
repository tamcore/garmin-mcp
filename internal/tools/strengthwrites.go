package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolSetActivityStrengthExerciseSets is the name of the replace-all strength write.
// It has no entry in the pinned manifest: it comes from an unmerged upstream proposal
// and is an addition.
const ToolSetActivityStrengthExerciseSets = "set_activity_strength_exercise_sets"

// setKindEnum renders the two set kinds Garmin records.
func setKindEnum() []any { return []any{string(api.SetActive), string(api.SetRest)} }

// exerciseSetInput is one absolutely-timed set of a replace-all write.
type exerciseSetInput struct {
	Kind            string  `json:"kind" jsonschema:"the set kind, ACTIVE or REST"`
	StartTime       string  `json:"start_time" jsonschema:"the absolute start instant, RFC 3339"`
	DurationSeconds float64 `json:"duration_seconds" jsonschema:"how long the set lasted"`
	Repetitions     int     `json:"repetitions" jsonschema:"the repetition count of an active set"`
	WeightGrams     float64 `json:"weight_grams" jsonschema:"the external weight in grams"`
	Category        string  `json:"category" jsonschema:"Garmin's exercise category"`
	ExerciseName    string  `json:"exercise_name" jsonschema:"Garmin's exercise name"`
}

// setActivityStrengthExerciseSetsInput is the replace-all argument set.
type setActivityStrengthExerciseSetsInput struct {
	ActivityID any                `json:"activity_id" jsonschema:"the strength activity to write"`
	Sets       []exerciseSetInput `json:"sets" jsonschema:"the whole set list, in order"`
}

func setActivityStrengthExerciseSetsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolSetActivityStrengthExerciseSets,
			Title: "Replace an activity's strength sets",
			Description: "replace the whole strength set list of one activity. It is a " +
				"replace-all write, so the list given here becomes the list Garmin stores, " +
				"and the result is read back and compared set by set before success is " +
				"reported. Repeating the call with the same list leaves the same end state",
			Tier:        policy.TierWrite,
			Category:    categoryHealth,
			Annotations: writeAnnotations(true),
		},
		Schema: NewSchema(activityIDProperty(), Property{
			Name:        argNameSets,
			Types:       []string{typeArray},
			Description: "the whole set list, each set absolutely timed",
			Items:       exerciseSetSchema(),
			MinItems:    new(1),
			MaxItems:    new(maxStrengthSets),
			Required:    true,
		}),
	}
}

// exerciseSetSchema declares one absolutely-timed set.
func exerciseSetSchema() map[string]any {
	return map[string]any{
		keyType: typeObject,
		keyProperties: map[string]any{
			argNameKind: map[string]any{
				keyType: typeString, "enum": setKindEnum(),
				keyDescription: "the set kind",
			},
			argNameStartTime: map[string]any{
				keyType: typeString, keyFormat: formatDateTime, keyMaxLength: maxInstantLen,
				keyDescription: "the absolute start instant, RFC 3339",
			},
			argNameDurationSeconds: map[string]any{
				keyType: typeNumber, keyMinimum: 0, keyMaximum: api.MaxSetDurationSeconds,
				keyDescription: "how long the set lasted",
			},
			argNameRepetitions: map[string]any{
				keyType: typeInteger, keyMinimum: 0, keyMaximum: maxRepetitions,
				keyDescription: "the repetition count of an active set",
			},
			argNameWeightGrams: map[string]any{
				keyType: typeNumber, keyMinimum: 0, keyMaximum: maxWeightGrams,
				keyDescription: "the external weight in grams",
			},
			argNameCategory: map[string]any{
				keyType: typeString, keyMaxLength: maxExerciseKeyLen,
				keyDescription: descExerciseCategory,
			},
			argNameExerciseName: map[string]any{
				keyType: typeString, keyMaxLength: maxExerciseKeyLen,
				keyDescription: "Garmin's exercise name",
			},
		},
		keyRequired:             []any{argNameKind, argNameStartTime, argNameDurationSeconds},
		keyAdditionalProperties: false,
	}
}

func registerSetActivityStrengthExerciseSets(registry *mcpserver.Registry, svc *service) error {
	handler := func(
		ctx context.Context, _ *mcp.CallToolRequest, in setActivityStrengthExerciseSetsInput,
	) (*mcp.CallToolResult, ExerciseSetList, error) {
		sets, err := parseStrengthSets(in.Sets, svc.bounds.MaxExerciseSets, svc.catalog)
		if err != nil {
			return nil, ExerciseSetList{}, err
		}
		write, err := svc.resolveActivityWrite(ctx, in.ActivityID)
		if err != nil {
			return nil, ExerciseSetList{}, err
		}

		saved, err := svc.strength.ReplaceSets(ctx, write.session, write.id, sets)
		if err != nil {
			return nil, ExerciseSetList{}, fail(err)
		}
		return nil, newExerciseSetList(
			write.id.Int64(), saved.Sets.Items(), svc.bounds.MaxExerciseSets), nil
	}
	return mcpserver.AddTool(
		registry, setActivityStrengthExerciseSetsContract().Registration(), handler)
}

// parseStrengthSets validates the whole list before any of it is dispatched, because
// the write replaces everything: a half-validated list would replace a real session
// with a wrong one.
func parseStrengthSets(
	entries []exerciseSetInput, limit int, catalog *api.ExerciseCatalog,
) ([]api.StrengthSet, error) {
	if err := boundedCount(argNameSets, len(entries), min(limit, maxStrengthSets)); err != nil {
		return nil, err
	}
	out := make([]api.StrengthSet, 0, len(entries))
	for _, entry := range entries {
		set, err := parseStrengthSet(entry, catalog)
		if err != nil {
			return nil, err
		}
		out = append(out, set)
	}
	return out, nil
}

// parseStrengthSet validates one absolutely-timed set.
func parseStrengthSet(
	entry exerciseSetInput, catalog *api.ExerciseCatalog,
) (api.StrengthSet, error) {
	kind, err := parseSetKind(entry.Kind)
	if err != nil {
		return api.StrengthSet{}, err
	}
	start, err := parseInstant(argNameStartTime, entry.StartTime)
	if err != nil {
		return api.StrengthSet{}, err
	}
	if err := validateSetMeasurements(entry); err != nil {
		return api.StrengthSet{}, err
	}
	if err := validateExerciseNaming(catalog, entry.Category, entry.ExerciseName); err != nil {
		return api.StrengthSet{}, err
	}

	return api.StrengthSet{
		Kind:            kind,
		Start:           start,
		DurationSeconds: entry.DurationSeconds,
		Repetitions:     entry.Repetitions,
		WeightGrams:     entry.WeightGrams,
		Category:        entry.Category,
		ExerciseName:    entry.ExerciseName,
	}, nil
}

// parseSetKind validates the set kind against the two values Garmin records.
func parseSetKind(value string) (api.SetKind, error) {
	switch api.SetKind(value) {
	case api.SetActive, api.SetRest:
		return api.SetKind(value), nil
	default:
		return "", invalidArgument("kind must be ACTIVE or REST")
	}
}

// validateSetMeasurements checks one set's numeric arguments.
func validateSetMeasurements(entry exerciseSetInput) error {
	if err := inRange(argNameDurationSeconds,
		entry.DurationSeconds, 0, api.MaxSetDurationSeconds); err != nil {
		return err
	}
	if err := inRange(argNameRepetitions, float64(entry.Repetitions), 0, maxRepetitions); err != nil {
		return err
	}
	return inRange(argNameWeightGrams, entry.WeightGrams, 0, maxWeightGrams)
}

// validateExerciseNaming validates a named category against the closed catalog in
// force, which is the fetched one when the start-up read succeeded.
func validateExerciseNaming(catalog *api.ExerciseCatalog, category, name string) error {
	if category == "" {
		return nil
	}
	if len(category) > maxExerciseKeyLen || len(name) > maxExerciseKeyLen {
		return invalidArgument("category and exercise_name must be short Garmin keys")
	}
	if err := catalog.Validate(category, name); err != nil {
		return invalidArgument(
			"category must be a Garmin exercise category from get_exercise_types")
	}
	return nil
}
