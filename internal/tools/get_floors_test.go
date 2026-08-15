package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func floorsPath() string {
	return client.PathFloorsChartDailyPrefix + "/" + dailyDate
}

// The fixture is the sampled shape. Note the two spellings inside one document:
// the descriptor list is floorsValueDescriptorDTOList and the rows are
// floorValuesArray. The plural moves between the words, and neither name may be
// derived from the other.
//
// The four buckets are, in order: both measurements, ascent only, descent only, and
// neither. Every row is mixed-type — two timestamp strings then two numbers.
const floorsFixture = `{"startTimestampGMT":"` + dailyDate + `T00:00:00.0",` +
	`"endTimestampGMT":"` + dailyDate + `T23:59:59.0",` +
	`"startTimestampLocal":"` + dailyDate + `T01:00:00.0",` +
	`"endTimestampLocal":"` + dailyDate + `T23:59:59.0",` +
	`"floorsValueDescriptorDTOList":[{"index":0,"key":"startTimeGMT"},` +
	`{"index":1,"key":"endTimeGMT"},{"index":2,"key":"floorsAscended"},` +
	`{"index":3,"key":"floorsDescended"}],` +
	`"floorValuesArray":[` +
	`["` + dailyDate + `T00:00:00.0","` + dailyDate + `T00:15:00.0",3,2],` +
	`["` + dailyDate + `T00:15:00.0","` + dailyDate + `T00:30:00.0",4,null],` +
	`["` + dailyDate + `T00:30:00.0","` + dailyDate + `T00:45:00.0",null,5],` +
	`["` + dailyDate + `T00:45:00.0","` + dailyDate + `T01:00:00.0",null,null]]}`

func TestGetFloorsDecodesTheEnvelopeAndReadsNoProfile(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript().
		With(floorsPath(), testkit.JSON(http.StatusOK, floorsFixture)))

	got := h.call(t, ToolGetFloors, map[string]any{argDate: dailyDate})
	if got["date"] != dailyDate {
		t.Errorf("date = %v, want the day that was asked for", got["date"])
	}
	if got["start_gmt"] != dailyDate+"T00:00:00.0" {
		t.Errorf("start_gmt = %v, want the envelope window", got["start_gmt"])
	}
	if got["start_local"] != dailyDate+"T01:00:00.0" {
		t.Errorf("start_local = %v, want the local pair kept apart from the GMT one",
			got["start_local"])
	}
	if got["count"] != float64(4) || got["truncated"] != false {
		t.Errorf("count/truncated = %v/%v, want 4/false", got["count"], got["truncated"])
	}

	// This endpoint is keyed by the date alone, so the profile must not be read.
	requests := h.fake.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want only the floors read", len(requests))
	}
	if requests[0].Path != floorsPath() {
		t.Errorf("path = %q, want the floors chart", requests[0].Path)
	}
}

func TestGetFloorsReportsADayGarminHoldsNothingFor(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript().
		With(floorsPath(), testkit.Behavior{Status: http.StatusNoContent}))

	advice := h.callError(t, ToolGetFloors, map[string]any{argDate: dailyDate})
	if !strings.Contains(advice, "could not interpret") {
		t.Errorf("advice = %q, want the drift remediation", advice)
	}
}

func TestGetFloorsRefusesAMalformedDate(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript())

	if advice := h.callError(t, ToolGetFloors,
		map[string]any{argDate: "2026-13-40"}); advice == "" {
		t.Error("get_floors returned an empty refusal")
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestFloorsDataLogValueReportsShapeAndNoReading(t *testing.T) {
	t.Parallel()

	ascended := 11.5
	floors := FloorsData{
		Date:    dailyDate,
		Buckets: []FloorsBucket{{FloorsAscended: &ascended}},
		Count:   1,
	}

	rendered := floors.LogValue().String()
	if strings.Contains(rendered, "11.5") {
		t.Errorf("LogValue rendered a floor count: %s", rendered)
	}
	if !strings.Contains(rendered, "buckets=1") {
		t.Errorf("LogValue = %s, want the bucket count", rendered)
	}
}

// TestGetFloorsDecodesTheMixedTypeRow is the trap this tool exists to survive. A
// floors tuple is two timestamp strings followed by two numbers, so the all-numeric
// row parser the cardio series uses would fail the whole read: client.Number accepts
// a numeric string and a timestamp is not one.
func TestGetFloorsDecodesTheMixedTypeRow(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript().
		With(floorsPath(), testkit.JSON(http.StatusOK, floorsFixture)))

	got := h.call(t, ToolGetFloors, map[string]any{argDate: dailyDate})
	buckets, ok := got["buckets"].([]any)
	if !ok || len(buckets) != 4 {
		t.Fatalf("buckets = %v, want four", got["buckets"])
	}

	both, _ := buckets[0].(map[string]any)
	if both["start_gmt"] != dailyDate+"T00:00:00.0" {
		t.Errorf("start_gmt = %v, want the timestamp string column", both["start_gmt"])
	}
	if both["end_gmt"] != dailyDate+"T00:15:00.0" {
		t.Errorf("end_gmt = %v, want the timestamp string column", both["end_gmt"])
	}
	if both["floors_ascended"] != float64(3) || both["floors_descended"] != float64(2) {
		t.Errorf("the numeric columns decoded as %v/%v, want 3/2",
			both["floors_ascended"], both["floors_descended"])
	}
}

// TestGetFloorsTreatsAscentAndDescentAsIndependent covers the three remaining bucket
// shapes. A bucket with no ascent measured must not report zero floors climbed.
func TestGetFloorsTreatsAscentAndDescentAsIndependent(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript().
		With(floorsPath(), testkit.JSON(http.StatusOK, floorsFixture)))

	got := h.call(t, ToolGetFloors, map[string]any{argDate: dailyDate})
	buckets, _ := got["buckets"].([]any)
	if len(buckets) != 4 {
		t.Fatalf("%d buckets returned, want four", len(buckets))
	}

	ascentOnly, _ := buckets[1].(map[string]any)
	if ascentOnly["floors_ascended"] != float64(4) {
		t.Errorf("floors_ascended = %v, want 4", ascentOnly["floors_ascended"])
	}
	if _, present := ascentOnly["floors_descended"]; present {
		t.Error("floors_descended is present for a bucket that measured no descent")
	}

	descentOnly, _ := buckets[2].(map[string]any)
	if descentOnly["floors_descended"] != float64(5) {
		t.Errorf("floors_descended = %v, want 5", descentOnly["floors_descended"])
	}
	if _, present := descentOnly["floors_ascended"]; present {
		t.Error("floors_ascended is present for a bucket that measured no ascent")
	}

	neither, _ := buckets[3].(map[string]any)
	for _, key := range []string{"floors_ascended", "floors_descended"} {
		if _, present := neither[key]; present {
			t.Errorf("%s is present for a bucket that measured neither", key)
		}
	}
	// The bucket still exists: a quarter-hour with no stairs is not a gap.
	if neither["start_gmt"] != dailyDate+"T00:45:00.0" {
		t.Errorf("start_gmt = %v, want the bucket kept with its window", neither["start_gmt"])
	}
}
