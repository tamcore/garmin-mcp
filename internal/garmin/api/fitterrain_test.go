package api_test

import (
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// TestDetectClimbsReportsTheSustainedAscent proves a steady climb is found once,
// with the vertical ascent rate the altitude stream implies.
func TestDetectClimbsReportsTheSustainedAscent(t *testing.T) {
	t.Parallel()

	summary := analyzeRide(t, rideFile(600))
	if len(summary.Climbs) != 1 {
		t.Fatalf("%d climbs, want 1", len(summary.Climbs))
	}

	climb := summary.Climbs[0]
	if climb.GainMeters != 599 || climb.Seconds != 599 {
		t.Errorf("climb = %+v, want 599 meters over 599 seconds", climb)
	}
	if !nearly(climb.VAM, 3600) {
		t.Errorf("VAM = %v, want 3600 meters per hour", climb.VAM)
	}
	if !climb.AvgPower.OK || climb.AvgPower.Value != ridePower {
		t.Errorf("climb power = %+v, want %d", climb.AvgPower, ridePower)
	}
}

// TestDetectClimbsIgnoresWhatIsTooShort keeps a rise over a road bump from being
// reported as a climb.
func TestDetectClimbsIgnoresWhatIsTooShort(t *testing.T) {
	t.Parallel()

	samples := make([]testkit.FITSample, 0, 60)
	for second := range 60 {
		grade, altitude := 0.0, 100.0
		if second >= 10 && second < 25 {
			grade, altitude = 8.0, 100.0+float64(second-10)
		}
		samples = append(samples, testkit.FITSample{
			Second:   second,
			Grade:    new(grade),
			Altitude: new(altitude),
			Distance: new(rideMetersPerS * float64(second)),
		})
	}

	if climbs := analyzeRide(t, testkit.FITFile{Samples: samples}).Climbs; len(climbs) != 0 {
		t.Errorf("climbs = %+v, want none for a fifteen second rise", climbs)
	}
}

// TestGradeBandsSplitTheRideBySteepness proves the terrain bands are cut at the
// declared edges and that only the bands the ride entered are reported.
func TestGradeBandsSplitTheRideBySteepness(t *testing.T) {
	t.Parallel()

	samples := make([]testkit.FITSample, 0, 120)
	for second := range 120 {
		grade := 0.0
		if second >= 60 {
			grade = 8.0
		}
		samples = append(samples, testkit.FITSample{
			Second: second,
			Grade:  new(grade),
			Power:  new(ridePower),
		})
	}

	bands := analyzeRide(t, testkit.FITFile{Samples: samples}).GradeBands
	if len(bands) != 2 {
		t.Fatalf("bands = %+v, want the two the ride entered", bands)
	}
	seen := map[string]float64{}
	for _, band := range bands {
		seen[band.Label] = band.Seconds
	}
	if seen["flat"] != 59 || seen["steep_climb"] != 60 {
		t.Errorf("bands = %+v, want 59 flat seconds and 60 steep ones", seen)
	}
}

// TestGradeBandsCapOneSampleGap proves a paused recorder cannot credit a band with
// the whole pause.
func TestGradeBandsCapOneSampleGap(t *testing.T) {
	t.Parallel()

	samples := []testkit.FITSample{
		{Second: 0, Grade: new(float64(0))},
		{Second: 600, Grade: new(float64(0))},
	}

	bands := analyzeRide(t, testkit.FITFile{Samples: samples}).GradeBands
	if len(bands) != 1 || bands[0].Seconds != 10 {
		t.Errorf("bands = %+v, want one band credited with the ten second cap", bands)
	}
}

// TestTemperatureSplitComparesTheEndsOfTheRange proves the hot and cool quarters are
// summarized from the temperature order, not from the ride order.
func TestTemperatureSplitComparesTheEndsOfTheRange(t *testing.T) {
	t.Parallel()

	samples := make([]testkit.FITSample, 0, 40)
	for second := range 40 {
		degrees, beats := 10, 120
		if second >= 20 {
			degrees, beats = 30, 150
		}
		samples = append(samples, testkit.FITSample{
			Second:      second,
			Temperature: new(degrees),
			HeartRate:   new(beats),
		})
	}

	split := analyzeRide(t, testkit.FITFile{Samples: samples}).Temperature
	if !split.OK || split.Samples != 40 {
		t.Fatalf("split = %+v, want a comparison over 40 samples", split)
	}
	if split.HotAvgC != 30 || split.CoolAvgC != 10 {
		t.Errorf("split = %+v, want 30 against 10 degrees", split)
	}
	if split.HotHeartRate.Value != 150 || split.CoolHeartRate.Value != 120 {
		t.Errorf("split heart rates = %+v, want 150 against 120", split)
	}
}

// TestTemperatureSplitNeedsEnoughSamples keeps a two-sample ride from producing a
// comparison that says nothing.
func TestTemperatureSplitNeedsEnoughSamples(t *testing.T) {
	t.Parallel()

	samples := []testkit.FITSample{
		{Second: 0, Temperature: new(10)},
		{Second: 1, Temperature: new(20)},
	}
	if split := analyzeRide(t, testkit.FITFile{Samples: samples}).Temperature; split.OK {
		t.Errorf("split = %+v, want none from two samples", split)
	}
}

// TestHeartRateDriftReportsDecouplingForALongRide proves the decoupling figure, and
// its sign: a ride whose power falls while the heart rate holds decouples positively.
func TestHeartRateDriftReportsDecouplingForALongRide(t *testing.T) {
	t.Parallel()

	const seconds = 3700
	samples := make([]testkit.FITSample, 0, seconds)
	for second := range seconds {
		watts := 200
		if second >= seconds/2 {
			watts = 180
		}
		samples = append(samples, testkit.FITSample{
			Second:    second,
			Power:     new(watts),
			HeartRate: new(rideHeartRate),
		})
	}

	drift := analyzeRide(t, testkit.FITFile{Samples: samples}).Drift
	if !drift.OK {
		t.Fatalf("drift = %+v, want a decoupling for a ride over an hour", drift)
	}
	if !nearly(drift.Percent, 10) {
		t.Errorf("decoupling = %v percent, want 10", drift.Percent)
	}
}

// TestHeartRateDriftNeedsAnHour keeps a short ride from reporting a figure the
// measure does not apply to.
func TestHeartRateDriftNeedsAnHour(t *testing.T) {
	t.Parallel()

	if drift := analyzeRide(t, rideFile(600)).Drift; drift.OK {
		t.Errorf("drift = %+v, want none for a ten minute ride", drift)
	}
}

// TestAnalyzeFITDerivesTheGradeWhenTheDeviceOmitsIt proves a file without a grade
// field still produces terrain bands, from the altitude and distance deltas.
func TestAnalyzeFITDerivesTheGradeWhenTheDeviceOmitsIt(t *testing.T) {
	t.Parallel()

	samples := make([]testkit.FITSample, 0, 120)
	for second := range 120 {
		samples = append(samples, testkit.FITSample{
			Second:   second,
			Altitude: new(rideBaseAlt + float64(second)),
			Distance: new(rideMetersPerS * float64(second)),
		})
	}

	summary := analyzeRide(t, testkit.FITFile{Samples: samples})
	if len(summary.GradeBands) != 1 || summary.GradeBands[0].Label != "very_steep_climb" {
		t.Errorf("bands = %+v, want the derived ten percent band", summary.GradeBands)
	}
	if len(summary.Climbs) != 1 {
		t.Errorf("climbs = %+v, want the derived grade to find the climb", summary.Climbs)
	}
}

// TestFITNumberIsAbsentUntilRead is the guard against a zero reading being mistaken
// for a recorded one anywhere in the analysis.
func TestFITNumberIsAbsentUntilRead(t *testing.T) {
	t.Parallel()

	var value api.FITNumber
	if value.OK || value.Value != 0 {
		t.Errorf("the zero FITNumber = %+v, want an absent reading", value)
	}
}
