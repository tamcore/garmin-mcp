package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// The names of the strength catalog read and the strength workout builder.
//
// get_exercise_types has no entry in the pinned manifest: it comes from an unmerged
// upstream proposal and is an addition. create_strength_workout is a compatibility
// name from the manifest.
const (
	ToolGetExerciseTypes      = "get_exercise_types"
	ToolCreateStrengthWorkout = "create_strength_workout"
)

// An ExerciseCategory is one strength category and the exercises listed under it.
type ExerciseCategory struct {
	Category    string         `json:"category" jsonschema:"Garmin's category key, e.g. BENCH_PRESS"`
	DisplayName string         `json:"display_name" jsonschema:"the human-readable label"`
	Count       int            `json:"count" jsonschema:"how many exercises this catalog lists"`
	Exercises   []ExerciseType `json:"exercises" jsonschema:"the listed exercises, ordered by name"`
}

// An ExerciseType is one exercise key with the label a user reads.
type ExerciseType struct {
	Name        string `json:"name" jsonschema:"Garmin's exercise key, e.g. BARBELL_BENCH_PRESS"`
	DisplayName string `json:"display_name" jsonschema:"the human-readable label"`
}

// An ExerciseCatalog is the whole strength catalog this server validates against.
type ExerciseCatalog struct {
	Categories []ExerciseCategory `json:"categories" jsonschema:"the categories, ordered by key"`
	Count      int                `json:"count" jsonschema:"how many categories this result carries"`
}

// LogValue reports the catalog size, never a movement.
func (c ExerciseCatalog) LogValue() slog.Value {
	return shape("exerciseCatalog", slog.Int("categories", len(c.Categories)))
}

func getExerciseTypesContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetExerciseTypes,
			Title: "Get the strength exercise catalog",
			Description: "read the strength categories and exercise keys this server accepts. " +
				"The category set is closed and an unknown category is refused before any " +
				"write; an exercise name this catalog does not list is still accepted, " +
				"because the catalog is a documented subset rather than a mirror",
			Tier:        policy.TierReadOnly,
			Category:    categoryOrdinary,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(),
	}
}

func registerGetExerciseTypes(registry *mcpserver.Registry, _ *service) error {
	handler := func(_ context.Context, _ *mcp.CallToolRequest, _ noArguments) (
		*mcp.CallToolResult, ExerciseCatalog, error,
	) {
		return nil, newExerciseCatalog(api.ExerciseTypes()), nil
	}
	return mcpserver.AddTool(registry, getExerciseTypesContract().Registration(), handler)
}

// newExerciseCatalog copies the compiled-in catalog into the result model.
func newExerciseCatalog(categories []api.ExerciseCategory) ExerciseCatalog {
	out := make([]ExerciseCategory, 0, len(categories))
	for _, category := range categories {
		exercises := make([]ExerciseType, 0, len(category.Exercises))
		for _, exercise := range category.Exercises {
			exercises = append(exercises, ExerciseType{
				Name: exercise.Name, DisplayName: exercise.DisplayName,
			})
		}
		out = append(out, ExerciseCategory{
			Category:    category.Category,
			DisplayName: category.DisplayName,
			Count:       category.Count,
			Exercises:   exercises,
		})
	}
	return ExerciseCatalog{Categories: out, Count: len(out)}
}

// strengthExercise is one exercise of a built strength workout.
type strengthExercise struct {
	Name        string `json:"name" jsonschema:"the exercise name, kept in the step description"`
	Sets        int    `json:"sets" jsonschema:"how many sets of this exercise"`
	Reps        int    `json:"reps" jsonschema:"how many repetitions per set"`
	RestSeconds int    `json:"rest_seconds" jsonschema:"the rest between sets in seconds"`
	Category    string `json:"category" jsonschema:"Garmin's exercise category"`
}

// createStrengthWorkoutInput is the strength builder argument set.
type createStrengthWorkoutInput struct {
	Name      string             `json:"name" jsonschema:"the workout name"`
	Exercises []strengthExercise `json:"exercises" jsonschema:"the exercises, in order"`
}

func createStrengthWorkoutContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolCreateStrengthWorkout,
			Title: "Create a strength workout",
			Description: "build a reps-based strength workout and upload it. A named category " +
				"is validated against the catalog first, because Garmin rejects an unknown " +
				"one outright. It creates a new workout every time it is called",
			Tier:        policy.TierWrite,
			Category:    categoryHealth,
			Annotations: writeAnnotations(false),
		},
		Schema: NewSchema(builderName(), Property{
			Name:        "exercises",
			Types:       []string{typeArray},
			Description: "the exercises, each with a name, sets, reps, rest and a category",
			Items:       strengthExerciseSchema(),
			MinItems:    new(1),
			MaxItems:    new(maxStrengthExercises),
			Required:    true,
		}),
	}
}

// strengthExerciseSchema declares one exercise entry.
func strengthExerciseSchema() map[string]any {
	return map[string]any{
		keyType: typeObject,
		keyProperties: map[string]any{
			argNameName: map[string]any{
				keyType: typeString, keyMaxLength: maxExerciseKeyLen,
				keyDescription: "the exercise name, kept in the step description",
			},
			argNameSets: map[string]any{
				keyType: typeInteger, keyMinimum: 1, keyMaximum: maxRepeats,
				keyDescription: "how many sets of this exercise",
			},
			argNameReps: map[string]any{
				keyType: typeInteger, keyMinimum: 1, keyMaximum: maxRepetitions,
				keyDescription: "how many repetitions per set",
			},
			argNameRestSeconds: map[string]any{
				keyType: typeInteger, keyMinimum: 0, keyMaximum: maxRestSeconds,
				keyDescription: "the rest between sets in seconds",
			},
			argNameCategory: map[string]any{
				keyType: typeString, keyMaxLength: maxExerciseKeyLen,
				keyDescription: descExerciseCategory,
			},
		},
		keyRequired:             []any{argNameName, argNameSets, argNameReps, argNameRestSeconds},
		keyAdditionalProperties: false,
	}
}

func registerCreateStrengthWorkout(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in createStrengthWorkoutInput) (
		*mcp.CallToolResult, SavedWorkoutResult, error,
	) {
		document, err := buildStrengthWorkout(in)
		if err != nil {
			return nil, SavedWorkoutResult{}, err
		}
		return svc.uploadBuilt(ctx, document)
	}
	return mcpserver.AddTool(registry, createStrengthWorkoutContract().Registration(), handler)
}

// buildStrengthWorkout composes the strength document.
func buildStrengthWorkout(in createStrengthWorkoutInput) (api.WorkoutDocument, error) {
	name, err := parseRequiredText(argNameName, in.Name, maxNameArgumentLen)
	if err != nil {
		return api.WorkoutDocument{}, err
	}
	if err := boundedCount("exercises", len(in.Exercises), maxStrengthExercises); err != nil {
		return api.WorkoutDocument{}, err
	}

	builder := newWorkoutBuilder(name, strengthSport())
	for _, exercise := range in.Exercises {
		if err := addStrengthExercise(builder, exercise); err != nil {
			return api.WorkoutDocument{}, err
		}
	}
	return builder.document()
}

// addStrengthExercise validates one exercise and appends its sets and rests.
func addStrengthExercise(builder *workoutBuilder, exercise strengthExercise) error {
	movement, err := parseRequiredText(argNameName, exercise.Name, maxExerciseKeyLen)
	if err != nil {
		return err
	}
	if err := validateStrengthCounts(exercise); err != nil {
		return err
	}
	if exercise.Category != "" {
		if err := api.ValidateExercise(exercise.Category, movement); err != nil {
			return invalidArgument(
				"category must be a Garmin exercise category from get_exercise_types")
		}
	}

	builder.addRepeat(exercise.Sets, func(nested *workoutBuilder) {
		nested.addStep(executableStep{
			StepType:          intervalStep(),
			EndCondition:      repsCondition(),
			EndConditionValue: float64(exercise.Reps),
			TargetType:        noTarget(),
			Description:       movement,
			Category:          exercise.Category,
			ExerciseName:      movement,
		})
		if exercise.RestSeconds > 0 {
			nested.addStep(timedStep(restStep(), float64(exercise.RestSeconds)))
		}
	})
	return nil
}

// validateStrengthCounts checks one exercise's numeric arguments.
func validateStrengthCounts(exercise strengthExercise) error {
	if err := inRange(argNameSets, float64(exercise.Sets), 1, maxRepeats); err != nil {
		return err
	}
	if err := inRange(argNameReps, float64(exercise.Reps), 1, maxRepetitions); err != nil {
		return err
	}
	return inRange(argNameRestSeconds, float64(exercise.RestSeconds), 0, maxRestSeconds)
}
