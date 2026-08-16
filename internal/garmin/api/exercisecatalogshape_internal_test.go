package api

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// The shapes a document is refused for rather than read around: a list over its
// bound, a structural member carried twice, data after the document, and keys that
// normalize alike. The fixtures come from exercisecatalogweb_internal_test.go, and
// nothing here reaches the network.

// TestMuscleListsOverTheBoundAreRefused states the one rule every bound here
// follows: over a bound the document is refused and the compiled-in subset
// answers. Trimming instead would serve a catalog that silently disagrees with
// what Garmin published, which is the outcome a fallback exists to avoid.
func TestMuscleListsOverTheBoundAreRefused(t *testing.T) {
	t.Parallel()

	flooded := make([]string, MaxCatalogMuscles+1)
	for index := range flooded {
		flooded[index] = fmt.Sprintf("MUSCLE_%d", index)
	}

	rows := syntheticRows()
	rows[fetchedOnlyCategory][fetchedOnlyExercise] = catalogEntry{PrimaryMuscles: flooded}

	server := serveDocument(t, documentOf(rows))
	assertFallback(t, server.Client(), server.URL)
}

// TestAMuscleListIsBoundedWhileReading is the muscle-list half of the guarantee
// the category bound already carries: the array is walked element by element, so
// a hostile list is refused at the element that crosses the bound rather than
// allocated whole and trimmed afterwards.
//
// The fixture crosses the bound and is then truncated mid-token. A decoder that
// read the array whole would report the syntax error; the streaming walk reports
// the bound.
func TestAMuscleListIsBoundedWhileReading(t *testing.T) {
	t.Parallel()

	var document strings.Builder
	document.WriteString(`{"categories":{"SQUAT":{"exercises":{"BACK_SQUAT":{"primaryMuscles":[`)
	for index := range MaxCatalogMuscles + 2 {
		fmt.Fprintf(&document, `"MUSCLE_%d",`, index)
	}
	document.WriteString(`"TRUNCATED`)

	_, err := ParseExerciseCatalog([]byte(document.String()))
	if err == nil {
		t.Fatal("a muscle list over the bound was accepted")
	}
	if !strings.Contains(err.Error(), "over the bound of") {
		t.Errorf("error = %v, want the bound to be reported: a syntax error here means the "+
			"list was read whole before the bound was applied", err)
	}
}

// TestRepeatedStructuralMembersAreRefused covers the members that carry the
// collision sets. A second one starts a fresh set, so one normalized key could
// appear in both blocks and the later block would silently win — the same
// order-dependence a key collision has, one level up.
func TestRepeatedStructuralMembersAreRefused(t *testing.T) {
	t.Parallel()

	documents := map[string]string{
		"categories twice": `{"categories":{"SQUAT":{"exercises":{"BACK_SQUAT":{}}}},` +
			`"categories":{"ROW":{"exercises":{"BARBELL_ROW":{}}}}}`,
		"exercises twice": `{"categories":{"SQUAT":{"exercises":{"BACK_SQUAT":{}},` +
			`"exercises":{"FRONT_SQUAT":{}}}}}`,
		"primaryMuscles twice": `{"categories":{"SQUAT":{"exercises":{"BACK_SQUAT":` +
			`{"primaryMuscles":["GLUTES"],"primaryMuscles":["QUADS"]}}}}}`,
	}

	for name, document := range documents {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := ParseExerciseCatalog([]byte(document))
			if err == nil {
				t.Fatal("a document with a repeated structural member was accepted")
			}
			if !strings.Contains(err.Error(), "twice") {
				t.Errorf("error = %v, want the repeated member to be named", err)
			}
		})
	}
}

