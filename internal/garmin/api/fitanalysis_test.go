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
	if !overall.Ascent.OK || overall.Ascent.Value != 599 {
		t.Errorf("ascent = %+v, want 599", overall.Ascent)
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

// TestPowerDurationCurveReportsEveryFittingWindow pins the curve of a steady ride:
// every window that fits reports the ride's power, and a window that does not fit is
// left out rather than reported from a shorter sample.
func TestPowerDurationCurveReportsEveryFittingWindow(t *testing.T) {
	t.Parallel()

	summary := analyzeRide(t, rideFile(600))
	if len(summary.Curve) != 5 {
		t.Fatalf("curve = %+v, want the five windows that fit in ten minutes", summary.Curve)
	}
	for _, best := range summary.Curve {
		if best.Watts != ridePower {
			t.Errorf("%ds best = %v watts, want %d", best.Seconds, best.Watts, ridePower)
		}
	}
	if got := summary.Curve[len(summary.Curve)-1].Seconds; got != 600 {
		t.Errorf("longest window = %ds, want 600", got)
	}
}

// TestPowerDurationCurveFindsThePeakWindow proves the curve reports the best window
// and where it started, not the first one or the average.
func TestPowerDurationCurveFindsThePeakWindow(t *testing.T) {
	t.Parallel()

	samples := make([]testkit.FITSample, 0, 60)
	for second := range 60 {
		watts := 100
		if second >= 30 && second < 35 {
			watts = 400
		}
		samples = append(samples, testkit.FITSample{Second: second, Power: new(watts)})
	}

	summary := analyzeRide(t, testkit.FITFile{Samples: samples})
	best := summary.Curve[0]
	if best.Seconds != 5 || best.Watts != 400 || best.StartOffset != 30 {
		t.Errorf("five second best = %+v, want 400 watts starting at 30 seconds", best)
	}
}

// TestPowerDurationCurveIsEmptyWithoutPower keeps a ride recorded without a power
// meter from reporting a curve of zeroes.
func TestPowerDurationCurveIsEmptyWithoutPower(t *testing.T) {
	t.Parallel()

	samples := make([]testkit.FITSample, 0, 60)
	for second := range 60 {
		samples = append(samples, testkit.FITSample{
			Second: second, HeartRate: new(rideHeartRate),
		})
	}

	summary := analyzeRide(t, testkit.FITFile{Samples: samples})
	if len(summary.Curve) != 0 {
		t.Errorf("curve = %+v, want none without a power meter", summary.Curve)
	}
	if summary.Overall.NormalizedPw.OK || summary.Overall.AvgPower.OK {
		t.Errorf("power figures = %+v, want none", summary.Overall)
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

// TestCurveDurationsIsACopy keeps a caller from reaching into the package's own
// duration set.
func TestCurveDurationsIsACopy(t *testing.T) {
	t.Parallel()

	first := api.CurveDurations()
	first[0] = 9999
	if api.CurveDurations()[0] != 5 {
		t.Error("CurveDurations() handed out its own backing array")
	}
}
