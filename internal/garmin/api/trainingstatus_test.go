package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// The shape tests of the one aggregated training-status document, which the scores
// read and the three trend reads share. Every fixture is synthetic.

// numericStatusBody pins the observed pairing: the status and the fitness trend arrive
// as numeric codes and the human-readable phrases arrive beside them as strings, in
// their own fields. Both travel in one device entry, which is the shape a sampled day
// showed, so the tolerance is proven rather than incidental.
const numericStatusBody = `{"mostRecentTrainingStatus":{"latestTrainingStatusData":{` +
	`"3001":{"calendarDate":"2026-01-31","primaryTrainingDevice":true,` +
	`"trainingStatus":3,"trainingStatusFeedbackPhrase":"PRODUCTIVE_1",` +
	`"sport":"RUNNING","fitnessTrend":1,` +
	`"acuteTrainingLoadDTO":{"acwrPercent":63,"acwrStatus":"OPTIMAL"}}}},` +
	`"mostRecentTrainingLoadBalance":{"metricsTrainingLoadBalanceDTOMap":{` +
	`"3001":{"trainingBalanceFeedbackPhrase":"AEROBIC_HIGH_SHORTAGE"}}}}`

// stringStatusBody is the same document with the codes spelled as strings, which this
// project has not observed and must not fail on.
const stringStatusBody = `{"mostRecentTrainingStatus":{"latestTrainingStatusData":{` +
	`"3001":{"trainingStatus":"UNOBSERVED_CODE_9999","fitnessTrend":"3"}}}}`

// textField pairs a decoded field with what it must render as.
type textField struct {
	value client.Text
	want  string
}

// TestTrainingStatusDecodesNumericCodesBesideStringPhrases is the evidence test.
//
// A sampled day showed trainingStatus and fitnessTrend as integers, with the phrases
// in separate string fields; client.Number would have failed on the phrases and a
// closed enum would refuse codes this project has never seen. Text carries both, and
// the numeric code is rendered as the digits Garmin sent rather than translated.
func TestTrainingStatusDecodesNumericCodesBesideStringPhrases(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript().With(
		client.PathTrainingStatusPrefix+"/"+trendEndDate,
		testkit.JSON(http.StatusOK, numericStatusBody)), client.Limits{})

	status, err := newTrainingTrends(t, h).TrainingLoadDay(t.Context(), h.session,
		mustDate(t, trendEndDate))
	if err != nil {
		t.Fatalf("TrainingLoadDay() = %v", err)
	}
	device, ok := status.PrimaryStatus()
	if !ok {
		t.Fatal("PrimaryStatus() reported no device")
	}

	fields := map[string]textField{
		"trainingStatus": {device.TrainingStatus, "3"},
		"fitnessTrend":   {device.FitnessTrend, "1"},
		"feedbackPhrase": {device.FeedbackPhrase, "PRODUCTIVE_1"},
		"sport":          {device.Sport, "RUNNING"},
	}
	for name, field := range fields {
		value, present := field.value.Value()
		if !present || value != field.want {
			t.Errorf("%s = %q (present %v), want %q", name, value, present, field.want)
		}
	}

	load := device.AcuteTrainingLoad
	if load == nil {
		t.Fatal("the acute training load did not decode")
	}
	if percent, present := load.ACWRPercent.Float64(); !present || percent != 63 {
		t.Errorf("acwrPercent = %v (present %v), want the number 63", percent, present)
	}
	if label, _ := load.ACWRStatus.Value(); label != "OPTIMAL" {
		t.Errorf("acwrStatus = %q, want the phrase beside the number", label)
	}
	balance, ok := status.PrimaryLoadBalance()
	if !ok {
		t.Fatal("PrimaryLoadBalance() reported no device")
	}
	if phrase, _ := balance.FeedbackPhrase.Value(); phrase != "AEROBIC_HIGH_SHORTAGE" {
		t.Errorf("trainingBalanceFeedbackPhrase = %q, want the phrase as sent", phrase)
	}
}

