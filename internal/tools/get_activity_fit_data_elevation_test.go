package tools

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// The profile summary the descent fixture writes into its session message.
const (
	fitTestDescent    = 43
	fitTestMaxCadence = 96
)

// fitDescendingRide builds a file whose device summary carries a descent and a peak
// cadence, so the tool result can be checked against the figures the device wrote
// rather than against anything derived.
func fitDescendingRide(seconds int) []byte {
	samples := make([]testkit.FITSample, 0, seconds)
	for second := range seconds {
		altitude := 700 - float64(second)
		samples = append(samples, testkit.FITSample{
			Second:    second,
			Power:     new(fitTestPower),
			Cadence:   new(fitTestCadence),
			HeartRate: new(fitTestHeartRate),
			Altitude:  &altitude,
			Distance:  new(10 * float64(second)),
		})
	}
	file := testkit.FITFile{Sport: 2, Session: true, Samples: samples, Summary: &testkit.FITSummaryFixture{
		AscentMeters:  new(12),
		DescentMeters: new(fitTestDescent),
		AvgCadence:    new(fitTestCadence),
		MaxCadence:    new(fitTestMaxCadence),
	}}
	return testkit.ZipFIT("activity.fit", file.Bytes())
}

// TestFITDataReportsDescentAndPeakCadence proves the two figures upstream reports
// and this server once dropped reach the rendered result, keyed as the schema names
// them, and that they carry the device's own summary rather than a derived number.
func TestFITDataReportsDescentAndPeakCadence(t *testing.T) {
	t.Parallel()

	h := newFITHarness(t, fitScript(fitDescendingRide(600)), Bounds{})
	data, err := h.svc.activityFITData(h.ctx, activityFITInput{ActivityID: int64(fitTestActivity)})
	if err != nil {
		t.Fatalf("activityFITData() = %v", err)
	}
	if len(data.Sessions) != 1 {
		t.Fatalf("%d sessions, want one", len(data.Sessions))
	}

	for name, segment := range map[string]FITSegmentView{
		"overall": data.Overall,
		"session": data.Sessions[0],
	} {
		rendered := map[string]any{}
		encoded, err := json.Marshal(segment)
		if err != nil {
			t.Fatalf("json.Marshal(%s) = %v", name, err)
		}
		if err := json.Unmarshal(encoded, &rendered); err != nil {
			t.Fatalf("json.Unmarshal(%s) = %v", name, err)
		}
		if rendered["descent_meters"] != float64(fitTestDescent) {
			t.Errorf("%s descent_meters = %v, want the device's %d",
				name, rendered["descent_meters"], fitTestDescent)
		}
		if rendered["max_cadence"] != float64(fitTestMaxCadence) {
			t.Errorf("%s max_cadence = %v, want the device's %d",
				name, rendered["max_cadence"], fitTestMaxCadence)
		}
	}
}

// TestFITDataReportsEachTruncationSeparately proves the result says which bound was
// hit, because the two mean different things: a cut sample stream voids the derived
// figures, and a cut span list voids the fold. One flag for both could not say which.
func TestTruncatedFITDataReportsEachTruncationSeparately(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, time.January, 2, 8, 0, 0, 0, time.UTC)
	records := make([]api.FITRecord, 0, 60)
	for second := range 60 {
		records = append(records, api.FITRecord{
			Time:     base.Add(time.Duration(second) * time.Second),
			Power:    api.FITNumber{Value: 200, OK: true},
			Distance: api.FITNumber{Value: 10 * float64(second), OK: true},
		})
	}

	data, err := newFITData(t.Context(), fitTestActivity, 1024, api.FITActivity{
		Records:           records,
		RecordsTruncated:  true,
		SessionsTruncated: true,
	}, false)
	if err != nil {
		t.Fatalf("newFITData() = %v", err)
	}

	if !data.SamplesTruncated || !data.SessionsTruncated {
		t.Errorf("samples_truncated=%v sessions_truncated=%v, want both reported separately",
			data.SamplesTruncated, data.SessionsTruncated)
	}
	// The flags explain an absence rather than qualify a number.
	if data.Overall.DistanceMeters != nil {
		t.Errorf("overall distance = %v, want absence beside the flag", *data.Overall.DistanceMeters)
	}
}
