package tools_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

const gearV2Body = `{"uuid":"` + testGearUUID + `","gearType":"SHOES","brand":"Synth",` +
	`"model":"Runner","name":"Daily","firstUseDate":"2025-08-22","usageType":"DISTANCE",` +
	`"maxUsageDistanceMeters":800000,"maxUsageDurationSeconds":0,"notes":"",` +
	`"associatedActivityTypes":[{"activityTypeKey":"running","defaultGear":true,"preferredGear":false}]}`

const gearNote = "rotate"

func gearV2Path() string { return client.PathGearV2 + "/" + testGearUUID }

func TestCreateGearReportsTheCreatedItem(t *testing.T) {
	script := testkit.NewScript().
		With(client.PathSocialProfile, okJSON(profileBody)).
		With(client.PathGearV2, okJSON(gearV2Body))
	h := newWriteHarness(t, script, enabledWrites())

	out := h.call(t, tools.ToolCreateGear, map[string]any{
		"gear_type": "SHOES", "brand": "Synth", "model": "Runner", "name": "Daily",
		"first_use_date": "2025-08-22", "max_usage_distance_km": 800,
		"activity_type_keys": []string{"running"},
	})

	if out["uuid"] != testGearUUID || out["max_usage_distance_km"] != float64(800) {
		t.Errorf("create_gear = %v, want the created item in kilometers", out)
	}
	if types, _ := out["default_activity_types"].([]any); len(types) != 1 || types[0] != "running" {
		t.Errorf("default_activity_types = %v, want [running]", out["default_activity_types"])
	}
	body := h.bodyFor(t, http.MethodPost, client.PathGearV2)
	if body["maxUsageDistanceMeters"] != float64(800000) {
		t.Errorf("maxUsageDistanceMeters = %v, want 800000", body["maxUsageDistanceMeters"])
	}
}

func TestUpdateGearTakesTheCompactUUIDAndChangesOnlyTheGivenFields(t *testing.T) {
	updated := strings.Replace(gearV2Body, `"notes":""`, `"notes":"`+gearNote+`"`, 1)
	script := testkit.NewScript().
		With(client.PathSocialProfile, okJSON(profileBody)).
		With(gearV2Path(), okJSON(gearV2Body), testkit.Behavior{Status: http.StatusNoContent}, okJSON(updated))
	h := newWriteHarness(t, script, enabledWrites())

	out := h.call(t, tools.ToolUpdateGear, map[string]any{
		argGearUUID: strings.ReplaceAll(testGearUUID, "-", ""),
		"notes":     gearNote,
	})

	if out["notes"] != gearNote {
		t.Errorf("notes = %v, want the re-read note", out["notes"])
	}
	body := h.bodyFor(t, http.MethodPut, gearV2Path())
	if body["notes"] != gearNote || body["name"] != "Daily" {
		t.Errorf("PUT body = %v, want the note changed and the name kept", body)
	}
}

func TestUpdateGearRefusesACallThatChangesNothing(t *testing.T) {
	h := newWriteHarness(t, testkit.NewScript(), enabledWrites())

	h.callError(t, tools.ToolUpdateGear, map[string]any{argGearUUID: testGearUUID})

	if len(h.fake.Requests()) != 0 {
		t.Errorf("an empty update reached Garmin: %v", h.recordedMethods())
	}
}

func TestSetGearStatusAndDeleteGearReportTheOutcome(t *testing.T) {
	script := testkit.NewScript().
		With(client.PathSocialProfile, repeat(okJSON(profileBody), 2)...).
		With(gearV2Path()+"/status/RETIRED", testkit.Behavior{Status: http.StatusNoContent}).
		With(gearV2Path(), testkit.Behavior{Status: http.StatusNoContent})
	h := newWriteHarness(t, script, enabledWrites())

	retired := h.call(t, tools.ToolSetGearStatus, map[string]any{argGearUUID: testGearUUID, "status": "retired"})
	deleted := h.call(t, tools.ToolDeleteGear, map[string]any{argGearUUID: testGearUUID})

	if retired["outcome"] != "gear_retired" || deleted["outcome"] != "gear_deleted" {
		t.Errorf("outcomes = %v, %v", retired["outcome"], deleted["outcome"])
	}
}

func TestGearLifecycleToolsRefuseATraversalAttempt(t *testing.T) {
	h := newWriteHarness(t, testkit.NewScript(), enabledWrites())

	for _, name := range []string{tools.ToolUpdateGear, tools.ToolSetGearStatus, tools.ToolDeleteGear} {
		h.callError(t, name, map[string]any{argGearUUID: traversalAttempt, "status": "active", "notes": "x"})
	}

	if len(h.fake.Requests()) != 0 {
		t.Errorf("a traversal attempt reached Garmin: %v", h.recordedMethods())
	}
}
