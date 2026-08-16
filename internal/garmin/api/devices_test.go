package api_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

const deviceSerialSentinel = "SENTINEL-SERIAL-0001"

const devicesBody = `[{"deviceId":4242,"productDisplayName":"Fake Watch 9","serialNumber":"` +
	deviceSerialSentinel + `","applicationKey":"fake","imageUrl":null,"futureField":{"x":1}},` +
	`{"deviceId":"4243","productDisplayName":"Fake Strap"}]`

func newDevices(t *testing.T, h harness) *api.Devices {
	t.Helper()

	devices, err := api.NewDevices(h.rc)
	if err != nil {
		t.Fatalf("NewDevices() = %v", err)
	}
	return devices
}

func TestDevicesListDecodesAPlainArray(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathDevices, testkit.JSON(http.StatusOK, devicesBody))
	h := newHarness(t, script, client.Limits{})

	got, err := newDevices(t, h).List(t.Context(), h.session)
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("%d devices decoded, want 2", len(got))
	}
	if id, ok := got[0].DeviceID.Int64(); !ok || id != 4242 {
		t.Errorf("DeviceID = %v/%v, want 4242", id, ok)
	}
	// The second device sends its id as a string, which the union decoder tolerates.
	if id, ok := got[1].DeviceID.Int64(); !ok || id != 4243 {
		t.Errorf("second DeviceID = %v/%v, want 4243 from the string form", id, ok)
	}
	if got[0].ProductDisplayName == nil || *got[0].ProductDisplayName != "Fake Watch 9" {
		t.Errorf("ProductDisplayName = %v, want the decoded value", got[0].ProductDisplayName)
	}
	if got[0].SerialNumber == nil || *got[0].SerialNumber != deviceSerialSentinel {
		t.Errorf("SerialNumber = %v, want the decoded value", got[0].SerialNumber)
	}
	if got[0].ImageURL != nil {
		t.Error("ImageURL must be nil for an explicit null")
	}
}

func TestDevicesListNormalizesAnEmptyResponse(t *testing.T) {
	t.Parallel()

	cases := map[string]testkit.Behavior{
		"empty array": testkit.JSON(http.StatusOK, `[]`),
		"no content":  {Status: http.StatusNoContent},
	}

	for name, behavior := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := testkit.NewScript().With(client.PathDevices, behavior)
			h := newHarness(t, script, client.Limits{})

			got, err := newDevices(t, h).List(t.Context(), h.session)
			if err != nil {
				t.Fatalf("List() = %v, want an empty result rather than an error", err)
			}
			if len(got) != 0 {
				t.Errorf("%d devices decoded, want 0", len(got))
			}
		})
	}
}

func TestDevicesListMapsAServerErrorOntoAPIError(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathDevices,
		testkit.JSON(http.StatusInternalServerError, `{"message":"synthetic"}`))
	h := newHarness(t, script, client.Limits{MaxAttempts: 1})

	_, err := newDevices(t, h).List(t.Context(), h.session)
	if !errors.Is(err, client.ErrServer) {
		t.Fatalf("List() = %v, want ErrServer", err)
	}

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("the failure is not an *APIError")
	}
	if apiErr.Endpoint != client.EndpointDevices {
		t.Errorf("Endpoint = %q, want the devices label", apiErr.Endpoint)
	}
}

func mustDeviceID(t *testing.T, value int64) client.ID {
	t.Helper()

	id, err := client.NewID(value)
	if err != nil {
		t.Fatalf("NewID(%d) = %v", value, err)
	}
	return id
}

const settingsBody = `{"deviceId":4242,"timeFormat":"time_twenty_four_hr",` +
	`"dateFormat":"month_day_year","measurementUnits":"statute_us",` +
	`"keyTonesEnabled":true,"keyVibrationEnabled":false,"alertTonesEnabled":true,` +
	`"activityTracking":{"moveAlertEnabled":true,"pulseOxSleepTrackingEnabled":false,` +
	`"highHrAlertEnabled":true,"lowHrAlertEnabled":false},` +
	`"alarms":[{"alarmId":1,"alarmTime":390,"alarmMode":"ON","alarmDays":["MONDAY"],` +
	`"alarmSound":"BEEP","backlight":"ON","alarmMessage":"Wake up"}],` +
	`"futureField":{"x":1}}`

func TestDevicesSettingsDecodesTheDocument(t *testing.T) {
	t.Parallel()

	id := mustDeviceID(t, 4242)
	script := testkit.NewScript().With(client.PathDeviceSettingsPrefix+"/"+id.String(),
		testkit.JSON(http.StatusOK, settingsBody))
	h := newHarness(t, script, client.Limits{})

	got, err := newDevices(t, h).Settings(t.Context(), h.session, id)
	if err != nil {
		t.Fatalf("Settings() = %v", err)
	}
	if deviceID, ok := got.DeviceID.Int64(); !ok || deviceID != 4242 {
		t.Errorf("DeviceID = %v/%v, want 4242", deviceID, ok)
	}
	if got.TimeFormat == nil || *got.TimeFormat != "time_twenty_four_hr" {
		t.Errorf("TimeFormat = %v, want the decoded value", got.TimeFormat)
	}
	if got.ActivityTracking == nil || got.ActivityTracking.MoveAlertEnabled == nil ||
		!*got.ActivityTracking.MoveAlertEnabled {
		t.Fatalf("ActivityTracking = %+v, want moveAlertEnabled true", got.ActivityTracking)
	}
	if got.Alarms.Len() != 1 {
		t.Fatalf("Alarms.Len() = %d, want 1", got.Alarms.Len())
	}
	alarm := got.Alarms.Items()[0]
	if !alarm.Enabled() {
		t.Error("Enabled() = false, want true for alarmMode ON")
	}
	if alarmTime, ok := alarm.AlarmTime.Int64(); !ok || alarmTime != 390 {
		t.Errorf("AlarmTime = %v/%v, want 390", alarmTime, ok)
	}
}

func TestDevicesSettingsRefusesAnUnsetID(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	if _, err := newDevices(t, h).Settings(t.Context(), h.session, client.ID{}); !errors.Is(
		err, client.ErrValidation) {
		t.Errorf("Settings() without an id = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

const lastUsedBody = `{"userDeviceId":4242,"lastUsedDeviceName":"Fake Watch 9",` +
	`"lastUsedDeviceApplicationKey":"fake-key","userProfileNumber":9001,` +
	`"lastUsedDeviceUploadTime":1700000000000,"imageUrl":null}`

func TestDevicesLastUsedDecodesTheDocument(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathDeviceLastUsed, testkit.JSON(http.StatusOK, lastUsedBody))
	h := newHarness(t, script, client.Limits{})

	got, err := newDevices(t, h).LastUsed(t.Context(), h.session)
	if err != nil {
		t.Fatalf("LastUsed() = %v", err)
	}
	if id, ok := got.UserDeviceID.Int64(); !ok || id != 4242 {
		t.Errorf("UserDeviceID = %v/%v, want 4242", id, ok)
	}
	if profile, ok := got.UserProfileNumber.Int64(); !ok || profile != 9001 {
		t.Errorf("UserProfileNumber = %v/%v, want 9001", profile, ok)
	}
	if got.ImageURL != nil {
		t.Error("ImageURL must be nil for an explicit null")
	}
}
