package tools

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func TestGetBodyCompositionDefaultsTheEndDayToTheStartDay(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript().
		With(client.PathBodyComposition, testkit.JSON(http.StatusOK, dailyBodyBody)))

	got := h.call(t, ToolGetBodyComposition, map[string]any{argStartDate: dailyDate})
	if got[argStartDate] != dailyDate || got[argEndDate] != dailyDate {
		t.Errorf("window = %v..%v, want a single day", got[argStartDate], got[argEndDate])
	}
	if got["reported_start_date"] != dailyDate {
		t.Errorf("reported_start_date = %v, want Garmin's echo", got["reported_start_date"])
	}

	requests := h.fake.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want 1", len(requests))
	}
	if start := requests[0].Query.Get(client.QueryStartDate); start != dailyDate {
		t.Errorf("startDate = %q, want %q", start, dailyDate)
	}
	if end := requests[0].Query.Get(client.QueryEndDate); end != dailyDate {
		t.Errorf("endDate = %q, want %q", end, dailyDate)
	}
}

func TestGetBodyCompositionReadsAWindow(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript().
		With(client.PathBodyComposition, testkit.JSON(http.StatusOK, dailyBodyBody)))

	got := h.call(t, ToolGetBodyComposition, map[string]any{
		argStartDate: dailyWindowStart, argEndDate: dailyDate,
	})
	if got[argStartDate] != dailyWindowStart || got[argEndDate] != dailyDate {
		t.Errorf("window = %v..%v, want the window that was asked for",
			got[argStartDate], got[argEndDate])
	}
}

func TestGetBodyCompositionRefusesAnInvertedOrOversizedWindow(t *testing.T) {
	t.Parallel()

	h := newToolHarnessWith(t, dailyScript(), client.Limits{MaxDateRangeDays: 7})

	inverted := h.callError(t, ToolGetBodyComposition, map[string]any{
		argStartDate: dailyDate, argEndDate: dailyWindowStart,
	})
	if !strings.Contains(inverted, argStartDate) {
		t.Errorf("advice = %q, want the ordering rule", inverted)
	}

	wide := h.callError(t, ToolGetBodyComposition, map[string]any{
		argStartDate: dailyWindowStart, argEndDate: dailyDate,
	})
	if !strings.Contains(wide, "7 days") {
		t.Errorf("advice = %q, want the configured window bound", wide)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestGetBodyCompositionTellsAnEmptyListFromAMissingOne is the absence path the
// sample settled. An account with no weigh-in gets dateWeightList as an empty array
// and totalAverage present with every metric null; a response that omits the list
// entirely is a different answer and must not look the same.
func TestGetBodyCompositionTellsAnEmptyListFromAMissingOne(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript().
		With(client.PathBodyComposition, testkit.JSON(http.StatusOK, dailyBodyBody)))

	got := h.call(t, ToolGetBodyComposition, map[string]any{argStartDate: dailyDate})
	if got["has_entry_list"] != true {
		t.Error("has_entry_list = false for a response that carried an empty array")
	}
	if got["entry_count"] != float64(0) {
		t.Errorf("entry_count = %v, want 0", got["entry_count"])
	}

	// The average is present even though the account records nothing, and every
	// metric is omitted rather than reported as a zero.
	average, ok := got["average"].(map[string]any)
	if !ok {
		t.Fatalf("average = %T, want the object Garmin sends even with no data",
			got["average"])
	}
	if average["until_epoch_ms"] != float64(1769903999999) {
		t.Errorf("until_epoch_ms = %v, want the last millisecond of the window",
			average["until_epoch_ms"])
	}
	for _, metric := range []string{
		keyWeight, "bmi", "body_fat", "body_water", "bone_mass", "muscle_mass",
		"physique_rating", "visceral_fat", "metabolic_age", "trend",
	} {
		if _, present := average[metric]; present {
			t.Errorf("%s is present for an account that records no weight", metric)
		}
	}
}

func TestGetBodyCompositionReportsAResponseWithNoList(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript().
		With(client.PathBodyComposition, testkit.JSON(http.StatusOK, `{}`)))

	got := h.call(t, ToolGetBodyComposition, map[string]any{argStartDate: dailyDate})
	if got["has_entry_list"] != false {
		t.Error("has_entry_list = true for a response that carried no list")
	}
	if _, present := got["average"]; present {
		t.Error("average is present for a response that carried no object")
	}
	if _, present := got["reported_start_date"]; present {
		t.Error("reported_start_date is present for a response that echoed no window")
	}
}

