package api_test

import (
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// Sentinel values a logged device/gear model must never contain. Each is at
// least 5 characters, so it cannot collide with a log line's own timestamp
// digits (see leakLogOptions' doc comment in sensitive_test.go).
const (
	sentinelDeviceName  = "SENTINEL-DEVICE-SETTINGS-NAME"
	sentinelAlarmSound  = "SENTINEL-ALARM-SOUND"
	sentinelAlarmMsg    = "SENTINEL-ALARM-MESSAGE"
	sentinelSolarDate   = "2031-07-04"
	sentinelTrainingTag = "SENTINEL-TRAINING-DEVICE-NAME"
)

// TestDeviceModelsAreNotLoggable proves that every model this session added to
// devices.go, devicestraining.go, devicesolar.go and devicealarms.go reports
// shape rather than content when handed to slog.
func TestDeviceModelsAreNotLoggable(t *testing.T) {
	t.Parallel()

	id := mustDeviceID(t, 4242)
	date := mustDate(t, sentinelSolarDate)
	settingsBody := `{"deviceId":4242,"timeFormat":"` + sentinelDeviceName + `",` +
		`"activityTracking":{"moveAlertEnabled":true},` +
		`"alarms":[{"alarmId":1,"alarmSound":"` + sentinelAlarmSound + `",` +
		`"alarmMessage":"` + sentinelAlarmMsg + `"}]}`
	lastUsedBody := `{"userDeviceId":4242,"lastUsedDeviceName":"` + sentinelDeviceName + `",` +
		`"userProfileNumber":9001}`
	primaryBody := `{"PrimaryTrainingDevice":{"deviceId":4242},` +
		`"PrimaryTrainingDevices":{"deviceWeights":[` +
		`{"deviceId":4242,"displayName":"` + sentinelTrainingTag + `"}]}}`
	solarBody := `{"deviceSolarInput":{"solarDailyDataDTOs":[` +
		`{"calendarDate":"` + sentinelSolarDate + `","solarIntensityAvg":77}]}}`

	script := testkit.NewScript().
		With(client.PathDeviceSettingsPrefix+"/"+id.String(), testkit.JSON(http.StatusOK, settingsBody)).
		With(client.PathDeviceLastUsed, testkit.JSON(http.StatusOK, lastUsedBody)).
		With(client.PathPrimaryTrainingDevice, testkit.JSON(http.StatusOK, primaryBody)).
		With(client.PathDeviceSolarPrefix+"/"+id.String()+"/"+date.String()+"/"+date.String(),
			testkit.JSON(http.StatusOK, solarBody)).
		With(client.PathDevices, testkit.JSON(http.StatusOK,
			`[{"deviceId":4242,"productDisplayName":"`+sentinelDeviceName+`"}]`))
	h := newHarness(t, script, client.Limits{})
	devices := newDevices(t, h)

	settings, err := devices.Settings(t.Context(), h.session, id)
	if err != nil {
		t.Fatalf("Settings() = %v", err)
	}
	lastUsed, err := devices.LastUsed(t.Context(), h.session)
	if err != nil {
		t.Fatalf("LastUsed() = %v", err)
	}
	primary, err := devices.PrimaryTrainingDevice(t.Context(), h.session)
	if err != nil {
		t.Fatalf("PrimaryTrainingDevice() = %v", err)
	}
	solarDays, _, err := devices.SolarData(t.Context(), h.session, id, date)
	if err != nil {
		t.Fatalf("SolarData() = %v", err)
	}
	alarms, err := devices.Alarms(t.Context(), h.session)
	if err != nil {
		t.Fatalf("Alarms() = %v", err)
	}

	if settings.Alarms.Len() != 1 || solarDays == nil || len(primary.TrainingDevices()) != 1 {
		t.Fatalf("fixtures did not decode as expected: settings=%+v solarDays=%v primary=%+v",
			settings, solarDays, primary)
	}

	models := map[string]any{
		"DeviceSettings":        settings,
		"ActivityTracking":      *settings.ActivityTracking,
		"DeviceAlarmFromDevice": settings.Alarms.Items()[0],
		"DeviceLastUsed":        lastUsed,
		"PrimaryTrainingDevice": primary,
		"TrainingDeviceWeight":  primary.TrainingDevices()[0],
		"DeviceSolarDay":        solarDays[0],
		"DeviceAlarmResult":     alarms,
	}

	needles := []string{
		sentinelDeviceName, sentinelAlarmSound, sentinelAlarmMsg, sentinelTrainingTag,
	}
	for name, value := range models {
		var logged strings.Builder
		slog.New(slog.NewTextHandler(&logged, leakLogOptions())).Info("garmin read", "model", value)

		rendered := logged.String()
		for _, needle := range needles {
			if strings.Contains(rendered, needle) {
				t.Errorf("logging %s leaks %q: %s", name, needle, rendered)
			}
		}
		if !strings.Contains(rendered, "model") {
			t.Errorf("logging %s produced no model group: %s", name, rendered)
		}
	}
}
