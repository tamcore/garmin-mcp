package api_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// The floors chart, beside the file it covers. Every fixture is synthetic.
//
// Two spellings live in one document: the descriptor list is
// floorsValueDescriptorDTOList and the rows are floorValuesArray. The plural moves
// between the words, so neither name may be derived from the other.
//
// The rows are mixed-type — two timestamp strings then two numbers — and cover a
// bucket with both measurements, ascent only, descent only, and neither.
const floorsBody = `{"startTimestampGMT":"` + testCalendarDate + `T00:00:00.0",` +
	`"endTimestampGMT":"` + testCalendarDate + `T23:59:59.0",` +
	`"startTimestampLocal":"` + testCalendarDate + `T01:00:00.0",` +
	`"endTimestampLocal":"` + testCalendarDate + `T23:59:59.0",` +
	`"floorsValueDescriptorDTOList":[{"index":0,"key":"startTimeGMT"},` +
	`{"index":1,"key":"endTimeGMT"},{"index":2,"key":"floorsAscended"},` +
	`{"index":3,"key":"floorsDescended"}],` +
	`"floorValuesArray":[` +
	`["` + testCalendarDate + `T00:00:00.0","` + testCalendarDate + `T00:15:00.0",3,2],` +
	`["` + testCalendarDate + `T00:15:00.0","` + testCalendarDate + `T00:30:00.0",4,null],` +
	`["` + testCalendarDate + `T00:30:00.0","` + testCalendarDate + `T00:45:00.0",null,5],` +
	`["` + testCalendarDate + `T00:45:00.0","` + testCalendarDate + `T01:00:00.0",null,null]]}`

func floorsPath() string {
	return client.PathFloorsChartDailyPrefix + "/" + testCalendarDate
}

func readFloors(t *testing.T, body string) api.Floors {
	t.Helper()

	script := testkit.NewScript().With(floorsPath(), testkit.JSON(http.StatusOK, body))
	h := newHarness(t, script, client.Limits{})

	got, err := newWellnessDaily(t, h).Floors(t.Context(), h.session,
		mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("Floors() = %v", err)
	}
	return got
}

func TestWellnessDailyFloorsDecodesTheEnvelope(t *testing.T) {
	t.Parallel()

	got := readFloors(t, floorsBody)

	// The GMT and local pairs are both kept, and neither is derived from the other.
	if start, ok := got.StartTimestampGMT.Value(); !ok || start != testCalendarDate+"T00:00:00.0" {
		t.Errorf("StartTimestampGMT = %q/%v, want the GMT window start", start, ok)
	}
	if start, ok := got.StartTimestampLocal.Value(); !ok || start != testCalendarDate+"T01:00:00.0" {
		t.Errorf("StartTimestampLocal = %q/%v, want the local window start", start, ok)
	}
	if len(got.Descriptors) != 4 {
		t.Fatalf("%d descriptors decoded, want 4", len(got.Descriptors))
	}
	if key, ok := got.Descriptors[2].Key.Value(); !ok || key != api.FloorsKeyFloorsAscended {
		t.Errorf("the third descriptor names %q, want %q", key, api.FloorsKeyFloorsAscended)
	}
	if len(got.Rows) != 4 {
		t.Errorf("%d rows decoded, want 4", len(got.Rows))
	}
}

// TestWellnessDailyFloorsDecodesAMixedTypeRow is the trap. A floors tuple is two
// timestamp strings followed by two numbers, so the all-numeric row parser this
// package uses for the cardio series cannot decode it: client.Number tolerates a
// numeric string, and a timestamp is not one, so the whole read would fail.
func TestWellnessDailyFloorsDecodesAMixedTypeRow(t *testing.T) {
	t.Parallel()

	buckets := readFloors(t, floorsBody).Buckets()
	if len(buckets) != 4 {
		t.Fatalf("%d buckets decoded, want 4", len(buckets))
	}

	both := buckets[0]
	if start, ok := both.StartTimeGMT.Value(); !ok || start != testCalendarDate+"T00:00:00.0" {
		t.Errorf("StartTimeGMT = %q/%v, want the string column", start, ok)
	}
	if end, ok := both.EndTimeGMT.Value(); !ok || end != testCalendarDate+"T00:15:00.0" {
		t.Errorf("EndTimeGMT = %q/%v, want the string column", end, ok)
	}
	if ascended, ok := both.FloorsAscended.Float64(); !ok || ascended != 3 {
		t.Errorf("FloorsAscended = %v/%v, want 3", ascended, ok)
	}
	if descended, ok := both.FloorsDescended.Float64(); !ok || descended != 2 {
		t.Errorf("FloorsDescended = %v/%v, want 2", descended, ok)
	}
}

