package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetDeviceLastUsed is the upstream compatibility name of the last-used-device
// read.
const ToolGetDeviceLastUsed = "get_device_last_used"

// DeviceLastUsed is the most-recently-used device document.
//
// Source: the curation in devices.py's get_device_last_used tool (devices.py:70-88):
// device.get("userDeviceId"), device.get("lastUsedDeviceName"),
// device.get("lastUsedDeviceApplicationKey") and device.get("userProfileNumber"). The
// upload time is kept as the raw UTC epoch millisecond value this project's other
// tools already use for an instant (for example get_heart_rates.go's
// TimeGMTMillis), rather than reproducing upstream's
// datetime.fromtimestamp(upload_time_ms / 1000.0) call (devices.py:80-81), which
// formats in the server process's own local timezone and is not a value this server
// can reproduce deterministically.
//
// It is device material: a device identifier and an application key describe a
// person's registered hardware, so it is returned to the authorized caller and never
// logged.
type DeviceLastUsed struct {
	UserDeviceID         *int64  `json:"user_device_id,omitempty" jsonschema:"the last-used device's identifier"`
	DeviceName           *string `json:"device_name,omitempty" jsonschema:"the device's display name"`
	DeviceKey            *string `json:"device_key,omitempty" jsonschema:"the device's application key"`
	UserProfileID        *int64  `json:"user_profile_id,omitempty" jsonschema:"the account's own profile identifier"`
	LastUploadTimeMillis *int64  `json:"last_upload_time_millis,omitempty" jsonschema:"last upload, UTC epoch ms"`
}

// LogValue reports presence only, never a device identifier.
func (d DeviceLastUsed) LogValue() slog.Value {
	return shape("deviceLastUsed",
		slog.String("userDeviceId", presence(d.UserDeviceID != nil)),
		slog.String("deviceName", presence(d.DeviceName != nil)),
	)
}

func getDeviceLastUsedContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetDeviceLastUsed,
			Title: "Get last used device",
			Description: "read the account's most-recently-used Garmin device: its " +
				"identifier, display name, application key, the account's own profile " +
				"identifier, and the last upload instant. Takes no arguments",
			Tier:        policy.TierReadOnly,
			Category:    categoryDevice,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(),
	}
}

// registerGetDeviceLastUsed registers the tool.
func registerGetDeviceLastUsed(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, _ noArguments) (
		*mcp.CallToolResult, DeviceLastUsed, error,
	) {
		session, err := svc.session(ctx)
		if err != nil {
			return nil, DeviceLastUsed{}, err
		}
		last, err := svc.devices.LastUsed(ctx, session)
		if err != nil {
			return nil, DeviceLastUsed{}, fail(err)
		}
		return nil, newDeviceLastUsed(last), nil
	}
	return mcpserver.AddTool(registry, getDeviceLastUsedContract().Registration(), handler)
}

// newDeviceLastUsed maps the domain document onto the result.
func newDeviceLastUsed(last api.DeviceLastUsed) DeviceLastUsed {
	return DeviceLastUsed{
		UserDeviceID:         optionalInt64(last.UserDeviceID),
		DeviceName:           last.LastUsedDeviceName,
		DeviceKey:            last.LastUsedDeviceApplicationKey,
		UserProfileID:        optionalInt64(last.UserProfileNumber),
		LastUploadTimeMillis: optionalInt64(last.LastUsedDeviceUploadTime),
	}
}
