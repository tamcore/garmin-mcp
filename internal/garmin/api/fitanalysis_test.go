package api_test

import (
	"math"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// nearly compares two derived figures, which are computed in floating point.
func nearly(got, want float64) bool { return math.Abs(got-want) < 1e-6 }

// analyzeRide decodes and analyses one synthetic file.
func analyzeRide(t *testing.T, file testkit.FITFile) api.FITSummary {
	t.Helper()

	activity, err := api.ParseFITActivity(file.Bytes(), api.FITLimits{})
	if err != nil {
		t.Fatalf("ParseFITActivity() = %v", err)
	}
	return api.AnalyzeFIT(activity)
}

// TestAnalyzeFITSummarizesTheWholeActivity pins the derived figures of a steady
// ride, where every one of them is known in advance.
func TestAnalyzeFITSummarizesTheWholeActivity(t *testing.T) {
	t.Parallel()

	summary := analyzeRide(t, rideFile(600))
	overall := summary.Overall
	if summary.Sport != "cycling" {
		t.Errorf("sport = %q, want cycling", summary.Sport)
	}
	if overall.Samples != 600 || overall.Seconds != 599 {
		t.Errorf("samples = %d over %v seconds, want 600 over 599", overall.Samples, overall.Seconds)
	}
	if !overall.AvgPower.OK || overall.AvgPower.Value != ridePower {
		t.Errorf("average power = %+v, want %d", overall.AvgPower, ridePower)
	}
	if !overall.MaxHeartRate.OK || overall.MaxHeartRate.Value != rideHeartRate {
		t.Errorf("max heart rate = %+v, want %d", overall.MaxHeartRate, rideHeartRate)
	}
	if !overall.Distance.OK || overall.Distance.Value != rideMetersPerS*599 {
		t.Errorf("distance = %+v, want %v", overall.Distance, rideMetersPerS*599)
	}
	// The file carries no session summary, so the ascent is derived. A derived
	// ascent banks a rise only once it clears the three metre noise threshold, so
	// the 599 metre climb reports the 597 metres already banked and holds the rest.
	if !overall.Ascent.OK || overall.Ascent.Value != 597 {
		t.Errorf("ascent = %+v, want the 597 banked metres of a 599 metre climb", overall.Ascent)
	}
}

// TestAnalyzeFITPrefersTheProfileSummary proves a figure the device wrote into the
// session message wins over the one the record stream implies. The two disagree on
// purpose here: the records describe a steady climb, and the summary says otherwise.
func TestAnalyzeFITPrefersTheProfileSummary(t *testing.T) {
	t.Parallel()

	file := rideFile(600)
	file.Summary = &testkit.FITSummaryFixture{
		ElapsedSeconds:  new(612.5),
		DistanceMeters:  new(6120.25),
		AscentMeters:    new(63),
		Calories:        new(877),
		AvgHeartRate:    new(169),
		MaxHeartRate:    new(191),
		AvgCadence:      new(88),
		AvgPower:        new(355),
		MaxPower:        new(612),
		NormalizedPower: new(389),
	}

	overall := analyzeRide(t, file).Overall
	for name, got := range map[string]struct {
		reading api.FITNumber
		want    float64
	}{
		"distance":      {overall.Distance, 6120.25},
		"ascent":        {overall.Ascent, 63},
		"calories":      {overall.Calories, 877},
		"average power": {overall.AvgPower, 355},
		"peak power":    {overall.MaxPower, 612},
		"normalized":    {overall.NormalizedPw, 389},
		"cadence":       {overall.AvgCadence, 88},
		"average heart": {overall.AvgHeartRate, 169},
		"peak heart":    {overall.MaxHeartRate, 191},
	} {
		if !got.reading.OK || got.reading.Value != got.want {
			t.Errorf("%s = %+v, want the profile's %v", name, got.reading, got.want)
		}
	}
	if overall.Seconds != 612.5 {
		t.Errorf("seconds = %v, want the profile's 612.5", overall.Seconds)
	}
	if !nearly(overall.Variability.Value, 389.0/355.0) {
		t.Errorf("variability index = %+v, want it recomputed from the profile figures",
			overall.Variability)
	}
}

// TestAnalyzeFITReadsTheLapSummaryNumbering proves the lap message is read with the
// lap message's own field numbers. A lap numbers average heart rate 15 where a
// session numbers it 16, which is exactly the class of mistake the profile prevents.
func TestAnalyzeFITReadsTheLapSummaryNumbering(t *testing.T) {
	t.Parallel()

	file := rideFile(60)
	file.Laps = []testkit.FITLapFixture{{StartSecond: 0, EndSecond: 59}}
	file.LapSummary = &testkit.FITSummaryFixture{
		DistanceMeters: new(1234.5),
		AscentMeters:   new(17),
		AvgHeartRate:   new(151),
		MaxHeartRate:   new(163),
	}

	summary := analyzeRide(t, file)
	if len(summary.Laps) != 1 {
		t.Fatalf("%d lap summaries, want 1", len(summary.Laps))
	}
	lap := summary.Laps[0]
	if !lap.AvgHeartRate.OK || lap.AvgHeartRate.Value != 151 {
		t.Errorf("lap average heart rate = %+v, want 151", lap.AvgHeartRate)
	}
	if !lap.MaxHeartRate.OK || lap.MaxHeartRate.Value != 163 {
		t.Errorf("lap peak heart rate = %+v, want 163", lap.MaxHeartRate)
	}
	if !lap.Distance.OK || lap.Distance.Value != 1234.5 {
		t.Errorf("lap distance = %+v, want 1234.5", lap.Distance)
	}
	if !lap.Ascent.OK || lap.Ascent.Value != 17 {
		t.Errorf("lap ascent = %+v, want 17", lap.Ascent)
	}
}

// TestAnalyzeFITComputesNormalizedPowerAndVariability proves the two derived power
// figures: a perfectly steady ride normalizes to its average and has an index of one.
func TestAnalyzeFITComputesNormalizedPowerAndVariability(t *testing.T) {
	t.Parallel()

	overall := analyzeRide(t, rideFile(600)).Overall
	if !overall.NormalizedPw.OK || !nearly(overall.NormalizedPw.Value, ridePower) {
		t.Errorf("normalized power = %+v, want %d for a steady ride", overall.NormalizedPw, ridePower)
	}
	if !overall.Variability.OK || !nearly(overall.Variability.Value, 1) {
		t.Errorf("variability index = %+v, want 1 for a steady ride", overall.Variability)
	}
}

// TestAnalyzeFITAveragesCyclingDynamics proves the pedal metrics reach the segment
// summary, and that a file without them reports none rather than zeroes.
func TestAnalyzeFITAveragesCyclingDynamics(t *testing.T) {
	t.Parallel()

	dynamics := analyzeRide(t, rideFile(60)).Overall.Dynamics
	if !dynamics.Present() {
		t.Fatal("Present() = false, want the recorded pedal metrics")
	}
	if dynamics.RightBalance.Value != 52 || dynamics.LeftTorque.Value != rideTorqueEff {
		t.Errorf("dynamics = %+v, want a 52 percent share and %v torque", dynamics, rideTorqueEff)
	}

	bare := testkit.FITFile{Samples: []testkit.FITSample{{Second: 0}, {Second: 1}}}
	if analyzeRide(t, bare).Overall.Dynamics.Present() {
		t.Error("Present() = true for a file without dynamics, want false")
	}
}

// TestAnalyzeFITCutsSegmentsFromTheLapWindows proves a lap summary covers only the
// records inside that lap.
func TestAnalyzeFITCutsSegmentsFromTheLapWindows(t *testing.T) {
	t.Parallel()

	file := rideFile(60)
	file.Laps = []testkit.FITLapFixture{
		{StartSecond: 0, EndSecond: 29},
		{StartSecond: 30, EndSecond: 59},
	}

	summary := analyzeRide(t, file)
	if len(summary.Laps) != 2 {
		t.Fatalf("%d lap summaries, want 2", len(summary.Laps))
	}
	for index, lap := range summary.Laps {
		if lap.Samples != 30 {
			t.Errorf("lap %d covers %d samples, want 30", index, lap.Samples)
		}
	}
	if len(summary.Sessions) != 1 || summary.Sessions[0].Samples != 60 {
		t.Errorf("session summaries = %+v, want one covering 60 samples", summary.Sessions)
	}
}

// TestAnalyzeFITHandlesAnEmptyStream proves an activity file with no records is a
// zero summary rather than a panic or a set of not-a-number readings.
func TestAnalyzeFITHandlesAnEmptyStream(t *testing.T) {
	t.Parallel()

	summary := api.AnalyzeFIT(api.FITActivity{})
	if summary.Overall.Samples != 0 || len(summary.Curve) != 0 || len(summary.Climbs) != 0 {
		t.Errorf("summary = %+v, want an empty one", summary)
	}
	if summary.Drift.OK || summary.Temperature.OK {
		t.Errorf("derived comparisons = %+v %+v, want neither", summary.Drift, summary.Temperature)
	}
}
