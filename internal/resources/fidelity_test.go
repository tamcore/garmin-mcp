package resources

import (
	"encoding/json"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
)

// TestEveryTemplateIsAValidWorkoutDocument is the check that makes these templates
// worth publishing.
//
// A template exists to be edited and handed to upload_workout. If this server's own
// upload path would refuse it, the document is worse than absent: a caller follows
// the example and gets an error that looks like their mistake. Upstream publishes
// its templates without this check.
func TestEveryTemplateIsAValidWorkoutDocument(t *testing.T) {
	t.Parallel()

	for _, doc := range documents() {
		if doc.spec.URI == structureReferenceURI {
			continue // the reference is prose about workouts, not a workout
		}
		t.Run(doc.spec.URI, func(t *testing.T) {
			t.Parallel()

			parsed, err := api.ParseWorkoutDocument([]byte(doc.body()))
			if err != nil {
				t.Fatalf("upload_workout would refuse this template: %v", err)
			}
			if !parsed.IsObject() {
				t.Error("the template is not a JSON object, so update_workout would refuse it")
			}
		})
	}
}

// TestEveryTemplateCarriesTheEnvelopeGarminExpects checks the shape rather than the
// bytes: one segment, ordered steps, and a sport named at both levels.
//
// ParseWorkoutDocument deliberately accepts any well-formed JSON object, because a
// caller's document is carried verbatim. That tolerance is right for the upload path
// and useless as a check on a document this repository ships, so the envelope is
// asserted here instead.
func TestEveryTemplateCarriesTheEnvelopeGarminExpects(t *testing.T) {
	t.Parallel()

	for _, doc := range documents() {
		if doc.spec.URI == structureReferenceURI {
			continue
		}
		t.Run(doc.spec.URI, func(t *testing.T) {
			t.Parallel()

			var decoded map[string]any
			if err := json.Unmarshal([]byte(doc.body()), &decoded); err != nil {
				t.Fatalf("decoding: %v", err)
			}

			if name, _ := decoded["workoutName"].(string); name == "" {
				t.Error("the template has no workoutName")
			}
			if _, ok := decoded["sportType"].(map[string]any); !ok {
				t.Error("the template names no sportType")
			}

			segments, ok := decoded["workoutSegments"].([]any)
			if !ok || len(segments) != 1 {
				t.Fatalf("workoutSegments = %v, want exactly one segment", decoded["workoutSegments"])
			}
			segment, ok := segments[0].(map[string]any)
			if !ok {
				t.Fatal("the segment is not an object")
			}
			steps, ok := segment["workoutSteps"].([]any)
			if !ok || len(steps) == 0 {
				t.Fatal("the segment carries no steps")
			}
			assertStepsAreOrdered(t, steps)
		})
	}
}

// assertStepsAreOrdered checks that every step declares its kind and a stepOrder,
// and that the orders are unique across the whole document, nested groups included.
// Garmin orders the steps it executes by that number, so a repeat inside a workout
// must not restart it.
func assertStepsAreOrdered(t *testing.T, steps []any) {
	t.Helper()

	seen := map[float64]bool{}
	var walk func([]any)
	walk = func(list []any) {
		for _, entry := range list {
			step, ok := entry.(map[string]any)
			if !ok {
				t.Fatal("a step is not an object")
			}
			if kind, _ := step[fieldType].(string); kind != executableStepDTO && kind != repeatGroupDTO {
				t.Errorf("step declares type %q, want %s or %s",
					kind, executableStepDTO, repeatGroupDTO)
			}
			order, ok := step[fieldStepOrder].(float64)
			if !ok {
				t.Error("a step declares no stepOrder")
				continue
			}
			if seen[order] {
				t.Errorf("stepOrder %v is used twice; Garmin executes steps in that order", order)
			}
			seen[order] = true

			assertStepIsComplete(t, step)

			if nested, ok := step[fieldWorkoutSteps].([]any); ok {
				walk(nested)
			}
		}
	}
	walk(steps)

	// Garmin executes steps in stepOrder, so the orders must be the sequence 1..N
	// with nothing skipped. Uniqueness alone would accept 1,2,30,4,5.
	for order := 1; order <= len(seen); order++ {
		if !seen[float64(order)] {
			t.Errorf("stepOrder %d is missing; the orders must run 1..%d with no gap",
				order, len(seen))
		}
	}
}

// assertStepIsComplete checks the fields Garmin requires of the step kind.
//
// ParseWorkoutDocument accepts any well-formed JSON object, so without this a
// template could lose its end condition and every test here would still pass while
// Garmin rejected the upload.
func assertStepIsComplete(t *testing.T, step map[string]any) {
	t.Helper()

	kind, _ := step[fieldType].(string)
	if kind == repeatGroupDTO {
		if _, ok := step["numberOfIterations"].(float64); !ok {
			t.Error("a repeat group declares no numberOfIterations")
		}
		if _, ok := step[fieldEndCondition].(map[string]any); !ok {
			t.Error("a repeat group declares no endCondition")
		}
		if nested, ok := step[fieldWorkoutSteps].([]any); !ok || len(nested) == 0 {
			t.Error("a repeat group carries no steps")
		}
		return
	}

	for _, field := range []string{fieldStepType, fieldEndCondition, fieldTargetType} {
		if _, ok := step[field].(map[string]any); !ok {
			t.Errorf("an executable step declares no %s", field)
		}
	}
	if _, ok := step[fieldEndConditionValue].(float64); !ok {
		t.Error("an executable step declares no endConditionValue")
	}

	// A heart-rate target is the one target that needs a zone beside it.
	target, _ := step[fieldTargetType].(map[string]any)
	if key, _ := target[keyTarget].(string); key == zoneTargetKey {
		if _, ok := step["zoneNumber"].(float64); !ok {
			t.Error("a heart-rate-zone step declares no zoneNumber")
		}
	}
}
