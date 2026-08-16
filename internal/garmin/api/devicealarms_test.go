package api_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

const twoDeviceListBody = `[{"deviceId":4242,"productDisplayName":"Fake Watch 9"},` +
	`{"deviceId":4243,"productDisplayName":"Fake Strap"}]`

func alarmSettingsBody(alarmID int) string {
	return `{"deviceId":4242,"alarms":[{"alarmId":` + strconv.Itoa(alarmID) +
		`,"alarmTime":390,"alarmMode":"ON","alarmSound":"BEEP"}]}`
}

func TestDevicesAlarmsWalksEveryDevice(t *testing.T) {
	t.Parallel()

	id1 := mustDeviceID(t, 4242)
	id2 := mustDeviceID(t, 4243)
	script := testkit.NewScript().
		With(client.PathDevices, testkit.JSON(http.StatusOK, twoDeviceListBody)).
		With(client.PathDeviceSettingsPrefix+"/"+id1.String(), testkit.JSON(http.StatusOK, alarmSettingsBody(1))).
		With(client.PathDeviceSettingsPrefix+"/"+id2.String(), testkit.JSON(http.StatusOK, alarmSettingsBody(2)))
	h := newHarness(t, script, client.Limits{})

	got, err := newDevices(t, h).Alarms(t.Context(), h.session)
	if err != nil {
		t.Fatalf("Alarms() = %v", err)
	}
	if got.Truncated {
		t.Error("Truncated = true, want false")
	}
	if len(got.Alarms) != 2 {
		t.Fatalf("%d alarms decoded, want 2", len(got.Alarms))
	}
	seen := map[int64]bool{}
	for _, alarm := range got.Alarms {
		id, ok := alarm.AlarmID.Int64()
		if !ok {
			t.Fatal("AlarmID not set")
		}
		seen[id] = true
		if !alarm.Enabled() {
			t.Errorf("alarm %d Enabled() = false, want true", id)
		}
	}
	if !seen[1] || !seen[2] {
		t.Errorf("alarms = %+v, want ids 1 and 2", got.Alarms)
	}
}

func TestDevicesAlarmsPropagatesADeviceSettingsFailure(t *testing.T) {
	t.Parallel()

	id1 := mustDeviceID(t, 4242)
	script := testkit.NewScript().
		With(client.PathDevices, testkit.JSON(http.StatusOK, twoDeviceListBody)).
		With(client.PathDeviceSettingsPrefix+"/"+id1.String(),
			testkit.JSON(http.StatusInternalServerError, `{"message":"synthetic"}`))
	h := newHarness(t, script, client.Limits{MaxAttempts: 1})

	if _, err := newDevices(t, h).Alarms(t.Context(), h.session); err == nil {
		t.Error("Alarms() = nil, want an error when a device's settings read fails")
	}
}

func TestDevicesAlarmsReportsNoTruncationForANormalAccount(t *testing.T) {
	t.Parallel()

	id1 := mustDeviceID(t, 4242)
	id2 := mustDeviceID(t, 4243)
	script := testkit.NewScript().
		With(client.PathDevices, testkit.JSON(http.StatusOK, twoDeviceListBody)).
		With(client.PathDeviceSettingsPrefix+"/"+id1.String(), testkit.JSON(http.StatusOK, alarmSettingsBody(1))).
		With(client.PathDeviceSettingsPrefix+"/"+id2.String(), testkit.JSON(http.StatusOK, alarmSettingsBody(2)))
	h := newHarness(t, script, client.Limits{})

	got, err := newDevices(t, h).Alarms(t.Context(), h.session)
	if err != nil {
		t.Fatalf("Alarms() = %v", err)
	}
	// Sanity check that the bound constant itself is generous, not that this run
	// hits it: two alarms is far under maxDeviceAlarmItems, so Truncated must stay
	// false for a normal account.
	if got.Truncated {
		t.Error("Truncated = true for two alarms, want false")
	}
}

// manyAlarmsSettingsBody builds a device settings document carrying count
// alarms, each a minimal object. Used to exercise maxDeviceAlarmItems, which
// this project's rule requires reporting through Truncated rather than an
// error (see api.GoalResult.Truncated for the same discipline elsewhere).
func manyAlarmsSettingsBody(count int) string {
	var b strings.Builder
	b.WriteString(`{"deviceId":4242,"alarms":[`)
	for i := range count {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"alarmId":`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`}`)
	}
	b.WriteString(`]}`)
	return b.String()
}

// TestDevicesAlarmsTruncatesTheItemTotal exercises the item-accumulation bound:
// a single device answering with more than maxDeviceAlarmItems alarms is
// truncated rather than retained in full, and Truncated reports it.
func TestDevicesAlarmsTruncatesTheItemTotal(t *testing.T) {
	t.Parallel()

	id1 := mustDeviceID(t, 4242)
	const overLimit = 2001 // one past this package's unexported maxDeviceAlarmItems.
	script := testkit.NewScript().
		With(client.PathDevices, testkit.JSON(http.StatusOK,
			`[{"deviceId":4242,"productDisplayName":"Fake Watch 9"}]`)).
		With(client.PathDeviceSettingsPrefix+"/"+id1.String(),
			testkit.JSON(http.StatusOK, manyAlarmsSettingsBody(overLimit)))
	h := newHarness(t, script, client.Limits{})

	got, err := newDevices(t, h).Alarms(t.Context(), h.session)
	if err != nil {
		t.Fatalf("Alarms() = %v", err)
	}
	if !got.Truncated {
		t.Error("Truncated = false, want true past the item bound")
	}
	if len(got.Alarms) >= overLimit {
		t.Errorf("%d alarms retained, want fewer than %d", len(got.Alarms), overLimit)
	}
}

// TestDevicesAlarmsSkipsDevicesWithoutAUsableID covers the fan-out's tolerant
// skip: a device with no deviceId, and one with a non-positive deviceId, both
// contribute no settings request and no error, while a valid sibling device
// still contributes its alarms.
func TestDevicesAlarmsSkipsDevicesWithoutAUsableID(t *testing.T) {
	t.Parallel()

	id1 := mustDeviceID(t, 4242)
	script := testkit.NewScript().
		With(client.PathDevices, testkit.JSON(http.StatusOK,
			`[{"productDisplayName":"No id"},{"deviceId":0,"productDisplayName":"Zero id"},`+
				`{"deviceId":4242,"productDisplayName":"Fake Watch 9"}]`)).
		With(client.PathDeviceSettingsPrefix+"/"+id1.String(), testkit.JSON(http.StatusOK, alarmSettingsBody(1)))
	h := newHarness(t, script, client.Limits{})

	got, err := newDevices(t, h).Alarms(t.Context(), h.session)
	if err != nil {
		t.Fatalf("Alarms() = %v", err)
	}
	if len(got.Alarms) != 1 {
		t.Fatalf("%d alarms decoded, want 1 from the one device with a usable id", len(got.Alarms))
	}
	if got := len(h.server.Requests()); got != 2 { // the device list plus one settings read.
		t.Errorf("the fake received %d requests, want 2", got)
	}
}
