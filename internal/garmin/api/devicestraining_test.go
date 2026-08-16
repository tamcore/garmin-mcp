package api_test

import (
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

const primaryTrainingDeviceBody = `{"PrimaryTrainingDevice":{"deviceId":4242},` +
	`"PrimaryTrainingDevices":{"deviceWeights":[` +
	`{"deviceId":4242,"displayName":"Fake Watch 9","primaryWearableDevice":true,` +
	`"primaryTrainingCapable":true,"imageUrl":null},` +
	`{"deviceId":4243,"displayName":"Fake Strap","primaryWearableDevice":false,` +
	`"primaryTrainingCapable":false}` +
	`]},"WearableDevices":{"wearableDeviceCount":2},"futureField":{"x":1}}`

func TestDevicesPrimaryTrainingDeviceDecodesTheDocument(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathPrimaryTrainingDevice,
		testkit.JSON(http.StatusOK, primaryTrainingDeviceBody))
	h := newHarness(t, script, client.Limits{})

	got, err := newDevices(t, h).PrimaryTrainingDevice(t.Context(), h.session)
	if err != nil {
		t.Fatalf("PrimaryTrainingDevice() = %v", err)
	}
	if id, ok := got.PrimaryDeviceID(); !ok || id != 4242 {
		t.Errorf("PrimaryDeviceID() = %v/%v, want 4242", id, ok)
	}
	devices := got.TrainingDevices()
	if len(devices) != 2 {
		t.Fatalf("TrainingDevices() = %d entries, want 2", len(devices))
	}
	if devices[0].DisplayName == nil || *devices[0].DisplayName != "Fake Watch 9" {
		t.Errorf("DisplayName = %v, want the decoded value", devices[0].DisplayName)
	}
	if devices[0].PrimaryWearableDevice == nil || !*devices[0].PrimaryWearableDevice {
		t.Error("PrimaryWearableDevice = false, want true for the first entry")
	}
	if count, ok := got.WearableDeviceCount(); !ok || count != 2 {
		t.Errorf("WearableDeviceCount() = %v/%v, want 2", count, ok)
	}
}

func TestDevicesPrimaryTrainingDeviceToleratesAnEmptyDocument(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathPrimaryTrainingDevice,
		testkit.JSON(http.StatusOK, `{}`))
	h := newHarness(t, script, client.Limits{})

	got, err := newDevices(t, h).PrimaryTrainingDevice(t.Context(), h.session)
	if err != nil {
		t.Fatalf("PrimaryTrainingDevice() = %v", err)
	}
	if _, ok := got.PrimaryDeviceID(); ok {
		t.Error("PrimaryDeviceID() reported present for an empty document")
	}
	if devices := got.TrainingDevices(); devices != nil {
		t.Errorf("TrainingDevices() = %v, want nil for an empty document", devices)
	}
	if _, ok := got.WearableDeviceCount(); ok {
		t.Error("WearableDeviceCount() reported present for an empty document")
	}
}
