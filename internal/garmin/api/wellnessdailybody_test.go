package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// The body-composition envelope, beside the file it covers. Every fixture is
// synthetic.
const (
	// The sampled account records no weight: the list is empty rather than absent
	// and every metric of the average is null. No metric type is asserted anywhere
	// in this package, because no sample has shown one.
	bodyCompositionBody = `{"startDate":"` + testWindowStart + `","endDate":"` +
		testCalendarDate + `","dateWeightList":[],"totalAverage":{` +
		`"from":1767225600000,"until":1769903999999,"weight":null,"bmi":null,` +
		`"bodyFat":null,"bodyWater":null,"boneMass":null,"muscleMass":null,` +
		`"physiqueRating":null,"visceralFat":null,"metabolicAge":null,"trend":null}}`
)

func TestWellnessDailyBodyCompositionDecodesTheAbsenceShape(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathBodyComposition,
		testkit.JSON(http.StatusOK, bodyCompositionBody))
	h := newHarness(t, script, client.Limits{})

	span := mustRange(t, testWindowStart, testCalendarDate)
	got, err := newWellnessDaily(t, h).BodyComposition(t.Context(), h.session, span)
	if err != nil {
		t.Fatalf("BodyComposition() = %v", err)
	}
	if got.StartDate == nil || *got.StartDate != testWindowStart {
		t.Errorf("StartDate = %v, want Garmin's echo of the window", got.StartDate)
	}
	// An account with no weigh-in gets an empty list, not a missing one, and the
	// two are different answers.
	if got.DateWeightList == nil {
		t.Fatal("DateWeightList = nil for a response that carried an empty array")
	}
	if got.DateWeightList.Len() != 0 {
		t.Errorf("DateWeightList holds %d entries, want none", got.DateWeightList.Len())
	}
	// totalAverage is present-but-empty, so absence is per metric.
	if got.TotalAverage == nil {
		t.Fatal("TotalAverage = nil for a response that carried the object")
	}
	if from, ok := got.TotalAverage.From.Int64(); !ok || from != 1767225600000 {
		t.Errorf("From = %v/%v, want the epoch milliseconds verbatim", from, ok)
	}
	if until, ok := got.TotalAverage.Until.Int64(); !ok || until != 1769903999999 {
		t.Errorf("Until = %v/%v, want the last millisecond of the window", until, ok)
	}
	assertNoMetricIsTyped(t, *got.TotalAverage)
	requests := h.server.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want 1", len(requests))
	}
	if got := requests[0].Query.Get(client.QueryStartDate); got != testWindowStart {
		t.Errorf("startDate = %q, want %q", got, testWindowStart)
	}
	if got := requests[0].Query.Get(client.QueryEndDate); got != testCalendarDate {
		t.Errorf("endDate = %q, want %q", got, testCalendarDate)
	}
}

func TestWellnessDailyBodyCompositionBoundsTheWindow(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{MaxDateRangeDays: 3})
	daily := newWellnessDaily(t, h)

	_, err := daily.BodyComposition(t.Context(), h.session, mustRange(t, testWindowStart, testCalendarDate))
	if !errors.Is(err, client.ErrValidation) {
		t.Errorf("BodyComposition() over the bound = %v, want a validation error", err)
	}
	if _, err := daily.BodyComposition(t.Context(), h.session,
		client.DateRange{}); !errors.Is(err, client.ErrValidation) {
		t.Errorf("BodyComposition() with no window = %v, want a validation error", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestWellnessDailyStatsAndBodyReadsBothHalves(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(summaryPath(), testkit.JSON(http.StatusOK, dailyStatsBody)).
		With(client.PathBodyComposition, testkit.JSON(http.StatusOK, bodyCompositionBody))
	h := newHarness(t, script, client.Limits{})

	stats, body, err := newWellnessDaily(t, h).StatsAndBody(t.Context(), h.session,
		mustDisplayName(t), mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("StatsAndBody() = %v", err)
	}
	if steps, ok := stats.TotalSteps.Int64(); !ok || steps != 9123 {
		t.Errorf("TotalSteps = %v/%v, want 9123", steps, ok)
	}
	if body.TotalAverage == nil {
		t.Error("StatsAndBody() dropped the body-composition average")
	}
	if got := len(h.server.Requests()); got != 2 {
		t.Errorf("the fake received %d requests, want 2", got)
	}
}

func TestWellnessDailyStatsAndBodyPropagatesTheFirstFailure(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(summaryPath(), testkit.Behavior{Status: http.StatusNoContent})
	h := newHarness(t, script, client.Limits{})

	_, _, err := newWellnessDaily(t, h).StatsAndBody(t.Context(), h.session,
		mustDisplayName(t), mustDate(t, testCalendarDate))
	if !errors.Is(err, client.ErrUnexpectedResponse) {
		t.Fatalf("StatsAndBody() = %v, want the stats failure", err)
	}
	if got := len(h.server.Requests()); got != 1 {
		t.Errorf("the fake received %d requests, want only the failed one", got)
	}
}

// decodedOrNil renders a verbatim metric, treating an explicit null and an absent
// field alike as no value. It asserts nothing about the type of a populated metric,
// because no sample has ever shown one.
func decodedOrNil(t *testing.T, raw json.RawMessage) any {
	t.Helper()

	if len(raw) == 0 || string(raw) == nullBody {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decoding a metric: %v", err)
	}
	return decoded
}

// assertNoMetricIsTyped checks that every metric carried its null through as no
// value. It deliberately asserts nothing about a populated metric: no sample has
// shown one, and a fixture that claimed a type would be the very defect this
// project has already been bitten by five times.
func assertNoMetricIsTyped(t *testing.T, average api.BodyCompositionAverage) {
	t.Helper()

	for name, raw := range map[string]json.RawMessage{
		"Weight": average.Weight, "BMI": average.BMI, "BodyFat": average.BodyFat,
		"BodyWater": average.BodyWater, "BoneMass": average.BoneMass,
		"MuscleMass": average.MuscleMass, "PhysiqueRating": average.PhysiqueRating,
		"VisceralFat": average.VisceralFat, "MetabolicAge": average.MetabolicAge,
		"Trend": average.Trend,
	} {
		if decoded := decodedOrNil(t, raw); decoded != nil {
			t.Errorf("%s = %v, want the null carried through as no value", name, decoded)
		}
	}
}
