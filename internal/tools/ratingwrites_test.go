package tools_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// Argument names of the rating writes.
const (
	argEventType = "event_type"
	argFeel      = "feel"
	argRPE       = "rpe"
)

// The Garmin keys the rating writes send. They are the wire names this server
// commits to, so a change here is a change to what an account records.
const (
	fieldFeel = "directWorkoutFeel"
	fieldRPE  = "directWorkoutRpe"
)

// ratingScript answers the profile lookup and the activity writes.
func ratingScript() testkit.Script {
	return testkit.NewScript().
		With(client.PathSocialProfile, repeat(okJSON(profileBody), 6)...).
		With(activityWritePath(), repeat(okJSON(updatedActivityBody), 6)...)
}

// summaryOf returns the summaryDTO patch of the recorded activity write.
func summaryOf(t *testing.T, h harness) map[string]any {
	t.Helper()

	body := h.bodyFor(t, http.MethodPut, activityWritePath())
	summary, ok := body["summaryDTO"].(map[string]any)
	if !ok {
		t.Fatalf("the write sent no summaryDTO: %v", body)
	}
	return summary
}

// TestSetActivityEventTypeWritesTheCategory covers the accepted path of the
// event-type write; the refusal of an unknown key is fixed in writes_test.go.
func TestSetActivityEventTypeWritesTheCategory(t *testing.T) {
	h := newWriteHarness(t, ratingScript(), enabledWrites())

	out := h.call(t, tools.ToolSetActivityEventType, map[string]any{
		argActivityID: testActivityID,
		argEventType:  "race",
	})

	if got := out["updated"]; got != "event_type" {
		t.Errorf("updated = %v, want event_type", got)
	}
	body := h.bodyFor(t, http.MethodPut, activityWritePath())
	eventType, ok := body["eventTypeDTO"].(map[string]any)
	if !ok {
		t.Fatalf("the write sent no eventTypeDTO: %v", body)
	}
	if got := eventType["typeKey"]; got != "race" {
		t.Errorf("typeKey = %v, want race", got)
	}
}

// TestSetActivityEventTypeRefusesAnOversizedKey keeps the length bound ahead of
// the catalog comparison, so an absurd value is refused by its size rather than
// walked through the key list.
func TestSetActivityEventTypeRefusesAnOversizedKey(t *testing.T) {
	h := newWriteHarness(t, ratingScript(), enabledWrites())

	message := h.callError(t, tools.ToolSetActivityEventType, map[string]any{
		argActivityID: testActivityID,
		argEventType:  strings.Repeat("a", 4096),
	})

	if len(h.fake.Requests()) != 0 {
		t.Errorf("an oversized event type still reached Garmin: %v", h.recordedMethods())
	}
	assertSanitized(t, message)
}

// TestSetActivityFeelWritesTheRatingIntoItsOwnField pins which field the rating
// lands in: writing the other one would silently replace a different rating.
func TestSetActivityFeelWritesTheRatingIntoItsOwnField(t *testing.T) {
	h := newWriteHarness(t, ratingScript(), enabledWrites())

	out := h.call(t, tools.ToolSetActivityFeel, map[string]any{
		argActivityID: testActivityID,
		argFeel:       75,
	})

	if got := out["updated"]; got != argFeel {
		t.Errorf("updated = %v, want feel", got)
	}
	summary := summaryOf(t, h)
	if got := summary[fieldFeel]; got != float64(75) {
		t.Errorf("%s = %v, want 75", fieldFeel, got)
	}
	if _, present := summary[fieldRPE]; present {
		t.Errorf("the feel write also sent %s, which would overwrite the other rating", fieldRPE)
	}
}

// TestSetPerceivedEffortScalesTheRatingTheWayGarminStoresIt fixes the factor: an
// RPE of 7 is stored as 70, and sending 7 unscaled would record a tenth of the
// effort the caller reported.
func TestSetPerceivedEffortScalesTheRatingTheWayGarminStoresIt(t *testing.T) {
	h := newWriteHarness(t, ratingScript(), enabledWrites())

	out := h.call(t, tools.ToolSetPerceivedEffort, map[string]any{
		argActivityID: testActivityID,
		argRPE:        7,
	})

	if got := out["updated"]; got != "perceived_effort" {
		t.Errorf("updated = %v, want perceived_effort", got)
	}
	summary := summaryOf(t, h)
	if got := summary[fieldRPE]; got != float64(70) {
		t.Errorf("%s = %v, want 70 for an RPE of 7", fieldRPE, got)
	}
	if _, present := summary[fieldFeel]; present {
		t.Errorf("the effort write also sent %s, which would overwrite the other rating", fieldFeel)
	}
}
