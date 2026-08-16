package api

import (
	"encoding/json"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// FoodLogEntry is one tolerantly-decoded entry from a day's food log,
// exposing at minimum the identifier delete_food_log needs.
//
// delete_food_log itself names this field: "Use get_nutrition_daily_food_log
// to find the logId and date" (nutrition.py:725), and the log_id parameter
// doc confirms its shape as "a 32-char hex UUID" (nutrition.py:728). That is
// the one evidenced spelling here; every other candidate key is a defensive
// guess, since no source documents the rest of this entry's wire shape:
// LogID falls back to "id" — the other spelling Garmin's other list
// endpoints use for a per-entry identifier — only if "logId" is absent;
// MealID matches Meal.MealID's own "mealId" (nutrition.py:591, :597, :611),
// since a log entry and its meal summary are read from the same
// nutrition-service tier; MealDate is read from "mealDate", matching the
// write body's own field name (nutrition.py:603), or "date" as the next most
// plausible alternative. A field this package cannot find under any of its
// candidate keys decodes as unset rather than failing the whole day.
type FoodLogEntry struct {
	LogID    client.Text
	MealID   client.Number
	MealDate client.Text
}

// UnmarshalJSON decodes one entry defensively: an unrecognized or absent key
// leaves the corresponding field unset rather than failing the entry.
func (e *FoodLogEntry) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	e.LogID = decodeFirstText(fields, "logId", "id")
	e.MealID = decodeFirstNumber(fields, "mealId")
	e.MealDate = decodeFirstText(fields, "mealDate", "date")
	return nil
}

// decodeFirstText decodes the first of keys present in fields as a
// client.Text, or returns the unset value when none of them decode.
func decodeFirstText(fields map[string]json.RawMessage, keys ...string) client.Text {
	for _, key := range keys {
		raw, ok := fields[key]
		if !ok {
			continue
		}
		var text client.Text
		if err := json.Unmarshal(raw, &text); err == nil && text.IsSet() {
			return text
		}
	}
	return client.Text{}
}

// decodeFirstNumber decodes the first of keys present in fields as a
// client.Number, or returns the unset value when none of them decode.
func decodeFirstNumber(fields map[string]json.RawMessage, keys ...string) client.Number {
	for _, key := range keys {
		raw, ok := fields[key]
		if !ok {
			continue
		}
		var number client.Number
		if err := json.Unmarshal(raw, &number); err == nil && number.IsSet() {
			return number
		}
	}
	return client.Number{}
}

// maxFoodLogEntries bounds the entries this package will tolerantly decode
// from one day, so a hostile or malformed response cannot force an unbounded
// allocation. It is generous headroom over any real day's log.
const maxFoodLogEntries = 1000

// foodLogWrapperKeys are the plausible top-level object keys a day's log
// entries might be nested under, tried in order, since no source documents
// the response shape (see FoodLogDay's doc comment in nutritionread.go).
func foodLogWrapperKeys() []string {
	return []string{"foodLogEntries", "entries", "items", "logs"}
}

// Entries returns a best-effort tolerant decode of the day's food-log
// entries: a bare top-level array, or an object with one of
// foodLogWrapperKeys' plausible wrapper keys. A shape this package does not
// recognize decodes to no entries rather than an error, because Payload
// remains the authoritative retained response either way. A day carrying more
// than maxFoodLogEntries entries is bounded silently here; EntriesTruncated
// reports whether that happened.
func (f FoodLogDay) Entries() []FoodLogEntry {
	items := f.rawEntryItems()
	if items == nil {
		return nil
	}
	if len(items) > maxFoodLogEntries {
		items = items[:maxFoodLogEntries]
	}
	return decodeFoodLogEntries(items)
}

// EntriesTruncated reports whether the day carried more entries than
// maxFoodLogEntries, so a caller cannot silently miss an entry it needed —
// for example, one delete_food_log could otherwise never reach — matching
// every other bounded result in this package (see fitmodel.go's
// RecordsTruncated for the same discipline).
func (f FoodLogDay) EntriesTruncated() bool {
	return len(f.rawEntryItems()) > maxFoodLogEntries
}

// rawEntryItems unwraps the day's raw payload down to its per-entry JSON
// items, trying a bare top-level array first and then each of
// foodLogWrapperKeys' plausible wrapper keys. A shape this package does not
// recognize yields no items.
func (f FoodLogDay) rawEntryItems() []json.RawMessage {
	raw := f.raw.Bytes()
	if len(raw) == 0 {
		return nil
	}

	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err == nil {
		return items
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil
	}
	for _, key := range foodLogWrapperKeys() {
		wrapped, ok := fields[key]
		if !ok {
			continue
		}
		var wrappedItems []json.RawMessage
		if err := json.Unmarshal(wrapped, &wrappedItems); err == nil {
			return wrappedItems
		}
	}
	return nil
}

// decodeFoodLogEntries decodes each item defensively; an item that fails to
// decode at all is skipped rather than failing the whole day. items must
// already be bounded by the caller.
func decodeFoodLogEntries(items []json.RawMessage) []FoodLogEntry {
	out := make([]FoodLogEntry, 0, len(items))
	for _, item := range items {
		var entry FoodLogEntry
		if err := json.Unmarshal(item, &entry); err == nil {
			out = append(out, entry)
		}
	}
	return out
}
