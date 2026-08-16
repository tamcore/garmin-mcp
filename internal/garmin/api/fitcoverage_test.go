package api

import (
	"testing"
	"time"
)

// The figure names the truncation tests index their expectations by.
const (
	nameDistance = "distance"
	nameAscent   = "ascent"
	nameDescent  = "descent"
	namePeakPow  = "peak power"
	nameAvgHeart = "average heart"
)

// coverageBase is the synthetic instant the truncation fixtures start at.
func coverageBase() time.Time { return time.Date(2026, time.January, 2, 8, 0, 0, 0, time.UTC) }

// coverageRecords builds a steady synthetic stream, so every class of derived figure
// has something to report.
func coverageRecords(count int) []FITRecord {
	out := make([]FITRecord, 0, count)
	for second := range count {
		out = append(out, FITRecord{
			Time:      coverageBase().Add(time.Duration(second) * time.Second),
			Power:     fitNumber(200),
			Cadence:   fitNumber(90),
			HeartRate: fitNumber(140),
			Altitude:  fitNumber(100 + float64(second)),
			Distance:  fitNumber(10 * float64(second)),
		})
	}
	return out
}

// TestTruncatedRecordsLeaveDerivedFiguresAbsent proves the sample bound does not turn a
// prefix into the activity: reporting the first stretch's distance as the ride's is
// the same defect as folding a subset of sessions.
func TestTruncatedRecordsLeaveDerivedFiguresAbsent(t *testing.T) {
	t.Parallel()

	summary, err := AnalyzeFIT(t.Context(), FITActivity{
		Records:          coverageRecords(120),
		RecordsTruncated: true,
	})
	if err != nil {
		t.Fatalf("AnalyzeFIT() = %v", err)
	}

	overall := summary.Overall
	for name, reading := range map[string]FITNumber{
		nameDistance:    overall.Distance,
		nameAscent:      overall.Ascent,
		nameDescent:     overall.Descent,
		namePeakPow:     overall.MaxPower,
		"peak cadence":  overall.MaxCadence,
		"average power": overall.AvgPower,
		nameAvgHeart:    overall.AvgHeartRate,
		"normalized":    overall.NormalizedPw,
	} {
		if reading.OK {
			t.Errorf("%s = %+v, want absence: the record stream stopped at the bound",
				name, reading)
		}
	}
	// The count of what was analysed stays, because it is true of the prefix.
	if overall.Samples != 120 {
		t.Errorf("samples = %d, want the 120 actually analysed", overall.Samples)
	}
}

// TestTruncatedRecordsDropTheWholeStreamAggregates proves the same for the analyses
// that describe the ride rather than one segment of it. The lists are the exception on
// purpose: a detected climb happened, and the result says the list is not exhaustive.
func TestTruncatedRecordsDropTheWholeStreamAggregates(t *testing.T) {
	t.Parallel()

	summary, err := AnalyzeFIT(t.Context(), FITActivity{
		Records:          coverageRecords(4000),
		RecordsTruncated: true,
	})
	if err != nil {
		t.Fatalf("AnalyzeFIT() = %v", err)
	}

	if len(summary.Curve) != 0 {
		t.Errorf("%d curve entries, want none: a best over a prefix is a lower bound",
			len(summary.Curve))
	}
	if len(summary.GradeBands) != 0 {
		t.Errorf("%d grade bands, want none: seconds per band under-count", len(summary.GradeBands))
	}
	if summary.Drift.OK || summary.Temperature.OK {
		t.Errorf("drift %+v temperature %+v, want neither: each halves a prefix rather "+
			"than the ride", summary.Drift, summary.Temperature)
	}
	if len(summary.Climbs) == 0 {
		t.Error("climbs = none, want the detected list kept: each entry is true of itself")
	}
}

// TestTruncatedRecordsKeepACoveredLap proves the suppression is scoped to what the
// bound actually cut. A lap that ended before the last retained sample was measured in
// full, and a device summary was computed before this server saw the file at all.
func TestTruncatedRecordsKeepACoveredLap(t *testing.T) {
	t.Parallel()

	summary, err := AnalyzeFIT(t.Context(), FITActivity{
		Records:          coverageRecords(120),
		RecordsTruncated: true,
		Laps: []FITSpan{
			{Start: coverageBase(), End: coverageBase().Add(60 * time.Second)},
			{Start: coverageBase().Add(60 * time.Second), End: coverageBase().Add(400 * time.Second)},
		},
		Sessions: []FITSpan{{
			Start:    coverageBase(),
			End:      coverageBase().Add(400 * time.Second),
			Distance: fitNumber(4321),
		}},
	})
	if err != nil {
		t.Fatalf("AnalyzeFIT() = %v", err)
	}

	if len(summary.Laps) != 2 {
		t.Fatalf("%d laps, want 2", len(summary.Laps))
	}
	if !summary.Laps[0].Distance.OK {
		t.Error("covered lap distance = absent, want it kept: the bound cut nothing from it")
	}
	if summary.Laps[1].Distance.OK {
		t.Errorf("uncovered lap distance = %+v, want absence", summary.Laps[1].Distance)
	}
	// The device wrote this one, so a cut in the samples cannot touch it.
	if !summary.Overall.Distance.OK || summary.Overall.Distance.Value != 4321 {
		t.Errorf("overall distance = %+v, want the profile's 4321", summary.Overall.Distance)
	}
}

