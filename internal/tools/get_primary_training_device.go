package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetPrimaryTrainingDevice is the upstream compatibility name of the
// primary-training-device read.
const ToolGetPrimaryTrainingDevice = "get_primary_training_device"

// TrainingDeviceEntry is one device's priority entry in the account's
// training-device roster.
//
// Source: the curation in devices.py's get_primary_training_device tool
// (devices.py:202-213): device.get("deviceId"), device.get("displayName"),
// device.get("primaryWearableDevice") and device.get("primaryTrainingCapable").
type TrainingDeviceEntry struct {
	DeviceID               *int64  `json:"device_id,omitempty" jsonschema:"the device's identifier"`
	DisplayName            *string `json:"display_name,omitempty" jsonschema:"the device's display name"`
	IsPrimaryWearable      *bool   `json:"is_primary_wearable,omitempty" jsonschema:"is the primary wearable"`
	PrimaryTrainingCapable *bool   `json:"primary_training_capable,omitempty" jsonschema:"can be primary for training"`
}

// PrimaryTrainingDeviceResult is the primary-training-device document.
//
// Source: devices.py's get_primary_training_device tool (devices.py:189-222):
// data.get("PrimaryTrainingDevice", {}).get("deviceId"),
// data.get("PrimaryTrainingDevices", {}).get("deviceWeights", []) and
// data.get("WearableDevices", {}).get("wearableDeviceCount").
type PrimaryTrainingDeviceResult struct {
	PrimaryDeviceID     *int64                `json:"primary_device_id,omitempty" jsonschema:"the primary device's id"`
	TrainingDevices     []TrainingDeviceEntry `json:"training_devices,omitempty" jsonschema:"the training-capable roster"`
	TrainingDeviceCount int                   `json:"training_device_count" jsonschema:"how many devices this carries"`
	WearableDeviceCount *int64                `json:"wearable_device_count,omitempty" jsonschema:"the wearable count"`

	// Truncated reports that the account has more training-capable devices than
	// this server's device bound allows.
	Truncated bool `json:"truncated" jsonschema:"whether the roster was cut at this server's bound"`
}

// LogValue reports counts only, never a device identifier.
func (p PrimaryTrainingDeviceResult) LogValue() slog.Value {
	return shape("primaryTrainingDevice",
		slog.Int("trainingDevices", p.TrainingDeviceCount),
		slog.Bool("truncated", p.Truncated),
	)
}

func getPrimaryTrainingDeviceContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetPrimaryTrainingDevice,
			Title: "Get primary training device",
			Description: "read the device Garmin designates primary for training " +
				"metrics, the priority-ordered roster of training-capable devices, and " +
				"the account's wearable device count. Takes no arguments",
			Tier:        policy.TierReadOnly,
			Category:    categoryDevice,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(),
	}
}

// registerGetPrimaryTrainingDevice registers the tool.
func registerGetPrimaryTrainingDevice(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, _ noArguments) (
		*mcp.CallToolResult, PrimaryTrainingDeviceResult, error,
	) {
		session, err := svc.session(ctx)
		if err != nil {
			return nil, PrimaryTrainingDeviceResult{}, err
		}
		doc, err := svc.devices.PrimaryTrainingDevice(ctx, session)
		if err != nil {
			return nil, PrimaryTrainingDeviceResult{}, fail(err)
		}
		return nil, newPrimaryTrainingDeviceResult(doc, svc.bounds.MaxDevices), nil
	}
	return mcpserver.AddTool(registry, getPrimaryTrainingDeviceContract().Registration(), handler)
}

// newPrimaryTrainingDeviceResult maps the domain document onto the bounded result.
//
// The roster is truncated rather than refused, for the same reason get_devices's
// list is: the first entries are the useful ones and no history is lost by seeing
// fewer of them.
func newPrimaryTrainingDeviceResult(
	doc api.PrimaryTrainingDevice, limit int,
) PrimaryTrainingDeviceResult {
	out := PrimaryTrainingDeviceResult{}
	if id, ok := doc.PrimaryDeviceID(); ok {
		out.PrimaryDeviceID = &id
	}

	devices := doc.TrainingDevices()
	truncated := len(devices) > limit
	if truncated {
		devices = devices[:limit]
	}
	entries := make([]TrainingDeviceEntry, 0, len(devices))
	for _, device := range devices {
		entries = append(entries, TrainingDeviceEntry{
			DeviceID:               optionalInt64(device.DeviceID),
			DisplayName:            device.DisplayName,
			IsPrimaryWearable:      device.PrimaryWearableDevice,
			PrimaryTrainingCapable: device.PrimaryTrainingCapable,
		})
	}
	out.TrainingDevices = entries
	out.TrainingDeviceCount = len(entries)
	out.Truncated = truncated

	if count, ok := doc.WearableDeviceCount(); ok {
		out.WearableDeviceCount = &count
	}
	return out
}
