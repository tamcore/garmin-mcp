package tools

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// latestLactateDocument is the two-entry shape Garmin answers the latest read with:
// one entry carries the speed, the next the heart rate under its historical
// misspelling. Both carry an account key, which must not reach the result.
const latestLactateDocument = `[{"userProfilePK":900001,"calendarDate":"` + scoresEndDate +
	`","sequence":1,"version":2,"speed":3.42,"heartRate":null},` +
	`{"userProfilePK":900001,"calendarDate":"` + scoresEndDate + `","hearRate":168}]`

const powerToWeightDocument = `[{"sport":"Running","functionalThresholdPower":301,` +
	`"weight":72000,"powerToWeight":4.18,"calendarDate":"` + scoresEndDate + `",` +
	`"isStale":true}]`

// lactateRangeDocument is two samples. series is a plain string — sampled, not
// assumed — and the second value arrives as a numeric string.
const lactateRangeDocument = `[{"from":"` + scoresEndDate + `","value":3.41,` +
	`"series":"running"},{"from":"2026-01-30","value":"3.38"}]`

// lactateSeriesValue is the one series value this project has sampled. The value set
// is open, so this is a fixture value and never a rule.
const lactateSeriesValue = "running"

// lactateSeriesObject is a series shaped like nothing Garmin has been seen to send.
const lactateSeriesObject = `[{"from":"` + scoresEndDate + `","value":3.41,` +
	`"series":{"kind":"daily"}}]`

// powerToWeightPaths returns the paths the latest read may ask for. The path carries
// the local clock's day, exactly as upstream's date.today() does, so a run that steps
// over midnight asks for the next day; both are scripted rather than one guessed.
func powerToWeightPaths() []string {
	today := time.Now()
	return []string{
		client.PathPowerToWeightLatestPrefix + "/" + client.NewDate(today).String(),
		client.PathPowerToWeightLatestPrefix + "/" +
			client.NewDate(today.AddDate(0, 0, 1)).String(),
	}
}

// scriptLatestLactate scripts both endpoints of the latest read.
func scriptLatestLactate(latest, power string) testkit.Script {
	script := testkit.NewScript().With(client.PathLatestLactateThreshold,
		testkit.JSON(http.StatusOK, latest))
	for _, path := range powerToWeightPaths() {
		script = script.With(path, testkit.JSON(http.StatusOK, power))
	}
	return script
}

// scriptLactateRange scripts the three series of the window read with the same two
// samples. A test that needs one series to differ overrides that one path.
func scriptLactateRange() testkit.Script {
	script := testkit.NewScript()
	for _, prefix := range []string{
		client.PathLactateThresholdSpeedRangePrefix,
		client.PathLactateThresholdHeartRateRangePrefix,
		client.PathFunctionalThresholdPowerRangePrefix,
	} {
		script = script.With(prefix+"/"+scoresStartDate+"/"+scoresEndDate,
			testkit.JSON(http.StatusOK, lactateRangeDocument))
	}
	return script
}

func TestGetLactateThresholdReturnsTheLatestReading(t *testing.T) {
	t.Parallel()

	h := newScoresHarness(t, scriptLatestLactate(latestLactateDocument, powerToWeightDocument))

	result := h.call(t, ToolGetLactateThreshold, nil)

	if got, _ := result["mode"].(string); got != lactateModeLatest {
		t.Errorf("mode = %q, want %q", got, lactateModeLatest)
	}
	if got := number(t, result, "lactate_threshold_speed_mps"); got != 3.42 {
		t.Errorf("lactate_threshold_speed_mps = %v, want 3.42", got)
	}
	// The heart rate arrives only under Garmin's misspelled key.
	if got := number(t, result, "lactate_threshold_heart_rate_bpm"); got != 168 {
		t.Errorf("lactate_threshold_heart_rate_bpm = %v, want 168", got)
	}
	if got := number(t, result, "functional_threshold_power_watts"); got != 301 {
		t.Errorf("functional_threshold_power_watts = %v, want 301", got)
	}
	if got := number(t, result, "power_to_weight"); got != 4.18 {
		t.Errorf("power_to_weight = %v, want 4.18", got)
	}
	if complete, _ := result["complete"].(bool); !complete {
		t.Error("complete = false, want true when both parts answered")
	}
	if got := len(list(t, result, "parts")); got != 2 {
		t.Errorf("parts holds %d entries, want the two endpoints of the latest read", got)
	}
}

