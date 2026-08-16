package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// What a fetched document is not allowed to be: too large to hold once it is
// expanded, ambiguous about its own keys, or something other than Garmin's
// taxonomy. The fixtures and the local servers come from
// exercisecatalogweb_internal_test.go, and nothing here reaches the network.

// TestCardinalityBoundsFallBack is the memory guarantee.
//
// The byte cap bounds the input; it does not bound what the input expands into. A
// document well under 4 MiB can declare hundreds of thousands of keys, and every
// one of them would become a map entry, a rendered exercise, and a copy in every
// snapshot handed to a caller. A low-memory deployment must fall back to the
// compiled-in subset rather than die at start-up, which is what these bounds buy.
func TestCardinalityBoundsFallBack(t *testing.T) {
	t.Parallel()

	cases := map[string]func() map[string]map[string]catalogEntry{
		"too many categories": func() map[string]map[string]catalogEntry {
			rows := syntheticRows()
			for index := range MaxCatalogCategories + 1 {
				rows[fmt.Sprintf("FLOOD_CATEGORY_%d", index)] = map[string]catalogEntry{
					"FLOOD_MOVEMENT": {},
				}
			}
			return rows
		},
		"too many exercises in one category": func() map[string]map[string]catalogEntry {
			rows := syntheticRows()
			entries := map[string]catalogEntry{}
			for index := range MaxCatalogExercisesPerCategory + 1 {
				entries[fmt.Sprintf("FLOOD_MOVEMENT_%d", index)] = catalogEntry{}
			}
			rows["SQUAT"] = entries
			return rows
		},
		"too many exercises in total": func() map[string]map[string]catalogEntry {
			rows := syntheticRows()
			needed := MaxCatalogExercises + 1
			for index := range (needed / MaxCatalogExercisesPerCategory) + 1 {
				entries := map[string]catalogEntry{}
				for position := range MaxCatalogExercisesPerCategory {
					entries[fmt.Sprintf("FLOOD_MOVEMENT_%d_%d", index, position)] = catalogEntry{}
				}
				rows[fmt.Sprintf("FLOOD_CATEGORY_%d", index)] = entries
			}
			return rows
		},
	}

	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			document := documentOf(build())
			if len(document) > MaxExerciseCatalogBytes {
				t.Fatalf("the document is %d bytes; the byte cap would refuse it and this "+
					"test would prove nothing about cardinality", len(document))
			}

			server := serveDocument(t, document)
			assertFallback(t, server.Client(), server.URL)
		})
	}
}

// TestNormalizedKeyCollisionsAreRefused states why a collision is not resolved:
// two raw keys that normalize to one would overwrite each other in an order map
// iteration decides, so two start-ups of the same binary against the same
// document could serve different catalogs and validate differently.
func TestNormalizedKeyCollisionsAreRefused(t *testing.T) {
	t.Parallel()

	t.Run("category", func(t *testing.T) {
		t.Parallel()

		rows := syntheticRows()
		rows[" squat "] = map[string]catalogEntry{"COLLIDING_MOVEMENT": {}}

		server := serveDocument(t, documentOf(rows))
		assertFallback(t, server.Client(), server.URL)
	})

	t.Run("exercise", func(t *testing.T) {
		t.Parallel()

		rows := syntheticRows()
		rows["SQUAT"][" back_squat "] = catalogEntry{}

		server := serveDocument(t, documentOf(rows))
		assertFallback(t, server.Client(), server.URL)
	})
}

