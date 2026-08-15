package api_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// segmentPath builds a per-activity segment path for the fixture activity.
func segmentPath(segment string) string {
	return client.PathActivityPrefix + "/18446744/" + segment
}

// analysisScript scripts every analysis read of one activity.
func analysisScript() testkit.Script {
	return testkit.NewScript().
		With(activityWritePath(), testkit.JSON(http.StatusOK,
			`{"activityId":18446744,"activityName":"Morning Run",`+
				`"summaryDTO":{"distance":10000},"surpriseBlock":{"x":1}}`)).
		With(segmentPath(client.SegmentSplits), testkit.JSON(http.StatusOK,
			`{"lapDTOs":[`+splitEntry+`]}`)).
		With(segmentPath(client.SegmentSplitSummaries), testkit.JSON(http.StatusOK,
			`{"splitSummaries":[{"splitType":"CLIMB_ACTIVE","noOfSplits":"4",`+
				`"duration":900,"unknownField":true}]}`)).
		With(segmentPath(client.SegmentWeather), testkit.JSON(http.StatusOK,
			`{"temp":48,"latitude":48.1,"longitude":11.5,"weatherTypeDTO":{"desc":"Clear"}}`)).
		With(segmentPath(client.SegmentHRInZones), testkit.JSON(http.StatusOK,
			`[{"zoneNumber":"1","secsInZone":600},{"zoneNumber":2,"secsInZone":"300"}]`)).
		With(segmentPath(client.SegmentPowerInZones), testkit.JSON(http.StatusOK, `[]`)).
		With(client.PathActivityTypes, testkit.JSON(http.StatusOK,
			`[{"typeId":1,"typeKey":"running","parentTypeId":17,"isHidden":false}]`)).
		With(client.PathActivityEventTypes, testkit.JSON(http.StatusOK,
			`{"typeId":9,"typeKey":"training"}`))
}

// TestActivitySummaryAndSplitsDecodeEveryShape covers the activity record and
// the two split collections, whose payload shape varies by activity type.
func TestActivitySummaryAndSplitsDecodeEveryShape(t *testing.T) {
	t.Parallel()

	h := newHarness(t, analysisScript(), client.Limits{})
	details := newActivityDetails(t, h)

	summary, err := details.Summary(t.Context(), h.session, mustID(t))
	if err != nil {
		t.Fatalf("Summary() = %v", err)
	}
	if id, ok := summary.ActivityID.Int64(); !ok || id != testActivityID {
		t.Errorf("ActivityID = %d/%v, want the fixture activity", id, ok)
	}
	if len(summary.Summary) == 0 || summary.Payload().Len() == 0 {
		t.Error("Summary() dropped the summaryDTO or the retained payload")
	}

	splits, err := details.Splits(t.Context(), h.session, mustID(t))
	if err != nil {
		t.Fatalf("Splits() = %v", err)
	}
	if splits.Len() != 1 {
		t.Errorf("%d splits, want the lapDTOs shape decoded", splits.Len())
	}

	summaries, err := details.SplitSummaries(t.Context(), h.session, mustID(t))
	if err != nil {
		t.Fatalf("SplitSummaries() = %v", err)
	}
	if summaries.Summaries.Len() != 1 {
		t.Errorf("%d split summaries, want 1", summaries.Summaries.Len())
	}
	if count, ok := summaries.Summaries.Items()[0].NoOfSplits.Int64(); !ok || count != 4 {
		t.Errorf("NoOfSplits = %d/%v, want the numeric string decoded", count, ok)
	}
	if summaries.Payload().Len() == 0 {
		t.Error("SplitSummaries() retained no raw payload")
	}
}

// TestWeatherAndZoneReadsDecodeEveryShape covers the three reads whose values
// arrive as numbers on one endpoint and as numeric strings on another.
func TestWeatherAndZoneReadsDecodeEveryShape(t *testing.T) {
	t.Parallel()

	h := newHarness(t, analysisScript(), client.Limits{})
	details := newActivityDetails(t, h)

	weather, err := details.Weather(t.Context(), h.session, mustID(t))
	if err != nil {
		t.Fatalf("Weather() = %v", err)
	}
	if temp, ok := weather.Temp.Float64(); !ok || temp != 48 {
		t.Errorf("Temp = %v/%v, want 48", temp, ok)
	}
	if weather.Payload().Len() == 0 {
		t.Error("Weather() retained no raw payload")
	}

	hr, err := details.HRInZones(t.Context(), h.session, mustID(t))
	if err != nil {
		t.Fatalf("HRInZones() = %v", err)
	}
	if len(hr) != 2 {
		t.Fatalf("%d heart-rate zones, want 2", len(hr))
	}
	if zone, ok := hr[0].ZoneNumber.Int64(); !ok || zone != 1 {
		t.Errorf("ZoneNumber = %d/%v, want the numeric string decoded", zone, ok)
	}

	power, err := details.PowerInZones(t.Context(), h.session, mustID(t))
	if err != nil {
		t.Fatalf("PowerInZones() = %v", err)
	}
	if len(power) != 0 {
		t.Errorf("%d power zones, want none: an activity without a power meter has none",
			len(power))
	}
}

// TestCatalogReadsAcceptBothShapes covers the activity-type and event-type
// catalogs, which answer with an array and, for the event types, a bare object.
func TestCatalogReadsAcceptBothShapes(t *testing.T) {
	t.Parallel()

	h := newHarness(t, analysisScript(), client.Limits{})
	details := newActivityDetails(t, h)

	types, err := details.Types(t.Context(), h.session)
	if err != nil {
		t.Fatalf("Types() = %v", err)
	}
	if len(types) != 1 {
		t.Fatalf("%d activity types, want 1", len(types))
	}
	if key, ok := types[0].TypeKey.Value(); !ok || key != typeKeyRunning {
		t.Errorf("TypeKey = %q/%v, want running", key, ok)
	}
	if parent, ok := types[0].ParentTypeID.Int64(); !ok || parent != 17 {
		t.Errorf("ParentTypeID = %d/%v, want 17: a type change needs the whole triple",
			parent, ok)
	}

	events, err := details.EventTypes(t.Context(), h.session)
	if err != nil {
		t.Fatalf("EventTypes() = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("%d event types, want the single object decoded as one", len(events))
	}
}

// TestAnalysisReadsRefuseAnUnsetActivity keeps validation ahead of dispatch for
// every per-activity read.
func TestAnalysisReadsRefuseAnUnsetActivity(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	details := newActivityDetails(t, h)

	calls := map[string]func() error{
		"summary": func() error {
			_, err := details.Summary(t.Context(), h.session, client.ID{})
			return err
		},
		"splits": func() error {
			_, err := details.Splits(t.Context(), h.session, client.ID{})
			return err
		},
		"split summaries": func() error {
			_, err := details.SplitSummaries(t.Context(), h.session, client.ID{})
			return err
		},
		"weather": func() error {
			_, err := details.Weather(t.Context(), h.session, client.ID{})
			return err
		},
		"hr in zones": func() error {
			_, err := details.HRInZones(t.Context(), h.session, client.ID{})
			return err
		},
		"power in zones": func() error {
			_, err := details.PowerInZones(t.Context(), h.session, client.ID{})
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, client.ErrValidation) {
				t.Errorf("call = %v, want ErrValidation", err)
			}
		})
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}
