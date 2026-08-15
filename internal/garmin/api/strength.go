package api

import (
	"fmt"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// SetStartTimeLayout is the timestamp form Garmin's exerciseSets endpoint
// stores. It is always rendered in UTC, so a set never carries a local offset a
// reader would have to guess at.
const SetStartTimeLayout = "2006-01-02T15:04:05.0"

// Bounds on a strength set list. Each one exists because the whole list is
// caller-supplied and is written in a single replace-all PUT.
const (
	// MaxStrengthSets bounds how many sets one activity may carry.
	MaxStrengthSets = 500
	// MaxSetDurationSeconds bounds one set at four hours.
	MaxSetDurationSeconds = 4 * 60 * 60
	// MaxRepetitions bounds the repetitions of one set.
	MaxRepetitions = 1000
	// MaxWeightGrams bounds the external weight of one set at 1000 kg.
	MaxWeightGrams = 1_000_000
	// MaxSetRepeat bounds how many times one planned set may repeat.
	MaxSetRepeat = 100
)

// SetKind is the type of one strength set.
type SetKind string

// The two set types Garmin records. Source: the setType field of
// get_activity_exercise_sets.
const (
	SetActive SetKind = "ACTIVE"
	SetRest   SetKind = "REST"
)

// StrengthSet is one strength set, fully resolved: it carries an absolute start
// time and needs no context to be written.
//
// It is health data — repetitions and weights describe a person's body — so it
// is never logged.
type StrengthSet struct {
	// Kind is ACTIVE or REST.
	Kind SetKind
	// Start is the absolute start instant. It is written in UTC.
	Start time.Time
	// DurationSeconds is how long the set lasted.
	DurationSeconds float64
	// Repetitions is the repetition count of an active set.
	Repetitions int
	// WeightGrams is the external weight of an active set, in grams.
	WeightGrams float64
	// Category is the Garmin exercise category of an active set.
	Category string
	// ExerciseName is the Garmin exercise name. It may be empty, which Garmin
	// accepts under a known category.
	ExerciseName string
}

// validate reports whether one set may be written.
func (s StrengthSet) validate() (StrengthSet, error) {
	switch {
	case s.Kind != SetActive && s.Kind != SetRest:
		return StrengthSet{}, fmt.Errorf("%w: a set must be ACTIVE or REST",
			client.ErrValidation)
	case s.Start.IsZero():
		return StrengthSet{}, fmt.Errorf("%w: a set needs a start time",
			client.ErrValidation)
	case s.DurationSeconds <= 0 || s.DurationSeconds > MaxSetDurationSeconds:
		return StrengthSet{}, fmt.Errorf(
			"%w: set duration must be positive and under four hours", client.ErrValidation)
	}
	if s.Kind == SetRest {
		return s.validateRest()
	}
	return s.validateActive()
}

// validateRest refuses the fields a rest set must not carry, so a rest never
// silently records repetitions or a weight.
func (s StrengthSet) validateRest() (StrengthSet, error) {
	if s.Repetitions != 0 || s.WeightGrams != 0 || s.Category != "" || s.ExerciseName != "" {
		return StrengthSet{}, fmt.Errorf(
			"%w: a rest set carries no repetitions, weight, category or exercise",
			client.ErrValidation)
	}
	return StrengthSet{
		Kind: SetRest, Start: s.Start.UTC(), DurationSeconds: s.DurationSeconds,
	}, nil
}

// validateActive checks the repetitions, the weight and the exercise of an
// active set.
func (s StrengthSet) validateActive() (StrengthSet, error) {
	switch {
	case s.Repetitions < 0 || s.Repetitions > MaxRepetitions:
		return StrengthSet{}, fmt.Errorf("%w: repetitions must be between 0 and 1000",
			client.ErrValidation)
	case s.WeightGrams < 0 || s.WeightGrams > MaxWeightGrams:
		return StrengthSet{}, fmt.Errorf("%w: weight must be between 0 and 1000 kg",
			client.ErrValidation)
	}
	if err := ValidateExercise(s.Category, s.ExerciseName); err != nil {
		return StrengthSet{}, err
	}
	return StrengthSet{
		Kind:            SetActive,
		Start:           s.Start.UTC(),
		DurationSeconds: s.DurationSeconds,
		Repetitions:     s.Repetitions,
		WeightGrams:     s.WeightGrams,
		Category:        normalizeExerciseKey(s.Category),
		ExerciseName:    normalizeExerciseKey(s.ExerciseName),
	}, nil
}

// PlannedSet describes one set of a plan, before the start times are resolved.
//
// The three ways to place a set in time are exclusive by precedence: an absolute
// StartTime wins, then an OffsetSeconds from the plan start, and otherwise the
// set follows whatever came before it.
type PlannedSet struct {
	// Kind is ACTIVE or REST. The zero value is ACTIVE.
	Kind SetKind
	// Repeat is how many times this set occurs. Zero means once.
	Repeat int
	// Repetitions is the repetition count of an active set.
	Repetitions int
	// WeightGrams is the external weight of an active set, in grams.
	WeightGrams float64
	// DurationSeconds is how long one occurrence lasts.
	DurationSeconds float64
	// RestSeconds inserts a rest set after each occurrence. Zero inserts none.
	RestSeconds float64
	// Category is the Garmin exercise category of an active set.
	Category string
	// ExerciseName is the Garmin exercise name. It may be empty.
	ExerciseName string
	// OffsetSeconds places the first occurrence that many seconds after the plan
	// start instead of after the previous set.
	OffsetSeconds *float64
	// StartTime places the first occurrence at an absolute instant.
	StartTime *time.Time
}

// SetPlan turns the way a person describes a strength session — this exercise,
// so many sets, this much rest between them — into the flat, absolutely-timed
// list Garmin stores.
type SetPlan struct {
	// Start is the instant the session began.
	Start time.Time
	// Sets are the planned sets, in order.
	Sets []PlannedSet
}

// Build resolves the plan into the sets that will be written.
//
// The receiver is not modified and the result is freshly allocated. A plan that
// produces no set, or more than MaxStrengthSets, is refused rather than
// truncated: a truncated strength session is a wrong one.
func (p SetPlan) Build() ([]StrengthSet, error) {
	if p.Start.IsZero() {
		return nil, fmt.Errorf("%w: a set plan needs a start time", client.ErrValidation)
	}

	cursor := p.Start.UTC()
	built := make([]StrengthSet, 0, len(p.Sets))
	for _, planned := range p.Sets {
		expanded, next, err := expandPlanned(p.Start.UTC(), cursor, planned)
		if err != nil {
			return nil, err
		}
		built = append(built, expanded...)
		cursor = next
		if len(built) > MaxStrengthSets {
			return nil, fmt.Errorf("%w: a set plan may not exceed %d sets",
				client.ErrValidation, MaxStrengthSets)
		}
	}
	if len(built) == 0 {
		return nil, fmt.Errorf("%w: a set plan needs at least one set", client.ErrValidation)
	}
	return built, nil
}

// expandPlanned expands one planned set into its occurrences and reports where
// the next set starts.
func expandPlanned(
	planStart, cursor time.Time, planned PlannedSet,
) ([]StrengthSet, time.Time, error) {
	repeat, err := plannedRepeat(planned)
	if err != nil {
		return nil, cursor, err
	}
	at := plannedStart(planStart, cursor, planned)

	out := make([]StrengthSet, 0, repeat*2)
	for range repeat {
		set, err := plannedSet(planned, at).validate()
		if err != nil {
			return nil, cursor, err
		}
		out = append(out, set)
		at = at.Add(secondsToDuration(planned.DurationSeconds))

		if planned.RestSeconds > 0 {
			rest, restErr := StrengthSet{
				Kind: SetRest, Start: at, DurationSeconds: planned.RestSeconds,
			}.validate()
			if restErr != nil {
				return nil, cursor, restErr
			}
			out = append(out, rest)
			at = at.Add(secondsToDuration(planned.RestSeconds))
		}
	}
	return out, at, nil
}

// plannedRepeat resolves and bounds the repeat count.
func plannedRepeat(planned PlannedSet) (int, error) {
	repeat := planned.Repeat
	if repeat == 0 {
		repeat = 1
	}
	if repeat < 0 || repeat > MaxSetRepeat {
		return 0, fmt.Errorf("%w: a set may repeat between 1 and %d times",
			client.ErrValidation, MaxSetRepeat)
	}
	return repeat, nil
}

// plannedStart resolves where a planned set begins: an absolute time wins, then
// an offset from the plan start, then the running cursor.
func plannedStart(planStart, cursor time.Time, planned PlannedSet) time.Time {
	switch {
	case planned.StartTime != nil:
		return planned.StartTime.UTC()
	case planned.OffsetSeconds != nil:
		return planStart.Add(secondsToDuration(*planned.OffsetSeconds))
	default:
		return cursor
	}
}

// plannedSet renders one occurrence of a planned set at an instant.
func plannedSet(planned PlannedSet, at time.Time) StrengthSet {
	kind := planned.Kind
	if kind == "" {
		kind = SetActive
	}
	if kind == SetRest {
		return StrengthSet{Kind: SetRest, Start: at, DurationSeconds: planned.DurationSeconds}
	}
	return StrengthSet{
		Kind:            SetActive,
		Start:           at,
		DurationSeconds: planned.DurationSeconds,
		Repetitions:     planned.Repetitions,
		WeightGrams:     planned.WeightGrams,
		Category:        planned.Category,
		ExerciseName:    planned.ExerciseName,
	}
}

// secondsToDuration converts fractional seconds into a duration without losing
// the sub-second part a Garmin set can carry.
func secondsToDuration(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}
