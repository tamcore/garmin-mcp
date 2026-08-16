package api

import (
	"context"
	"encoding/json"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// maxServingUnits bounds the serving-unit catalog. Garmin's real catalog is a
// short, closed set of unit abbreviations (nutrition.py:256: "e.g. G, ML,
// OZ"); this is headroom, not an observed count.
const maxServingUnits = 256

// ServingUnits is the valid serving-unit catalog for a custom food.
//
// Its wire shape is not evidenced anywhere: get_custom_food_serving_units
// passes the response straight through as JSON (nutrition.py:259-264) without
// decoding it. Every shape this package can plausibly recognize is decoded
// tolerantly, matching the direction nutritionreadfoodlog.go's FoodLogEntry
// decoding already takes: a bare top-level array of unit-code strings; a bare
// array of objects such as `[{"code":"G","name":"Gram"}]`, reading the code
// out of the first candidate key present; a single-key object wrapper such as
// `{"servingUnits":[...]}`; or a multi-key wrapper such as
// `{"servingUnits":[...],"unitSystem":"metric"}`. Nothing confirms which of
// these Garmin actually sends, so a shape this package does not recognize
// decodes to no units rather than failing the read — the retained raw payload
// stays the authoritative response either way.
type ServingUnits struct {
	units client.List[client.Text]
	raw   client.Payload
}

// Units returns a copy of the decoded unit codes, bounded by maxServingUnits.
func (s ServingUnits) Units() []string {
	items := s.units.Items()
	if len(items) > maxServingUnits {
		items = items[:maxServingUnits]
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if value, ok := item.Value(); ok {
			out = append(out, value)
		}
	}
	return out
}

// Payload is the retained raw response.
func (s ServingUnits) Payload() client.Payload { return s.raw }

// CustomFoodServingUnits reads the valid serving units for a custom food.
//
// Source: get_custom_food_serving_units, GET
// "/nutrition-service/metadata/customFoodServingUnits" (nutrition.py:260).
func (n *Nutrition) CustomFoodServingUnits(
	ctx context.Context, session client.Session,
) (ServingUnits, error) {
	req := readRequest(client.OpGetCustomFoodServingUnits,
		client.EndpointNutritionCustomFoodServingUnits,
		client.PathNutritionCustomFoodServingUnits, nil)

	var raw json.RawMessage
	payload, err := n.req.read(ctx, session, req, &raw)
	if err != nil {
		return ServingUnits{}, err
	}
	return ServingUnits{units: decodeServingUnits(raw), raw: payload}, nil
}

// servingUnitWrapperKeys are the plausible top-level object keys the
// serving-unit catalog might be nested under, tried in order. Not evidenced;
// see ServingUnits' doc comment.
func servingUnitWrapperKeys() []string {
	return []string{"servingUnits", "units", "unitList"}
}

// servingUnitCodeKeys are the plausible keys carrying one unit's code inside
// an array-of-objects shape, tried in order. Not evidenced; see ServingUnits'
// doc comment.
func servingUnitCodeKeys() []string {
	return []string{"code", "unitCode", "unit", "abbreviation", "value"}
}

// decodeServingUnits tolerantly decodes every shape ServingUnits' doc comment
// documents. An empty or absent body, or a shape this package does not
// recognize, decodes to no units rather than an error.
func decodeServingUnits(raw json.RawMessage) client.List[client.Text] {
	if len(raw) == 0 {
		return client.List[client.Text]{}
	}

	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err == nil {
		return decodeServingUnitItems(items)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return client.List[client.Text]{}
	}
	for _, key := range servingUnitWrapperKeys() {
		wrapped, ok := fields[key]
		if !ok {
			continue
		}
		var inner []json.RawMessage
		if err := json.Unmarshal(wrapped, &inner); err == nil {
			return decodeServingUnitItems(inner)
		}
	}
	// A single key this package does not otherwise recognize is still worth
	// trying: the earlier, stricter behavior required exactly one key before
	// decoding it, and a wrapper this narrow is still a plausible catalog
	// envelope even under an unnamed key.
	if len(fields) == 1 {
		for _, wrapped := range fields {
			var inner []json.RawMessage
			if err := json.Unmarshal(wrapped, &inner); err == nil {
				return decodeServingUnitItems(inner)
			}
		}
	}
	return client.List[client.Text]{}
}

// decodeServingUnitItems decodes each catalog entry defensively: a bare
// string is used directly; an object has its code read from the first of
// servingUnitCodeKeys present. An entry that decodes as neither is skipped
// rather than failing the whole catalog.
func decodeServingUnitItems(items []json.RawMessage) client.List[client.Text] {
	if len(items) > maxServingUnits {
		items = items[:maxServingUnits]
	}
	texts := make([]client.Text, 0, len(items))
	for _, item := range items {
		var text client.Text
		if err := json.Unmarshal(item, &text); err == nil && text.IsSet() {
			texts = append(texts, text)
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(item, &fields); err != nil {
			continue
		}
		if code := decodeFirstText(fields, servingUnitCodeKeys()...); code.IsSet() {
			texts = append(texts, code)
		}
	}
	return client.NewList(texts...)
}
