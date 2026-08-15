package api

import (
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
var collectorStamp = time.Date(2026, time.January, 2, 8, 0, 0, 0, time.UTC)

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
		SetTimestamp(collectorStamp).
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

// maxAnalysisRecordVisits is the most record visits one analysis pass may cost in the
// worst case this package accepts.
//
// The worst case is not hypothetical: a file is free to make every session and every
// lap cover the whole record stream, and the analysis then walks the retained samples
// once per span, several passes deep, allocating a power series per span on the way.
// The cost is therefore the product of the span bounds and the record bound, and this
// figure is the ceiling that product may not cross. It is stated as arithmetic rather
// than as a wall clock so it is deterministic and so a later widening of any of the
// three bounds fails here rather than in production.
const maxAnalysisRecordVisits = 15_000_000

// TestTheSpanBoundsKeepTheAnalysisAffordable is the bound that matters, as opposed to
// the mechanism that applies it.
//
// A test that asserts the collected count equals the configured bound proves the
// mechanism and nothing about the figure: it passes just as happily at a thousand
// spans as at twenty. This one fails when the figures are loose, whatever the
// mechanism does.
func TestTheSpanBoundsKeepTheAnalysisAffordable(t *testing.T) {
	t.Parallel()

	spans := DefaultMaxFITSessions + DefaultMaxFITLaps
	if visits := spans * DefaultMaxFITRecords; visits > maxAnalysisRecordVisits {
		t.Errorf("%d spans over %d records is %d record visits per analysis pass, want at "+
			"most %d: the span bounds are too loose to be a bound",
			spans, DefaultMaxFITRecords, visits, maxAnalysisRecordVisits)
	}
}

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
