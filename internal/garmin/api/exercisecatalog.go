package api

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// The strength-exercise catalog: the published one read by [LoadExerciseCatalog],
// and the compiled-in subset it falls back to. Which answered is reported by
// [ExerciseCatalog.Source]; why the published document is preferred, and what the
// one web-tier read is allowed to do, is in docs/parity.md.
//
// Garmin validates a strength set against its own FIT enum — an unknown category
// is a 400, a null name under a known category is accepted — which is why
// [ExerciseCatalog.Validate] closes the category set and leaves names open.

// ExerciseType is one exercise name with the label a user reads.
type ExerciseType struct {
	// Name is Garmin's exercise key, for example "BARBELL_BENCH_PRESS".
	Name string `json:"name"`
	// DisplayName is the human-readable label derived from the key.
	DisplayName string `json:"displayName"`
	// PrimaryMuscles are the muscle groups the published catalog names as
	// primary. It is empty for the compiled-in subset, which has no such data.
	PrimaryMuscles []string `json:"primaryMuscles,omitempty"`
	// SecondaryMuscles are the muscle groups the published catalog names as
	// secondary. It is empty for the compiled-in subset.
	SecondaryMuscles []string `json:"secondaryMuscles,omitempty"`
}

// clone returns a copy no caller can use to reach another caller's slices.
func (e ExerciseType) clone() ExerciseType {
	return ExerciseType{
		Name:             e.Name,
		DisplayName:      e.DisplayName,
		PrimaryMuscles:   copyKeys(e.PrimaryMuscles),
		SecondaryMuscles: copyKeys(e.SecondaryMuscles),
	}
}

// ExerciseCategory is one strength category and the exercises under it.
type ExerciseCategory struct {
	// Category is Garmin's category key, for example "BENCH_PRESS".
	Category string `json:"category"`
	// DisplayName is the human-readable label derived from the key.
	DisplayName string `json:"displayName"`
	// Count is how many exercises this catalog lists for the category.
	Count int `json:"count"`
	// Exercises are the listed exercises, ordered by name.
	Exercises []ExerciseType `json:"exercises"`
}

// clone returns a deep copy of one category.
func (c ExerciseCategory) clone() ExerciseCategory {
	exercises := make([]ExerciseType, 0, len(c.Exercises))
	for _, exercise := range c.Exercises {
		exercises = append(exercises, exercise.clone())
	}
	return ExerciseCategory{
		Category:    c.Category,
		DisplayName: c.DisplayName,
		Count:       c.Count,
		Exercises:   exercises,
	}
}

// CatalogSource names which catalog answered a read, so a caller can tell the
// published catalog from the compiled-in fallback.
type CatalogSource string

const (
	// CatalogSourceWeb is the catalog Garmin publishes at [ExerciseCatalogURL].
	CatalogSourceWeb CatalogSource = "garmin_web_catalog"
	// CatalogSourceBuiltin is the compiled-in subset.
	CatalogSourceBuiltin CatalogSource = "built_in_subset"
)

// ExerciseCatalog is one immutable catalog snapshot.
//
// It is built once and never mutated, so any number of concurrent tool calls may
// read the same value. Every accessor hands back a fresh copy, so no caller can
// reach what another caller reads.
type ExerciseCatalog struct {
	source     CatalogSource
	categories []ExerciseCategory
	index      map[string]int
	exercises  int
}

