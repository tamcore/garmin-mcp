package api

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// rangeTargetDescending maps each range target type whose display order was
// verified in Garmin Connect to whether targetValueOne holds the larger value.
// Garmin shows a pair in stored order, so a reversed pair reads backwards. Pace is
// stored in m/s with the faster speed first; speed and heart rate are low first.
var rangeTargetDescending = map[string]bool{
	"pace.zone":       true,
	"speed.zone":      false,
	"heart.rate.zone": false,
}

// rangeTargetFields names each step-target triple that can carry a range.
var rangeTargetFields = [][3]string{
	{"targetType", "targetValueOne", "targetValueTwo"},
	{"secondaryTargetType", "secondaryTargetValueOne", "secondaryTargetValueTwo"},
}

// orderRangeTargets returns body with every known range target in the order
// Garmin Connect writes it. A body with no reversed pair is returned unchanged,
// byte for byte.
func orderRangeTargets(body []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var tree any
	if err := decoder.Decode(&tree); err != nil {
		return nil, fmt.Errorf("%w: a workout document must be valid JSON", client.ErrValidation)
	}
	if !swapReversedRangeTargets(tree) {
		return body, nil
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(tree); err != nil {
		return nil, fmt.Errorf("encoding workout document: %w", err)
	}
	return bytes.TrimSpace(out.Bytes()), nil
}

// swapReversedRangeTargets walks a freshly decoded document and reports whether
// it swapped any pair.
func swapReversedRangeTargets(node any) bool {
	swapped := false
	switch value := node.(type) {
	case []any:
		for _, item := range value {
			swapped = swapReversedRangeTargets(item) || swapped
		}
	case map[string]any:
		for _, fields := range rangeTargetFields {
			swapped = swapRangeTarget(value, fields) || swapped
		}
		for _, item := range value {
			swapped = swapReversedRangeTargets(item) || swapped
		}
	}
	return swapped
}

func swapRangeTarget(step map[string]any, fields [3]string) bool {
	target, _ := step[fields[0]].(map[string]any)
	key, _ := target["workoutTargetTypeKey"].(string)
	descending, known := rangeTargetDescending[key]
	if !known {
		return false
	}
	one, okOne := step[fields[1]].(json.Number)
	two, okTwo := step[fields[2]].(json.Number)
	if !okOne || !okTwo {
		return false
	}
	first, errOne := one.Float64()
	second, errTwo := two.Float64()
	if errOne != nil || errTwo != nil || first == second || (first > second) == descending {
		return false
	}
	step[fields[1]], step[fields[2]] = two, one
	return true
}