// TestWellnessDailyFloorsTreatsAscentAndDescentAsIndependent covers the other three
// bucket shapes. A bucket that measured no ascent must report absent, not zero.
func TestWellnessDailyFloorsTreatsAscentAndDescentAsIndependent(t *testing.T) {
	t.Parallel()

	buckets := readFloors(t, floorsBody).Buckets()
	if len(buckets) != 4 {
		t.Fatalf("%d buckets decoded, want 4", len(buckets))
	}

	if ascended, ok := buckets[1].FloorsAscended.Float64(); !ok || ascended != 4 {
		t.Errorf("the ascent-only bucket = %v/%v, want 4", ascended, ok)
	}
	if buckets[1].FloorsDescended.IsSet() {
		t.Error("the ascent-only bucket reported a descent")
	}

	if descended, ok := buckets[2].FloorsDescended.Float64(); !ok || descended != 5 {
		t.Errorf("the descent-only bucket = %v/%v, want 5", descended, ok)
	}
	if buckets[2].FloorsAscended.IsSet() {
		t.Error("the descent-only bucket reported an ascent")
	}

	if buckets[3].FloorsAscended.IsSet() || buckets[3].FloorsDescended.IsSet() {
		t.Error("the empty bucket reported a measurement")
	}
	if start, ok := buckets[3].StartTimeGMT.Value(); !ok || start == "" {
		t.Error("the empty bucket lost its window; a quarter-hour with no stairs is not a gap")
	}
}

// TestWellnessDailyFloorsTakesColumnsFromTheDescriptor proves the row is read by
// descriptor and not by position: the same values in a shuffled column order must
// decode to the same bucket.
func TestWellnessDailyFloorsTakesColumnsFromTheDescriptor(t *testing.T) {
	t.Parallel()

	shuffled := `{"floorsValueDescriptorDTOList":[{"index":3,"key":"startTimeGMT"},` +
		`{"index":2,"key":"endTimeGMT"},{"index":1,"key":"floorsAscended"},` +
		`{"index":0,"key":"floorsDescended"}],` +
		`"floorValuesArray":[[2,3,"` + testCalendarDate + `T00:15:00.0","` +
		testCalendarDate + `T00:00:00.0"]]}`

	buckets := readFloors(t, shuffled).Buckets()
	if len(buckets) != 1 {
		t.Fatalf("%d buckets decoded, want 1", len(buckets))
	}
	if start, ok := buckets[0].StartTimeGMT.Value(); !ok || start != testCalendarDate+"T00:00:00.0" {
		t.Errorf("StartTimeGMT = %q/%v, want the column the descriptor names", start, ok)
	}
	if ascended, ok := buckets[0].FloorsAscended.Float64(); !ok || ascended != 3 {
		t.Errorf("FloorsAscended = %v/%v, want the column the descriptor names", ascended, ok)
	}
}

// TestWellnessDailyFloorsFallsBackToTheDocumentedOrder covers a response with no
// descriptor list, where the documented layout is the only information left.
func TestWellnessDailyFloorsFallsBackToTheDocumentedOrder(t *testing.T) {
	t.Parallel()

	body := `{"floorValuesArray":[["` + testCalendarDate + `T00:00:00.0","` +
		testCalendarDate + `T00:15:00.0",3,2]]}`

	buckets := readFloors(t, body).Buckets()
	if len(buckets) != 1 {
		t.Fatalf("%d buckets decoded, want 1", len(buckets))
	}
	if ascended, ok := buckets[0].FloorsAscended.Float64(); !ok || ascended != 3 {
		t.Errorf("FloorsAscended = %v/%v, want the documented third column", ascended, ok)
	}
}

// TestWellnessDailyFloorsSurvivesADriftedColumn keeps one bad column from costing
// the whole day: a timestamp where a number belongs leaves that field absent, and a
// short row leaves the missing columns absent, but the read still succeeds.
func TestWellnessDailyFloorsSurvivesADriftedColumn(t *testing.T) {
	t.Parallel()

	body := `{"floorsValueDescriptorDTOList":[{"index":0,"key":"startTimeGMT"},` +
		`{"index":2,"key":"floorsAscended"}],` +
		`"floorValuesArray":[["` + testCalendarDate + `T00:00:00.0",1,"not-a-number"],` +
		`["` + testCalendarDate + `T00:15:00.0"]]}`

	buckets := readFloors(t, body).Buckets()
	if len(buckets) != 2 {
		t.Fatalf("%d buckets decoded, want 2", len(buckets))
	}
	if buckets[0].FloorsAscended.IsSet() {
		t.Error("a non-numeric value decoded as a floor count")
	}
	if start, ok := buckets[0].StartTimeGMT.Value(); !ok || start == "" {
		t.Error("one drifted column cost the whole bucket")
	}
	if buckets[1].FloorsAscended.IsSet() {
		t.Error("a column past the end of a short row decoded as a value")
	}
}

func TestWellnessDailyFloorsRefusesAnEmptyBodyAndAnUnsetDate(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(floorsPath(), testkit.Behavior{Status: http.StatusNoContent})
	h := newHarness(t, script, client.Limits{})
	daily := newWellnessDaily(t, h)

	if _, err := daily.Floors(t.Context(), h.session,
		mustDate(t, testCalendarDate)); !errors.Is(err, client.ErrUnexpectedResponse) {
		t.Errorf("Floors() for an empty body = %v, want ErrUnexpectedResponse", err)
	}
	if _, err := daily.Floors(t.Context(), h.session, client.Date{}); !errors.Is(
		err, client.ErrValidation) {
		t.Errorf("Floors() with no date = %v, want a validation error", err)
	}
}
