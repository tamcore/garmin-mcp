package api

import (
	"context"
	"fmt"
	"math"
	"net/http"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// StrengthWrites replaces the set list of a strength activity, and creates a
// completed strength activity with its sets already attached.
//
// Source: set_activity_exercise_sets in python-garminconnect 0.3.10, which PUTs
// the whole exerciseSets document with replace-all semantics, plus the two
// unmerged upstream proposals this project treats as in scope:
// set_activity_strength_exercise_sets and create_strength_training_activity.
//
// Both writes verify. A replace that reports success without reading the saved
// list back cannot tell a stored set list from one Garmin quietly rejected or
// reshaped, and that is precisely the failure the upstream proposals describe.
// Every method here therefore re-reads what it wrote and reports a mismatch as
// an error.
type StrengthWrites struct {
	req     requester
	details *ActivityDetails
	writes  *ActivityWrites
}

// NewStrengthWrites returns a strength write client over the request layer.
func NewStrengthWrites(rc *client.Client) (*StrengthWrites, error) {
	req, err := newRequester(rc)
	if err != nil {
		return nil, err
	}
	details, err := NewActivityDetails(rc)
	if err != nil {
		return nil, err
	}
	writes, err := NewActivityWrites(rc)
	if err != nil {
		return nil, err
	}
	return &StrengthWrites{req: req, details: details, writes: writes}, nil
}

// StrengthActivityTypeKey is the activity type a strength session is recorded
// under.
const StrengthActivityTypeKey = "strength_training"

// setBody is one set as Garmin stores it. Source: the response shape of
// get_activity_exercise_sets, which set_activity_exercise_sets mirrors.
type setBody struct {
	SetType         string         `json:"setType"`
	StartTime       string         `json:"startTime"`
	Duration        float64        `json:"duration"`
	RepetitionCount *int           `json:"repetitionCount,omitempty"`
	Weight          *float64       `json:"weight,omitempty"`
	Exercises       []exerciseBody `json:"exercises,omitempty"`
}

// exerciseBody is the recognized exercise of one set. Garmin accepts a null name
// under a known category, which is why the name is a pointer.
type exerciseBody struct {
	Category string  `json:"category"`
	Name     *string `json:"name"`
}

// renderSets turns validated sets into the replace-all document.
func renderSets(sets []StrengthSet) any {
	rendered := make([]setBody, 0, len(sets))
	for _, set := range sets {
		rendered = append(rendered, renderSet(set))
	}
	return struct {
		Sets []setBody `json:"exerciseSets"`
	}{Sets: rendered}
}

// renderSet renders one set.
func renderSet(set StrengthSet) setBody {
	body := setBody{
		SetType:   string(set.Kind),
		StartTime: set.Start.UTC().Format(SetStartTimeLayout),
		Duration:  set.DurationSeconds,
	}
	if set.Kind == SetRest {
		return body
	}

	repetitions := set.Repetitions
	weight := set.WeightGrams
	body.RepetitionCount = &repetitions
	body.Weight = &weight

	exercise := exerciseBody{Category: set.Category}
	if set.ExerciseName != "" {
		name := set.ExerciseName
		exercise.Name = &name
	}
	body.Exercises = []exerciseBody{exercise}
	return body
}

// ReplaceSets replaces the complete set list of one strength activity and
// verifies what was saved.
//
// Replace-all with an absolute list is idempotent — the same list PUT twice
// converges on the same activity — so the write carries
// client.EffectIdempotentWrite. The verification read is a separate request and
// is never skipped: a write Garmin accepted but reshaped is reported as a
// failure, not as success.
func (s *StrengthWrites) ReplaceSets(
	ctx context.Context, session client.Session, id client.ID, sets []StrengthSet,
) (ExerciseSets, error) {
	req := writeRequest(client.OpSetActivityExerciseSets, client.EndpointActivityExerciseSet,
		http.MethodPut, activitySegmentPath(id, client.SegmentExerciseSets),
		client.EffectIdempotentWrite)
	if err := requireID(req, id); err != nil {
		return ExerciseSets{}, err
	}

	valid, err := validateSets(req, sets)
	if err != nil {
		return ExerciseSets{}, err
	}
	body, err := jsonBody(req, renderSets(valid))
	if err != nil {
		return ExerciseSets{}, err
	}
	req.Body = body

	if _, err := s.req.write(ctx, session, req, nil); err != nil {
		return ExerciseSets{}, err
	}
	return s.verifySets(ctx, session, req, id, valid)
}

// validateSets validates and bounds a caller-supplied set list.
func validateSets(req client.Request, sets []StrengthSet) ([]StrengthSet, error) {
	switch {
	case len(sets) == 0:
		return nil, invalid(req, fmt.Errorf("%w: a set list needs at least one set",
			client.ErrValidation))
	case len(sets) > MaxStrengthSets:
		return nil, invalid(req, fmt.Errorf("%w: a set list may not exceed %d sets",
			client.ErrValidation, MaxStrengthSets))
	}

	valid := make([]StrengthSet, 0, len(sets))
	for _, set := range sets {
		checked, err := set.validate()
		if err != nil {
			return nil, invalid(req, err)
		}
		valid = append(valid, checked)
	}
	return valid, nil
}

// verifySets reads the saved list back and compares it with what was written.
func (s *StrengthWrites) verifySets(
	ctx context.Context, session client.Session, req client.Request,
	id client.ID, written []StrengthSet,
) (ExerciseSets, error) {
	saved, err := s.details.ExerciseSets(ctx, session, id)
	if err != nil {
		return ExerciseSets{}, err
	}
	if err := compareSets(written, saved.Sets.Items()); err != nil {
		return ExerciseSets{}, mismatch(req, err)
	}
	return saved, nil
}

// weightTolerance is the difference two encodings of the same weight may show.
// Garmin round-trips grams through a float, so an exact comparison would report
// a mismatch that is not one.
const weightTolerance = 0.5

// compareSets reports whether the saved list is the list that was written. A
// message names the position and the field, never the value: a repetition count
// and a weight are health data.
func compareSets(written []StrengthSet, saved []ExerciseSet) error {
	if len(written) != len(saved) {
		return fmt.Errorf("%w: the saved set list has %d sets, %d were written",
			client.ErrMalformedPayload, len(saved), len(written))
	}
	for index, want := range written {
		if err := compareSet(want, saved[index]); err != nil {
			return fmt.Errorf("set %d: %w", index+1, err)
		}
	}
	return nil
}

// compareSet compares one written set with its saved counterpart.
func compareSet(want StrengthSet, got ExerciseSet) error {
	if kind, _ := got.SetType.Value(); kind != string(want.Kind) {
		return fmt.Errorf("%w: the saved set type differs from the written one",
			client.ErrMalformedPayload)
	}
	if want.Kind == SetRest {
		return nil
	}
	if reps, _ := got.RepetitionCount.Int64(); reps != int64(want.Repetitions) {
		return fmt.Errorf("%w: the saved repetition count differs from the written one",
			client.ErrMalformedPayload)
	}
	if weight, _ := got.Weight.Float64(); math.Abs(weight-want.WeightGrams) > weightTolerance {
		return fmt.Errorf("%w: the saved weight differs from the written one",
			client.ErrMalformedPayload)
	}
	return compareExercise(want, got)
}

// compareExercise compares the recognized exercise of one set.
func compareExercise(want StrengthSet, got ExerciseSet) error {
	exercises := got.Exercises.Items()
	if len(exercises) == 0 {
		return fmt.Errorf("%w: the saved set carries no exercise", client.ErrMalformedPayload)
	}

	category, _ := exercises[0].Category.Value()
	if normalizeExerciseKey(category) != want.Category {
		return fmt.Errorf("%w: the saved exercise category differs from the written one",
			client.ErrMalformedPayload)
	}
	if want.ExerciseName == "" {
		return nil
	}
	name, present := exercises[0].Name.Value()
	if !present || normalizeExerciseKey(name) != want.ExerciseName {
		return fmt.Errorf("%w: the saved exercise name differs from the written one",
			client.ErrMalformedPayload)
	}
	return nil
}

// StrengthActivity is the strict request model for a completed strength session.
type StrengthActivity struct {
	// Name is the activity title. It may be empty.
	Name string
	// StartLocal is the local start timestamp in StartTimeLayout form, which is
	// what the activity record stores.
	StartLocal string
	// TimeZone is the IANA timezone of the session.
	TimeZone string
	// Plan describes the sets. Its Start is the absolute instant the sets are
	// timed from, which is why it is separate from StartLocal.
	Plan SetPlan
}

// CreatedStrengthActivity is the verified outcome of a strength create.
type CreatedStrengthActivity struct {
	// Activity is the identifier Garmin assigned.
	Activity client.ID
	// Sets is the set list read back from Garmin after it was written.
	Sets ExerciseSets
}

// Create creates a completed private strength activity, attaches its sets and
// verifies the saved result.
//
// The create itself is EffectUnsafeWrite and is never retried: a repeat would
// leave a second activity behind. The set write that follows is idempotent, and
// both the identifier and the set list are read back before success is reported.
// An activity Garmin kept but whose sets it did not store as written is reported
// as an error, so a caller repairs or removes it rather than believing the
// session was recorded.
func (s *StrengthWrites) Create(
	ctx context.Context, session client.Session, activity StrengthActivity,
) (CreatedStrengthActivity, error) {
	req := writeRequest(client.OpCreateStrengthActivity, client.EndpointActivity,
		http.MethodPost, client.PathActivityPrefix, client.EffectUnsafeWrite)

	sets, err := activity.Plan.Build()
	if err != nil {
		return CreatedStrengthActivity{}, invalid(req, err)
	}
	created, err := s.writes.CreateManual(ctx, session, ManualActivity{
		TypeKey:         StrengthActivityTypeKey,
		StartLocal:      activity.StartLocal,
		TimeZone:        activity.TimeZone,
		Name:            activity.Name,
		DurationSeconds: totalSeconds(sets),
	})
	if err != nil {
		return CreatedStrengthActivity{}, err
	}

	id, err := created.ID()
	if err != nil {
		return CreatedStrengthActivity{}, mismatch(req, err)
	}
	saved, err := s.ReplaceSets(ctx, session, id, sets)
	if err != nil {
		return CreatedStrengthActivity{}, err
	}
	if err := s.verifyActivity(ctx, session, req, id); err != nil {
		return CreatedStrengthActivity{}, err
	}
	return CreatedStrengthActivity{Activity: id, Sets: saved}, nil
}

// verifyActivity reads the created activity back and checks that the identifier
// Garmin reported is the one it stored.
func (s *StrengthWrites) verifyActivity(
	ctx context.Context, session client.Session, req client.Request, id client.ID,
) error {
	summary, err := s.details.Summary(ctx, session, id)
	if err != nil {
		return err
	}
	stored, ok := summary.ActivityID.Int64()
	if !ok || stored != id.Int64() {
		return mismatch(req, fmt.Errorf(
			"%w: the created activity does not report the identifier it was created with",
			client.ErrMalformedPayload))
	}
	return nil
}

// totalSeconds is the elapsed duration of a set list, rounded up to whole
// seconds, which is what the activity record stores.
func totalSeconds(sets []StrengthSet) int {
	var total float64
	for _, set := range sets {
		total += set.DurationSeconds
	}
	return int(math.Ceil(total))
}
