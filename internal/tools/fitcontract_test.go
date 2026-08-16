package tools

import (
	"math"
	"reflect"
	"strconv"
	"strings"
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
		Seconds:  api.FITNumber{Value: 600, OK: true},
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

// A cadenceSurface is one result type carrying a cadence, and whether its unit is the
// sport's. Only the session and lap fields are dynamic; Record.Cadence is always rpm.
type cadenceSurface struct {
	view       reflect.Type
	sportBound bool
}

func fitCadenceSurfaces() map[string]cadenceSurface {
	return map[string]cadenceSurface{
		// Prefers the session or lap summary, so the unit follows the sport.
		"segment": {reflect.TypeFor[FITSegmentView](), true},
		// Derived from the record stream, which is rpm whatever the sport.
		"climb":       {reflect.TypeFor[FITClimbView](), false},
		"grade band":  {reflect.TypeFor[FITGradeBandView](), false},
		"record":      {reflect.TypeFor[FITRecordView](), false},
		"shift event": {reflect.TypeFor[FITShiftEventView](), false},
	}
}

// cadenceFields yields every cadence key of one view with its description.
func cadenceFields(view reflect.Type) map[string]string {
	out := map[string]string{}
	for field := range view.Fields() {
		key, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if strings.Contains(key, "cadence") {
			out[key] = field.Tag.Get("jsonschema")
		}
	}
	return out
}

// TestCadenceKeysNameNoUnit proves no cadence key states a unit, because one of the
// surfaces has a unit the sport decides and one spelling per document is the point.
func TestCadenceKeysNameNoUnit(t *testing.T) {
	t.Parallel()

	for name, surface := range fitCadenceSurfaces() {
		for key := range cadenceFields(surface.view) {
			if strings.HasSuffix(key, "_rpm") {
				t.Errorf("%s carries %q, want a key that does not assert a unit", name, key)
			}
		}
	}
}

// TestCadenceDescriptionsMatchTheirOwnField proves each surface describes the unit it
// actually has, rather than one borrowed from a neighbour.
//
// Both directions are failures. A session cadence described as plain rpm is wrong for
// every run, and a record cadence described as sport-dependent is wrong for every
// file: Record.Cadence has no running form to depend on.
func TestCadenceDescriptionsMatchTheirOwnField(t *testing.T) {
	t.Parallel()

	const strides = "strides"
	for name, surface := range fitCadenceSurfaces() {
		for key, description := range cadenceFields(surface.view) {
			if !strings.Contains(description, "rpm") {
				t.Errorf("%s %q description = %q, want it to name rpm", name, key, description)
			}
			mentions := strings.Contains(description, strides)
			if surface.sportBound && !mentions {
				t.Errorf("%s %q description = %q, want the running unit named: the "+
					"session and lap fields are sport-dynamic", name, key, description)
			}
			if !surface.sportBound && mentions {
				t.Errorf("%s %q description = %q, want rpm alone: this reading comes "+
					"from the record stream, which has no running form", name, key, description)
			}
		}
	}
}

// TestDriftDescriptionStatesDirectionWithoutGradingIt proves the decoupling schema
// says which way the ratio moved and stops there.
//
// This server serves no interpretation label, because the threshold that separates a
// coupled effort from a decoupled one is not published by upstream and is not in any
// Garmin document. A description calling every negative figure well coupled would
// smuggle that unsourced threshold back in as prose, which is the same invented
// finding wearing different clothes.
func TestDriftDescriptionStatesDirectionWithoutGradingIt(t *testing.T) {
	t.Parallel()

	field, ok := reflect.TypeFor[FITDriftView]().FieldByName("Percent")
	if !ok {
		t.Fatal("FITDriftView has no Percent field")
	}
	description := strings.ToLower(field.Tag.Get("jsonschema"))

	if strings.Contains(description, "coupled") {
		t.Errorf("description = %q, want no grading label: the threshold behind one "+
			"is not sourceable", description)
	}
	for _, direction := range []string{"positive", caseNegative} {
		if !strings.Contains(description, direction) {
			t.Errorf("description = %q, want it to state what %s means",
				description, direction)
		}
	}
}