// TestGetLactateThresholdSendsTheMixedCaseSport pins the casing upstream sends to the
// power-to-weight endpoint alone.
func TestGetLactateThresholdSendsTheMixedCaseSport(t *testing.T) {
	t.Parallel()

	h := newScoresHarness(t, scriptLatestLactate(latestLactateDocument, powerToWeightDocument))

	h.call(t, ToolGetLactateThreshold, nil)

	found := false
	for _, request := range h.fake.Requests() {
		if !strings.HasPrefix(request.Path, client.PathPowerToWeightLatestPrefix) {
			continue
		}
		found = true
		if got := request.Query.Get(client.QuerySport); got != client.SportRunningMixedCase {
			t.Errorf("sport = %q, want the mixed-case %q", got, client.SportRunningMixedCase)
		}
	}
	if !found {
		t.Error("the power-to-weight endpoint was never asked")
	}
}

// TestGetLactateThresholdReturnsNoAccountKey proves the account identifiers in the
// latest document stay behind.
func TestGetLactateThresholdReturnsNoAccountKey(t *testing.T) {
	t.Parallel()

	h := newScoresHarness(t, scriptLatestLactate(latestLactateDocument, powerToWeightDocument))

	rendered := h.text(t, ToolGetLactateThreshold, nil)
	for _, forbidden := range []string{"userProfilePK", "900001"} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("the result carries %q", forbidden)
		}
	}
}

// TestGetLactateThresholdStatesAnUnavailablePart proves a partial answer is stated
// rather than looking like an account that holds nothing.
func TestGetLactateThresholdStatesAnUnavailablePart(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathLatestLactateThreshold,
		testkit.JSON(http.StatusOK, latestLactateDocument))
	for _, path := range powerToWeightPaths() {
		script = script.With(path,
			testkit.JSON(http.StatusInternalServerError, `{"error":"upstream exploded"}`))
	}
	h := newScoresHarness(t, script)

	result := h.call(t, ToolGetLactateThreshold, nil)

	if complete, _ := result["complete"].(bool); complete {
		t.Error("complete = true, want false when a part failed")
	}
	if got := number(t, result, "lactate_threshold_speed_mps"); got != 3.42 {
		t.Errorf("the part that answered was lost: speed = %v", got)
	}
	if _, present := result["functional_threshold_power_watts"]; present {
		t.Error("a failed part produced a reading")
	}

	power := lactatePartNamed(t, result, lactatePartPower)
	if available, _ := power["available"].(bool); available {
		t.Error("the failed part reports itself available")
	}
	note, _ := power["note"].(string)
	assertNoRawPayload(t, note)
	if strings.Contains(note, "upstream exploded") {
		t.Errorf("the note %q quotes the Garmin payload", note)
	}
}

// TestGetLactateThresholdFailsWholeOnARejectedSession proves a failure about the
// session ends the call rather than being reported as one missing part.
func TestGetLactateThresholdFailsWholeOnARejectedSession(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathLatestLactateThreshold,
		testkit.JSON(http.StatusUnauthorized, `{"error":"expired"}`))
	h := newScoresHarness(t, script)

	advice := h.callError(t, ToolGetLactateThreshold, nil)
	assertNoRawPayload(t, advice)
	if !strings.Contains(advice, "Re-authenticate") {
		t.Errorf("advice = %q, want the authentication advice", advice)
	}
}

func TestGetLactateThresholdReturnsTheWindowSeries(t *testing.T) {
	t.Parallel()

	h := newScoresHarness(t, scriptLactateRange())

	result := h.call(t, ToolGetLactateThreshold, scoresWindowArgs())

	if got, _ := result["mode"].(string); got != lactateModeRange {
		t.Errorf("mode = %q, want %q", got, lactateModeRange)
	}
	if got := len(list(t, result, "parts")); got != 3 {
		t.Errorf("parts holds %d entries, want the three series of the window read", got)
	}
	for _, key := range []string{"speed_history", "heart_rate_history", "power_history"} {
		samples := list(t, result, key)
		if len(samples) != 2 {
			t.Fatalf("%s holds %d samples, want two", key, len(samples))
		}
		if got := number(t, entry(t, samples, 1), "value"); got != 3.38 {
			t.Errorf("%s[1].value = %v, want 3.38 from the string form", key, got)
		}
		if got, _ := entry(t, samples, 0)["date"].(string); got != scoresEndDate {
			t.Errorf("%s[0].date = %q, want %q", key, got, scoresEndDate)
		}
	}
}

