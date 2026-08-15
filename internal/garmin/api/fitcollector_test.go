package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/muktihari/fit/profile/basetype"
	"github.com/muktihari/fit/profile/factory"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/typedef"
	"github.com/muktihari/fit/proto"
)

// This file tests the collector directly rather than through ParseFITActivity.
//
// Two of its properties are about what the collector *keeps* rather than about what a
// decode returns, and a test that only reads the returned model cannot tell the
// difference: a coordinate parked in the reused message struct and a span list that
// grew past its bound before the result was rendered are both invisible from outside.

// unknownRecordField is a record field number the FIT profile does not define, which
// is what makes a field carrying it an unknown field once it is decoded.
const unknownRecordField = 200

// The synthetic position the hidden-field record carries. It is a made-up pair of
// semicircle values: nothing here is a recorded location.
const (
	syntheticLat  = 574653324
	syntheticLong = 138126490
)

// collectorStamp is a synthetic instant. It describes nothing and nobody.
//
// It is a function rather than a variable because a package-level variable is mutable
// state, which AGENTS.md forbids, and time.Date cannot be a constant.
func collectorStamp() time.Time {
	return time.Date(2026, time.January, 2, 8, 0, 0, 0, time.UTC)
}

// TestCollectorScrubsEveryFieldAPositionCouldHideIn is the retention half of the
// position guarantee, and it is deliberately not satisfied by clearing the two profile
// position fields.
//
// mesgdef.Record carries two more places a latitude fits: UnknownFields, which is every
// field number the profile does not define, and DeveloperFields, which an application
// names itself. Both are aliases of the decoder's message and no method on the struct
// suppresses either, so a collector that scrubbed only PositionLat and PositionLong
// would leave a coordinate-bearing custom field sitting in it after the sample was
// read — which is what this asserts against.
func TestCollectorScrubsEveryFieldAPositionCouldHideIn(t *testing.T) {
	t.Parallel()

	collector := &fitCollector{limits: FITLimits{}.withDefaults(), stop: func() {}}
	collector.addRecord(recordWithHiddenPosition())

	if got := collector.record.PositionLat; got != basetype.Sint32Invalid {
		t.Errorf("the retained latitude is %d, want the invalid sentinel", got)
	}
	if got := collector.record.PositionLong; got != basetype.Sint32Invalid {
		t.Errorf("the retained longitude is %d, want the invalid sentinel", got)
	}
	if got := collector.record.UnknownFields; got != nil {
		t.Errorf("the collector retained %d unknown field(s), want none: an undefined field "+
			"number is free to carry a coordinate", len(got))
	}
	if got := collector.record.DeveloperFields; got != nil {
		t.Errorf("the collector retained %d developer field(s), want none: an application "+
			"names its own fields and one of them may be a position", len(got))
	}

	// The sample itself must still have been collected, or every assertion above
	// would pass on a collector that read nothing.
	if len(collector.records) != 1 {
		t.Fatalf("%d records collected, want the sample to have been read", len(collector.records))
	}
}

// recordWithHiddenPosition builds one record message carrying a position in all the
// places a record can hold one: the two profile fields, an undefined field number and
// a developer field. Every value is synthetic.
func recordWithHiddenPosition() *proto.Message {
	record := mesgdef.NewRecord(nil).
		SetTimestamp(collectorStamp()).
		SetPositionLat(syntheticLat).
		SetPositionLong(syntheticLong).
		SetPower(210)

	unknown := factory.CreateField(typedef.MesgNumRecord, unknownRecordField)
	unknown.Value = proto.Int32(syntheticLat)
	record.SetUnknownFields(unknown)
	record.SetDeveloperFields(proto.DeveloperField{Value: proto.Int32(syntheticLong)})

	mesg := record.ToMesg(nil)
	return &mesg
}