// TestTrainingStatusPassesAnUnobservedCodeThrough proves nothing models the status
// codes as a closed set: one day of one account shows a fraction of Garmin's range, so
// a code this project has never seen must arrive intact rather than be refused.
func TestTrainingStatusPassesAnUnobservedCodeThrough(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript().With(
		client.PathTrainingStatusPrefix+"/"+trendEndDate,
		testkit.JSON(http.StatusOK, stringStatusBody)), client.Limits{})

	status, err := newTrainingTrends(t, h).TrainingLoadDay(t.Context(), h.session,
		mustDate(t, trendEndDate))
	if err != nil {
		t.Fatalf("TrainingLoadDay() = %v", err)
	}
	device, _ := status.PrimaryStatus()
	if got, _ := device.TrainingStatus.Value(); got != "UNOBSERVED_CODE_9999" {
		t.Errorf("trainingStatus = %q, want an unknown code carried through unchanged", got)
	}
	if got, _ := device.FitnessTrend.Value(); got != "3" {
		t.Errorf("fitnessTrend = %q, want the string spelling accepted too", got)
	}
}

// TestOneDocumentServesTheScoresReadAndTheTrendReads is the merge test.
//
// The aggregated status is modeled once. This drives the same bytes through the scores
// client's read and through the trend client's read and requires both to answer with
// the same figures: if the two models were ever split again, this fails.
func TestOneDocumentServesTheScoresReadAndTheTrendReads(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript().With(
		client.PathTrainingStatusPrefix+"/"+trendEndDate,
		testkit.JSON(http.StatusOK, trendStatusBody)), client.Limits{})

	scores, err := api.NewTrainingScores(h.rc)
	if err != nil {
		t.Fatalf("NewTrainingScores() = %v", err)
	}
	fromScores, err := scores.TrainingStatusData(t.Context(), h.session,
		mustDate(t, trendEndDate))
	if err != nil {
		t.Fatalf("TrainingStatusData() = %v", err)
	}
	fromTrends, err := newTrainingTrends(t, h).TrainingLoadDay(t.Context(), h.session,
		mustDate(t, trendEndDate))
	if err != nil {
		t.Fatalf("TrainingLoadDay() = %v", err)
	}

	scoresDevice, ok := fromScores.PrimaryStatus()
	if !ok {
		t.Fatal("the scores read reported no device")
	}
	trendsDevice, ok := fromTrends.PrimaryStatus()
	if !ok {
		t.Fatal("the trend read reported no device")
	}

	scoresPhrase, _ := scoresDevice.FeedbackPhrase.Value()
	trendsPhrase, _ := trendsDevice.FeedbackPhrase.Value()
	if scoresPhrase != trendsPhrase {
		t.Errorf("feedback phrase = %q from the scores read and %q from the trend read",
			scoresPhrase, trendsPhrase)
	}
	scoresLoad, _ := scoresDevice.AcuteTrainingLoad.DailyTrainingLoadChronic.Float64()
	trendsLoad, _ := trendsDevice.AcuteTrainingLoad.DailyTrainingLoadChronic.Float64()
	if scoresLoad != trendsLoad {
		t.Errorf("chronic load = %v from the scores read and %v from the trend read",
			scoresLoad, trendsLoad)
	}

	// The two reads differ in exactly one thing: the sanitized operation label.
	if got := fromScores.Payload().Op(); got != client.OpGetTrainingStatus {
		t.Errorf("scores read op = %q, want %q", got, client.OpGetTrainingStatus)
	}
	if got := fromTrends.Payload().Op(); got != client.OpGetTrainingLoadTrend {
		t.Errorf("trend read op = %q, want %q", got, client.OpGetTrainingLoadTrend)
	}
}

