package tools

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// deviceSettingsBody is a synthetic device-settings document. 390 minutes-from-midnight
// is 06:30, and the alarm counts below pin the two counts get_device_settings reports.
const deviceSettingsBody = `{"deviceId":4242,"timeFormat":"time_twenty_four_hr",` +
	`"dateFormat":"month_day_year","measurementUnits":"statute_us",` +
	`"keyTonesEnabled":true,"keyVibrationEnabled":false,"alertTonesEnabled":true,` +
	`"activityTracking":{"moveAlertEnabled":true,"pulseOxSleepTrackingEnabled":false,` +
	`"highHrAlertEnabled":true,"lowHrAlertEnabled":false},` +
	`"alarms":[{"alarmId":1,"alarmTime":390,"alarmMode":"ON","alarmSound":"BEEP"},` +
	`{"alarmId":2,"alarmTime":60,"alarmMode":"OFF"}]}`

func deviceSettingsPath(t *testing.T) string {
	t.Helper()

	id, err := client.NewID(4242)
	if err != nil {
		t.Fatalf("client.NewID() = %v", err)
	}
	return client.PathDeviceSettingsPrefix + "/" + id.String()
}

func TestGetDeviceSettingsDeclaresTheUpstreamContract(t *testing.T) {
	t.Parallel()

	contract := getDeviceSettingsContract()
	if got := contract.Spec.Name; got != ToolGetDeviceSettings {
		t.Errorf("wire name = %q, want %q", got, ToolGetDeviceSettings)
	}
	if got := contract.Spec.Category; got != categoryDevice {
		t.Errorf("log category = %q, want %q", got, categoryDevice)
	}

	schema := contract.Schema
	if got := schema.Required(); len(got) != 0 {
		t.Errorf("required = %v, want none: device_id is optional", got)
	}
	properties := schema.Properties()
	if len(properties) != 1 {
		t.Fatalf("declared %d properties, want exactly one", len(properties))
	}
	property := properties[0]
	if property.Name != argDeviceID {
		t.Errorf("property name = %q, want %q", property.Name, argDeviceID)
	}
	if !property.Nullable {
		t.Error("device_id is not declared nullable, want it nullable per the manifest")
	}
}

func TestGetDeviceSettingsReturnsTheCuratedDocumentForAnExplicitDevice(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(deviceSettingsPath(t), testkit.JSON(http.StatusOK, deviceSettingsBody))
	svc, fake := deviceToolsService(t, script)
	session := deviceToolsSession(t, deviceToolsServer(t, svc))

	result := callDeviceTool(t, session, ToolGetDeviceSettings, map[string]any{argDeviceID: "4242"})
	out := deviceToolStructured(t, result)

	if got := out["time_format"]; got != "time_twenty_four_hr" {
		t.Errorf("time_format = %v, want %q", got, "time_twenty_four_hr")
	}
	if got, _ := out["alarm_count"].(float64); got != 2 {
		t.Errorf("alarm_count = %v, want 2", got)
	}
	if got, _ := out["enabled_alarm_count"].(float64); got != 1 {
		t.Errorf("enabled_alarm_count = %v, want 1", got)
	}
	tracking, ok := out["activity_tracking"].(map[string]any)
	if !ok {
		t.Fatal("activity_tracking is missing")
	}
	if got, _ := tracking["move_alert_enabled"].(bool); !got {
		t.Error("move_alert_enabled = false, want true")
	}
	if got := len(fake.Requests()); got != 1 {
		t.Errorf("dispatched %d requests, want 1", got)
	}
}

func TestGetDeviceSettingsDefaultsToTheLastUsedDeviceWhenOmitted(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(client.PathDeviceLastUsed, testkit.JSON(http.StatusOK, deviceLastUsedBody)).
		With(client.PathDeviceSettingsPrefix+"/998877", testkit.JSON(http.StatusOK, deviceSettingsBody))
	svc, fake := deviceToolsService(t, script)
	session := deviceToolsSession(t, deviceToolsServer(t, svc))

	result := callDeviceTool(t, session, ToolGetDeviceSettings, nil)
	out := deviceToolStructured(t, result)
	if got := out["measurement_units"]; got != "statute_us" {
		t.Errorf("measurement_units = %v, want %q", got, "statute_us")
	}
	if got := len(fake.Requests()); got != 2 {
		t.Errorf("dispatched %d requests, want 2 (last-used lookup, then settings)", got)
	}
}

func TestGetDeviceSettingsRefusesAMalformedDeviceID(t *testing.T) {
	t.Parallel()

	svc, fake := deviceToolsService(t, testkit.NewScript())
	session := deviceToolsSession(t, deviceToolsServer(t, svc))

	result := callDeviceTool(t, session, ToolGetDeviceSettings, map[string]any{argDeviceID: "not-a-device"})
	if !result.IsError {
		t.Fatal("a malformed device_id succeeded, want an error result")
	}
	if got := len(fake.Requests()); got != 0 {
		t.Errorf("dispatched %d requests, want 0", got)
	}
}

func TestGetDeviceSettingsRefusesARegistryItDoesNotHave(t *testing.T) {
	t.Parallel()

	svc, _ := deviceToolsService(t, testkit.NewScript())
	err := registerGetDeviceSettings(nil, svc)
	if !errors.Is(err, mcpserver.ErrMissingDependency) {
		t.Errorf("registering on no registry = %v, want ErrMissingDependency", err)
	}
}

func TestGetDeviceSettingsLogsCountsOnlyNeverAnAlarmSchedule(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(deviceSettingsPath(t), testkit.JSON(http.StatusOK, deviceSettingsBody))
	svc, _ := deviceToolsService(t, script)

	id, err := client.NewID(4242)
	if err != nil {
		t.Fatalf("client.NewID() = %v", err)
	}
	raw, err := svc.devices.Settings(deviceToolsContext(t), mustSession(t, svc, deviceToolsPrincipal), id)
	if err != nil {
		t.Fatalf("Settings() = %v", err)
	}

	settings := newDeviceSettings(raw)
	if got := settings.AlarmCount; got != 2 {
		t.Fatalf("AlarmCount = %d, want 2", got)
	}
	if settings.LogValue().String() == "" {
		t.Fatal("LogValue produced nothing")
	}
}
