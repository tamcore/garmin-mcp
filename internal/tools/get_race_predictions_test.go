package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// raceDisplayName is the synthetic display name the race-prediction path needs, since
// RacePredictions reads the account's own profile first. Every value here is invented.
const raceDisplayName = "fake-tester-race"

const raceSocialProfileBody = `{"profileId":900001,"displayName":"` + raceDisplayName + `",` +
	`"fullName":"Fake Tester"}`

// racePredictionsDocument carries all four predicted distances. Source field names:
// challenges.py:525-544 (calendarDate, time5K, time10K, timeHalfMarathon,
// timeMarathon).
const racePredictionsDocument = `{"calendarDate":"2026-03-01",` +
	`"time5K":1230.5,"time10K":2565,"timeHalfMarathon":5760,"timeMarathon":12600}`

func racePredictionsScript(body string) testkit.Script {
	return testkit.NewScript().
		With(client.PathSocialProfile, testkit.JSON(http.StatusOK, raceSocialProfileBody)).
		With(client.PathRacePredictionsPrefix+"/latest/"+raceDisplayName,
			testkit.JSON(http.StatusOK, body))
}

func TestGetRacePredictionsFormatsEveryDistance(t *testing.T) {
	t.Parallel()

	h := newChallengesHarness(t, racePredictionsScript(racePredictionsDocument),
		[]string{ToolGetRacePredictions}, registerGetRacePredictions)

	result := h.call(t, ToolGetRacePredictions, nil)
	if got, _ := result["prediction_date"].(string); got != "2026-03-01" {
		t.Errorf("prediction_date = %q, want 2026-03-01", got)
	}

	predictions := object(t, result, "predictions")
	fiveK := object(t, predictions, "5K")
	if got, _ := fiveK["time"].(string); got != "20:30" {
		t.Errorf("5K time = %q, want 20:30 for 1230.5 seconds", got)
	}
	if got := number(t, fiveK, "time_seconds"); got != 1230.5 {
		t.Errorf("5K time_seconds = %v, want 1230.5", got)
	}

	marathon := object(t, predictions, "marathon")
	if got, _ := marathon["time"].(string); got != "3:30:00" {
		t.Errorf("marathon time = %q, want 3:30:00 for 12600 seconds", got)
	}
}

// TestGetRacePredictionsReportsAnUnmodeledDistanceAsAbsent proves a distance Garmin
// omits stays absent rather than becoming a zero.
func TestGetRacePredictionsReportsAnUnmodeledDistanceAsAbsent(t *testing.T) {
	t.Parallel()

	body := `{"calendarDate":"2026-03-01","time5K":1230.5}`
	h := newChallengesHarness(t, racePredictionsScript(body),
		[]string{ToolGetRacePredictions}, registerGetRacePredictions)

	predictions := object(t, h.call(t, ToolGetRacePredictions, nil), "predictions")
	tenK := object(t, predictions, "10K")
	if _, present := tenK["time"]; present {
		t.Errorf("10K time = %v for an unmodeled distance, want the key absent", tenK["time"])
	}
	if _, present := tenK["time_seconds"]; present {
		t.Errorf("10K time_seconds = %v for an unmodeled distance, want the key absent",
			tenK["time_seconds"])
	}
}

func TestRacePredictionsLogValueReportsShapeOnly(t *testing.T) {
	t.Parallel()

	seconds := 1230.5
	value := RacePredictions{
		Predictions: RacePredictionsByDistance{FiveK: RacePrediction{TimeSeconds: &seconds}},
	}.LogValue().String()

	if strings.Contains(value, "1230.5") {
		t.Errorf("the log value %q carries a predicted time", value)
	}
	if !strings.Contains(value, "racePredictions") {
		t.Errorf("the log value %q does not name the model", value)
	}
}
