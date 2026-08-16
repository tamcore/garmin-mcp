package api

import (
	"context"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// TrainingDeviceWeight is one device's priority entry in the account's
// training-device roster.
//
// Source: the curation in devices.py's get_primary_training_device tool
// (devices.py:202-213): device.get("deviceId"), device.get("displayName"),
// device.get("primaryWearableDevice"), device.get("primaryTrainingCapable") and
// device.get("imageUrl").
type TrainingDeviceWeight struct {
	DeviceID               client.Number `json:"deviceId"`
	DisplayName            *string       `json:"displayName"`
	PrimaryWearableDevice  *bool         `json:"primaryWearableDevice"`
	PrimaryTrainingCapable *bool         `json:"primaryTrainingCapable"`
	ImageURL               *string       `json:"imageUrl"`
}

// primaryTrainingDeviceRef names the one device Garmin designates primary.
// Source: data.get("PrimaryTrainingDevice", {}).get("deviceId")
// (devices.py:189-190).
type primaryTrainingDeviceRef struct {
	DeviceID client.Number `json:"deviceId"`
}

// trainingDeviceWeights is the roster of every training-capable device.
// Source: data.get("PrimaryTrainingDevices", {}).get("deviceWeights", [])
// (devices.py:193-195).
type trainingDeviceWeights struct {
	DeviceWeights client.List[TrainingDeviceWeight] `json:"deviceWeights"`
}

// wearableDevices carries the account's wearable-device count.
// Source: data.get("WearableDevices", {}).get("wearableDeviceCount")
// (devices.py:218-222).
type wearableDevices struct {
	WearableDeviceCount client.Number `json:"wearableDeviceCount"`
}

// PrimaryTrainingDevice is the primary-training-device document: the
// designated device, the priority-ordered roster of training-capable devices,
// and the account's wearable count.
//
// Source: python-garminconnect's get_primary_training_device, cross-checked
// against devices.py's get_primary_training_device tool, which is what
// evidences the three top-level keys below — the pinned client itself types
// this dict[str, Any] with no field curation of its own.
type PrimaryTrainingDevice struct {
	Primary  *primaryTrainingDeviceRef `json:"PrimaryTrainingDevice"`
	Roster   *trainingDeviceWeights    `json:"PrimaryTrainingDevices"`
	Wearable *wearableDevices          `json:"WearableDevices"`
}

// PrimaryDeviceID reports the designated primary device's identifier, and
// whether one is set.
func (p PrimaryTrainingDevice) PrimaryDeviceID() (int64, bool) {
	if p.Primary == nil {
		return 0, false
	}
	return p.Primary.DeviceID.Int64Exact()
}

// TrainingDevices is the priority-ordered roster of training-capable devices.
func (p PrimaryTrainingDevice) TrainingDevices() []TrainingDeviceWeight {
	if p.Roster == nil {
		return nil
	}
	return p.Roster.DeviceWeights.Items()
}

// WearableDeviceCount reports the account's wearable-device count, and
// whether it was present.
func (p PrimaryTrainingDevice) WearableDeviceCount() (int64, bool) {
	if p.Wearable == nil {
		return 0, false
	}
	return p.Wearable.WearableDeviceCount.Int64Exact()
}

// PrimaryTrainingDevice reads the primary-training-device document.
func (d *Devices) PrimaryTrainingDevice(
	ctx context.Context, session client.Session,
) (PrimaryTrainingDevice, error) {
	req := readRequest(client.OpGetPrimaryTrainingDevice, client.EndpointPrimaryTrainingDevice,
		client.PathPrimaryTrainingDevice, nil)

	var doc PrimaryTrainingDevice
	if _, err := d.req.read(ctx, session, req, &doc); err != nil {
		return PrimaryTrainingDevice{}, err
	}
	return doc, nil
}
