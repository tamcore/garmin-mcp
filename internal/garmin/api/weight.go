package api

import (
	"fmt"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// Weight reads and writes the weigh-in surface: add_weigh_in,
// add_weigh_in_with_timestamps, get_weigh_ins, get_daily_weigh_ins and
// delete_weigh_ins.
//
// Source: add_weigh_in, add_weigh_in_with_timestamps, get_weigh_ins,
// get_daily_weigh_ins, delete_weigh_in and delete_weigh_ins in
// python-garminconnect 0.3.10 garminconnect/__init__.py, cross-checked against
// the Taxuspt pinned curation at src/garmin_mcp/weight_management.py, which is
// the only source for the get_weigh_ins/get_daily_weigh_ins response field
// spellings — upstream 0.3.10 never reads a field out of either response
// itself.
//
// Every document this client reads or writes is health data: a body weight, a
// body-fat percentage and a muscle-mass figure are all readings tied to a
// person, so no model here is ever logged with its content, only its shape.
type Weight struct {
	req requester
}

// NewWeight returns a weight-management client over the request layer.
func NewWeight(rc *client.Client) (*Weight, error) {
	req, err := newRequester(rc)
	if err != nil {
		return nil, err
	}
	return &Weight{req: req}, nil
}

// WeightUnit is a validated weigh-in unit token.
//
// Source: garminconnect/__init__.py, VALID_WEIGHT_UNITS = {"kg", "lbs"},
// checked in both add_weigh_in and add_weigh_in_with_timestamps before either
// dispatches anything. The Taxuspt tool docstrings advertise "kg or lb" for
// unit_key, but the library enforces "lbs"; this type follows the library,
// which is what actually reaches Garmin's wire, not the docstring.
type WeightUnit string

// The two unit tokens Garmin's weigh-in write accepts.
const (
	WeightUnitKg  WeightUnit = client.WeightUnitKg
	WeightUnitLbs WeightUnit = client.WeightUnitLbs
)

// ParseWeightUnit validates a weigh-in unit token against the two Garmin
// recognizes.
func ParseWeightUnit(value string) (WeightUnit, error) {
	switch WeightUnit(value) {
	case WeightUnitKg, WeightUnitLbs:
		return WeightUnit(value), nil
	default:
		return "", fmt.Errorf("%w: unit must be %q or %q",
			client.ErrValidation, WeightUnitKg, WeightUnitLbs)
	}
}
