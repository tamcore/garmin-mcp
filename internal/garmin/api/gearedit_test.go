package api_test

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

const gearDetailBody = `{"uuid":"` + testGearUUID + `","gearType":"SHOES","brand":"Synth",` +
	`"model":"Runner","name":"Old","firstUseDate":"2025-01-01","usageType":"DISTANCE",` +
	`"maxUsageDistanceMeters":0,"maxUsageDurationSeconds":0,"notes":"",` +
	`"associatedActivityTypes":[],"gearPk":4400,"unknownField":{"kept":true}}`

const (
	gearNameDaily = "Daily"
	gearNameNew   = "New"
	gearKeyNotes  = "notes"
)

func gearV2Path() string { return client.PathGearV2 + "/" + testGearUUID }

func TestGearUUIDHyphenatesTheCompactForm(t *testing.T) {
	t.Parallel()

	gear, err := api.ParseGearUUID("1f3c9d2a000040008000abcdef012345")
	if err != nil {
		t.Fatalf("ParseGearUUID() = %v", err)
	}
	if got := gear.Hyphenated(); got != testGearUUID {
		t.Errorf("Hyphenated() = %q, want %q", got, testGearUUID)
	}
	if got := mustGearUUID(t).Hyphenated(); got != testGearUUID {
		t.Errorf("Hyphenated() of the hyphenated form = %q, want it unchanged", got)
	}
}

func TestGearCreatePostsTheFullV2Object(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathGearV2, testkit.JSON(http.StatusOK, gearDetailBody))
	h := newHarness(t, script, client.Limits{})
	shoes, brand, model, name := "shoes", " Synth ", "Runner", gearNameDaily
	keys := []string{"running"}
	meters := int64(800000)
	firstUse := mustDate(t, "2025-08-22")

	created, err := newGear(t, h).Create(t.Context(), h.session, api.GearFields{
		GearType: &shoes, Brand: &brand, Model: &model, Name: &name,
		FirstUseDate: &firstUse, MaxDistanceMeters: &meters, ActivityTypeKeys: &keys,
	})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if created.UUID == nil || *created.UUID != testGearUUID {
		t.Errorf("Create() uuid = %v, want the identifier Garmin assigned", created.UUID)
	}

	request := h.server.Requests()[0]
	if request.Method != http.MethodPost || request.Path != client.PathGearV2 {
		t.Fatalf("request = %s %s, want POST %s", request.Method, request.Path, client.PathGearV2)
	}
	body := decodeBody(t, request.Body)
	want := map[string]any{
		"gearType": "SHOES", "brand": "Synth", "model": "Runner", "name": gearNameDaily,
		"firstUseDate": "2025-08-22", "usageType": "DISTANCE",
		"maxUsageDistanceMeters": float64(800000), "maxUsageDurationSeconds": float64(0),
	}
	for key, value := range want {
		if body[key] != value {
			t.Errorf("%s = %v, want %v", key, body[key], value)
		}
	}
	for _, key := range []string{"uuid", gearKeyNotes} {
		if value, present := body[key]; !present || value != nil {
			t.Errorf("%s = %v, want an explicit null", key, value)
		}
	}
	types, _ := body["associatedActivityTypes"].([]any)
	if len(types) != 1 {
		t.Fatalf("associatedActivityTypes = %v, want one entry", body["associatedActivityTypes"])
	}
	entry, _ := types[0].(map[string]any)
	if entry["activityTypeKey"] != "running" || entry["defaultGear"] != true || entry["preferredGear"] != false {
		t.Errorf("activity type entry = %v", entry)
	}
}

func TestGearCreateRefusesMissingRequiredFields(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	name := "Daily"

	_, err := newGear(t, h).Create(t.Context(), h.session, api.GearFields{Name: &name})
	if !errors.Is(err, client.ErrValidation) {
		t.Errorf("Create() = %v, want ErrValidation", err)
	}
	if len(h.server.Requests()) != 0 {
		t.Error("an incomplete create reached Garmin")
	}
}

