package api

import (
	"encoding/json"
	"fmt"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// dogEditableKeys are the profile keys the web app's update sends back besides
// the breeds. Source: the field allowlist in the profile client.
func dogEditableKeys() []string {
	return []string{
		"name", "nickname", "gender", "isNeutered", "height", "weight",
		"birthdate", "birthdatePrecision", "adoptionDate", "adoptionDatePrecision",
	}
}

// editablePayload carries the current editable fields over, mapping the breed
// objects back to the ids the update takes. A breed reference without an exact id
// fails the update rather than being dropped from it.
func editablePayload(req client.Request, current map[string]json.RawMessage) (map[string]any, error) {
	payload := map[string]any{"primaryBreedId": nil, "secondaryBreedIds": nil}
	for _, key := range dogEditableKeys() {
		if value, ok := current[key]; ok {
			payload[key] = value
		}
	}
	unreadable := unexpected(req, fmt.Errorf("%w: a current breed has no identifier",
		client.ErrUnexpectedResponse))
	var primary *BreedRef
	if raw := current["primaryBreed"]; len(raw) > 0 && string(raw) != jsonNull {
		if json.Unmarshal(raw, &primary) != nil || primary == nil {
			return nil, unreadable
		}
		id, ok := primary.ID.Int64Exact()
		if !ok {
			return nil, unreadable
		}
		payload["primaryBreedId"] = id
	}
	var secondary []BreedRef
	if raw := current["secondaryBreeds"]; len(raw) > 0 && string(raw) != jsonNull {
		if json.Unmarshal(raw, &secondary) != nil {
			return nil, unreadable
		}
	}
	ids := make([]int64, 0, len(secondary))
	for _, b := range secondary {
		id, ok := b.ID.Int64Exact()
		if !ok {
			return nil, unreadable
		}
		ids = append(ids, id)
	}
	if len(ids) > 0 {
		payload["secondaryBreedIds"] = ids
	}
	return payload, nil
}