// TestAFabricatedCatalogIsRefused is the reason the plausibility gate is more than
// a count.
//
// A document of invented categories can be larger than the compiled-in subset in
// every dimension. If size alone decided, those categories would become the closed
// set that strength writes validate against, and a caller could be led into
// writing a category Garmin rejects — or away from one it accepts. The document
// therefore has to be recognizable as Garmin's own taxonomy.
func TestAFabricatedCatalogIsRefused(t *testing.T) {
	t.Parallel()

	fabricated := map[string]map[string]catalogEntry{}
	for index := range 60 {
		entries := map[string]catalogEntry{}
		for position := range 40 {
			entries[fmt.Sprintf("FABRICATED_MOVEMENT_%d_%d", index, position)] = catalogEntry{
				PrimaryMuscles: []string{fillerMuscle},
			}
		}
		fabricated[fmt.Sprintf("FABRICATED_CATEGORY_%d", index)] = entries
	}

	// Larger than the compiled-in subset in both dimensions, so a count-only gate
	// would have admitted it.
	if len(fabricated) <= len(BuiltinExerciseCatalog().Categories()) {
		t.Fatal("the fabricated document is not larger; the test would prove nothing")
	}

	server := serveDocument(t, documentOf(fabricated))
	assertFallback(t, server.Client(), server.URL)

	// Half the compiled-in names is the floor, so a document missing more than
	// half of them is refused even when every category key is right.
	partial := syntheticRows()
	dropped, total := 0, 0
	for _, row := range builtinCatalogRows() {
		for _, name := range row.names {
			total++
			if dropped*2 <= total {
				delete(partial[row.category], name)
				dropped++
			}
		}
	}
	partialServer := serveDocument(t, documentOf(partial))
	assertFallback(t, partialServer.Client(), partialServer.URL)
}

// TestRenderedSizeBoundRefusesAnOversizedCatalog covers the last expansion step:
// a catalog small enough to hold becomes a result a client is asked to receive in
// one message, and that is bounded on its own.
func TestRenderedSizeBoundRefusesAnOversizedCatalog(t *testing.T) {
	t.Parallel()

	rows := map[string][]ExerciseType{}
	muscles := make([]string, MaxCatalogMuscles)
	for index := range muscles {
		muscles[index] = strings.Repeat("M", MaxExerciseKeyLen)
	}
	for index := range 64 {
		exercises := make([]ExerciseType, 0, 64)
		for position := range 64 {
			name := fmt.Sprintf("%s_%d_%d", strings.Repeat("N", 40), index, position)
			exercises = append(exercises, ExerciseType{
				Name: name, DisplayName: name,
				PrimaryMuscles: muscles, SecondaryMuscles: muscles,
			})
		}
		rows[fmt.Sprintf("BULK_CATEGORY_%d", index)] = exercises
	}

	catalog := newExerciseCatalog(CatalogSourceWeb, rows)
	if err := checkRenderedSize(catalog); err == nil {
		t.Fatalf("a catalog rendering over %d bytes was accepted", MaxRenderedCatalogBytes)
	}
	if err := checkRenderedSize(BuiltinExerciseCatalog()); err != nil {
		t.Errorf("the compiled-in subset was refused by its own bound: %v", err)
	}
}

// TestABoundIsAppliedWhileReadingNotAfterwards is the difference between bounding
// the document and bounding what it expands into.
//
// The fixture crosses the category bound and is then deliberately truncated
// mid-token. A decoder that read the whole document first would report the syntax
// error, because it would never get as far as counting; the streaming walk reports
// the bound, because it stopped at the key that crossed it and never held the
// rest.
func TestABoundIsAppliedWhileReadingNotAfterwards(t *testing.T) {
	t.Parallel()

	var document strings.Builder
	document.WriteString(`{"categories":{`)
	for index := range MaxCatalogCategories + 2 {
		fmt.Fprintf(&document, `"FLOOD_CATEGORY_%d":{"exercises":{"FLOOD_MOVEMENT":{}}},`, index)
	}
	document.WriteString(`"TRUNCATED_CATEGORY":{"exercises":{"X`)

	_, err := ParseExerciseCatalog([]byte(document.String()))
	if err == nil {
		t.Fatal("a document over the category bound was accepted")
	}
	if !strings.Contains(err.Error(), "over the bound of") {
		t.Errorf("error = %v, want the bound to be reported: a syntax error here means the "+
			"whole document was read before any bound was applied", err)
	}
}

