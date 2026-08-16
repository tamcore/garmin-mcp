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

// ToolCreateStrengthTrainingActivity is the name of the strength-session create. It
// has no entry in the pinned manifest: it comes from an unmerged upstream proposal
// and is an addition.
const ToolCreateStrengthTrainingActivity = "create_strength_training_activity"

// plannedSetInput is one planned set of a created strength session.
type plannedSetInput struct {
	Kind            string   `json:"kind" jsonschema:"the set kind, ACTIVE or REST"`
	Repeat          int      `json:"repeat" jsonschema:"how many times this set occurs"`
	Repetitions     int      `json:"repetitions" jsonschema:"the repetition count of an active set"`
	WeightGrams     float64  `json:"weight_grams" jsonschema:"the external weight in grams"`
	DurationSeconds float64  `json:"duration_seconds" jsonschema:"how long one occurrence lasts"`
	RestSeconds     float64  `json:"rest_seconds" jsonschema:"a rest inserted after each occurrence"`
	Category        string   `json:"category" jsonschema:"Garmin's exercise category"`
	ExerciseName    string   `json:"exercise_name" jsonschema:"Garmin's exercise name"`
	OffsetSeconds   *float64 `json:"offset_seconds" jsonschema:"seconds after the session start"`
	StartTime       string   `json:"start_time" jsonschema:"an absolute start instant, RFC 3339"`
}

// createStrengthTrainingActivityInput is the strength-session create argument set.
type createStrengthTrainingActivityInput struct {
	Name      string            `json:"name" jsonschema:"the activity title"`
	StartTime string            `json:"start_time" jsonschema:"the absolute session start, RFC 3339"`
	TimeZone  string            `json:"time_zone" jsonschema:"the IANA timezone of the session"`
	Sets      []plannedSetInput `json:"sets" jsonschema:"the planned sets, in order"`
}

// A CreatedStrengthActivityResult reports the created session and its saved sets.
type CreatedStrengthActivityResult struct {
	ActivityID int64           `json:"activity_id" jsonschema:"the identifier Garmin assigned"`
	Sets       ExerciseSetList `json:"sets" jsonschema:"the set list read back from Garmin"`
}

// LogValue reports that a session was created, never what was created.
func (r CreatedStrengthActivityResult) LogValue() slog.Value {
	return shape("createdStrengthActivity", slog.Int(argNameSets, r.Sets.Count))
}

func createStrengthTrainingActivityContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolCreateStrengthTrainingActivity,
			Title: "Create a strength training activity",
			Description: "create one completed strength session with its sets already " +
				"attached, and read the saved sets back to verify them. A set follows the " +
				"one before it unless it names an offset from the session start or an " +
				"absolute instant, and an absolute instant wins. It creates a new activity " +
				"every time it is called",
			Tier:        policy.TierWrite,
			Category:    categoryHealth,
			Annotations: writeAnnotations(false),
		},
		Schema: NewSchema(
			builderName(),
			Property{
				Name:        argNameStartTime,
				Types:       []string{typeString},
				Description: "the absolute session start, RFC 3339",
				Format:      formatDateTime,
				MaxLength:   new(maxInstantLen),
				Required:    true,
			},
			Property{
				Name:        "time_zone",
				Types:       []string{typeString},
				Description: `the IANA timezone of the session, for example "Europe/Paris"`,
				MaxLength:   new(maxTimeZoneArgumentLen),
				Default:     defaultManualTimeZone,
			},
			Property{
				Name:        argNameSets,
				Types:       []string{typeArray},
				Description: "the planned sets, in order",
				Items:       plannedSetSchema(),
				MinItems:    new(1),
				MaxItems:    new(maxStrengthSets),
				Required:    true,
			},
		),
	}
}

