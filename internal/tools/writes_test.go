package tools_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// Synthetic write fixtures. Every value here is invented.
const (
	updatedActivityBody = `{"activityId":987654321,"activityName":"Renamed"}`
	activityTypesBody   = `[{"typeId":1,"typeKey":"running","parentTypeId":17},` +
		`{"typeId":9,"typeKey":"walking","parentTypeId":17}]`
	testGearUUID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
)

func writeScript() testkit.Script {
	return testkit.NewScript().
		With(client.PathSocialProfile, repeat(okJSON(profileBody), 4)...).
		With(activityWritePath(), repeat(okJSON(updatedActivityBody), 6)...).
		With(client.PathActivityTypes, okJSON(activityTypesBody))
}

func TestAWriteToolIsRefusedUnderTheCurrentPolicy(t *testing.T) {
	h := newHarness(t, readScript())

	message := h.callError(t, tools.ToolSetActivityName, map[string]any{
		argActivityID:   testActivityID,
		argActivityName: "Refused",
	})

	if len(h.fake.Requests()) != 0 {
		t.Errorf("the refusal still reached Garmin: %v", h.recordedMethods())
	}
	if message == "" {
		t.Error("the refusal carried no message")
	}
}

func TestEveryWriteAndDestructiveToolIsRefusedUnderTheCurrentPolicy(t *testing.T) {
	h := newHarness(t, readScript())

	for _, name := range append(wantWriteToolNames, wantDestructiveToolNames...) {
		t.Run(name, func(t *testing.T) {
			result := h.rawCall(t, name, map[string]any{})
			if !result.IsError && !result.NeedsInput() {
				t.Errorf("%s succeeded, but no write scope is granted anywhere yet", name)
			}
		})
	}
}

func TestSetActivityNameWritesTheNameAndReportsTheUpdate(t *testing.T) {
	h := newWriteHarness(t, writeScript(), enabledWrites())

	out := h.call(t, tools.ToolSetActivityName, map[string]any{
		argActivityID:   testActivityID,
		argActivityName: "Morning run",
	})

	if got := out["updated"]; got != "activity_name" {
		t.Errorf("updated = %v, want activity_name", got)
	}
	body := h.bodyFor(t, http.MethodPut, activityWritePath())
	if got := body["activityName"]; got != "Morning run" {
		t.Errorf("the write sent activityName = %v, want the requested name", got)
	}
}

func TestSetActivityNameRefusesAnEmptyName(t *testing.T) {
	h := newWriteHarness(t, writeScript(), enabledWrites())

	message := h.callError(t, tools.ToolSetActivityName, map[string]any{
		argActivityID:   testActivityID,
		argActivityName: "",
	})

	if len(h.fake.Requests()) != 0 {
		t.Errorf("an invalid argument still reached Garmin: %v", h.recordedMethods())
	}
	if !strings.Contains(message, "refused the arguments") {
		t.Errorf("the refusal %q does not say the arguments were refused", message)
	}
}

func TestSetActivityTypeResolvesTheWholeTripleFromGarminsCatalog(t *testing.T) {
	h := newWriteHarness(t, writeScript(), enabledWrites())

	h.call(t, tools.ToolSetActivityType, map[string]any{
		argActivityID: testActivityID,
		argTypeKey:    typeRunning,
	})

	body := h.bodyFor(t, http.MethodPut, activityWritePath())
	activityType, ok := body["activityTypeDTO"].(map[string]any)
	if !ok {
		t.Fatalf("the write sent no activityTypeDTO: %v", body)
	}
	if got := activityType["typeId"]; got != float64(1) {
		t.Errorf("typeId = %v, want the catalog's 1", got)
	}
	if got := activityType["parentTypeId"]; got != float64(17) {
		t.Errorf("parentTypeId = %v, want the catalog's 17", got)
	}
}

func TestSetActivityTypeRefusesATypeGarminDoesNotList(t *testing.T) {
	h := newWriteHarness(t, writeScript(), enabledWrites())

	h.callError(t, tools.ToolSetActivityType, map[string]any{
		argActivityID: testActivityID,
		argTypeKey:    "teleporting",
	})

	for _, recorded := range h.recordedMethods() {
		if strings.HasPrefix(recorded, http.MethodPut) {
			t.Errorf("an unknown type still reached a write: %v", h.recordedMethods())
		}
	}
}

func TestSetActivityFeelRefusesAValueGarminDoesNotStore(t *testing.T) {
	h := newWriteHarness(t, writeScript(), enabledWrites())

	h.callError(t, tools.ToolSetActivityFeel, map[string]any{
		argActivityID: testActivityID,
		argFeel:       60,
	})

	if len(h.fake.Requests()) != 0 {
		t.Errorf("an invalid feel still reached Garmin: %v", h.recordedMethods())
	}
}

func TestSetPerceivedEffortRefusesAnOutOfRangeRating(t *testing.T) {
	h := newWriteHarness(t, writeScript(), enabledWrites())

	h.callError(t, tools.ToolSetPerceivedEffort, map[string]any{
		argActivityID: testActivityID,
		"rpe":         11,
	})

	if len(h.fake.Requests()) != 0 {
		t.Errorf("an out-of-range rating still reached Garmin: %v", h.recordedMethods())
	}
}

func TestSetActivityEventTypeRefusesAnUnknownKey(t *testing.T) {
	h := newWriteHarness(t, writeScript(), enabledWrites())

	h.callError(t, tools.ToolSetActivityEventType, map[string]any{
		argActivityID: testActivityID,
		"event_type":  "brunch",
	})

	if len(h.fake.Requests()) != 0 {
		t.Errorf("an unknown event type still reached Garmin: %v", h.recordedMethods())
	}
}