// TestMalformedDocumentShapesFallBack walks the shapes a drifting or hostile
// document can take at each level of the streaming decode.
//
// Each one has to end in a payload error rather than a panic, a partial catalog,
// or a decoder left at the wrong nesting depth — the last of which would be the
// worst outcome, because it reads as a smaller catalog rather than as a failure.
func TestMalformedDocumentShapesFallBack(t *testing.T) {
	t.Parallel()

	documents := map[string]string{
		"not an object": `["categories"]`,
		"a category member without a name": `{"categories":{"SQUAT":{"exercises":` +
			`{"BACK_SQUAT":{}}}},`,
		"muscles are not a list": `{"categories":{"SQUAT":{"exercises":` +
			`{"BACK_SQUAT":{"primaryMuscles":"GLUTES"}}}}}`,
		"muscle list is truncated": `{"categories":{"SQUAT":{"exercises":` +
			`{"BACK_SQUAT":{"primaryMuscles":["GLUTES"`,
		"category object is truncated":   `{"categories":{"SQUAT":{"exercises":{"BACK_SQUAT":{}}`,
		"exercise object is truncated":   `{"categories":{"SQUAT":{"exercises":{"BACK_SQUAT":{"primaryMuscles":[]`,
		"an ignored member is malformed": `{"footer":{,"categories":{}}`,
		"no categories member":           `{"other":{}}`,
		"categories is not an object":    `{"categories":[]}`,
		"category is not an object":      `{"categories":{"SQUAT":42}}`,
		"exercises is not an object":     `{"categories":{"SQUAT":{"exercises":7}}}`,
		"exercise is not an object":      `{"categories":{"SQUAT":{"exercises":{"BACK_SQUAT":5}}}}`,
		"truncated after a name":         `{"categories":{"SQUAT":{"exercises":{"BACK_SQUAT"`,
		"truncated in the document":      `{"categories":`,
		"unclosed categories":            `{"categories":{"SQUAT":{"exercises":{}}}`,
		"trailing garbage member":        `{"padding":@,"categories":{}}`,
		"muscles are not strings":        `{"categories":{"SQUAT":{"exercises":{"BACK_SQUAT":{"primaryMuscles":[1]}}}}}`,
	}

	for name, document := range documents {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			catalog, err := ParseExerciseCatalog([]byte(document))
			if err == nil {
				t.Fatalf("ParseExerciseCatalog(%s) reported success", document)
			}
			if !errors.Is(err, client.ErrMalformedPayload) {
				t.Errorf("error = %v, want a payload error", err)
			}
			if catalog != nil {
				t.Errorf("a catalog came back with an error: %v", catalog.Source())
			}
		})
	}
}

// TestAnIgnoredMemberIsSkippedNotRead covers the members this package does not
// read: a document may carry anything alongside the catalog, and skipping it must
// not disturb the walk.
func TestAnIgnoredMemberIsSkippedNotRead(t *testing.T) {
	t.Parallel()

	rows := syntheticRows()
	document := documentOf(rows)

	// Wrap the categories object in a document carrying unread members before and
	// after it, and unread members inside a category.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(document, &fields); err != nil {
		t.Fatalf("decode the synthetic document: %v", err)
	}
	wrapped, err := json.Marshal(map[string]any{
		"schemaVersion":  3,
		categoriesMember: fields[categoriesMember],
		"footer":         map[string]any{"generated": "synthetic", "nested": []any{1, 2, 3}},
	})
	if err != nil {
		t.Fatalf("encode the wrapped document: %v", err)
	}

	catalog, err := ParseExerciseCatalog(wrapped)
	if err != nil {
		t.Fatalf("ParseExerciseCatalog() = %v", err)
	}
	if catalog.Source() != CatalogSourceWeb {
		t.Errorf("Source() = %q, want the fetched catalog", catalog.Source())
	}
	if _, known := catalog.Lookup(fetchedOnlyCategory); !known {
		t.Errorf("%q was lost while skipping the unread members", fetchedOnlyCategory)
	}
}
