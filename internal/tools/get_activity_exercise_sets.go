package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetActivityExerciseSets is the upstream compatibility name of the strength-set
// tool.
const ToolGetActivityExerciseSets = "get_activity_exercise_sets"

// Exercise is one recognized movement inside a strength set. Garmin sends a null name
// under a known category, which is a valid state rather than an error.
type Exercise struct {
	Category    *string  `json:"category,omitempty" jsonschema:"the exercise category"`
	Name        *string  `json:"name,omitempty" jsonschema:"the recognized exercise name"`
	Probability *float64 `json:"probability,omitempty" jsonschema:"Garmin's confidence in the recognition"`
}

// ExerciseSet is one strength set. Repetitions and weights are health data.
type ExerciseSet struct {
	SetType      *string    `json:"set_type,omitempty" jsonschema:"the set type, ACTIVE or REST"`
	StartTime    *string    `json:"start_time,omitempty" jsonschema:"when the set started"`
	DurationSecs *float64   `json:"duration_seconds,omitempty" jsonschema:"how long the set lasted"`
	Repetitions  *int       `json:"repetitions,omitempty" jsonschema:"the repetition count"`
	Weight       *float64   `json:"weight,omitempty" jsonschema:"the weight in grams, as Garmin records it"`
	MessageIndex *int       `json:"message_index,omitempty" jsonschema:"the set's ordinal in the activity"`
	Exercises    []Exercise `json:"exercises,omitempty" jsonschema:"the movements recognized in the set"`
}

// ExerciseSetList is the strength-set collection, bounded.
//
// The sets arrive as an array or as a single object; the domain client's union
// decoder folds both, so this result is always an array.
type ExerciseSetList struct {
	ActivityID int64         `json:"activity_id" jsonschema:"the activity these sets belong to"`
	Sets       []ExerciseSet `json:"sets" jsonschema:"the strength sets, in order"`
	Count      int           `json:"count" jsonschema:"how many sets this result carries"`
	Truncated  bool          `json:"truncated" jsonschema:"whether the list was cut at this server's bound"`
}

// LogValue reports the set count, never a set.
func (l ExerciseSetList) LogValue() slog.Value {
	return shape("exerciseSetList",
		slog.Int("sets", len(l.Sets)),
		slog.Bool("truncated", l.Truncated),
	)
}

func getActivityExerciseSetsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetActivityExerciseSets,
			Title: "Get activity exercise sets",
			Description: "read one strength activity's sets: type, duration, repetitions, " +
				"weight and the movements Garmin recognized. An activity that is not a " +
				"strength session returns an empty list",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(activityIDProperty()),
	}
}

// registerGetActivityExerciseSets registers the tool.
func registerGetActivityExerciseSets(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in activityIDInput) (
		*mcp.CallToolResult, ExerciseSetList, error,
	) {
		id, err := parseActivityIdentifier(in.ActivityID)
		if err != nil {
			return nil, ExerciseSetList{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, ExerciseSetList{}, err
		}

		sets, err := svc.details.ExerciseSets(ctx, session, id)
		if err != nil {
			return nil, ExerciseSetList{}, fail(err)
		}
		bounded := newExerciseSetList(id.Int64(), sets.Sets.Items(), svc.bounds.MaxExerciseSets)
		return nil, bounded, nil
	}
	return mcpserver.AddTool(registry, getActivityExerciseSetsContract().Registration(), handler)
}

// newExerciseSetList maps the domain models onto the bounded result.
func newExerciseSetList(activityID int64, sets []api.ExerciseSet, limit int) ExerciseSetList {
	truncated := len(sets) > limit
	if truncated {
		sets = sets[:limit]
	}

	out := make([]ExerciseSet, 0, len(sets))
	for _, set := range sets {
		out = append(out, ExerciseSet{
			SetType:      optionalText(set.SetType),
			StartTime:    set.StartTime,
			DurationSecs: optionalFloat(set.Duration),
			Repetitions:  optionalInt(set.RepetitionCount),
			Weight:       optionalFloat(set.Weight),
			MessageIndex: optionalInt(set.MessageIndex),
			Exercises:    newExercises(set.Exercises.Items()),
		})
	}
	return ExerciseSetList{
		ActivityID: activityID,
		Sets:       out,
		Count:      len(out),
		Truncated:  truncated,
	}
}

func newExercises(exercises []api.Exercise) []Exercise {
	out := make([]Exercise, 0, len(exercises))
	for _, exercise := range exercises {
		out = append(out, Exercise{
			Category:    optionalText(exercise.Category),
			Name:        optionalText(exercise.Name),
			Probability: optionalFloat(exercise.Probability),
		})
	}
	return out
}
