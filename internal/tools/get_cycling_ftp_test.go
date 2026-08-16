package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// cyclingFTPPath is the sport-keyed path of the latest cycling threshold power.
const cyclingFTPPath = client.PathLatestFunctionalThresholdPowerPrefix + "/" +
	client.SportCycling

// cyclingFTPDocument is the single-object shape Garmin answers with. Every value is
// invented.
const cyclingFTPDocument = `{"sport":"CYCLING","functionalThresholdPower":244,` +
	`"calendarDate":"` + scoresEndDate + `","isStale":false,` +
	`"biometricSourceType":"DEVICE_MEASURED"}`

func TestGetCyclingFTPReturnsTheLatestRecord(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(cyclingFTPPath,
		testkit.JSON(http.StatusOK, cyclingFTPDocument))
	h := newScoresHarness(t, script)

	result := h.call(t, ToolGetCyclingFTP, nil)

	if reported, _ := result["reported"].(bool); !reported {
		t.Error("reported = false, want true for an account with a threshold power")
	}
	if got := number(t, result, "functional_threshold_power_watts"); got != 244 {
		t.Errorf("functional_threshold_power_watts = %v, want 244", got)
	}
	if got, _ := result["sport"].(string); got != client.SportCycling {
		t.Errorf("sport = %q, want %q", got, client.SportCycling)
	}
	if got, _ := result["calendar_date"].(string); got != scoresEndDate {
		t.Errorf("calendar_date = %q, want %q", got, scoresEndDate)
	}
	if stale, _ := result["is_stale"].(bool); stale {
		t.Error("is_stale = true, want false")
	}
	if got := number(t, result, "records_found"); got != 1 {
		t.Errorf("records_found = %v, want 1", got)
	}
}

// TestGetCyclingFTPPicksTheMostRecentOfSeveralRecords proves the answer does not depend
// on the order Garmin listed the records in.
func TestGetCyclingFTPPicksTheMostRecentOfSeveralRecords(t *testing.T) {
	t.Parallel()

	body := `[{"sport":"CYCLING","functionalThresholdPower":230,"calendarDate":"2025-11-02"},` +
		`{"sport":"CYCLING","functionalThresholdPower":244,"calendarDate":"` + scoresEndDate + `"}]`
	script := testkit.NewScript().With(cyclingFTPPath, testkit.JSON(http.StatusOK, body))
	h := newScoresHarness(t, script)

	result := h.call(t, ToolGetCyclingFTP, nil)
	if got := number(t, result, "functional_threshold_power_watts"); got != 244 {
		t.Errorf("functional_threshold_power_watts = %v, want the most recent 244", got)
	}
	if got := number(t, result, "records_found"); got != 2 {
		t.Errorf("records_found = %v, want 2", got)
	}
}

// TestGetCyclingFTPReportsAnAccountWithoutARecord proves an empty answer is normal.
func TestGetCyclingFTPReportsAnAccountWithoutARecord(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{caseEmptyArray: `[]`, jsonNull: jsonNull} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := testkit.NewScript().With(cyclingFTPPath, testkit.JSON(http.StatusOK, body))
			h := newScoresHarness(t, script)

			result := h.call(t, ToolGetCyclingFTP, nil)
			if reported, _ := result["reported"].(bool); reported {
				t.Error("reported = true, want false for an account with no record")
			}
			if got := number(t, result, "records_found"); got != 0 {
				t.Errorf("records_found = %v, want 0", got)
			}
		})
	}
}

// TestGetCyclingFTPTakesNoAccountSelector proves an argument naming an account is
// refused by the published schema rather than acted on.
func TestGetCyclingFTPTakesNoAccountSelector(t *testing.T) {
	t.Parallel()

	h := newScoresHarness(t, testkit.NewScript())

	advice := h.callError(t, ToolGetCyclingFTP, map[string]any{keyUserID: "900001"})
	assertNoRawPayload(t, advice)
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want none", got)
	}
}

// TestCyclingFTPLogValueReportsShapeOnly proves the log record carries no reading.
func TestCyclingFTPLogValueReportsShapeOnly(t *testing.T) {
	t.Parallel()

	watts := 244.0
	value := CyclingFTP{Reported: true, FTPWatts: &watts, RecordsFound: 1}.LogValue().String()

	if strings.Contains(value, "244") {
		t.Errorf("the log value %q carries a reading", value)
	}
	if !strings.Contains(value, "cyclingFTP") {
		t.Errorf("the log value %q does not name the model", value)
	}
}