// newExerciseCatalog builds a snapshot from category rows. The rows are consumed,
// not retained: everything the snapshot holds is freshly allocated here.
func newExerciseCatalog(source CatalogSource, rows map[string][]ExerciseType) *ExerciseCatalog {
	keys := make([]string, 0, len(rows))
	for key := range rows {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	categories := make([]ExerciseCategory, 0, len(keys))
	index := make(map[string]int, len(keys))
	total := 0
	for position, key := range keys {
		category := buildCategory(key, rows[key])
		categories = append(categories, category)
		index[key] = position
		total += category.Count
	}
	return &ExerciseCatalog{
		source: source, categories: categories, index: index, exercises: total,
	}
}

// buildCategory renders one category with its exercises ordered by name.
func buildCategory(category string, exercises []ExerciseType) ExerciseCategory {
	sorted := make([]ExerciseType, 0, len(exercises))
	for _, exercise := range exercises {
		sorted = append(sorted, exercise.clone())
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	return ExerciseCategory{
		Category:    category,
		DisplayName: displayLabel(category),
		Count:       len(sorted),
		Exercises:   sorted,
	}
}

// resolve reports the catalog to read. A nil catalog is the compiled-in subset,
// so every caller that has not been given one still validates against something.
func (c *ExerciseCatalog) resolve() *ExerciseCatalog {
	if c == nil {
		return BuiltinExerciseCatalog()
	}
	return c
}

// Source reports which catalog this snapshot came from.
func (c *ExerciseCatalog) Source() CatalogSource { return c.resolve().source }

// ExerciseCount reports how many exercises the snapshot carries in total.
func (c *ExerciseCatalog) ExerciseCount() int { return c.resolve().exercises }

// Types returns the whole catalog, ordered by category. The result is freshly
// built, so no caller can mutate what another caller reads.
func (c *ExerciseCatalog) Types() []ExerciseCategory {
	resolved := c.resolve()
	out := make([]ExerciseCategory, 0, len(resolved.categories))
	for _, category := range resolved.categories {
		out = append(out, category.clone())
	}
	return out
}

// Categories returns the recognized category keys, ordered.
func (c *ExerciseCatalog) Categories() []string {
	resolved := c.resolve()
	keys := make([]string, 0, len(resolved.categories))
	for _, category := range resolved.categories {
		keys = append(keys, category.Category)
	}
	return keys
}

// Lookup returns one category and whether it is recognized.
func (c *ExerciseCatalog) Lookup(category string) (ExerciseCategory, bool) {
	resolved := c.resolve()
	position, known := resolved.index[normalizeExerciseKey(category)]
	if !known {
		return ExerciseCategory{}, false
	}
	return resolved.categories[position].clone(), true
}

// MaxExerciseKeyLen bounds an exercise or category key, so a hostile value
// cannot reach Garmin or a log line at length.
const MaxExerciseKeyLen = 64

// Validate reports whether a category and an optional exercise name may be
// written.
//
// The category must be one this catalog knows, because that set is closed and an
// unknown parent is a guaranteed 400. An empty name is valid — Garmin accepts a
// null name under a known parent — and a name the catalog does not list is
// accepted after a lexical check, because no catalog here is a mirror of
// Garmin's full enum and refusing an unlisted name would refuse valid work.
func (c *ExerciseCatalog) Validate(category, name string) error {
	if _, known := c.Lookup(category); !known {
		return fmt.Errorf("%w: exercise category is not one Garmin recognizes",
			client.ErrValidation)
	}
	if name == "" {
		return nil
	}
	if !isExerciseKey(normalizeExerciseKey(name)) {
		return fmt.Errorf("%w: exercise name must be upper-case letters, digits or underscores",
			client.ErrValidation)
	}
	return nil
}

// normalizeExerciseKey upper-cases and trims a key, which is the form Garmin
// stores.
func normalizeExerciseKey(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

// isExerciseKey reports whether value is a bounded upper-case enum key.
func isExerciseKey(value string) bool {
	if value == "" || len(value) > MaxExerciseKeyLen {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}

// copyKeys returns a copy of a key list, or nil for an empty one.
func copyKeys(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// displayLabel renders an enum key as a readable label: BARBELL_BENCH_PRESS
// becomes "Barbell Bench Press".
func displayLabel(key string) string {
	words := strings.Split(strings.ToLower(key), "_")
	for index, word := range words {
		if word == "" {
			continue
		}
		words[index] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ")
}
