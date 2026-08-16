//go:build garminlive

package live

import (
	"context"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
)

// minPublishedExercises is the floor this suite believes. The published catalog
// carried far more than this when the fetch was written, so a document that has
// shrunk past it is drift worth reporting rather than a catalog worth serving.
const minPublishedExercises = 500

// TestTheCatalogHelperFetchesNothingWithoutTheAcknowledgement is gate-free and
// network-free on purpose: it is the assertion that the gate is what decides
// whether this suite contacts Garmin's CDN at all.
//
// It runs in both states. Without the acknowledgement the helper must answer from
// the compiled-in subset, which it can only do by not fetching; with it, the
// helper is allowed to fetch and this test says nothing about the outcome.
func TestTheCatalogHelperFetchesNothingWithoutTheAcknowledgement(t *testing.T) {
	if gate() == "" {
		t.Skip("the acknowledgement is set, so the helper is allowed to fetch")
	}

	attempts := 0
	catalog := gatedExerciseCatalog(func(context.Context) *api.ExerciseCatalog {
		attempts++
		return api.BuiltinExerciseCatalog()
	})

	// The attempt is what is asserted, not the result. A refused or unreachable
	// network would make the result look right while the request still left the
	// process, which is exactly the failure this test exists to catch.
	if attempts != 0 {
		t.Errorf("the suite attempted %d catalog reads with the gate closed, want none: "+
			"a build tag alone must never contact the real service", attempts)
	}
	if source := catalog.Source(); source != api.CatalogSourceBuiltin {
		t.Errorf("Source() = %q with the gate closed, want the compiled-in subset", source)
	}
}

// TestPublishedExerciseCatalogStillAnswers is the drift detector for the one
// web-tier URL this project reads.
//
// It is the only test that contacts that URL: every other test of the fetch runs
// against a local server. It needs no account and writes nothing — it is here
// because this is the suite that is allowed to touch the real service, and
// because a silent fall back to the compiled-in subset in production would
// otherwise look exactly like success.
func TestPublishedExerciseCatalogStillAnswers(t *testing.T) {
	// The acknowledgement gates this read like every other request this suite
	// makes. Without it the test skips: a missing gate is a skip, never a failure,
	// and never a request.
	if skip := gate(); skip != "" {
		t.Skip(skip)
	}

	catalog := api.LoadExerciseCatalog(t.Context())

	if catalog.Source() != api.CatalogSourceWeb {
		t.Fatalf("the published catalog at %s did not answer: the server would serve "+
			"the compiled-in subset of %d exercises",
			api.ExerciseCatalogURL, catalog.ExerciseCount())
	}
	if catalog.ExerciseCount() < minPublishedExercises {
		t.Errorf("the published catalog carries %d exercises, want at least %d",
			catalog.ExerciseCount(), minPublishedExercises)
	}

	muscles := 0
	for _, category := range catalog.Types() {
		for _, exercise := range category.Exercises {
			muscles += len(exercise.PrimaryMuscles)
		}
	}
	if muscles == 0 {
		t.Error("no exercise reports a primary muscle group; the document shape changed")
	}
}