func TestGearUpdateSendsTheMergedObjectAndRereads(t *testing.T) {
	t.Parallel()

	updated := strings.Replace(gearDetailBody, `"name":"Old"`, `"name":"New"`, 1)
	script := testkit.NewScript().With(gearV2Path(),
		testkit.JSON(http.StatusOK, gearDetailBody),
		testkit.Behavior{Status: http.StatusNoContent},
		testkit.JSON(http.StatusOK, updated))
	h := newHarness(t, script, client.Limits{})
	name, notes := gearNameNew, "rotate weekly"
	keys := []string{}

	detail, err := newGear(t, h).Update(t.Context(), h.session, mustGearUUID(t), api.GearFields{
		Name: &name, Notes: &notes, ActivityTypeKeys: &keys,
	})
	if err != nil {
		t.Fatalf("Update() = %v", err)
	}
	if detail.Name == nil || *detail.Name != gearNameNew {
		t.Errorf("Update() name = %v, want the re-read value", detail.Name)
	}

	requests := h.server.Requests()
	if len(requests) != 3 || requests[1].Method != http.MethodPut {
		t.Fatalf("requests = %d, want GET, PUT, GET", len(requests))
	}
	body := decodeBody(t, requests[1].Body)
	if body["name"] != gearNameNew || body[gearKeyNotes] != "rotate weekly" || body["brand"] != "Synth" {
		t.Errorf("PUT body = %v, want the overlay on the current object", body)
	}
	if _, kept := body["unknownField"]; !kept {
		t.Error("an unnamed field was dropped from the PUT body")
	}
	if types, ok := body["associatedActivityTypes"].([]any); !ok || len(types) != 0 {
		t.Errorf("associatedActivityTypes = %v, want an empty list", body["associatedActivityTypes"])
	}
}

func TestGearUpdateRefusesInvalidFieldsBeforeAnyCall(t *testing.T) {
	t.Parallel()

	bad := "shoes-2"
	negative := int64(-1)
	control := "a\x00b"
	upper := []string{"Running"}
	cases := map[string]api.GearFields{
		"gear type":     {GearType: &bad},
		"threshold":     {MaxDistanceMeters: &negative},
		"notes":         {Notes: &control},
		"activity type": {ActivityTypeKeys: &upper},
		"name":          {Name: &control},
	}
	for name, fields := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, testkit.NewScript(), client.Limits{})
			_, err := newGear(t, h).Update(t.Context(), h.session, mustGearUUID(t), fields)
			if !errors.Is(err, client.ErrValidation) {
				t.Errorf("Update() = %v, want ErrValidation", err)
			}
			if len(h.server.Requests()) != 0 {
				t.Error("an invalid update reached Garmin")
			}
		})
	}
}

func TestGearSetStatusAndDeleteTargetTheirPaths(t *testing.T) {
	t.Parallel()

	statusPath := gearV2Path() + "/status/RETIRED"
	script := testkit.NewScript().
		With(statusPath, testkit.Behavior{Status: http.StatusNoContent}).
		With(gearV2Path(), testkit.Behavior{Status: http.StatusNoContent})
	h := newHarness(t, script, client.Limits{})
	gear := newGear(t, h)

	if _, err := gear.SetStatus(t.Context(), h.session, mustGearUUID(t), api.GearStatusRetired); err != nil {
		t.Fatalf("SetStatus() = %v", err)
	}
	if _, err := gear.Delete(t.Context(), h.session, mustGearUUID(t)); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if _, err := gear.SetStatus(t.Context(), h.session, mustGearUUID(t), "BROKEN"); !errors.Is(
		err, client.ErrValidation) {
		t.Errorf("SetStatus(BROKEN) = %v, want ErrValidation", err)
	}

	requests := h.server.Requests()
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	if requests[0].Method != http.MethodPut || requests[0].Path != statusPath {
		t.Errorf("status request = %s %s", requests[0].Method, requests[0].Path)
	}
	if requests[1].Method != http.MethodDelete || requests[1].Path != gearV2Path() {
		t.Errorf("delete request = %s %s", requests[1].Method, requests[1].Path)
	}
}

func TestGearUsageConversionsRoundAndRefuse(t *testing.T) {
	t.Parallel()

	if got, err := api.KilometersToMeters(800.4); err != nil || got != 800400 {
		t.Errorf("KilometersToMeters(800.4) = %d, %v", got, err)
	}
	if got, err := api.KilometersToMeters(0); err != nil || got != 0 {
		t.Errorf("KilometersToMeters(0) = %d, %v, want 0 for no threshold", got, err)
	}
	if got, err := api.MinutesToSeconds(1.5); err != nil || got != 90 {
		t.Errorf("MinutesToSeconds(1.5) = %d, %v", got, err)
	}
	for _, value := range []float64{-1, 0.0001, 1e12} {
		if _, err := api.KilometersToMeters(value); !errors.Is(err, client.ErrValidation) {
			t.Errorf("KilometersToMeters(%v) = %v, want ErrValidation", value, err)
		}
	}
}

func TestGearDetailIsNotLoggable(t *testing.T) {
	t.Parallel()

	name := sentinelGearName
	var logged strings.Builder
	slog.New(slog.NewTextHandler(&logged, leakLogOptions())).Info("garmin read",
		"model", api.GearDetail{Name: &name, Notes: &name, Brand: &name})
	if strings.Contains(logged.String(), sentinelGearName) {
		t.Errorf("logging GearDetail leaks the name: %s", logged.String())
	}
}
