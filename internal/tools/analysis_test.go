package tools_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// Synthetic analysis fixtures. The coordinates are the null island and the health
// values are invented.
const (
	splitSummariesBody = `{"splitSummaries":[{"splitType":"RWD_RUN","noOfSplits":6,` +
		`"duration":1200.0,"distance":4000.0,"totalAscent":40.0,"averageSpeed":3.2,` +
		`"maxSpeed":4.1,"calories":320}]}`
	hrZonesBody = `[{"zoneNumber":1,"secsInZone":600,"zoneLowBoundary":90,` +
		`"zoneHighBoundary":117},{"zoneNumber":2,"secsInZone":900,"zoneLowBoundary":118,` +
		`"zoneHighBoundary":137}]`
	powerZonesBody = `[{"zoneNumber":"1","secsInZone":"120","zoneLowBoundary":"0",` +
		`"zoneHighBoundary":"150"}]`
	weatherBody = `{"temp":50.0,"apparentTemp":48.0,"dewPoint":41.0,"relativeHumidity":72,` +
		`"windSpeed":11,"windDirection":210,"latitude":0.0,"longitude":0.0,` +
		`"issueDate":"2026-01-31T06:00:00.0"}`
	statuteSettingsBody = `{"id":900001,"userData":{"measurementSystem":"statute_us"}}`
	personalRecordsBody = `[{"id":1,"typeId":3,"activityId":9001,"value":1500.0,` +
		`"activityName":"Synthetic 5k","prStartTimeGmt":"2026-01-10T06:00:00.0"}]`
	profileSettingsBody = `{"id":1,"profileId":900001,"displayName":"fake-tester",` +
		`"fullName":"Fake Tester","gender":"UNSPECIFIED","birthDate":"1990-01-01",` +
		`"measurementSystem":"metric"}`
	activityFileBody = "SYNTHETIC-FIT-BYTES"
)

func analysisScript() testkit.Script {
	return testkit.NewScript().
		With(client.PathSocialProfile, repeat(okJSON(profileBody), 8)...).
		With(client.PathUserSettings, repeat(okJSON(settingsBody), 4)...).
		With(activityDetailPath(client.SegmentSplits), okJSON(`{"lapDTOs":[`+splitEntry+`]}`)).
		With(activityDetailPath(client.SegmentSplitSummaries), okJSON(splitSummariesBody)).
		With(activityDetailPath(client.SegmentHRInZones), okJSON(hrZonesBody)).
		With(activityDetailPath(client.SegmentPowerInZones), okJSON(powerZonesBody)).
		With(activityDetailPath(client.SegmentWeather), okJSON(weatherBody))
}

func TestGetActivitySplitsNormalizesTheShapeGarminAnswersWith(t *testing.T) {
	h := newHarness(t, analysisScript())

	out := h.call(t, tools.ToolGetActivitySplits, map[string]any{argActivityID: testActivityID})

	if got := out["count"]; got != float64(1) {
		t.Errorf("count = %v, want 1", got)
	}
}

func TestGetActivitySplitSummariesReturnsTheAggregatedGroups(t *testing.T) {
	h := newHarness(t, analysisScript())

	out := h.call(t, tools.ToolGetActivitySplitSummaries,
		map[string]any{argActivityID: testActivityID})

	summaries, _ := out["summaries"].([]any)
	if len(summaries) != 1 {
		t.Fatalf("got %d summaries, want 1", len(summaries))
	}
	first, _ := summaries[0].(map[string]any)
	if got := first["split_type"]; got != "RWD_RUN" {
		t.Errorf("split_type = %v, want RWD_RUN", got)
	}
}

func TestHeartRateZonesAreReturnedInOrder(t *testing.T) {
	h := newHarness(t, analysisScript())

	out := h.call(t, tools.ToolGetActivityHRInZones,
		map[string]any{argActivityID: testActivityID})

	if zones, _ := out["zones"].([]any); len(zones) != 2 {
		t.Fatalf("got %d zones, want 2", len(zones))
	}
}

func TestPowerZonesDecodeTheNumericStringsGarminSends(t *testing.T) {
	h := newHarness(t, analysisScript())

	out := h.call(t, tools.ToolGetActivityPowerInZones,
		map[string]any{argActivityID: testActivityID})

	zones, _ := out["zones"].([]any)
	if len(zones) != 1 {
		t.Fatalf("got %d zones, want 1", len(zones))
	}
	first, _ := zones[0].(map[string]any)
	if got := first["seconds_in_zone"]; got != float64(120) {
		t.Errorf("seconds_in_zone = %v, want 120 decoded from the numeric string", got)
	}
}

func TestActivityWeatherConvertsToCelsiusForAMetricAccount(t *testing.T) {
	h := newHarness(t, analysisScript())

	out := h.call(t, tools.ToolGetActivityWeather, map[string]any{argActivityID: testActivityID})

	if got := out["temperature_unit"]; got != "C" {
		t.Errorf("temperature_unit = %v, want C for a metric account", got)
	}
	if got := out["temperature"]; got != float64(10) {
		t.Errorf("temperature = %v, want 50F converted to 10C", got)
	}
}