// TestGetLactateThresholdCarriesTheSeriesAsAString proves the series reaches the
// caller as the string Garmin sent, unchanged and unmapped. The value set is open, so
// this pins the type and the passthrough, never the value.
func TestGetLactateThresholdCarriesTheSeriesAsAString(t *testing.T) {
	t.Parallel()

	h := newScoresHarness(t, scriptLactateRange())

	result := h.call(t, ToolGetLactateThreshold, scoresWindowArgs())

	first := entry(t, list(t, result, "speed_history"), 0)
	series, ok := first["series"].(string)
	if !ok {
		t.Fatalf("series = %#v, want a string", first["series"])
	}
	if series != lactateSeriesValue {
		t.Errorf("series = %q, want the sampled %q carried through", series, lactateSeriesValue)
	}
	// A sample that declares no series carries none, rather than an empty string.
	if _, present := entry(t, list(t, result, "speed_history"), 1)["series"]; present {
		t.Error("a sample with no series produced one")
	}
}

// TestGetLactateThresholdRefusesAnObjectSeries proves the narrowing.
//
// Until series was typed it was carried as an open value, so an object reached the
// caller as an object. It is a string on the wire, and an untyped passthrough is what
// leaked identifiers elsewhere in this project, so a shape no sample has shown is now
// refused: the series it belongs to reports itself unavailable, and no unread Garmin
// object leaves the process. This test fails against the untyped field, which passed
// the object straight through.
func TestGetLactateThresholdRefusesAnObjectSeries(t *testing.T) {
	t.Parallel()

	script := scriptLactateRange().With(
		client.PathLactateThresholdSpeedRangePrefix+"/"+scoresStartDate+"/"+scoresEndDate,
		testkit.JSON(http.StatusOK, lactateSeriesObject))
	h := newScoresHarness(t, script)

	result := h.call(t, ToolGetLactateThreshold, scoresWindowArgs())

	rendered := h.text(t, ToolGetLactateThreshold, scoresWindowArgs())
	if strings.Contains(rendered, "kind") {
		t.Error("an object-shaped series reached the caller")
	}
	speed := lactatePartNamed(t, result, lactatePartSpeed)
	if available, _ := speed["available"].(bool); available {
		t.Error("the speed series reports itself available after a payload it could not read")
	}
	if complete, _ := result["complete"].(bool); complete {
		t.Error("complete = true, want false when a series could not be read")
	}
	// The other two series still answer.
	if got := len(list(t, result, "power_history")); got != 2 {
		t.Errorf("power_history holds %d samples, want the two that answered", got)
	}
}

