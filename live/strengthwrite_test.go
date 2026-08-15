//go:build garminlive

package live

import (
	"math"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/tools"
)

// The strength session the lifecycle records. Every figure is synthetic: nothing
// here is read from the account and nothing describes a real session.
const (
	strengthSetSeconds = 60.0
	strengthReps       = 10
	strengthWeight     = 20000.0
	strengthGapSeconds = 90

	// replacementReps is what the replace-all write changes the repetition count to,
	// so a write that silently kept the created list is caught rather than passed.
	replacementReps = 8
)

// strengthGramTolerance is the difference two encodings of the same weight may show.
// Garmin round-trips grams through a float, so an exact comparison would report a
// mismatch that is not one. It is the api package's own tolerance.
const strengthGramTolerance = 0.5

// strengthSecondTolerance is the difference two encodings of the same duration may
// show. The tool rounds for the wire, so the two may differ by less than a second.
const strengthSecondTolerance = 1.0

// Set field names in the tool result and in the arguments.
const (
	keySets         = "sets"
	keySetType      = "set_type"
	keyRepetitions  = "repetitions"
	keyWeight       = "weight"
	keyWeightGrams  = "weight_grams"
	keyDurationSecs = "duration_seconds"
	keyKind         = "kind"
	keyStartTime    = "start_time"
)

// setActive is the kind every set in this test carries.
const setActive = "ACTIVE"

// TestLiveStrengthActivityLifecycle creates a completed strength session, replaces
// its whole set list and compares the result position by position.
//
// Both api-layer writes verify their own results — Create reads the activity back
// and ReplaceSets compares the saved list with the written one — so a failure inside
// them is already a failure here. What this test adds is the outside view: the sets
// are re-read through the read-only tool afterwards and compared field by field, so
// a write the api layer accepted and Garmin later reshaped is caught too.
func TestLiveStrengthActivityLifecycle(t *testing.T) {
	w := liveWriteEnv(t)

	start := time.Now().UTC().AddDate(0, 0, -1).Truncate(time.Hour)
	planned := plannedSets(start)
	created := w.call(t, tools.ToolCreateStrengthTrainingActivity, map[string]any{
		keyName:      w.names.name(labelNameStrength),
		keyStartTime: start.Format(time.RFC3339),
		keyTimeZone:  manualTimeZone,
		keySets:      planned,
	})
	id := identifier(t, created, tools.ToolCreateStrengthTrainingActivity, argActivityID)
	w.keepClean(t, kindActivity, id)

	if !w.owned.owns(kindActivity, id) {
		t.Fatal("the write guard did not learn the created strength activity from Garmin's " +
			"own response, so the set write against it would be refused")
	}
	assertSetsMatch(t, tools.ToolCreateStrengthTrainingActivity, planned, created)

	replacement := replacementSets(start)
	saved := w.call(t, tools.ToolSetActivityStrengthExerciseSets, map[string]any{
		argActivityID: id, keySets: replacement,
	})
	assertSetsMatch(t, tools.ToolSetActivityStrengthExerciseSets, replacement, saved)

	reread := w.call(t, tools.ToolGetActivityExerciseSets, map[string]any{argActivityID: id})
	assertSetsMatch(t, tools.ToolGetActivityExerciseSets, replacement, reread)

	w.deleteViaTool(t, tools.ToolDeleteActivity, argActivityID, kindActivity, id,
		w.activityGone(t, id))
}

// The exercises the sets name.
//
// Garmin validates the category against a closed parent set and the name against its
// own sub-category enum, and the compiled-in catalog is a documented subset rather
// than a mirror: it lists names Garmin refuses. These two are checked against the
// live service, and the rest of the sets name a category with no exercise name,
// which Garmin accepts under any known parent. See docs/implementation-status.md for
// the catalog entries that are known not to survive a real write.
const (
	categoryBench = "BENCH_PRESS"
	exerciseBench = "BARBELL_BENCH_PRESS"
	categorySquat = "SQUAT"
	categoryRow   = "ROW"
)

