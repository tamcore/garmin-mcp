package tools

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// primaryTrainingDeviceBody is a synthetic primary-training-device document.
const primaryTrainingDeviceBody = `{"PrimaryTrainingDevice":{"deviceId":4242},` +
	`"PrimaryTrainingDevices":{"deviceWeights":[` +
	`{"deviceId":4242,"displayName":"` + syntheticWatchName + `","primaryWearableDevice":true,` +
	`"primaryTrainingCapable":true},` +
	`{"deviceId":4243,"displayName":"Synthetic Strap","primaryWearableDevice":false,` +
	`"primaryTrainingCapable":false}` +
	`]},"WearableDevices":{"wearableDeviceCount":2}}`

func TestGetPrimaryTrainingDeviceDeclaresTheUpstreamContract(t *testing.T) {
	t.Parallel()

	contract := getPrimaryTrainingDeviceContract()
	if got := contract.Spec.Name; got != ToolGetPrimaryTrainingDevice {
		t.Errorf("wire name = %q, want %q", got, ToolGetPrimaryTrainingDevice)
	}
	if got := contract.Spec.Category; got != categoryDevice {
		t.Errorf("log category = %q, want %q", got, categoryDevice)
	}
	if len(contract.Schema.Properties()) != 0 {
		t.Errorf("declared %d properties, want none", len(contract.Schema.Properties()))
	}
}

func TestGetPrimaryTrainingDeviceReturnsTheCuratedDocument(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathPrimaryTrainingDevice,
		testkit.JSON(http.StatusOK, primaryTrainingDeviceBody))
	svc, _ := deviceToolsService(t, script)
	session := deviceToolsSession(t, deviceToolsServer(t, svc))

	result := callDeviceTool(t, session, ToolGetPrimaryTrainingDevice, nil)
	out := deviceToolStructured(t, result)

	if got, _ := out["primary_device_id"].(float64); got != 4242 {
		t.Errorf("primary_device_id = %v, want 4242", got)
	}
	if got, _ := out["training_device_count"].(float64); got != 2 {
		t.Errorf("training_device_count = %v, want 2", got)
	}
	if got, _ := out["wearable_device_count"].(float64); got != 2 {
		t.Errorf("wearable_device_count = %v, want 2", got)
	}
	devices := list(t, out, "training_devices")
	first := entry(t, devices, 0)
	if got, _ := first["display_name"].(string); got != syntheticWatchName {
		t.Errorf("training_devices[0].display_name = %q, want %q", got, syntheticWatchName)
	}
	if got, _ := first["is_primary_wearable"].(bool); !got {
		t.Error("training_devices[0].is_primary_wearable = false, want true")
	}
	if truncated, _ := out["truncated"].(bool); truncated {
		t.Error("truncated = true, want false for a two-device roster")
	}
}

func TestGetPrimaryTrainingDeviceToleratesAnEmptyDocument(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathPrimaryTrainingDevice, testkit.JSON(http.StatusOK, `{}`))
	svc, _ := deviceToolsService(t, script)
	session := deviceToolsSession(t, deviceToolsServer(t, svc))

	result := callDeviceTool(t, session, ToolGetPrimaryTrainingDevice, nil)
	out := deviceToolStructured(t, result)

	if _, present := out["primary_device_id"]; present {
		t.Error("primary_device_id is present, want it omitted for an empty document")
	}
	if got, _ := out["training_device_count"].(float64); got != 0 {
		t.Errorf("training_device_count = %v, want 0", got)
	}
}

func TestGetPrimaryTrainingDeviceTruncatesAnOversizedRoster(t *testing.T) {
	t.Parallel()

	entries := make([]string, 0, DefaultMaxDevices+5)
	for index := range DefaultMaxDevices + 5 {
		entries = append(entries, `{"deviceId":`+strconv.Itoa(index)+`,"displayName":"Synthetic"}`)
	}
	body := `{"PrimaryTrainingDevices":{"deviceWeights":[` + strings.Join(entries, ",") + `]}}`

	script := testkit.NewScript().With(client.PathPrimaryTrainingDevice, testkit.JSON(http.StatusOK, body))
	svc, _ := deviceToolsService(t, script)
	session := deviceToolsSession(t, deviceToolsServer(t, svc))

	result := callDeviceTool(t, session, ToolGetPrimaryTrainingDevice, nil)
	out := deviceToolStructured(t, result)

	if got, _ := out["training_device_count"].(float64); got != float64(DefaultMaxDevices) {
		t.Errorf("training_device_count = %v, want the bound %d", got, DefaultMaxDevices)
	}
	if truncated, _ := out["truncated"].(bool); !truncated {
		t.Error("truncated = false, want true when the bound was applied")
	}
}

func TestGetPrimaryTrainingDeviceRefusesARegistryItDoesNotHave(t *testing.T) {
	t.Parallel()

	svc, _ := deviceToolsService(t, testkit.NewScript())
	err := registerGetPrimaryTrainingDevice(nil, svc)
	if !errors.Is(err, mcpserver.ErrMissingDependency) {
		t.Errorf("registering on no registry = %v, want ErrMissingDependency", err)
	}
}