// TestTruncatedSpansAreNotFolded proves the span bound is not folded over.
//
// Calories is the sharp end: it has no derived route, so a partial sum would be the
// only figure a caller ever sees, and the per-session breakdown that would have exposed
// the gap is exactly what the same truncation removed.
func TestTruncatedSpansAreNotFolded(t *testing.T) {
	t.Parallel()

	summary, err := AnalyzeFIT(t.Context(), FITActivity{
		Records:           coverageRecords(120),
		SessionsTruncated: true,
		Sessions: []FITSpan{
			{
				Start: coverageBase(), End: coverageBase().Add(60 * time.Second),
				Calories: fitNumber(700), Ascent: fitNumber(40),
			},
			{
				Start: coverageBase().Add(60 * time.Second), End: coverageBase().Add(119 * time.Second),
				Calories: fitNumber(300), Ascent: fitNumber(20),
			},
		},
	})
	if err != nil {
		t.Fatalf("AnalyzeFIT() = %v", err)
	}

	if summary.Overall.Calories.OK {
		t.Errorf("overall calories = %+v, want absence: the session list is a subset, so "+
			"1000 is the total of only the sessions that survived the bound",
			summary.Overall.Calories)
	}
	// Ascent is not folded either, but the record stream can answer and was not cut.
	if !summary.Overall.Ascent.OK {
		t.Error("overall ascent = absent, want the record-derived fallback")
	}
	if summary.Overall.Ascent.Value == 60 {
		t.Error("overall ascent = 60, want the derived figure rather than the folded subset")
	}
}

// TestTruncatedLapsLeaveTheSessionFoldAlone proves lap truncation does not discard
// whole session figures the bound never touched.
func TestTruncatedLapsLeaveTheSessionFoldAlone(t *testing.T) {
	t.Parallel()

	summary, err := AnalyzeFIT(t.Context(), FITActivity{
		Records:       coverageRecords(120),
		LapsTruncated: true,
		Sessions: []FITSpan{
			{
				Start: coverageBase(), End: coverageBase().Add(60 * time.Second),
				Calories: fitNumber(700), Ascent: fitNumber(40),
			},
			{
				Start:    coverageBase().Add(60 * time.Second),
				End:      coverageBase().Add(119 * time.Second),
				Calories: fitNumber(300), Ascent: fitNumber(20),
			},
		},
	})
	if err != nil {
		t.Fatalf("AnalyzeFIT() = %v", err)
	}

	if !summary.Overall.Calories.OK || summary.Overall.Calories.Value != 1000 {
		t.Errorf("overall calories = %+v, want the folded 1000: the session list is whole",
			summary.Overall.Calories)
	}
	if !summary.Overall.Ascent.OK || summary.Overall.Ascent.Value != 60 {
		t.Errorf("overall ascent = %+v, want the folded 60", summary.Overall.Ascent)
	}
}

// TestUncoveredSegmentReportsNoEndOfItsOwn proves a suppressed segment does not keep
// the prefix's end as if it were the segment's.
func TestUncoveredSegmentReportsNoEndOfItsOwn(t *testing.T) {
	t.Parallel()

	records := coverageRecords(120)
	last := records[len(records)-1].Time
	summary, err := AnalyzeFIT(t.Context(), FITActivity{
		Records:          records,
		RecordsTruncated: true,
		Sessions: []FITSpan{{
			Start: coverageBase(),
			End:   coverageBase().Add(400 * time.Second),
		}},
	})
	if err != nil {
		t.Fatalf("AnalyzeFIT() = %v", err)
	}

	if summary.Overall.End.Equal(last) {
		t.Errorf("overall end = %v, want the prefix end withheld", summary.Overall.End)
	}
	if !summary.Overall.End.IsZero() {
		t.Errorf("overall end = %v, want none: the fold left no window and the records "+
			"end where the bound cut them", summary.Overall.End)
	}
	if summary.Overall.Seconds.OK {
		t.Errorf("overall duration = %+v, want absence for the same reason",
			summary.Overall.Seconds)
	}
	// A span that carries its own window keeps it, because the device wrote it.
	if len(summary.Sessions) != 1 {
		t.Fatalf("%d sessions, want 1", len(summary.Sessions))
	}
	if !summary.Sessions[0].End.Equal(coverageBase().Add(400 * time.Second)) {
		t.Errorf("session end = %v, want the window the file declared", summary.Sessions[0].End)
	}
	if !summary.Sessions[0].Seconds.OK || summary.Sessions[0].Seconds.Value != 400 {
		t.Errorf("session duration = %+v, want the declared 400", summary.Sessions[0].Seconds)
	}
}
