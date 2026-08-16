package api_test

import (
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// descentBaseAlt is the altitude the synthetic descending file starts from, high
// enough that a 599 metre drop stays above sea level.
const descentBaseAlt = 700.0

// flatAltitude is the unchanging altitude of the file that measured no move.
const flatAltitude = 100.0

// TestAnalyzeFITDerivesTheDescentFromTheRecordStream proves the fallback: a file
// without a session summary reports a descent derived by the same walk as the
// ascent, in the other direction, and reports the peak cadence of the stream.
func TestAnalyzeFITDerivesTheDescentFromTheRecordStream(t *testing.T) {
	t.Parallel()

	samples := rideSamples(600)
	for index := range samples {
		// A steady drop of one metre per second, mirroring the climb rideSamples
		// builds, so the derived descent must equal the derived ascent of a climb.
		dropped := descentBaseAlt - rideClimbPerS*float64(index)
		samples[index].Altitude = &dropped
	}

	overall := analyzeRide(t, testkit.FITFile{Sport: 2, Session: true, Samples: samples}).Overall
	// The threshold banks a drop only once it clears three metres, so a 599 metre
	// descent reports the 597 already banked — the same arithmetic the ascent test
	// pins for a climb.
	if !overall.Descent.OK || overall.Descent.Value != 597 {
		t.Errorf("descent = %+v, want the 597 banked metres of a 599 metre drop", overall.Descent)
	}
	if overall.Ascent.OK && overall.Ascent.Value != 0 {
		t.Errorf("ascent = %+v, want none on a file that only falls", overall.Ascent)
	}
	if !overall.MaxCadence.OK || overall.MaxCadence.Value != rideCadence {
		t.Errorf("peak cadence = %+v, want the stream's %d", overall.MaxCadence, rideCadence)
	}
}

// TestElevationIsAbsentWithoutAnAltitudeSeries proves a file whose records carry no
// altitude reports no ascent and no descent, rather than a zero.
//
// A zero here would be a claim: it would say the activity gained and lost nothing,
// which is a measurement a file without a barometer never made. A treadmill run and
// a genuinely flat outdoor one would be indistinguishable in the result.
func TestElevationIsAbsentWithoutAnAltitudeSeries(t *testing.T) {
	t.Parallel()

	samples := rideSamples(60)
	for index := range samples {
		samples[index].Altitude = nil
	}

	overall := analyzeRide(t, testkit.FITFile{Sport: 2, Session: true, Samples: samples}).Overall
	for name, reading := range map[string]api.FITNumber{
		nameAscent:  overall.Ascent,
		nameDescent: overall.Descent,
	} {
		if reading.OK {
			t.Errorf("%s = %+v, want absence on a file that recorded no altitude", name, reading)
		}
	}
	// The rest of the segment is unaffected: one absent sensor is not an absent
	// activity.
	if overall.Samples != 60 || !overall.AvgPower.OK {
		t.Errorf("segment = %d samples, average power %+v, want the ride still summarized",
			overall.Samples, overall.AvgPower)
	}
}

// TestFlatTerrainReportsAMeasuredZero is the other half of the rule, and the reason
// the fix is not "treat zero as absent": a stream that did carry altitude and did
// not move reports 0, because that zero is a measurement.
func TestFlatTerrainReportsAMeasuredZero(t *testing.T) {
	t.Parallel()

	samples := rideSamples(60)
	for index := range samples {
		level := flatAltitude
		samples[index].Altitude = &level
	}

	overall := analyzeRide(t, testkit.FITFile{Sport: 2, Session: true, Samples: samples}).Overall
	for name, reading := range map[string]api.FITNumber{
		nameAscent:  overall.Ascent,
		nameDescent: overall.Descent,
	} {
		if !reading.OK || reading.Value != 0 {
			t.Errorf("%s = %+v, want a measured zero on flat terrain", name, reading)
		}
	}
}