// The price of one worst-case analysis, stated as arithmetic.
//
// The worst case is not hypothetical: a file is free to make every session and every lap
// cover the whole record stream, and the analysis then walks the retained samples once
// per walk per span, allocating a power series per span on the way. The cost is the
// product of the span bounds, the record bound and the number of walks — and the third
// factor is the one an earlier version of this file omitted, which priced a five-walk
// analysis as though it made one pass.
//
// The figures are ceilings the current bounds meet **exactly**, deliberately. A ceiling
// with slack in it is a ceiling chosen to fit; one set at the product means any widening
// of any bound, and any walk added to a span, fails here rather than in production.
const (
	// analysisWalksPerSpan is how many times the analysis walks the records of one
	// span. It is derived by enumerating deriveSegment and what it calls, in order:
	// distanceOf, ascentOf, the accumulator loop in withPowerMetrics, powerSeries, and
	// dynamicsOf. recordsIn is not among them: it binary-searches the window rather
	// than walking it. This figure is documentation of a structure, not something the
	// test can measure — see the comment on TestTheSpanBoundsKeepTheAnalysisAffordable.
	analysisWalksPerSpan = 5

	// analysisSpans is how many windows one analysis summarizes: every retained session,
	// every retained lap, and the whole-activity segment.
	analysisSpans = DefaultMaxFITSessions + DefaultMaxFITLaps + 1

	// maxAnalysisRecordVisits is the most record visits one whole analysis may cost.
	maxAnalysisRecordVisits = 66_300_000

	// maxAnalysisSeriesVisits is the most per-second series elements normalized power
	// may visit across one whole analysis. A span's series is bounded by
	// maxSeriesSeconds rather than by the record count, because a file may spread its
	// samples over a whole day, and every element of it costs a fourth power.
	maxAnalysisSeriesVisits = 19_094_400
)

// TestTheSpanBoundsKeepTheAnalysisAffordable is the bound that matters, as opposed to
// the mechanism that applies it.
//
// A test that asserts the collected count equals the configured bound proves the
// mechanism and nothing about the figure: it passes just as happily at a thousand spans
// as at twenty. This one fails when the figures are loose, whatever the mechanism does.
//
// What it does *not* do is stated plainly rather than implied: analysisWalksPerSpan is
// read off the source of deriveSegment, and no assertion here fails if a sixth walk is
// added to a span. A slice of plain structs offers nothing to count visits through, and
// a wall-clock budget under -race is a flake rather than a bound. The figure is
// therefore reviewed, not enforced, and the two figures it feeds are enforced exactly.
//
// The measured cost of the worst case these figures describe — 60 000 samples spread
// over 24 h, with all 20 sessions and all 200 laps covering the whole stream — was
// 0.85 s of CPU on the development machine on 2026-08-15, of which roughly a third is
// the normalized-power series. That is the number the context threaded through
// AnalyzeFIT exists to make interruptible.
func TestTheSpanBoundsKeepTheAnalysisAffordable(t *testing.T) {
	t.Parallel()

	visits := analysisSpans * analysisWalksPerSpan * DefaultMaxFITRecords
	if visits > maxAnalysisRecordVisits {
		t.Errorf("%d spans walked %d times over %d records is %d record visits per analysis, "+
			"want at most %d: the bounds are too loose to be a bound",
			analysisSpans, analysisWalksPerSpan, DefaultMaxFITRecords, visits,
			maxAnalysisRecordVisits)
	}
	if series := analysisSpans * maxSeriesSeconds; series > maxAnalysisSeriesVisits {
		t.Errorf("%d spans over a %d-element series is %d series visits per analysis, want at "+
			"most %d: the normalized-power cost is not bounded by the record count",
			analysisSpans, maxSeriesSeconds, series, maxAnalysisSeriesVisits)
	}
}

