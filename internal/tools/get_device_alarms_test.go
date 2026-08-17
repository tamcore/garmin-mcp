package tools

import (
	"errors"
	"net/http"
	"strconv"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// twoDeviceListBody registers two synthetic devices, so Alarms fans its per-device
// settings read out over both.
const twoDeviceListBody = `[{"deviceId":4242},{"deviceId":4243}]`

// alarmSettingsBodyFor is one device's settings document, carrying one alarm whose
// alarmTime (minutes-from-midnight) and alarmMode place it in the sort and the
// enabled count get_device_alarms reports. Neither pinned source documents the wire
// shape of backlight (api.DeviceAlarm keeps it as json.RawMessage for exactly that
// reason), so the object shape here is a synthetic stand-in carrying one identifying
// key ("lat"), which is what the sanitizing-drop test exercises.
func alarmSettingsBodyFor(alarmID, minutes int, mode string) string {
	return `{"deviceId":4242,"alarms":[{"alarmId":` + strconv.Itoa(alarmID) +
		`,"alarmTime":` + strconv.Itoa(minutes) + `,"alarmMode":"` + mode + `",` +
		`"alarmDays":["MONDAY"],"alarmSound":"BEEP","backlight":{"lat":52.1,"mode":"ON"},` +
		`"alarmMessage":"Wake up"}]}`
}

func TestGetDeviceAlarmsDeclaresTheUpstreamContract(t *testing.T) {
	t.Parallel()

	contract := getDeviceAlarmsContract()
	if got := contract.Spec.Name; got != ToolGetDeviceAlarms {
		t.Errorf("wire name = %q, want %q", got, ToolGetDeviceAlarms)
	}
	if got := contract.Spec.Category; got != categoryDevice {
		t.Errorf("log category = %q, want %q", got, categoryDevice)
	}
	if len(contract.Schema.Properties()) != 0 {
		t.Errorf("declared %d properties, want none", len(contract.Schema.Properties()))
	}
}

func TestGetDeviceAlarmsReturnsEveryAlarmSortedByTime(t *testing.T) {
	t.Parallel()

	id1, err := client.NewID(4242)
	if err != nil {
		t.Fatalf("client.NewID() = %v", err)
	}
	id2, err := client.NewID(4243)
	if err != nil {
		t.Fatalf("client.NewID() = %v", err)
	}
	script := testkit.NewScript().
		With(client.PathDevices, testkit.JSON(http.StatusOK, twoDeviceListBody)).
		With(client.PathDeviceSettingsPrefix+"/"+id1.String(),
			testkit.JSON(http.StatusOK, alarmSettingsBodyFor(1, 390, "ON"))).
		With(client.PathDeviceSettingsPrefix+"/"+id2.String(),
			testkit.JSON(http.StatusOK, alarmSettingsBodyFor(2, 60, "OFF")))
	svc, _ := deviceToolsService(t, script)
	session := deviceToolsSession(t, deviceToolsServer(t, svc))

	result := callDeviceTool(t, session, ToolGetDeviceAlarms, nil)
	out := deviceToolStructured(t, result)

	if got, _ := out["total_alarms"].(float64); got != 2 {
		t.Fatalf("total_alarms = %v, want 2", got)
	}
	if got, _ := out["enabled_alarms"].(float64); got != 1 {
		t.Errorf("enabled_alarms = %v, want 1", got)
	}

	alarms := list(t, out, "alarms")
	first := entry(t, alarms, 0)
	if got, _ := first["time"].(string); got != "01:00" {
		t.Errorf("alarms[0].time = %q, want %q (the earlier, disabled alarm sorts first)", got, "01:00")
	}
	if got, _ := first["enabled"].(bool); got {
		t.Error("alarms[0].enabled = true, want false")
	}
	second := entry(t, alarms, 1)
	if got, _ := second["time"].(string); got != "06:30" {
		t.Errorf("alarms[1].time = %q, want %q", got, "06:30")
	}
	if got, _ := second["message"].(string); got != "Wake up" {
		t.Errorf("alarms[1].message = %q, want %q", got, "Wake up")
	}
}

func TestGetDeviceAlarmsSanitizesDaysAndBacklight(t *testing.T) {
	t.Parallel()

	id, err := client.NewID(4242)
	if err != nil {
		t.Fatalf("client.NewID() = %v", err)
	}
	script := testkit.NewScript().
		With(client.PathDevices, testkit.JSON(http.StatusOK, `[{"deviceId":4242}]`)).
		With(client.PathDeviceSettingsPrefix+"/"+id.String(),
			testkit.JSON(http.StatusOK, alarmSettingsBodyFor(1, 390, "ON")))
	svc, _ := deviceToolsService(t, script)
	session := deviceToolsSession(t, deviceToolsServer(t, svc))

	result := callDeviceTool(t, session, ToolGetDeviceAlarms, nil)
	out := deviceToolStructured(t, result)

	if got, _ := out["dropped_fields"].(float64); got != 1 {
		t.Errorf("dropped_fields = %v, want 1 for the sanitized backlight \"lat\" key", got)
	}
	alarm := entry(t, list(t, out, "alarms"), 0)
	days := alarm["days"]
	array, ok := days.([]any)
	if !ok || len(array) != 1 {
		t.Fatalf("days = %#v, want a one-element array", days)
	}
	backlight, ok := alarm["backlight"].(map[string]any)
	if !ok {
		t.Fatalf("backlight = %#v, want a sanitized object", alarm["backlight"])
	}
	if _, present := backlight["lat"]; present {
		t.Error("backlight still carries \"lat\", want it dropped by sanitization")
	}
	if got, _ := backlight["mode"].(string); got != "ON" {
		t.Errorf("backlight.mode = %q, want %q to survive sanitization", got, "ON")
	}
}

func TestGetDeviceAlarmsReportsNoAlarmsAsEmpty(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathDevices, testkit.JSON(http.StatusOK, `[]`))
	svc, _ := deviceToolsService(t, script)
	session := deviceToolsSession(t, deviceToolsServer(t, svc))

	result := callDeviceTool(t, session, ToolGetDeviceAlarms, nil)
	out := deviceToolStructured(t, result)
	if got, _ := out["total_alarms"].(float64); got != 0 {
		t.Errorf("total_alarms = %v, want 0", got)
	}
}

func TestGetDeviceAlarmsRefusesARegistryItDoesNotHave(t *testing.T) {
	t.Parallel()

	svc, _ := deviceToolsService(t, testkit.NewScript())
	err := registerGetDeviceAlarms(nil, svc)
	if !errors.Is(err, mcpserver.ErrMissingDependency) {
		t.Errorf("registering on no registry = %v, want ErrMissingDependency", err)
	}
}