func registerCreateStrengthTrainingActivity(registry *mcpserver.Registry, svc *service) error {
	handler := func(
		ctx context.Context, _ *mcp.CallToolRequest, in createStrengthTrainingActivityInput,
	) (*mcp.CallToolResult, CreatedStrengthActivityResult, error) {
		activity, err := buildStrengthActivity(in, svc.bounds.MaxExerciseSets, svc.catalog)
		if err != nil {
			return nil, CreatedStrengthActivityResult{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, CreatedStrengthActivityResult{}, err
		}

		created, err := svc.strength.Create(ctx, session, activity)
		if err != nil {
			return nil, CreatedStrengthActivityResult{}, fail(err)
		}
		return nil, CreatedStrengthActivityResult{
			ActivityID: created.Activity.Int64(),
			Sets: newExerciseSetList(created.Activity.Int64(),
				created.Sets.Sets.Items(), svc.bounds.MaxExerciseSets),
		}, nil
	}
	return mcpserver.AddTool(
		registry, createStrengthTrainingActivityContract().Registration(), handler)
}

// buildStrengthActivity validates every argument and composes the request model.
func buildStrengthActivity(
	in createStrengthTrainingActivityInput, limit int, catalog *api.ExerciseCatalog,
) (api.StrengthActivity, error) {
	name, err := parseRequiredText(argNameName, in.Name, maxNameArgumentLen)
	if err != nil {
		return api.StrengthActivity{}, err
	}
	start, err := parseInstant(argNameStartTime, in.StartTime)
	if err != nil {
		return api.StrengthActivity{}, err
	}
	zone, location, err := parseTimeZone(
		"time_zone", optionalTextArg(in.TimeZone, defaultManualTimeZone))
	if err != nil {
		return api.StrengthActivity{}, err
	}
	sets, err := parsePlannedSets(in.Sets, limit, catalog)
	if err != nil {
		return api.StrengthActivity{}, err
	}

	return api.StrengthActivity{
		Name:       name,
		StartLocal: start.In(location).Format(api.StartTimeLayout),
		TimeZone:   zone,
		Plan:       api.SetPlan{Start: start, Sets: sets},
	}, nil
}

// parsePlannedSets validates the whole plan before any of it is dispatched.
func parsePlannedSets(
	entries []plannedSetInput, limit int, catalog *api.ExerciseCatalog,
) ([]api.PlannedSet, error) {
	if err := boundedCount(argNameSets, len(entries), min(limit, maxStrengthSets)); err != nil {
		return nil, err
	}
	out := make([]api.PlannedSet, 0, len(entries))
	for _, entry := range entries {
		set, err := parsePlannedSet(entry, catalog)
		if err != nil {
			return nil, err
		}
		out = append(out, set)
	}
	return out, nil
}

// parsePlannedSet validates one planned set and its placement in time.
func parsePlannedSet(
	entry plannedSetInput, catalog *api.ExerciseCatalog,
) (api.PlannedSet, error) {
	kind, err := parseSetKind(optionalTextArg(entry.Kind, string(api.SetActive)))
	if err != nil {
		return api.PlannedSet{}, err
	}
	if err := validatePlannedMeasurements(entry); err != nil {
		return api.PlannedSet{}, err
	}
	if err := validateExerciseNaming(catalog, entry.Category, entry.ExerciseName); err != nil {
		return api.PlannedSet{}, err
	}
	start, err := plannedStart(entry.StartTime)
	if err != nil {
		return api.PlannedSet{}, err
	}

	return api.PlannedSet{
		Kind:            kind,
		Repeat:          entry.Repeat,
		Repetitions:     entry.Repetitions,
		WeightGrams:     entry.WeightGrams,
		DurationSeconds: entry.DurationSeconds,
		RestSeconds:     entry.RestSeconds,
		Category:        entry.Category,
		ExerciseName:    entry.ExerciseName,
		OffsetSeconds:   entry.OffsetSeconds,
		StartTime:       start,
	}, nil
}

// plannedStart validates the optional absolute placement of one set.
func plannedStart(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	start, err := parseInstant(argNameStartTime, value)
	if err != nil {
		return nil, err
	}
	return &start, nil
}

// validatePlannedMeasurements checks one planned set's numeric arguments.
func validatePlannedMeasurements(entry plannedSetInput) error {
	checks := []struct {
		field     string
		value     float64
		low, high float64
	}{
		{"repeat", float64(entry.Repeat), 0, api.MaxSetRepeat},
		{argNameRepetitions, float64(entry.Repetitions), 0, maxRepetitions},
		{argNameWeightGrams, entry.WeightGrams, 0, maxWeightGrams},
		{argNameDurationSeconds, entry.DurationSeconds, 0, api.MaxSetDurationSeconds},
		{argNameRestSeconds, entry.RestSeconds, 0, api.MaxSetDurationSeconds},
	}
	for _, check := range checks {
		if err := inRange(check.field, check.value, check.low, check.high); err != nil {
			return err
		}
	}
	if entry.OffsetSeconds != nil {
		return inRange("offset_seconds", *entry.OffsetSeconds, 0, api.MaxSetDurationSeconds)
	}
	return nil
}

// plannedSetSchema declares one planned set.
func plannedSetSchema() map[string]any {
	return map[string]any{
		keyType:                 typeObject,
		keyProperties:           plannedSetProperties(),
		keyRequired:             []any{argNameDurationSeconds},
		keyAdditionalProperties: false,
	}
}

// plannedSetProperties declares the fields of one planned set.
func plannedSetProperties() map[string]any {
	return map[string]any{
		argNameKind: map[string]any{
			keyType: typeString, "enum": setKindEnum(), "default": string(api.SetActive),
			keyDescription: "the set kind",
		},
		"repeat": map[string]any{
			keyType: typeInteger, keyMinimum: 0, keyMaximum: api.MaxSetRepeat,
			keyDescription: "how many times this set occurs; zero means once",
		},
		argNameRepetitions: map[string]any{
			keyType: typeInteger, keyMinimum: 0, keyMaximum: maxRepetitions,
			keyDescription: "the repetition count of an active set",
		},
		argNameWeightGrams: map[string]any{
			keyType: typeNumber, keyMinimum: 0, keyMaximum: maxWeightGrams,
			keyDescription: "the external weight in grams",
		},
		argNameDurationSeconds: map[string]any{
			keyType: typeNumber, keyMinimum: 0, keyMaximum: api.MaxSetDurationSeconds,
			keyDescription: "how long one occurrence lasts",
		},
		argNameRestSeconds: map[string]any{
			keyType: typeNumber, keyMinimum: 0, keyMaximum: api.MaxSetDurationSeconds,
			keyDescription: "a rest inserted after each occurrence; zero inserts none",
		},
		argNameCategory: map[string]any{
			keyType: typeString, keyMaxLength: maxExerciseKeyLen,
			keyDescription: descExerciseCategory,
		},
		argNameExerciseName: map[string]any{
			keyType: typeString, keyMaxLength: maxExerciseKeyLen,
			keyDescription: "Garmin's exercise name",
		},
		"offset_seconds": map[string]any{
			keyType: typeNumber, keyMinimum: 0, keyMaximum: api.MaxSetDurationSeconds,
			keyDescription: "place the first occurrence this many seconds after the start",
		},
		argNameStartTime: map[string]any{
			keyType: typeString, keyFormat: formatDateTime, keyMaxLength: maxInstantLen,
			keyDescription: "place the first occurrence at this absolute instant",
		},
	}
}