func TestBodyCompositionLogValueReportsShapeAndNoReading(t *testing.T) {
	t.Parallel()

	composition := BodyComposition{
		StartDate:    dailyDate,
		EndDate:      dailyDate,
		HasEntryList: true,
		Entries:      []any{map[string]any{"weight": 72000.0}},
		EntryCount:   1,
		Average:      &BodyCompositionAverage{Weight: 72000.0},
	}

	rendered := composition.LogValue().String()
	if strings.Contains(rendered, "72000") {
		t.Errorf("LogValue rendered a weight: %s", rendered)
	}
	if !strings.Contains(rendered, "average=set") || !strings.Contains(rendered, "entries=1") {
		t.Errorf("LogValue = %s, want the shape of the result", rendered)
	}
}
func TestBoundedUntypedSkipsAnEntryItCannotDecode(t *testing.T) {
	t.Parallel()

	entries := []json.RawMessage{json.RawMessage(`{"a":1}`), json.RawMessage("null")}
	out := boundedUntyped(entries, 10)
	if out.Truncated {
		t.Error("Truncated = true for a list inside its bound")
	}
	if len(out.Values) != 1 {
		t.Errorf("%d entries kept, want the one decodable record", len(out.Values))
	}
}

// dailyBodyIdentifiedBody is a synthetic weigh-in window in which both the untyped
// per-weigh-in record and the untyped metric carry an account identifier. Nothing
// here is a recording of a real account.
const dailyBodyIdentifiedBody = `{"startDate":"2026-01-31","endDate":"2026-01-31",` +
	`"dateWeightList":[{"weight":72000,"userProfilePK":900001,"sourceType":"INDEX_SCALE"}],` +
	`"totalAverage":{"from":1769817600000,"until":1769903999999,` +
	`"weight":{"value":72000,"userProfilePK":900001},"bmi":null,"bodyFat":null,` +
	`"bodyWater":null,"boneMass":null,"muscleMass":null,"physiqueRating":null,` +
	`"visceralFat":null,"metabolicAge":null,"trend":null}}`

// TestGetBodyCompositionDropsIdentifyingFields is the passthrough-egress regression
// for both untyped surfaces of this tool at once: the per-weigh-in record, whose
// element shape has never been observed, and the ten metrics, which accept whatever
// JSON Garmin sends. Neither may carry an account identifier to the caller.
func TestGetBodyCompositionDropsIdentifyingFields(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript().
		With(client.PathBodyComposition, testkit.JSON(http.StatusOK, dailyBodyIdentifiedBody)))

	got := h.call(t, ToolGetBodyComposition, map[string]any{argStartDate: dailyDate})

	rendered, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshalling the result: %v", err)
	}
	for _, forbidden := range []string{keyUserProfilePK, fixtureProfilePK} {
		if strings.Contains(string(rendered), forbidden) {
			t.Errorf("the result carries %q, which identifies an account", forbidden)
		}
	}
	if got["dropped_fields"] != float64(2) {
		t.Errorf("dropped_fields = %v, want 2", got["dropped_fields"])
	}

	// The reading itself survives: the sanitiser removes identifiers, not data.
	entries, ok := got["entries"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("entries = %#v, want the one record", got["entries"])
	}
	record, ok := entries[0].(map[string]any)
	if !ok {
		t.Fatalf("entries[0] = %#v, want an object", entries[0])
	}
	if _, present := record[keyWeight]; !present {
		t.Error("the weigh-in record lost its weight, want only the identifier removed")
	}
}