func TestSetActivityDescriptionWritesTheNotes(t *testing.T) {
	h := newWriteHarness(t, writeScript(), enabledWrites())

	out := h.call(t, tools.ToolSetActivityDescription, map[string]any{
		argActivityID:  testActivityID,
		argDescription: "Felt strong throughout",
	})

	if got := out["updated"]; got != "description" {
		t.Errorf("updated = %v, want description", got)
	}
}

// The manifest documents an empty description as the way to clear the notes. The
// request layer refuses an empty write field, so the tool refuses it too rather than
// sending something Garmin would reject; the description says so.
func TestSetActivityDescriptionRefusesAnEmptyString(t *testing.T) {
	h := newWriteHarness(t, writeScript(), enabledWrites())

	h.callError(t, tools.ToolSetActivityDescription, map[string]any{
		argActivityID:  testActivityID,
		argDescription: "",
	})

	if len(h.fake.Requests()) != 0 {
		t.Errorf("an empty description still reached Garmin: %v", h.recordedMethods())
	}
}

func gearScript() testkit.Script {
	return testkit.NewScript().
		With(client.PathSocialProfile, repeat(okJSON(profileBody), 2)...).
		With(client.PathGearPrefix+"/link/"+testGearUUID+"/activity/"+testActivityID,
			okJSON(`{}`)).
		With(client.PathGearPrefix+"/unlink/"+testGearUUID+"/activity/"+testActivityID,
			okJSON(`{}`))
}

func TestGearToolsLinkAndUnlinkTheNamedGear(t *testing.T) {
	cases := map[string]struct {
		tool string
		want string
	}{
		tools.ToolAddGearToActivity:      {tools.ToolAddGearToActivity, "gear_linked"},
		tools.ToolRemoveGearFromActivity: {tools.ToolRemoveGearFromActivity, "gear_unlinked"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newWriteHarness(t, gearScript(), enabledWrites())

			out := h.call(t, tc.tool, map[string]any{
				argActivityID: 987654321,
				"gear_uuid":   testGearUUID,
			})
			if got := out["updated"]; got != tc.want {
				t.Errorf("updated = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGearToolsRefuseAGearIdentifierThatIsNotAUUID(t *testing.T) {
	h := newWriteHarness(t, gearScript(), enabledWrites())

	h.callError(t, tools.ToolAddGearToActivity, map[string]any{
		argActivityID: 987654321,
		"gear_uuid":   traversalAttempt,
	})

	if len(h.fake.Requests()) != 0 {
		t.Errorf("a traversal attempt still reached Garmin: %v", h.recordedMethods())
	}
}

func manualScript() testkit.Script {
	return testkit.NewScript().
		With(client.PathSocialProfile, okJSON(profileBody)).
		With(client.PathActivityPrefix, okJSON(`{"activityId":987654321}`))
}

func TestCreateManualActivityAppliesTheDeclaredDefaults(t *testing.T) {
	h := newWriteHarness(t, manualScript(), enabledWrites())

	out := h.call(t, tools.ToolCreateManualActivity, map[string]any{
		argTypeKey:         "yoga",
		argDate:            testCalendarDate,
		"duration_minutes": 45,
	})

	if got := out["activity_id"]; got != float64(987654321) {
		t.Errorf("activity_id = %v, want the identifier Garmin assigned", got)
	}
	body := h.bodyFor(t, http.MethodPost, client.PathActivityPrefix)
	summary, _ := body["summaryDTO"].(map[string]any)
	start, _ := summary["startTimeLocal"].(string)
	if !strings.HasSuffix(start, "09:00:00.000") {
		t.Errorf("startTimeLocal = %q, want the declared 09:00 default", start)
	}
}

func TestCreateManualActivityRefusesAnUnknownTimezone(t *testing.T) {
	h := newWriteHarness(t, manualScript(), enabledWrites())

	h.callError(t, tools.ToolCreateManualActivity, map[string]any{
		argTypeKey:         "yoga",
		argDate:            testCalendarDate,
		"duration_minutes": 45,
		"time_zone":        "Mars/Olympus_Mons",
	})

	if len(h.fake.Requests()) != 0 {
		t.Errorf("an unknown timezone still reached Garmin: %v", h.recordedMethods())
	}
}

func deleteScript() testkit.Script {
	return testkit.NewScript().
		With(client.PathSocialProfile, okJSON(profileBody)).
		With(activityWritePath(), testkit.JSON(http.StatusNoContent, ""))
}

func TestADestructiveToolIsRefusedWhenConfirmationIsUnavailable(t *testing.T) {
	opts := enabledWrites()
	opts.confirmer = refusingConfirmer{}
	h := newWriteHarness(t, deleteScript(), opts)

	message := h.callError(t, tools.ToolDeleteActivity, map[string]any{
		argActivityID: testActivityID,
	})

	if len(h.fake.Requests()) != 0 {
		t.Errorf("an unconfirmed delete still reached Garmin: %v", h.recordedMethods())
	}
	if message == "" {
		t.Error("the refusal carried no message")
	}
}

func TestADestructiveToolRunsOnceItIsConfirmed(t *testing.T) {
	h := newWriteHarness(t, deleteScript(), enabledWrites())

	out := h.call(t, tools.ToolDeleteActivity, map[string]any{argActivityID: testActivityID})

	if deleted, _ := out["deleted"].(bool); !deleted {
		t.Errorf("deleted = %v, want true", out["deleted"])
	}
	if got := h.recordedMethods(); len(got) == 0 ||
		!strings.HasPrefix(got[len(got)-1], http.MethodDelete) {
		t.Errorf("recorded %v, want a DELETE", got)
	}
}
