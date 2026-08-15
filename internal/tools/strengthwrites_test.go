package tools_test

import (
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// savedSetsBody is the set list the fake reports back after a replace. It has to
// match what the tool wrote, because the API layer verifies the save set by set.
const savedSetsBody = `{"exerciseSets":[{"setType":"ACTIVE",` +
	`"startTime":"2026-01-31T06:12:00.0","duration":45.0,"repetitionCount":10,` +
	`"weight":40000.0,"exercises":[{"category":"BENCH_PRESS","name":"BARBELL_BENCH_PRESS"}]}]}`

func strengthWriteScript() testkit.Script {
	return testkit.NewScript().
		With(client.PathSocialProfile, repeat(okJSON(profileBody), 4)...).
		With(activityDetailPath(client.SegmentExerciseSets), repeat(okJSON(savedSetsBody), 3)...)
}

// oneSet is the single set the replace-all tests send.
func oneSet() map[string]any {
	return map[string]any{
		argKind:            setKindActive,
		argStartTime:       testSetStart,
		argDurationSeconds: 45,
		argRepetitions:     10,
		argWeightGrams:     40000,
		argCategory:        testCategory,
		argExerciseName:    testExerciseName,
	}
}

func TestSetActivityStrengthExerciseSetsWritesAndVerifiesTheList(t *testing.T) {
	h := newWriteHarness(t, strengthWriteScript(), enabledWrites())

	out := h.call(t, tools.ToolSetActivityStrengthExerciseSets, map[string]any{
		argActivityID: testActivityID,
		argSets:       []any{oneSet()},
	})

	if got := out["count"]; got != float64(1) {
		t.Errorf("count = %v, want the one saved set", got)
	}
	recorded := h.recordedMethods()
	wantRead := http.MethodGet + " " + activityDetailPath(client.SegmentExerciseSets)
	if len(recorded) < 2 || recorded[len(recorded)-1] != wantRead {
		t.Errorf("recorded %v, want the write followed by the verifying read", recorded)
	}
}

func TestSetActivityStrengthExerciseSetsRefusesAnEmptyList(t *testing.T) {
	h := newWriteHarness(t, strengthWriteScript(), enabledWrites())

	h.callError(t, tools.ToolSetActivityStrengthExerciseSets, map[string]any{
		argActivityID: testActivityID,
		argSets:       []any{},
	})

	if len(h.fake.Requests()) != 0 {
		t.Errorf("an empty replace-all list still reached Garmin: %v", h.recordedMethods())
	}
}

func TestSetActivityStrengthExerciseSetsRefusesAnUnknownCategory(t *testing.T) {
	h := newWriteHarness(t, strengthWriteScript(), enabledWrites())

	set := oneSet()
	set[argCategory] = "UNASSIGNED"
	h.callError(t, tools.ToolSetActivityStrengthExerciseSets, map[string]any{
		argActivityID: testActivityID,
		argSets:       []any{set},
	})

	if len(h.fake.Requests()) != 0 {
		t.Errorf("an unknown category still reached Garmin: %v", h.recordedMethods())
	}
}

func TestSetActivityStrengthExerciseSetsRefusesATimestampWithoutAnOffset(t *testing.T) {
	h := newWriteHarness(t, strengthWriteScript(), enabledWrites())

	set := oneSet()
	set[argStartTime] = "2026-01-31 06:12:00"
	h.callError(t, tools.ToolSetActivityStrengthExerciseSets, map[string]any{
		argActivityID: testActivityID,
		argSets:       []any{set},
	})

	if len(h.fake.Requests()) != 0 {
		t.Errorf("an ambiguous timestamp still reached Garmin: %v", h.recordedMethods())
	}
}

func strengthCreateScript() testkit.Script {
	return testkit.NewScript().
		With(client.PathSocialProfile, repeat(okJSON(profileBody), 4)...).
		With(client.PathActivityPrefix, okJSON(`{"activityId":987654321}`)).
		With(activityDetailPath(client.SegmentExerciseSets), repeat(okJSON(savedSetsBody), 3)...).
		With(activityWritePath(), okJSON(`{"activityId":987654321}`))
}

func TestCreateStrengthTrainingActivityCreatesAndVerifiesTheSession(t *testing.T) {
	h := newWriteHarness(t, strengthCreateScript(), enabledWrites())

	out := h.call(t, tools.ToolCreateStrengthTrainingActivity, map[string]any{
		argName:      "Push day",
		argStartTime: testSetStart,
		argSets: []any{map[string]any{
			argKind:            setKindActive,
			argDurationSeconds: 45,
			argRepetitions:     10,
			argWeightGrams:     40000,
			argCategory:        testCategory,
			argExerciseName:    testExerciseName,
		}},
	})

	if got := out["activity_id"]; got != float64(987654321) {
		t.Errorf("activity_id = %v, want the identifier Garmin assigned", got)
	}
	sets, _ := out["sets"].(map[string]any)
	if got := sets["count"]; got != float64(1) {
		t.Errorf("the verified set list has count = %v, want 1", got)
	}
}

func TestCreateStrengthTrainingActivityRefusesAnEmptyPlan(t *testing.T) {
	h := newWriteHarness(t, strengthCreateScript(), enabledWrites())

	h.callError(t, tools.ToolCreateStrengthTrainingActivity, map[string]any{
		argName:      "Empty",
		argStartTime: testSetStart,
		argSets:      []any{},
	})

	if len(h.fake.Requests()) != 0 {
		t.Errorf("an empty plan still reached Garmin: %v", h.recordedMethods())
	}
}
