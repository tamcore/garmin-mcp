//go:build garminlive

package live

import "encoding/json"

// foodMetaDataField is the one outer object every custom-food identity field this
// file reads is nested under, matching foodMetaDataDTO's own wire shape
// (internal/garmin/api/nutritionwritefood.go).
const foodMetaDataField = "foodMetaData"

// nestedStringField reads one string field nested under foodMetaData in a JSON
// document, or "" when the document does not carry it in that shape.
func nestedStringField(body []byte, encoding, inner string) string {
	document, err := decompressed(body, encoding)
	if err != nil {
		return ""
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(document, &fields); err != nil {
		return ""
	}
	outerRaw, present := fields[foodMetaDataField]
	if !present {
		return ""
	}
	var innerFields map[string]json.RawMessage
	if err := json.Unmarshal(outerRaw, &innerFields); err != nil {
		return ""
	}
	innerRaw, present := innerFields[inner]
	if !present {
		return ""
	}
	var value string
	if err := json.Unmarshal(innerRaw, &value); err != nil {
		return ""
	}
	return value
}
