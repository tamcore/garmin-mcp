package tools

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// deviceSolarTestDate is the synthetic calendar day the solar fixture below covers.
const deviceSolarTestDate = "2026-01-31"

// deviceSolarBody is a synthetic solar-charging envelope for one day.
const deviceSolarBody = `{"deviceSolarInput":{"solarDailyDataDTOs":[` +
	`{"calendarDate":"2026-01-31","solarIntensityAvg":42.5,"solarIntensityMax":88,` +
	`"batteryCharged":3,"batteryUsed":2,"batteryNet":1}` +
	`]}}`

func deviceSolarPath(t *testing.T) string {
	t.Helper()

	id, err := client.NewID(4242)
	if err != nil {
		t.Fatalf("client.NewID() = %v", err)
	}
	date, err := client.ParseDate(deviceSolarTestDate)
	if err != nil {
		t.Fatalf("client.ParseDate() = %v", err)
	}
	return client.PathDeviceSolarPrefix + "/" + id.String() + "/" + date.String() + "/" + date.String()
}

func TestGetDeviceSolarDataDeclaresTheUpstreamContract(t *testing.T) {
	t.Parallel()

	contract := getDeviceSolarDataContract()
	if got := contract.Spec.Name; got != ToolGetDeviceSolarData {
		t.Errorf("wire name = %q, want %q", got, ToolGetDeviceSolarData)
	}
	if got := contract.Spec.Category; got != categoryDevice {
		t.Errorf("log category = %q, want %q", got, categoryDevice)
	}

	schema := contract.Schema
	want := []string{argDeviceID, argDate}
	if got := schema.Required(); len(got) != len(want) {
		t.Fatalf("required = %v, want %v", got, want)
	}
	for _, property := range schema.Properties() {
		if property.Name == argDeviceID && len(property.Types) != 1 {
			t.Errorf("device_id types = %v, want exactly [string]", property.Types)
		}
	}
}

func TestGetDeviceSolarDataReturnsTheCuratedDay(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(deviceSolarPath(t), testkit.JSON(http.StatusOK, deviceSolarBody))
	svc, fake := deviceToolsService(t, script)
	session := deviceToolsSession(t, deviceToolsServer(t, svc))

	result := callDeviceTool(t, session, ToolGetDeviceSolarData,
		map[string]any{argDeviceID: "4242", argDate: deviceSolarTestDate})
	out := deviceToolStructured(t, result)

	if got := out["device_id"]; got != "4242" {
		t.Errorf("device_id = %v, want the caller's own value %q", got, "4242")
	}
	if got, _ := out["count"].(float64); got != 1 {
		t.Fatalf("count = %v, want 1", got)
	}
	days := list(t, out, "solar_data")
	day := entry(t, days, 0)
	if got, _ := day[argDate].(string); got != deviceSolarTestDate {
		t.Errorf("date = %q, want %q", got, deviceSolarTestDate)
	}
	if got, _ := day["solar_intensity_avg"].(float64); got != 42.5 {
		t.Errorf("solar_intensity_avg = %v, want 42.5", got)
	}
	if got, _ := day["battery_net_percent"].(float64); got != 1 {
		t.Errorf("battery_net_percent = %v, want 1", got)
	}
	if got := len(fake.Requests()); got != 1 {
		t.Errorf("dispatched %d requests, want 1", got)
	}
}

func TestGetDeviceSolarDataReportsNoSolarCapabilityAsEmpty(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(deviceSolarPath(t),
		testkit.JSON(http.StatusOK, `{"deviceSolarInput":{"solarDailyDataDTOs":[]}}`))
	svc, _ := deviceToolsService(t, script)
	session := deviceToolsSession(t, deviceToolsServer(t, svc))

	result := callDeviceTool(t, session, ToolGetDeviceSolarData,
		map[string]any{argDeviceID: "4242", argDate: deviceSolarTestDate})
	out := deviceToolStructured(t, result)
	if got, _ := out["count"].(float64); got != 0 {
		t.Errorf("count = %v, want 0", got)
	}
}

func TestGetDeviceSolarDataRefusesAMalformedDate(t *testing.T) {
	t.Parallel()

	svc, fake := deviceToolsService(t, testkit.NewScript())
	session := deviceToolsSession(t, deviceToolsServer(t, svc))

	result := callDeviceTool(t, session, ToolGetDeviceSolarData,
		map[string]any{argDeviceID: "4242", argDate: malformedDate})
	if !result.IsError {
		t.Fatal("a malformed date succeeded, want an error result")
	}
	if got := len(fake.Requests()); got != 0 {
		t.Errorf("dispatched %d requests, want 0", got)
	}
}

func TestGetDeviceSolarDataRefusesARegistryItDoesNotHave(t *testing.T) {
	t.Parallel()

	svc, _ := deviceToolsService(t, testkit.NewScript())
	err := registerGetDeviceSolarData(nil, svc)
	if !errors.Is(err, mcpserver.ErrMissingDependency) {
		t.Errorf("registering on no registry = %v, want ErrMissingDependency", err)
	}
}
