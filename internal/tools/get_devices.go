package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetDevices is the upstream compatibility name of the device tool.
const ToolGetDevices = "get_devices"

// DeviceSummary is one registered device.
//
// A serial number identifies a person's hardware, so it is returned to the authorized
// caller but never logged: LogValue reports its presence only.
type DeviceSummary struct {
	DeviceID           *int64  `json:"device_id,omitempty" jsonschema:"the Garmin device identifier"`
	UnitID             *int64  `json:"unit_id,omitempty" jsonschema:"the Garmin unit identifier"`
	SerialNumber       *string `json:"serial_number,omitempty" jsonschema:"the device serial number"`
	ProductDisplayName *string `json:"product_display_name,omitempty" jsonschema:"the product name"`
	ProductSKU         *string `json:"product_sku,omitempty" jsonschema:"the product SKU"`
}

// DeviceList is the plain array of registered devices, bounded.
type DeviceList struct {
	Devices []DeviceSummary `json:"devices" jsonschema:"the registered devices"`
	Count   int             `json:"count" jsonschema:"how many devices this result carries"`

	// Truncated reports that the account has more devices than the bound allows.
	Truncated bool `json:"truncated" jsonschema:"whether the list was cut at this server's bound"`
}

// LogValue reports the device count, never a serial number.
func (l DeviceList) LogValue() slog.Value {
	return shape("deviceList",
		slog.Int("devices", len(l.Devices)),
		slog.Bool("truncated", l.Truncated),
	)
}

func getDevicesContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetDevices,
			Title: "Get devices",
			Description: "read every Garmin device registered to the account: identifier, " +
				"serial number, product name and SKU. Takes no arguments. An account with " +
				"no paired device returns an empty list, which is a normal state",
			Tier:        policy.TierReadOnly,
			Category:    categoryDevice,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(),
	}
}

// registerGetDevices registers the tool.
func registerGetDevices(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, _ noArguments) (
		*mcp.CallToolResult, DeviceList, error,
	) {
		session, err := svc.session(ctx)
		if err != nil {
			return nil, DeviceList{}, err
		}
		devices, err := svc.devices.List(ctx, session)
		if err != nil {
			return nil, DeviceList{}, fail(err)
		}
		return nil, newDeviceList(devices, svc.bounds.MaxDevices), nil
	}
	return mcpserver.AddTool(registry, getDevicesContract().Registration(), handler)
}

// newDeviceList maps the domain models onto the bounded result.
//
// A device list is truncated rather than refused: the first devices are the useful
// ones, and an account cannot lose history by seeing fewer of them.
func newDeviceList(devices []api.Device, limit int) DeviceList {
	truncated := len(devices) > limit
	if truncated {
		devices = devices[:limit]
	}

	out := make([]DeviceSummary, 0, len(devices))
	for _, device := range devices {
		out = append(out, DeviceSummary{
			DeviceID:           optionalInt64(device.DeviceID),
			UnitID:             optionalInt64(device.UnitID),
			SerialNumber:       device.SerialNumber,
			ProductDisplayName: device.ProductDisplayName,
			ProductSKU:         device.ProductSKU,
		})
	}
	return DeviceList{Devices: out, Count: len(out), Truncated: truncated}
}
