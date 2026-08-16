package api

import (
	"encoding/json"
	"fmt"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// The streaming walk of the published document: each level is opened, read member
// by member with its bounds applied as the keys arrive, and closed. Why the walk
// streams rather than unmarshalling whole is in docs/parity.md.

// decodeDocument reads the top-level object and returns the catalog rows its
// "categories" member declares.
func decodeDocument(decoder *json.Decoder) (map[string][]ExerciseType, int, error) {
	if err := expectObject(decoder); err != nil {
		return nil, 0, err
	}

	var rows map[string][]ExerciseType
	exercises := 0
	for decoder.More() {
		field, err := objectKey(decoder)
		if err != nil {
			return nil, 0, err
		}
		if field != categoriesMember {
			if err := skipValue(decoder); err != nil {
				return nil, 0, err
			}
			continue
		}
		// A second member would start a fresh collision set, so one key could
		// appear in both blocks and the later one would silently win.
		if rows != nil {
			return nil, 0, repeatedMember(categoriesMember)
		}
		if rows, exercises, err = decodeCategories(decoder); err != nil {
			return nil, 0, err
		}
	}
	if err := endObject(decoder); err != nil {
		return nil, 0, err
	}
	if rows == nil {
		return nil, 0, fmt.Errorf("%w: the exercise catalog declares no categories member",
			client.ErrMalformedPayload)
	}
	if err := expectEnd(decoder); err != nil {
		return nil, 0, err
	}
	return rows, exercises, nil
}

// decodeCategories reads the categories object one category at a time.
func decodeCategories(decoder *json.Decoder) (map[string][]ExerciseType, int, error) {
	if err := expectObject(decoder); err != nil {
		return nil, 0, err
	}

	rows := map[string][]ExerciseType{}
	seen := map[string]struct{}{}
	exercises := 0
	for decoder.More() {
		rawKey, err := objectKey(decoder)
		if err != nil {
			return nil, 0, err
		}
		// Recorded before it is judged, so a skipped key still collides: "" and
		// "   " normalize alike and would otherwise escape the check.
		key := normalizeExerciseKey(rawKey)
		if _, taken := seen[key]; taken {
			return nil, 0, collision("category", key)
		}
		seen[key] = struct{}{}
		if len(seen) > MaxCatalogCategories {
			return nil, 0, boundCrossed("categories", len(seen), MaxCatalogCategories)
		}
		if !isExerciseKey(key) {
			if err := skipValue(decoder); err != nil {
				return nil, 0, err
			}
			continue
		}

		listed, err := decodeCategory(decoder, key)
		if err != nil {
			return nil, 0, err
		}
		if len(listed) == 0 {
			continue
		}
		rows[key] = listed
		exercises += len(listed)
		if exercises > MaxCatalogExercises {
			return nil, 0, boundCrossed("exercises", exercises, MaxCatalogExercises)
		}
	}
	if err := endObject(decoder); err != nil {
		return nil, 0, err
	}
	return rows, exercises, nil
}

// decodeCategory reads one category object and returns its exercises.
func decodeCategory(decoder *json.Decoder, category string) ([]ExerciseType, error) {
	if err := expectObject(decoder); err != nil {
		return nil, err
	}

	var listed []ExerciseType
	seenExercises := false
	for decoder.More() {
		field, err := objectKey(decoder)
		if err != nil {
			return nil, err
		}
		if field != exercisesMember {
			if err := skipValue(decoder); err != nil {
				return nil, err
			}
			continue
		}
		if seenExercises {
			return nil, repeatedMember(exercisesMember)
		}
		seenExercises = true
		if listed, err = decodeExercises(decoder, category); err != nil {
			return nil, err
		}
	}
	if err := endObject(decoder); err != nil {
		return nil, err
	}
	return listed, nil
}

// decodeExercises reads one category's exercise object, one exercise at a time.
func decodeExercises(decoder *json.Decoder, category string) ([]ExerciseType, error) {
	if err := expectObject(decoder); err != nil {
		return nil, err
	}

	listed := []ExerciseType{}
	seen := map[string]struct{}{}
	for decoder.More() {
		rawName, err := objectKey(decoder)
		if err != nil {
			return nil, err
		}
		name := normalizeExerciseKey(rawName)
		if _, taken := seen[name]; taken {
			return nil, collision("exercise", name)
		}
		seen[name] = struct{}{}
		if len(seen) > MaxCatalogExercisesPerCategory {
			return nil, boundCrossed("exercises under "+category,
				len(seen), MaxCatalogExercisesPerCategory)
		}
		if !isExerciseKey(name) {
			if err := skipValue(decoder); err != nil {
				return nil, err
			}
			continue
		}

		entry, err := decodeEntry(decoder)
		if err != nil {
			return nil, err
		}
		listed = append(listed, ExerciseType{
			Name:             name,
			DisplayName:      displayLabel(name),
			PrimaryMuscles:   entry.PrimaryMuscles,
			SecondaryMuscles: entry.SecondaryMuscles,
		})
	}
	if err := endObject(decoder); err != nil {
		return nil, err
	}
	return listed, nil
}

// decodeEntry reads one exercise object, streaming its muscle lists.
func decodeEntry(decoder *json.Decoder) (catalogEntry, error) {
	if err := expectObject(decoder); err != nil {
		return catalogEntry{}, err
	}

	var entry catalogEntry
	seen := map[string]struct{}{}
	for decoder.More() {
		field, err := objectKey(decoder)
		if err != nil {
			return catalogEntry{}, err
		}
		if field != primaryMusclesMember && field != secondaryMusclesMember {
			if err := skipValue(decoder); err != nil {
				return catalogEntry{}, err
			}
			continue
		}
		if _, taken := seen[field]; taken {
			return catalogEntry{}, repeatedMember(field)
		}
		seen[field] = struct{}{}

		muscles, err := decodeMuscles(decoder, field)
		if err != nil {
			return catalogEntry{}, err
		}
		if field == primaryMusclesMember {
			entry.PrimaryMuscles = muscles
		} else {
			entry.SecondaryMuscles = muscles
		}
	}
	if err := endObject(decoder); err != nil {
		return catalogEntry{}, err
	}
	return entry, nil
}

// decodeMuscles reads one muscle list element by element, refusing at the element
// that crosses the bound: over a bound a document is refused, never trimmed.
func decodeMuscles(decoder *json.Decoder, member string) ([]string, error) {
	// A null list is absent, not malformed: JSON encoders write one for an empty
	// list and a drifting document must not cost the whole catalog for it.
	present, err := openList(decoder)
	if err != nil || !present {
		return nil, err
	}

	var muscles []string
	count := 0
	for decoder.More() {
		value, err := stringElement(decoder)
		if err != nil {
			return nil, err
		}
		count++
		if count > MaxCatalogMuscles {
			return nil, boundCrossed(member, count, MaxCatalogMuscles)
		}
		// The published document carries empty entries, and a muscle group is an
		// enum key like any other, so an unusable value is dropped.
		key := normalizeExerciseKey(value)
		if !isExerciseKey(key) {
			continue
		}
		muscles = append(muscles, key)
	}
	if err := endArray(decoder); err != nil {
		return nil, err
	}
	return muscles, nil
}