func TestActivityWeatherKeepsFahrenheitForAStatuteAccount(t *testing.T) {
	script := analysisScript().With(client.PathUserSettings,
		repeat(okJSON(statuteSettingsBody), 4)...)
	h := newHarness(t, script)

	out := h.call(t, tools.ToolGetActivityWeather, map[string]any{argActivityID: testActivityID})

	if got := out["temperature_unit"]; got != "F" {
		t.Errorf("temperature_unit = %v, want F for a statute account", got)
	}
	if got := out["temperature"]; got != float64(50) {
		t.Errorf("temperature = %v, want the unconverted 50F", got)
	}
}

func TestActivityWeatherCarriesNoCoordinate(t *testing.T) {
	h := newHarness(t, analysisScript())

	out := h.call(t, tools.ToolGetActivityWeather, map[string]any{argActivityID: testActivityID})

	for _, forbidden := range []string{"latitude", "longitude"} {
		if _, present := out[forbidden]; present {
			t.Errorf("the weather result carries %q, which places a person", forbidden)
		}
	}
}

func profileReadScript() testkit.Script {
	return testkit.NewScript().
		With(client.PathSocialProfile, repeat(okJSON(profileBody), 4)...).
		With(client.PathUserProfileSettings, okJSON(profileSettingsBody)).
		With(client.PathPersonalRecords+"/"+testDisplayName, okJSON(personalRecordsBody))
}

func TestGetUserProfileSettingsReturnsTheProfileDocument(t *testing.T) {
	h := newHarness(t, profileReadScript())

	out := h.call(t, tools.ToolGetUserProfileSettings, nil)

	if got := out["profile_id"]; got != float64(900001) {
		t.Errorf("profile_id = %v, want 900001", got)
	}
}

func TestGetPersonalRecordReadsThroughTheValidatedDisplayName(t *testing.T) {
	h := newHarness(t, profileReadScript())

	out := h.call(t, tools.ToolGetPersonalRecord, nil)

	if got := out["count"]; got != float64(1) {
		t.Errorf("count = %v, want 1", got)
	}
	if !strings.Contains(strings.Join(h.requests(), " "), testDisplayName) {
		t.Errorf("the read did not go through the display name: %v", h.requests())
	}
}

func downloadScript() testkit.Script {
	return testkit.NewScript().
		With(client.PathSocialProfile, repeat(okJSON(profileBody), 4)...).
		With(client.PathActivityOriginalDownload+"/"+testActivityID, testkit.Behavior{
			Status: http.StatusOK, ContentType: "application/zip", Body: activityFileBody,
		}).
		With(client.PathActivityGPXDownload+"/"+testActivityID, testkit.Behavior{
			Status: http.StatusOK, ContentType: "application/gpx+xml", Body: "<gpx/>",
		})
}

func TestDownloadActivityFileReturnsAnEmbeddedResourceAndNamesNoPath(t *testing.T) {
	h := newWriteHarness(t, downloadScript(), enabledWrites())

	result := h.rawCall(t, tools.ToolDownloadActivityFile,
		map[string]any{argActivityID: testActivityID})
	if result.IsError {
		t.Fatalf("download_activity_file failed: %s", resultText(result))
	}
	if len(result.Content) == 0 {
		t.Fatal("the result carries no content block")
	}

	out := structured(t, tools.ToolDownloadActivityFile, result)
	if got := out[argFormat]; got != "fit" {
		t.Errorf("format = %v, want the declared fit default", got)
	}
	if got := out["media_type"]; got != "application/zip" {
		t.Errorf("media_type = %v, want this server's own label", got)
	}
	uri, _ := out["uri"].(string)
	if !strings.HasPrefix(uri, "garmin://activity/") {
		t.Errorf("uri = %q, want a garmin:// resource URI and never a filesystem path", uri)
	}
}

func TestDownloadActivityFileHonoursTheRequestedFormat(t *testing.T) {
	h := newWriteHarness(t, downloadScript(), enabledWrites())

	out := h.call(t, tools.ToolDownloadActivityFile, map[string]any{
		argActivityID: testActivityID,
		argFormat:     "gpx",
	})

	if got := out[argFormat]; got != "gpx" {
		t.Errorf("format = %v, want gpx", got)
	}
}

func TestDownloadActivityFileRefusesAnUnknownFormat(t *testing.T) {
	h := newWriteHarness(t, downloadScript(), enabledWrites())

	h.callError(t, tools.ToolDownloadActivityFile, map[string]any{
		argActivityID: testActivityID,
		argFormat:     traversalAttempt,
	})

	if len(h.fake.Requests()) != 0 {
		t.Errorf("an unknown format still reached Garmin: %v", h.recordedMethods())
	}
}

func TestDownloadActivityFileAcceptsNoDirectoryArgument(t *testing.T) {
	t.Parallel()

	contract, ok := tools.Contracts()[tools.ToolDownloadActivityFile]
	if !ok {
		t.Fatalf("%s is not registered", tools.ToolDownloadActivityFile)
	}
	for _, property := range contract.Schema.Properties() {
		if property.Name == "output_dir" {
			t.Error("the download tool declares output_dir: a tool argument must not " +
				"choose a server filesystem path")
		}
	}
}