// TestDataAfterTheDocumentIsRefused closes the walk. A catalog this package
// recognizes, followed by a second value or by garbage, is not the document
// Garmin publishes, and accepting the recognized prefix would serve whatever a
// truncating or injecting intermediary left behind.
func TestDataAfterTheDocumentIsRefused(t *testing.T) {
	t.Parallel()

	recognized := string(syntheticDocument())
	documents := map[string]string{
		"a second document": recognized + recognized,
		"trailing garbage":  recognized + " not json",
		"trailing value":    recognized + " 42",
	}

	for name, document := range documents {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			catalog, err := ParseExerciseCatalog([]byte(document))
			if err == nil {
				t.Fatalf("%s after the document was accepted, serving %d exercises",
					name, catalog.ExerciseCount())
			}
			if !errors.Is(err, client.ErrMalformedPayload) {
				t.Errorf("error = %v, want a payload error", err)
			}
		})
	}
}

// TestKeysThatNormalizeToNothingStillCollide covers the keys that are skipped as
// unusable. They are recorded before they are judged, so two of them collide like
// any other pair: "" and "   " are the same key, and a document carrying both is
// refused rather than quietly reduced to one.
func TestKeysThatNormalizeToNothingStillCollide(t *testing.T) {
	t.Parallel()

	documents := map[string]string{
		"category": `{"categories":{"":{"exercises":{}},"   ":{"exercises":{}}}}`,
		"exercise": `{"categories":{"SQUAT":{"exercises":{"":{},"   ":{}}}}}`,
	}

	for name, document := range documents {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := ParseExerciseCatalog([]byte(document))
			if err == nil {
				t.Fatal("two keys that normalize alike were accepted")
			}
			if !strings.Contains(err.Error(), "normalize") {
				t.Errorf("error = %v, want the collision to be reported", err)
			}
		})
	}
}

// observedMaxMuscles is the longest muscle list the published document carried
// when this was measured, on 2026-08-16: TOTAL_BODY / MAN_MAKERS / primaryMuscles.
// The whole muscle vocabulary was 18 distinct groups.
const observedMaxMuscles = 10

// TestAMuscleListAtGarminsObservedMaximumLoads is the guard this suite lacked
// when MaxCatalogMuscles was set to a number nobody had measured.
//
// The bound was 8, Garmin's own document carries 10, and under the
// refuse-never-trim rule that cost the entire catalog: a running server fell back
// to the 98-exercise subset and only the credentialed live test noticed. That test
// needs an account and an acknowledgement, so it cannot protect CI. This one
// carries the observed shape offline: a bound set below reality fails here first.
func TestAMuscleListAtGarminsObservedMaximumLoads(t *testing.T) {
	t.Parallel()

	if MaxCatalogMuscles <= observedMaxMuscles {
		t.Fatalf("MaxCatalogMuscles = %d, which is not above the %d Garmin publishes",
			MaxCatalogMuscles, observedMaxMuscles)
	}

	muscles := make([]string, observedMaxMuscles)
	for index := range muscles {
		muscles[index] = fmt.Sprintf("MUSCLE_%d", index)
	}

	rows := syntheticRows()
	rows[fetchedOnlyCategory][fetchedOnlyExercise] = catalogEntry{
		PrimaryMuscles: muscles, SecondaryMuscles: muscles,
	}

	catalog, err := ParseExerciseCatalog(documentOf(rows))
	if err != nil {
		t.Fatalf("a document carrying Garmin's own longest muscle list was refused: %v", err)
	}
	if catalog.Source() != CatalogSourceWeb {
		t.Fatalf("Source() = %q, want the fetched catalog", catalog.Source())
	}

	category, _ := catalog.Lookup(fetchedOnlyCategory)
	if len(category.Exercises) != 1 {
		t.Fatalf("%d exercises, want the one the document carries", len(category.Exercises))
	}
	if got := len(category.Exercises[0].PrimaryMuscles); got != observedMaxMuscles {
		t.Errorf("PrimaryMuscles carries %d groups, want all %d: a bound that trims would "+
			"serve a catalog that disagrees with Garmin's", got, observedMaxMuscles)
	}
}
