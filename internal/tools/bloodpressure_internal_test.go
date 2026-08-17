package tools

import (
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func setBloodPressureRegistration() newToolsRegistration {
	return newToolsRegistration{name: ToolSetBloodPressure, register: registerSetBloodPressure}
}

// TestSetBloodPressureRecordsAReading drives set_blood_pressure through the
// real server and pins the dispatched wire body.
func TestSetBloodPressureRecordsAReading(t *testing.T) {
	t.Parallel()

	svc, fake := newToolsService(t, testkit.NewScript().
		With(client.PathBloodPressureSet, testkit.JSON(http.StatusOK, `{}`)))
	server := newToolsServer(t, svc, newToolsServerConfig{
		write: []newToolsRegistration{setBloodPressureRegistration()},
	})
	session := newToolsSession(t, server)

	out := newToolsCall(t, session, ToolSetBloodPressure, map[string]any{
		argSystolic: 120, argDiastolic: 80, argPulse: 65, "notes": "resting",
	})
	if out[argSystolic] != float64(120) {
		t.Errorf("systolic = %v, want 120", out[argSystolic])
	}
	if out["message"] == "" {
		t.Error("message is empty")
	}

	requests := fake.Requests()
	if len(requests) != 1 || requests[0].Method != http.MethodPost {
		t.Errorf("requests = %+v, want one POST", requests)
	}
}

func TestSetBloodPressureRefusesAnOutOfRangeSystolic(t *testing.T) {
	t.Parallel()

	svc, fake := newToolsService(t, testkit.NewScript())
	server := newToolsServer(t, svc, newToolsServerConfig{
		write: []newToolsRegistration{setBloodPressureRegistration()},
	})
	session := newToolsSession(t, server)

	message := newToolsCallError(t, session, ToolSetBloodPressure, map[string]any{
		argSystolic: 400, argDiastolic: 80, argPulse: 65,
	})
	if message == "" {
		t.Error("the refusal carried no message")
	}
	if got := len(fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}
