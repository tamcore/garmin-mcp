package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// trainingStatusPath is the dated path of the aggregated training-status read.
const trainingStatusPath = client.PathTrainingStatusPrefix + "/" + scoresEndDate

// statusProductive is the status the most recently dated device reports in the
// fixtures below, named once so the device-choice assertions read the same value.
const statusProductive = "PRODUCTIVE"

// trainingStatusDocument carries two reporting devices, so the choice of device is
// provably the most recently dated one and not the map order.
const trainingStatusDocument = `{"mostRecentTrainingStatus":{"latestTrainingStatusData":{` +
	`"3001":{"calendarDate":"2026-01-29","trainingStatus":"MAINTAINING","sport":"RUNNING"},` +
	`"3002":{"calendarDate":"` + scoresEndDate + `","trainingStatus":"` + statusProductive + `",` +
	`"trainingStatusFeedbackPhrase":"PRODUCTIVE_1","sport":"RUNNING","fitnessTrend":1,` +
	`"acuteTrainingLoadDTO":{"dailyTrainingLoadAcute":412,"dailyTrainingLoadChronic":388,` +
	`"dailyAcuteChronicWorkloadRatio":1.06,"acwrStatus":"OPTIMAL","acwrPercent":106,` +
	`"minTrainingLoadChronic":300,"maxTrainingLoadChronic":460}}}},` +
	`"mostRecentVO2Max":{"generic":{"vo2MaxValue":52,"vo2MaxPreciseValue":52.4},` +
	`"cycling":null},` +
	`"mostRecentTrainingLoadBalance":{"metricsTrainingLoadBalanceDTOMap":{` +
	`"3002":{"monthlyLoadAerobicLow":1200,"monthlyLoadAerobicHigh":540,` +
	`"monthlyLoadAnaerobic":210,"trainingBalanceFeedbackPhrase":"BALANCED"}}}}`

func trainingStatusArgs() map[string]any {
	return map[string]any{argDate: scoresEndDate}
}

func TestGetTrainingStatusReturnsTheMostRecentDevicesView(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(trainingStatusPath,
		testkit.JSON(http.StatusOK, trainingStatusDocument))
	h := newScoresHarness(t, script)

	result := h.call(t, ToolGetTrainingStatus, trainingStatusArgs())

	if reported, _ := result["reported"].(bool); !reported {
		t.Error("reported = false, want true for a day a device reported")
	}
	if got, _ := result["training_status"].(string); got != statusProductive {
		t.Errorf("training_status = %q, want the most recently dated PRODUCTIVE", got)
	}
	if got, _ := result["reported_date"].(string); got != scoresEndDate {
		t.Errorf("reported_date = %q, want %q", got, scoresEndDate)
	}
	if got := number(t, result, "status_devices_reported"); got != 2 {
		t.Errorf("status_devices_reported = %v, want 2", got)
	}
	if got := number(t, result, "acute_load"); got != 412 {
		t.Errorf("acute_load = %v, want 412", got)
	}
	if got := number(t, result, "load_ratio"); got != 1.06 {
		t.Errorf("load_ratio = %v, want 1.06", got)
	}
	if got, _ := result["acwr_status"].(string); got != "OPTIMAL" {
		t.Errorf("acwr_status = %q, want OPTIMAL", got)
	}
	if got := number(t, result, "vo2_max_precise"); got != 52.4 {
		t.Errorf("vo2_max_precise = %v, want 52.4", got)
	}
	if _, present := result["cycling_vo2_max"]; present {
		t.Error("a null cycling block produced a cycling VO2 max")
	}
	if got := number(t, result, "monthly_load_anaerobic"); got != 210 {
		t.Errorf("monthly_load_anaerobic = %v, want 210", got)
	}
	if got, _ := result["training_balance_feedback"].(string); got != "BALANCED" {
		t.Errorf("training_balance_feedback = %q, want BALANCED", got)
	}
}

// TestGetTrainingStatusReturnsNoDeviceIdentifier proves the device keys stay behind.
func TestGetTrainingStatusReturnsNoDeviceIdentifier(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(trainingStatusPath,
		testkit.JSON(http.StatusOK, trainingStatusDocument))
	h := newScoresHarness(t, script)

	rendered := h.text(t, ToolGetTrainingStatus, trainingStatusArgs())
	for _, forbidden := range []string{"3001", "3002"} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("the result carries the device identifier %q", forbidden)
		}
	}
}