// TestAnalyzeFITStopsWhenTheCallerGivesUp is why the arithmetic above is affordable
// rather than merely stated.
//
// The analysis is the one stage of the FIT path whose cost the file sets rather than the
// request. It used to run to completion whatever the caller did, so a deadline bounded
// the download and then waited out tens of millions of record visits. Cancellation is
// reported as itself, so a cancelled caller is never told its file was malformed.
func TestAnalyzeFITStopsWhenTheCallerGivesUp(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	activity := FITActivity{
		Records: []FITRecord{{Time: collectorStamp(), Power: fitNumber(200)}},
		Laps:    []FITSpan{{}, {}},
	}
	summary, err := AnalyzeFIT(ctx, activity)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("AnalyzeFIT() = %v, want the caller's cancellation reported as itself", err)
	}
	if len(summary.Laps) != 0 || len(summary.Curve) != 0 {
		t.Error("AnalyzeFIT() returned a partial summary, want nothing a caller could use")
	}

	// A caller that gives up part-way through must stop at the next *span*, not only
	// before the first stage. The span loops are the multiplier — one span is a bounded
	// amount of work and two hundred of them are not — so a check only at the top would
	// leave the expensive half uninterruptible. The two loops are driven separately, one
	// per span class.
	activity.Sessions = []FITSpan{{}, {}}
	for _, after := range []int{analysisWholeActivityStages, analysisWholeActivityStages + 1} {
		if _, err := AnalyzeFIT(&cancelAfter{checks: after}, activity); !errors.Is(
			err, context.Canceled) {
			t.Errorf("AnalyzeFIT() = %v after %d context checks, want the span loop to stop",
				err, after)
		}
	}
}

// analysisWholeActivityStages is how many whole-activity stages AnalyzeFIT runs before
// it reaches the span loops, counted off its stages list. It is what cancelAfter counts
// past in order to land the cancellation inside one of those loops.
const analysisWholeActivityStages = 7

// cancelAfter is a context that is live for its first checks reads of Err and cancelled
// from then on, so a test can land a cancellation on a chosen step of AnalyzeFIT rather
// than racing a clock. It carries no deadline and no value, which is all AnalyzeFIT asks
// of a context, and it is used from one goroutine.
type cancelAfter struct {
	checks int
	seen   int
}

func (c *cancelAfter) Err() error {
	c.seen++
	if c.seen > c.checks {
		return context.Canceled
	}
	return nil
}

func (c *cancelAfter) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *cancelAfter) Done() <-chan struct{}       { return nil }
func (c *cancelAfter) Value(any) any               { return nil }

// TestCollectorBoundsSessionsAndLapsAtTheirOwnCount pins the span bounds.
//
// The bound is not a formality. Every span is summarized against the whole retained
// record stream, which is bounded at tens of thousands of samples, and a file is free
// to make every span cover all of it — so the collected span count is a multiplier on
// the analysis, several passes deep. This asserts the two classes are bounded
// separately, and at the count a result actually carries rather than at a figure
// chosen for what a device might conceivably write.
func TestCollectorBoundsSessionsAndLapsAtTheirOwnCount(t *testing.T) {
	t.Parallel()

	const offered = DefaultMaxFITLaps * 3

	collector := &fitCollector{limits: FITLimits{}.withDefaults(), stop: func() {}}
	for range offered {
		collector.OnMesg(factory.CreateMesg(typedef.MesgNumSession))
		collector.OnMesg(factory.CreateMesg(typedef.MesgNumLap))
	}

	if len(collector.sessions) != DefaultMaxFITSessions {
		t.Errorf("%d sessions collected from %d offered, want the bound of %d",
			len(collector.sessions), offered, DefaultMaxFITSessions)
	}
	if len(collector.laps) != DefaultMaxFITLaps {
		t.Errorf("%d laps collected from %d offered, want the bound of %d",
			len(collector.laps), offered, DefaultMaxFITLaps)
	}
	if !collector.spansCut {
		t.Error("the collector did not report that it cut the span lists")
	}
}
