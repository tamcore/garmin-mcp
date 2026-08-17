package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// bodyCompTestDate is this file's synthetic calendar date, distinct from the
// widely-shared "2026-03-01" literal several other test files already use.
const bodyCompTestDate = "2026-04-12"

func addBodyCompositionRegistration() newToolsRegistration {
	return newToolsRegistration{name: ToolAddBodyComposition, register: registerAddBodyComposition}
}

// TestAddBodyCompositionUploadsAFITFile drives add_body_composition through
// the real server and proves it dispatches a multipart FIT upload to
// /upload-service/upload.
func TestAddBodyCompositionUploadsAFITFile(t *testing.T) {
	t.Parallel()

	svc, fake := newToolsService(t, testkit.NewScript().
		With(client.PathUpload, testkit.JSON(http.StatusOK, `{}`)))
	server := newToolsServer(t, svc, newToolsServerConfig{
		write: []newToolsRegistration{addBodyCompositionRegistration()},
	})
	session := newToolsSession(t, server)

	out := newToolsCall(t, session, ToolAddBodyComposition, map[string]any{
		argDate: bodyCompTestDate, argWeighInWeight: 70.5, "percent_fat": 18.2,
	})
	if out[argDate] != bodyCompTestDate {
		t.Errorf("date = %v, want %s", out[argDate], bodyCompTestDate)
	}
	if out[argWeighInWeight] != 70.5 {
		t.Errorf("weight = %v, want 70.5", out[argWeighInWeight])
	}

	requests := fake.Requests()
	if len(requests) != 1 {
		t.Fatalf("len(requests) = %d, want 1", len(requests))
	}
	if !strings.Contains(requests[0].Header.Get("Content-Type"), "multipart/form-data") {
		t.Errorf("content type = %q, want multipart/form-data", requests[0].Header.Get("Content-Type"))
	}
}

func TestAddBodyCompositionRefusesAnAbsurdWeight(t *testing.T) {
	t.Parallel()

	svc, fake := newToolsService(t, testkit.NewScript())
	server := newToolsServer(t, svc, newToolsServerConfig{
		write: []newToolsRegistration{addBodyCompositionRegistration()},
	})
	session := newToolsSession(t, server)

	message := newToolsCallError(t, session, ToolAddBodyComposition, map[string]any{
		argDate: bodyCompTestDate, argWeighInWeight: 5000.0,
	})
	if message == "" {
		t.Error("the refusal carried no message")
	}
	if got := len(fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestAddBodyCompositionRefusesAnOutOfRangePhysiqueRating(t *testing.T) {
	t.Parallel()

	svc, fake := newToolsService(t, testkit.NewScript())
	server := newToolsServer(t, svc, newToolsServerConfig{
		write: []newToolsRegistration{addBodyCompositionRegistration()},
	})
	session := newToolsSession(t, server)

	message := newToolsCallError(t, session, ToolAddBodyComposition, map[string]any{
		argDate: bodyCompTestDate, argWeighInWeight: 70.0, "physique_rating": 20,
	})
	if message == "" {
		t.Error("the refusal carried no message")
	}
	if got := len(fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}
