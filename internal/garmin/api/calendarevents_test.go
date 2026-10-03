package api_test

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
)

// TestCalendarItemDistanceMetersScalesTheUnit pins the unit table
// (calendar_events.py:22-27, :69-78): a distance goal is reported in metres, and
// an unknown or missing unit reports no distance.
func TestCalendarItemDistanceMetersScalesTheUnit(t *testing.T) {
	t.Parallel()

	type result struct {
		want float64
		ok   bool
	}
	for target, want := range map[string]result{
		`{"unitType":"distance","unit":"meter","value":5000}`:     {5000, true},
		`{"unitType":"distance","unit":"kilometer","value":21.1}`: {21100, true},
		`{"unitType":"distance","unit":"yard","value":100}`:       {91.44, true},
		`{"unitType":"distance","unit":"mile","value":26.2}`:      {42164.8128, true},
		`{"unitType":"distance","unit":"furlong","value":8}`:      {},
		`{"unitType":"distance","value":5000}`:                    {},
		`{"unitType":"duration","unit":"meter","value":5000}`:     {},
	} {
		var item api.CalendarItem
		if err := json.Unmarshal([]byte(`{"completionTarget":`+target+`}`), &item); err != nil {
			t.Fatalf("decode %s: %v", target, err)
		}
		got, ok := item.DistanceMeters()
		if ok != want.ok || math.Abs(got-want.want) > 1e-9 {
			t.Errorf("%s: DistanceMeters() = %v/%v, want %v/%v", target, got, ok, want.want, want.ok)
		}
	}
}