// plannedSets is the set list the create sends. Both sets are absolutely placed, so
// the create and the replacement describe the same instants.
func plannedSets(start time.Time) []map[string]any {
	return []map[string]any{
		strengthSet(start, strengthReps, categoryBench, exerciseBench),
		strengthSet(start.Add(strengthGapSeconds*time.Second), strengthReps,
			categorySquat, ""),
	}
}

// replacementSets is the list the replace-all write sends. It differs from the
// created list in length, in repetition count and in exercise, so a write that did
// nothing at all cannot pass the comparison.
func replacementSets(start time.Time) []map[string]any {
	return []map[string]any{
		strengthSet(start, replacementReps, categoryBench, exerciseBench),
		strengthSet(start.Add(strengthGapSeconds*time.Second), replacementReps,
			categoryRow, ""),
		strengthSet(start.Add(2*strengthGapSeconds*time.Second), replacementReps,
			categorySquat, ""),
	}
}

// strengthSet builds one absolutely-timed set. An empty exercise name is omitted,
// which is the "category only" form Garmin accepts under any known parent.
func strengthSet(start time.Time, reps int, category, exercise string) map[string]any {
	set := map[string]any{
		keyKind:         setActive,
		keyStartTime:    start.Format(time.RFC3339),
		keyDurationSecs: strengthSetSeconds,
		keyRepetitions:  reps,
		keyWeightGrams:  strengthWeight,
		"category":      category,
	}
	if exercise != "" {
		set["exercise_name"] = exercise
	}
	return set
}

// assertSetsMatch compares a set list a tool returned with the list that was sent,
// position by position.
//
// Every value compared is one this suite wrote, so a mismatch names the field and
// the relative difference without disclosing anything of the account's.
func assertSetsMatch(t *testing.T, tool string, sent []map[string]any, result map[string]any) {
	t.Helper()

	got := savedSetList(result)
	if len(got) != len(sent) {
		t.Fatalf("%s reported %d sets and %d were written", tool, len(got), len(sent))
	}

	for index, want := range sent {
		saved, ok := got[index].(map[string]any)
		if !ok {
			t.Fatalf("%s returned a set at position %d that is not an object", tool, index)
		}
		assertSetMatches(t, tool, index, want, saved)
	}
}

// savedSetList reads the set array out of a result.
//
// Two shapes carry it. The replace-all write and the read return the set list as the
// result itself, so the array is at the top level. The strength create returns the
// created activity with the list nested under the same key, because the identifier it
// assigned belongs beside it. Both are accepted here rather than in each caller.
func savedSetList(result map[string]any) []any {
	switch nested := result[keySets].(type) {
	case []any:
		return nested
	case map[string]any:
		items, _ := nested[keySets].([]any)
		return items
	default:
		return nil
	}
}

// assertSetMatches compares one saved set with the one written at that position.
func assertSetMatches(t *testing.T, tool string, index int, want, saved map[string]any) {
	t.Helper()

	if kind, _ := saved[keySetType].(string); kind != want[keyKind] {
		t.Errorf("%s: the set at position %d came back with a different kind", tool, index)
	}
	if reps, _ := saved[keyRepetitions].(float64); int(reps) != want[keyRepetitions] {
		t.Errorf("%s: the repetition count of the set at position %d differs from the one "+
			"written", tool, index)
	}

	assertSetFigure(t, tool, index, "weight", saved[keyWeight],
		want[keyWeightGrams], strengthGramTolerance)
	assertSetFigure(t, tool, index, "duration", saved[keyDurationSecs],
		want[keyDurationSecs], strengthSecondTolerance)
}

// assertSetFigure compares one numeric field of one set against what was written.
func assertSetFigure(
	t *testing.T, tool string, index int, field string, got, want any, tolerance float64,
) {
	t.Helper()

	expected, _ := want.(float64)
	value, present := got.(float64)
	if !present {
		t.Errorf("%s: the set at position %d came back without the %s written",
			tool, index, field)
		return
	}
	if delta := math.Abs(value - expected); delta > tolerance {
		t.Errorf("%s: the %s of the set at position %d differs from the one written by %.3f%%",
			tool, field, index, 100*relative(delta, expected))
	}
}