// TestGetTrainingStatusAcceptsANumericStatusCode proves the status field survives
// arriving as a number rather than a phrase.
func TestGetTrainingStatusAcceptsANumericStatusCode(t *testing.T) {
	t.Parallel()

	body := `{"mostRecentTrainingStatus":{"latestTrainingStatusData":{` +
		`"3001":{"calendarDate":"` + scoresEndDate + `","trainingStatus":3}}}}`
	script := testkit.NewScript().With(trainingStatusPath, testkit.JSON(http.StatusOK, body))
	h := newScoresHarness(t, script)

	result := h.call(t, ToolGetTrainingStatus, trainingStatusArgs())
	if got, _ := result["training_status"].(string); got != "3" {
		t.Errorf("training_status = %q, want the numeric code rendered as 3", got)
	}
}

// TestGetTrainingStatusReportsADayNoDeviceReported proves an empty document is a normal
// answer.
func TestGetTrainingStatusReportsADayNoDeviceReported(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{
		caseEmptyObject: `{}`,
		"null blocks":   `{"mostRecentTrainingStatus":null,"mostRecentVO2Max":null}`,
		"no devices":    `{"mostRecentTrainingStatus":{"latestTrainingStatusData":{}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := testkit.NewScript().With(trainingStatusPath,
				testkit.JSON(http.StatusOK, body))
			h := newScoresHarness(t, script)

			result := h.call(t, ToolGetTrainingStatus, trainingStatusArgs())
			if reported, _ := result["reported"].(bool); reported {
				t.Error("reported = true, want false when no device reported")
			}
			if _, present := result["training_status"]; present {
				t.Error("a status was reported for a day no device covered")
			}
		})
	}
}

func TestGetTrainingStatusRefusesABadDateBeforeDispatch(t *testing.T) {
	t.Parallel()

	h := newScoresHarness(t, testkit.NewScript())

	advice := h.callError(t, ToolGetTrainingStatus, map[string]any{argDate: "31-01-2026"})
	assertNoRawPayload(t, advice)
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want none", got)
	}
}

// TestTrainingStatusLogValueReportsShapeOnly proves the log record carries no reading.
func TestTrainingStatusLogValueReportsShapeOnly(t *testing.T) {
	t.Parallel()

	vo2 := 52.4
	status := "PRODUCTIVE"
	value := TrainingStatus{
		Date:                  scoresEndDate,
		Reported:              true,
		TrainingStatus:        &status,
		VO2Max:                &vo2,
		StatusDevicesReported: 2,
	}.LogValue().String()

	if strings.Contains(value, "52.4") || strings.Contains(value, "PRODUCTIVE") {
		t.Errorf("the log value %q carries a reading", value)
	}
	if !strings.Contains(value, "trainingStatus") {
		t.Errorf("the log value %q does not name the model", value)
	}
}

// splicedDeviceDocument reports two devices in both blocks, and the device whose
// status wins is not the one whose key sorts first. The lower-sorting device carries
// values that are obviously its own, so a spliced answer is visible.
const splicedDeviceDocument = `{"mostRecentTrainingStatus":{"latestTrainingStatusData":{` +
	`"1001":{"calendarDate":"2026-01-29","trainingStatus":"MAINTAINING","sport":"RUNNING"},` +
	`"3002":{"calendarDate":"` + scoresEndDate + `","trainingStatus":"` + statusProductive + `",` +
	`"sport":"RUNNING"}}},` +
	`"mostRecentTrainingLoadBalance":{"metricsTrainingLoadBalanceDTOMap":{` +
	`"1001":{"monthlyLoadAerobicLow":11,"monthlyLoadAerobicHigh":12,` +
	`"monthlyLoadAnaerobic":13},` +
	`"3002":{"monthlyLoadAerobicLow":1200,"monthlyLoadAerobicHigh":540,` +
	`"monthlyLoadAnaerobic":210}}}}`

// TestGetTrainingStatusReadsOneDeviceInBothBlocks is the regression for a result
// spliced across two devices.
//
// The status block chose the most recently dated device while the load balance block
// chose whichever key sorted first. On a two-device account those are different
// devices, so one result described a training status from one watch beside a monthly
// load from another, with nothing in the answer saying so.
func TestGetTrainingStatusReadsOneDeviceInBothBlocks(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(trainingStatusPath,
		testkit.JSON(http.StatusOK, splicedDeviceDocument))
	h := newScoresHarness(t, script)

	result := h.call(t, ToolGetTrainingStatus, trainingStatusArgs())

	if got, _ := result["training_status"].(string); got != statusProductive {
		t.Fatalf("training_status = %q, want the most recently dated device's PRODUCTIVE", got)
	}
	// 1200 belongs to the device the status came from; 11 belongs to the other one.
	if got := number(t, result, "monthly_load_aerobic_low"); got != 1200 {
		t.Errorf("monthly_load_aerobic_low = %v, want 1200 from the same device as the "+
			"status, not the other device's value", got)
	}
	if got := number(t, result, "monthly_load_anaerobic"); got != 210 {
		t.Errorf("monthly_load_anaerobic = %v, want 210 from the same device", got)
	}
}