func TestTrainingStatusPrefersThePrimaryDevice(t *testing.T) {
	t.Parallel()

	path := client.PathTrainingStatusPrefix + "/" + trendEndDate
	h := newHarness(t, testkit.NewScript().With(path,
		testkit.JSON(http.StatusOK, trendStatusBody)), client.Limits{})

	day, err := newTrainingTrends(t, h).TrainingLoadDay(t.Context(), h.session,
		mustDate(t, trendEndDate))
	if err != nil {
		t.Fatalf("TrainingLoadDay() = %v", err)
	}

	status, ok := day.PrimaryStatus()
	if !ok {
		t.Fatal("PrimaryStatus() reported no device")
	}
	if got, _ := status.FeedbackPhrase.Value(); got != "PRODUCTIVE_1" {
		t.Errorf("feedback phrase = %q, want the primary device's", got)
	}
	if status.AcuteTrainingLoad == nil {
		t.Fatal("the acute training load did not decode")
	}
	if got, ok := status.AcuteTrainingLoad.DailyTrainingLoadChronic.Float64(); !ok || got != 300.5 {
		t.Errorf("chronic load = %v (set %v), want 300.5", got, ok)
	}
	vo2 := day.MostRecentVO2Max
	if vo2 == nil || vo2.Generic == nil {
		t.Fatal("the VO2 max section did not decode")
	}
	// Upstream's candidate paths reach vo2MaxValue before vo2MaxPreciseValue and the
	// first match wins, so the rounded figure is the compatible answer here.
	if got, _ := vo2.Generic.Value().Float64(); got != 52 {
		t.Errorf("vo2 max = %v, want the rounded 52 upstream reports first", got)
	}
}

func TestTrainingStatusFallsBackToTheLowestDeviceKey(t *testing.T) {
	t.Parallel()

	body := `{"mostRecentTrainingStatus":{"latestTrainingStatusData":{` +
		`"9001":{"trainingStatusFeedbackPhrase":"HIGH_KEY"},` +
		`"1001":{"trainingStatusFeedbackPhrase":"LOW_KEY"}}},` +
		`"mostRecentTrainingLoadBalance":{"metricsTrainingLoadBalanceDTOMap":{` +
		`"9001":{"trainingBalanceFeedbackPhrase":"HIGH_KEY"},` +
		`"1001":{"trainingBalanceFeedbackPhrase":"LOW_KEY"}}}}`
	path := client.PathTrainingStatusPrefix + "/" + trendEndDate
	h := newHarness(t, testkit.NewScript().With(path,
		testkit.JSON(http.StatusOK, body)), client.Limits{})

	day, err := newTrainingTrends(t, h).TrainingLoadBalance(t.Context(), h.session,
		mustDate(t, trendEndDate))
	if err != nil {
		t.Fatalf("TrainingLoadBalance() = %v", err)
	}

	// Deterministic on purpose: upstream takes an arbitrary dict entry, and a Go map
	// would answer differently between two identical calls.
	for range 8 {
		status, _ := day.PrimaryStatus()
		if got, _ := status.FeedbackPhrase.Value(); got != "LOW_KEY" {
			t.Fatalf("PrimaryStatus() = %q, want the lowest key deterministically", got)
		}
		balance, _ := day.PrimaryLoadBalance()
		if got, _ := balance.FeedbackPhrase.Value(); got != "LOW_KEY" {
			t.Fatalf("PrimaryLoadBalance() = %q, want the lowest key deterministically", got)
		}
	}
}

func TestTrainingStatusTolerantlyReportsMissingSections(t *testing.T) {
	t.Parallel()

	path := client.PathTrainingStatusPrefix + "/" + trendEndDate
	h := newHarness(t, testkit.NewScript().With(path, testkit.JSON(http.StatusOK,
		`{"mostRecentTrainingStatus":null,"mostRecentTrainingLoadBalance":null}`)),
		client.Limits{})

	day, err := newTrainingTrends(t, h).VO2MaxFromTrainingStatus(t.Context(), h.session,
		mustDate(t, trendEndDate))
	if err != nil {
		t.Fatalf("VO2MaxFromTrainingStatus() = %v", err)
	}
	if _, ok := day.PrimaryStatus(); ok {
		t.Error("PrimaryStatus() reported a device for a null section")
	}
	if _, ok := day.PrimaryLoadBalance(); ok {
		t.Error("PrimaryLoadBalance() reported a device for a null section")
	}
}

