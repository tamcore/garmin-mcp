package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// Decoding the published catalog document. Every bound and every check here
// exists so a 200 carrying something other than the catalog cannot replace good
// compiled-in data, exhaust memory, or reach a Garmin write as a valid category.

// Cardinality bounds. The byte cap on the response bounds the input; these bound
// what the input can expand into, which is what decides how much memory a
// snapshot and its copies cost.
//
// Every one of them is set from the published document as measured on 2026-08-16,
// with headroom of roughly five to eight times, so Garmin can grow the catalog
// substantially before a bound costs the fetch. Measured: 47 categories, 131
// exercises in the largest category (PLANK), 1510 exercises in total, and 10
// muscle groups in the longest list (TOTAL_BODY / MAN_MAKERS). The muscle bound
// was the one number in this file that was never measured, and at 8 it refused
// Garmin's own document; it is now above both the observed maximum and the whole
// observed muscle vocabulary, which was 18 distinct values.
const (
	// MaxCatalogCategories bounds how many categories a fetched catalog may carry.
	// Observed 47.
	MaxCatalogCategories = 256

	// MaxCatalogExercisesPerCategory bounds one category's exercise list.
	// Observed 131.
	MaxCatalogExercisesPerCategory = 1024

	// MaxCatalogExercises bounds the whole fetched catalog. Observed 1510.
	MaxCatalogExercises = 8192

	// MaxCatalogMuscles bounds the muscle groups one exercise reports in each
	// list. Observed 10, out of a vocabulary of 18 distinct groups. Over it the
	// document is refused, like every other bound here.
	MaxCatalogMuscles = 64

	// MaxRenderedCatalogBytes bounds what a catalog costs once it is rendered into
	// a tool result. A caller receives that in one MCP message, so the ceiling is
	// what a client can be asked to receive rather than what Garmin can serve.
	MaxRenderedCatalogBytes = 2 << 20
)

// The member names this package reads out of the published document.
const (
	categoriesMember       = "categories"
	exercisesMember        = "exercises"
	primaryMusclesMember   = "primaryMuscles"
	secondaryMusclesMember = "secondaryMuscles"
)

// catalogEntry is one exercise as the published document carries it.
type catalogEntry struct {
	PrimaryMuscles   []string `json:"primaryMuscles"`
	SecondaryMuscles []string `json:"secondaryMuscles"`
}

// ParseExerciseCatalog decodes a published catalog document into a snapshot.
//
// A malformed key is skipped and a category left with no exercise is dropped,
// because a drifting document must not cost the whole catalog. Anything that
// cannot be resolved safely — a key collision, a bound crossed, a document this
// package cannot recognize as Garmin's taxonomy — is refused outright, so the
// caller falls back rather than serving it.
func ParseExerciseCatalog(raw []byte) (*ExerciseCatalog, error) {
	return parseExerciseCatalog(bytes.NewReader(raw))
}

// parseExerciseCatalog decodes the document as a stream, applying each bound at
// the key that crosses it so nothing beyond the accepted structure is ever held.
func parseExerciseCatalog(reader io.Reader) (*ExerciseCatalog, error) {
	decoder := json.NewDecoder(reader)
	rows, exercises, err := decodeDocument(decoder)
	if err != nil {
		return nil, err
	}
	if err := checkCatalogPlausible(rows, exercises); err != nil {
		return nil, err
	}

	catalog := newExerciseCatalog(CatalogSourceWeb, mergeBuiltinRows(rows))
	if err := checkRenderedSize(catalog); err != nil {
		return nil, err
	}
	return catalog, nil
}

// The recognition invariant: a fetched document must carry every compiled-in
// category except the FIT no-category sentinel, and reproduce at least half of the
// compiled-in exercise names under their own parent.
//
// A count-only gate would admit a fabricated document, whose categories would then
// become the closed set strength writes validate against. Measured against the
// published document: 33 of 33 required categories and 63 of 98 names (64%), so
// the floor leaves room for drift. It is recognition, not authentication — the
// trust anchor is TLS to connect.garmin.com. See docs/parity.md.
const (
	// unknownCategory is the FIT no-category sentinel. It is compiled in and
	// absent from the published document, so it is the one category not required.
	unknownCategory = "UNKNOWN"

	// minRecognizedNameRatio is the share of compiled-in exercise names a fetched
	// document must reproduce under the same category.
	minRecognizedNameRatio = 0.5
)

// checkCatalogPlausible refuses a fetched document that carries less than the
// catalog already compiled in, or that this package cannot recognize as Garmin's.
func checkCatalogPlausible(rows map[string][]ExerciseType, exercises int) error {
	builtin := BuiltinExerciseCatalog()
	if len(rows) < len(builtin.categories) || exercises < builtin.exercises {
		return fmt.Errorf(
			"%w: the fetched exercise catalog carries %d categories and %d exercises, "+
				"fewer than the compiled-in subset", client.ErrMalformedPayload,
			len(rows), exercises)
	}
	return checkCatalogRecognized(rows)
}

// checkCatalogRecognized applies the recognition invariant described above.
func checkCatalogRecognized(rows map[string][]ExerciseType) error {
	names, recognized := 0, 0
	for _, row := range builtinCatalogRows() {
		if row.category == unknownCategory {
			continue
		}
		listed, present := rows[row.category]
		if !present {
			return fmt.Errorf(
				"%w: the fetched exercise catalog does not carry the compiled-in category %q, "+
					"so it is not Garmin's exercise taxonomy",
				client.ErrMalformedPayload, row.category)
		}
		known := make(map[string]struct{}, len(listed))
		for _, exercise := range listed {
			known[exercise.Name] = struct{}{}
		}
		for _, name := range row.names {
			names++
			if _, found := known[name]; found {
				recognized++
			}
		}
	}

	if names == 0 || float64(recognized) < minRecognizedNameRatio*float64(names) {
		return fmt.Errorf(
			"%w: the fetched exercise catalog reproduces %d of %d compiled-in exercise names, "+
				"below the %.0f%% floor that makes it recognizable as Garmin's taxonomy",
			client.ErrMalformedPayload, recognized, names, minRecognizedNameRatio*100)
	}
	return nil
}

// checkRenderedSize refuses a catalog that would not fit in one tool result. It
// measures the rendered document rather than estimating it, because what a caller
// receives is exactly this encoding.
func checkRenderedSize(catalog *ExerciseCatalog) error {
	rendered, err := json.Marshal(catalog.Types())
	if err != nil {
		return fmt.Errorf("%w: the fetched exercise catalog cannot be rendered: %w",
			client.ErrMalformedPayload, err)
	}
	if len(rendered) > MaxRenderedCatalogBytes {
		return boundCrossed("rendered bytes", len(rendered), MaxRenderedCatalogBytes)
	}
	return nil
}

// mergeBuiltinRows adds every compiled-in category and name the fetched document
// does not carry, so the fetch can only widen what validates, never narrow it.
func mergeBuiltinRows(fetched map[string][]ExerciseType) map[string][]ExerciseType {
	for category, exercises := range builtinRows() {
		listed, present := fetched[category]
		if !present {
			fetched[category] = exercises
			continue
		}
		known := make(map[string]struct{}, len(listed))
		for _, exercise := range listed {
			known[exercise.Name] = struct{}{}
		}
		for _, exercise := range exercises {
			if _, found := known[exercise.Name]; !found {
				listed = append(listed, exercise)
			}
		}
		fetched[category] = listed
	}
	return fetched
}