// TestGetLactateThresholdAcceptsAMissingHeartRateSeries proves absence decodes.
//
// The sampled window carried no heart-rate history at all, and whether Garmin answers
// such an account with null, with an empty array or with nothing is not settled, so
// all three are accepted and each leaves the part available and the series empty.
func TestGetLactateThresholdAcceptsAMissingHeartRateSeries(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{
		jsonNull:       jsonNull,
		caseEmptyArray: `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := scriptLactateRange().With(
				client.PathLactateThresholdHeartRateRangePrefix+"/"+
					scoresStartDate+"/"+scoresEndDate,
				testkit.JSON(http.StatusOK, body))
			h := newScoresHarness(t, script)

			result := h.call(t, ToolGetLactateThreshold, scoresWindowArgs())

			if complete, _ := result["complete"].(bool); !complete {
				t.Error("complete = false, want true: an empty series is an answer")
			}
			heartRate := lactatePartNamed(t, result, lactatePartHeartRate)
			if available, _ := heartRate["available"].(bool); !available {
				t.Error("the heart-rate part reports itself unavailable for an empty answer")
			}
			if samples, present := result["heart_rate_history"]; present {
				if list, ok := samples.([]any); !ok || len(list) != 0 {
					t.Errorf("heart_rate_history = %#v, want absent or empty", samples)
				}
			}
			if got := len(list(t, result, "speed_history")); got != 2 {
				t.Errorf("speed_history holds %d samples, want the two that answered", got)
			}
		})
	}
}

func TestGetLactateThresholdRefusesHalfAWindow(t *testing.T) {
	t.Parallel()

	for name, args := range map[string]map[string]any{
		"start only": {argStartDate: scoresStartDate},
		"end only":   {argEndDate: scoresEndDate},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newScoresHarness(t, testkit.NewScript())

			advice := h.callError(t, ToolGetLactateThreshold, args)
			assertNoRawPayload(t, advice)
			if !strings.Contains(advice, "together") {
				t.Errorf("advice = %q, want it to say the dates go together", advice)
			}
			if got := len(h.fake.Requests()); got != 0 {
				t.Errorf("the fake received %d requests, want none", got)
			}
		})
	}
}

func TestGetLactateThresholdBoundsTheWindow(t *testing.T) {
	t.Parallel()

	h := newScoresHarnessWith(t, testkit.NewScript(), client.Limits{MaxDateRangeDays: 3})

	advice := h.callError(t, ToolGetLactateThreshold, scoresWindowArgs())
	assertNoRawPayload(t, advice)
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want none", got)
	}
}

// TestLactateThresholdLogValueReportsShapeOnly proves the log record carries no reading.
func TestLactateThresholdLogValueReportsShapeOnly(t *testing.T) {
	t.Parallel()

	speed := 3.42
	value := LactateThreshold{
		Mode:         lactateModeLatest,
		SpeedMPS:     &speed,
		SpeedHistory: []ThresholdPoint{{Value: &speed}},
		Complete:     true,
	}.LogValue().String()

	if strings.Contains(value, "3.42") {
		t.Errorf("the log value %q carries a reading", value)
	}
	if !strings.Contains(value, "lactateThreshold") {
		t.Errorf("the log value %q does not name the model", value)
	}
}

// lactatePartNamed finds one part of the result by name.
func lactatePartNamed(t *testing.T, result map[string]any, name string) map[string]any {
	t.Helper()

	parts := list(t, result, "parts")
	for index := range parts {
		part := entry(t, parts, index)
		if got, _ := part["name"].(string); got == name {
			return part
		}
	}
	t.Fatalf("the result carries no part named %q", name)
	return nil
}

// TestLactateThresholdAsksForTheLocalCalendarDay is the regression for a latest
// reading fetched under the wrong day.
//
// The power-to-weight path is keyed by a calendar date, and the day was taken with
// client.NewDate, which converts to UTC first. Just after local midnight in a zone
// ahead of UTC that names yesterday, so the tool asked Garmin for a day the account
// had already left — while the comment above it claimed the local day was used.
func TestLactateThresholdAsksForTheLocalCalendarDay(t *testing.T) {
	t.Parallel()

	ahead := time.FixedZone("ahead", 2*60*60)
	justAfterMidnight := time.Date(2026, time.February, 3, 0, 30, 0, 0, ahead)
	localDay, utcDay := "2026-02-03", "2026-02-02"

	script := testkit.NewScript().
		With(client.PathLatestLactateThreshold, testkit.JSON(http.StatusOK, `[]`)).
		With(client.PathPowerToWeightLatestPrefix+"/"+localDay,
			testkit.JSON(http.StatusOK, `[]`)).
		With(client.PathPowerToWeightLatestPrefix+"/"+utcDay,
			testkit.JSON(http.StatusOK, `[]`))

	h := newScoresHarnessAt(t, script, client.Limits{}, func() time.Time {
		return justAfterMidnight
	})
	h.call(t, ToolGetLactateThreshold, map[string]any{})

	asked := ""
	for _, request := range h.fake.Requests() {
		if strings.Contains(request.Path, client.PathPowerToWeightLatestPrefix) {
			asked = request.Path
		}
	}
	if asked == "" {
		t.Fatal("the power-to-weight path was never asked for, so this proves nothing")
	}
	if !strings.HasSuffix(asked, localDay) {
		t.Errorf("asked for %q, want the account's local day %s rather than the UTC day %s",
			asked, localDay, utcDay)
	}
}
