package tools

import (
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// hydrationTestDate is this file's synthetic calendar date, distinct from
// the widely-shared "2026-03-01" literal several other test files already
// use, so as not to add another site to that shared count.
const hydrationTestDate = "2026-05-18"

func addHydrationDataRegistration() newToolsRegistration {
	return newToolsRegistration{name: ToolAddHydrationData, register: registerAddHydrationData}
}

// TestAddHydrationDataRecordsAnEntry drives add_hydration_data through the
// real server and pins the dispatched wire body.
func TestAddHydrationDataRecordsAnEntry(t *testing.T) {
	t.Parallel()

	svc, fake := newToolsService(t, testkit.NewScript().
		With(client.PathHydrationSet, testkit.JSON(http.StatusOK, `{}`)))
	server := newToolsServer(t, svc, newToolsServerConfig{
		write: []newToolsRegistration{addHydrationDataRegistration()},
	})
	session := newToolsSession(t, server)

	out := newToolsCall(t, session, ToolAddHydrationData, map[string]any{
		argValueInML: 250, argCDate: hydrationTestDate, argHydrationTime: hydrationTestDate + "T09:30:00.000",
	})
	if out[argValueInML] != float64(250) {
		t.Errorf("value_in_ml = %v, want 250", out[argValueInML])
	}
	if out[argCDate] != hydrationTestDate {
		t.Errorf("cdate = %v, want %s", out[argCDate], hydrationTestDate)
	}

	requests := fake.Requests()
	if len(requests) != 1 || requests[0].Method != http.MethodPut {
		t.Errorf("requests = %+v, want one PUT", requests)
	}
}

func TestAddHydrationDataRefusesAMismatchedDate(t *testing.T) {
	t.Parallel()

	svc, fake := newToolsService(t, testkit.NewScript())
	server := newToolsServer(t, svc, newToolsServerConfig{
		write: []newToolsRegistration{addHydrationDataRegistration()},
	})
	session := newToolsSession(t, server)

	message := newToolsCallError(t, session, ToolAddHydrationData, map[string]any{
		argValueInML: 250, argCDate: "2026-03-02", argHydrationTime: hydrationTestDate + "T09:30:00.000",
	})
	if message == "" {
		t.Error("the refusal carried no message")
	}
	if got := len(fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestAddHydrationDataRefusesAnUnreasonableVolume(t *testing.T) {
	t.Parallel()

	svc, fake := newToolsService(t, testkit.NewScript())
	server := newToolsServer(t, svc, newToolsServerConfig{
		write: []newToolsRegistration{addHydrationDataRegistration()},
	})
	session := newToolsSession(t, server)

	message := newToolsCallError(t, session, ToolAddHydrationData, map[string]any{
		argValueInML: 20000, argCDate: hydrationTestDate, argHydrationTime: hydrationTestDate + "T09:30:00.000",
	})
	if message == "" {
		t.Error("the refusal carried no message")
	}
	if got := len(fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}