// TestTrainingStatusReportsAnEmptyDeviceMap covers the section that arrived with no
// device in it, which is not the same as no section at all.
func TestTrainingStatusReportsAnEmptyDeviceMap(t *testing.T) {
	t.Parallel()

	body := `{"mostRecentTrainingStatus":{"latestTrainingStatusData":{}},` +
		`"mostRecentTrainingLoadBalance":{"metricsTrainingLoadBalanceDTOMap":{}}}`
	path := client.PathTrainingStatusPrefix + "/" + trendEndDate
	h := newHarness(t, testkit.NewScript().With(path,
		testkit.JSON(http.StatusOK, body)), client.Limits{})

	day, err := newTrainingTrends(t, h).TrainingLoadDay(t.Context(), h.session,
		mustDate(t, trendEndDate))
	if err != nil {
		t.Fatalf("TrainingLoadDay() = %v", err)
	}
	if _, ok := day.PrimaryStatus(); ok {
		t.Error("PrimaryStatus() reported a device for an empty map")
	}
	if _, ok := day.PrimaryLoadBalance(); ok {
		t.Error("PrimaryLoadBalance() reported a device for an empty map")
	}
	if day.Payload().Status() != http.StatusOK {
		t.Errorf("Payload() status = %d, want 200", day.Payload().Status())
	}
}

// TestSelectStatusDeviceIsTheOneSelectorBothReadersUse pins the rule that stopped a
// result being spliced across two devices.
//
// The status read and the trend reads each used to choose a device for themselves,
// out of the same document, by different rules. One selector is what makes the
// document's own PrimaryStatus and any caller's choice the same device; split it
// again and this fails.
func TestSelectStatusDeviceIsTheOneSelectorBothReadersUse(t *testing.T) {
	t.Parallel()

	primary, older, newer := true, "2026-01-29", "2026-02-03"
	devices := map[string]api.TrainingStatusDevice{
		"1001": {CalendarDate: &older},
		"3002": {CalendarDate: &newer},
	}

	// With no primary flag the most recently dated device wins, not the lowest key.
	key, device, ok := api.SelectStatusDevice(devices)
	if !ok || key != "3002" || device.CalendarDate == nil || *device.CalendarDate != newer {
		t.Fatalf("SelectStatusDevice() = %q/%+v, want the most recently dated 3002", key, device)
	}

	// A device Garmin marks primary outranks a later date.
	devices["1001"] = api.TrainingStatusDevice{CalendarDate: &older, PrimaryTrainingDevice: &primary}
	key, _, ok = api.SelectStatusDevice(devices)
	if !ok || key != "1001" {
		t.Errorf("SelectStatusDevice() chose %q, want the primary device 1001", key)
	}

	// The document's own accessor must agree with the shared selector, because a
	// second rule is exactly what produced the spliced result.
	document := api.TrainingStatus{
		MostRecentTrainingStatus: &api.TrainingStatusLatest{LatestData: devices},
	}
	fromDocument, ok := document.PrimaryStatus()
	if !ok {
		t.Fatal("PrimaryStatus() reported no device for a document carrying two")
	}
	_, fromSelector, _ := api.SelectStatusDevice(devices)
	if fromDocument.CalendarDate == nil || fromSelector.CalendarDate == nil ||
		*fromDocument.CalendarDate != *fromSelector.CalendarDate {
		t.Error("PrimaryStatus() and SelectStatusDevice() chose different devices")
	}
	if _, _, ok := api.SelectStatusDevice(nil); ok {
		t.Error("an empty device map reported a device")
	}
}

// TestVO2MaxEntryPrefersTheRoundedValueLikeUpstream pins the precedence to the pinned
// upstream's, where the candidate paths list vo2MaxValue before vo2MaxPreciseValue
// and the first match wins. Reversing it is a silent one-tenth parity break.
func TestVO2MaxEntryPrefersTheRoundedValueLikeUpstream(t *testing.T) {
	t.Parallel()

	var both api.VO2MaxEntry
	if err := json.Unmarshal(
		[]byte(`{"vo2MaxValue":52.0,"vo2MaxPreciseValue":52.3}`), &both,
	); err != nil {
		t.Fatalf("Unmarshal() = %v", err)
	}
	if got, ok := both.Value().Float64(); !ok || got != 52.0 {
		t.Errorf("Value() = %v, want the rounded 52 upstream reports first", got)
	}

	var preciseOnly api.VO2MaxEntry
	if err := json.Unmarshal([]byte(`{"vo2MaxPreciseValue":52.3}`), &preciseOnly); err != nil {
		t.Fatalf("Unmarshal() = %v", err)
	}
	if got, ok := preciseOnly.Value().Float64(); !ok || got != 52.3 {
		t.Errorf("Value() = %v, want the precise 52.3 when it is the only one sent", got)
	}
}
