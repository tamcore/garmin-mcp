package tools

import (
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
)

// This file covers what the FIT result *renders* and *logs*, as opposed to how it is
// obtained. The two are separate subjects: the tests next door drive a download and a
// decode through a scripted service, while these need neither and assert on the view
// models and on the log record the result produces.

// TestFITDataLogsItsShapeOnly proves the result model logs counts, never a reading.
func TestFITDataLogsItsShapeOnly(t *testing.T) {
	t.Parallel()

	h := newFITHarness(t, fitScript(fitRide(120, fitTestPower)), Bounds{})
	data, err := h.svc.activityFITData(h.ctx, activityFITInput{ActivityID: int64(fitTestActivity)})
	if err != nil {
		t.Fatalf("activityFITData() = %v", err)
	}

	rendered := data.LogValue().String()
	for _, forbidden := range []string{strconv.Itoa(fitTestPower), strconv.Itoa(fitTestHeartRate)} {
		if contains(rendered, forbidden) {
			t.Errorf("log value %q carries a reading", rendered)
		}
	}
}

// shiftLogProbe is a shift count no other figure in the log line can be. It is
// deliberately not round, so finding it in the rendered line can only mean the shift
// list's own length was logged.
const shiftLogProbe = 137

// TestFITDataLogsShiftsAsPresenceRatherThanACount covers the one figure of this
// result that a list length gives away exactly.
//
// The returned shift list is bounded at two hundred entries and a real ride stays
// under that, so logging its length logs how many times the rider changed gear — a
// count of what a person did, which is a reading however coarse the surrounding
// fields are. This asserts the count is absent and the presence flag is there, so a
// log line still says whether the file carried electronic shifting.
func TestFITDataLogsShiftsAsPresenceRatherThanACount(t *testing.T) {
	t.Parallel()

	data := FITData{Shifts: FITShiftView{
		Events: make([]FITShiftEventView, shiftLogProbe),
	}}

	rendered := data.LogValue().String()
	if contains(rendered, strconv.Itoa(shiftLogProbe)) {
		t.Errorf("log value %q carries the exact number of gear changes", rendered)
	}
	if !contains(rendered, "shifts=true") {
		t.Errorf("log value %q does not report that the file carried shifting at all", rendered)
	}

	if empty := (FITData{}).LogValue().String(); !contains(empty, "shifts=false") {
		t.Errorf("log value %q does not report the absence of shifting", empty)
	}
}

// TestTheFITDecodeIsBoundedAtWhatTheResultRenders ties the two bounds together.
//
// Every session and lap the decode retains is summarized against the whole retained
// sample stream, so a span this tool would never show still costs a pass over sixty
// thousand records. Decoding more spans than can be rendered therefore buys nothing
// and pays for exactly that, and leaving the decode bound unset — which is what it
// was — takes whatever default the api package happens to carry.
func TestTheFITDecodeIsBoundedAtWhatTheResultRenders(t *testing.T) {
	t.Parallel()

	limits := fitLimits()
	if limits.MaxSessions != maxFITSessions {
		t.Errorf("the decode retains %d sessions and the result renders %d",
			limits.MaxSessions, maxFITSessions)
	}
	if limits.MaxLaps != maxFITLaps {
		t.Errorf("the decode retains %d laps and the result renders %d",
			limits.MaxLaps, maxFITLaps)
	}
}

// TestFITViewsRenderTheOptionalSections covers the renderers whose sections only
// appear when the file carries the readings behind them.
func TestFITViewsRenderTheOptionalSections(t *testing.T) {
	t.Parallel()

	segment := api.FITSegment{
		Sport:    "cycling",
		Start:    time.Date(2026, time.January, 2, 8, 0, 0, 0, time.UTC),
		Seconds:  600,
		Dynamics: api.FITDynamics{RightBalance: api.FITNumber{Value: 51.4, OK: true}},
	}
	view := newFITSegmentView(segment)
	if view.Sport == nil || *view.Sport != "cycling" || view.Start == nil {
		t.Errorf("segment = %+v, want the sport and start rendered", view)
	}
	if view.Dynamics == nil || view.Dynamics.RightBalance == nil || *view.Dynamics.RightBalance != 51.4 {
		t.Errorf("dynamics = %+v, want the recorded balance", view.Dynamics)
	}

	temperature := newFITTemperatureView(api.FITTemperature{OK: true, Samples: 40, HotAvgC: 28.44})
	if temperature == nil || temperature.HottestAverageC != 28.4 {
		t.Errorf("temperature = %+v, want the rounded hot average", temperature)
	}
	if newFITTemperatureView(api.FITTemperature{}) != nil {
		t.Error("a temperature section was rendered for a file that carries none")
	}

	drift := newFITDriftView(api.FITDrift{OK: true, Percent: 7.126})
	if drift == nil || drift.Percent != 7.13 {
		t.Errorf("drift = %+v, want the rounded decoupling", drift)
	}
	if newFITDriftView(api.FITDrift{}) != nil {
		t.Error("a drift section was rendered for a ride that carries none")
	}
}

// TestFITShiftEventNamesTheDerailleur proves both positions are labelled.
func TestFITShiftEventNamesTheDerailleur(t *testing.T) {
	t.Parallel()

	front := newFITShiftEventView(api.FITShiftEvent{Front: true})
	rear := newFITShiftEventView(api.FITShiftEvent{})
	if front.Position != shiftPositionFront || rear.Position != shiftPositionRear {
		t.Errorf("positions = %q and %q, want front and rear", front.Position, rear.Position)
	}
}

// TestFITRoundingRefusesANonNumber keeps a not-a-number out of a rendered result,
// where it would serialize as invalid JSON.
func TestFITRoundingRefusesANonNumber(t *testing.T) {
	t.Parallel()

	for name, value := range map[string]float64{
		"not a number": math.NaN(),
		"infinite":     math.Inf(1),
	} {
		if got := fitRound(value, placesTwo); got != 0 {
			t.Errorf("fitRound(%s) = %v, want 0", name, got)
		}
	}
	if got := fitRound(1.2345, placesTwo); got != 1.23 {
		t.Errorf("fitRound(1.2345) = %v, want 1.23", got)
	}
	if fitOptional(api.FITNumber{}, placesOne) != nil {
		t.Error("an absent reading was rendered as a number")
	}
	if fitInstant(time.Time{}) != nil {
		t.Error("a zero instant was rendered as a timestamp")
	}
}
