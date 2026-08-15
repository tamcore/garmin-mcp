package api_test

import (
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// This file covers the power analysis the FIT profile does not carry: the power
// duration curve, and the standard duration set it reports.

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
