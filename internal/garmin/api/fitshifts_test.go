package api_test

import (
	"testing"

	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// TestShiftsAreClassifiedByCadence pins the four upstream quality bands against the
// cadence the record stream carries at the shift.
func TestShiftsAreClassifiedByCadence(t *testing.T) {
	t.Parallel()

	cadences := map[int]int{10: 90, 20: 50, 30: 0, 40: 120}
	samples := make([]testkit.FITSample, 0, 60)
	for second := range 60 {
		cadence := 80
		if value, ok := cadences[second]; ok {
			cadence = value
		}
		samples = append(samples, testkit.FITSample{
			Second:  second,
			Cadence: new(cadence),
			Grade:   new(2.5),
		})
	}

	file := testkit.FITFile{Samples: samples, Shifts: []testkit.FITShiftFixture{
		{Second: 10, RearGear: 7, FrontGear: 2},
		{Second: 20, RearGear: 8, FrontGear: 2},
		{Second: 30, RearGear: 9, FrontGear: 2},
		{Second: 40, Front: true, RearGear: 9, FrontGear: 1},
	}}

	shifts := analyzeRide(t, file).Shifts
	if shifts.Total != 4 || shifts.Front != 1 || shifts.Rear != 3 {
		t.Errorf("shifts = %+v, want four changes, one of them at the front", shifts)
	}
	if shifts.Proactive != 1 || shifts.Reactive != 1 || shifts.Coasting != 1 || shifts.SpunOut != 1 {
		t.Errorf("shifts = %+v, want one of each quality", shifts)
	}
	if shifts.Classified != 4 || len(shifts.Events) != 4 {
		t.Errorf("shifts = %+v, want every change classified and listed", shifts)
	}
	if got := shifts.Events[0]; got.Quality != "proactive" || got.Grade.Value != 2.5 {
		t.Errorf("first event = %+v, want a proactive shift at 2.5 percent", got)
	}
}

// TestAnUnmatchedShiftIsNotClassified keeps a gear change with no nearby sample from
// being given a quality it has no evidence for.
func TestAnUnmatchedShiftIsNotClassified(t *testing.T) {
	t.Parallel()

	file := testkit.FITFile{
		Samples: []testkit.FITSample{{Second: 0, Cadence: new(90)}},
		Shifts:  []testkit.FITShiftFixture{{Second: 300, RearGear: 5, FrontGear: 2}},
	}

	shifts := analyzeRide(t, file).Shifts
	if shifts.Total != 1 || shifts.Classified != 0 {
		t.Errorf("shifts = %+v, want the change counted but not classified", shifts)
	}
	if len(shifts.Events) != 1 || shifts.Events[0].Quality != "" {
		t.Errorf("events = %+v, want one unclassified event", shifts.Events)
	}
}
