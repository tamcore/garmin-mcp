package tools_test

import (
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

const (
	testDogBody = `{"id":4242,"name":"Rex","species":"dog","primaryBreed":{"id":2},` +
		`"secondaryBreeds":[{"id":5}],"gender":"male","isNeutered":true,"weight":10.5,` +
		`"birthdate":"2020-01-02","birthdatePrecision":"day","status":"active"}`
	argDogID      = "dog_id"
	argDogIDs     = "dog_ids"
	argDogStatus  = "status"
	testDogName   = "Rex"
	dogStatusGone = "retired"
)

func testDogPath() string { return client.PathPets + "/4242" }

func TestGetDogsMapsTheProfiles(t *testing.T) {
	script := testkit.NewScript().
		With(client.PathSocialProfile, okJSON(profileBody)).
		With(client.PathPets, okJSON("["+testDogBody+"]"))
	h := newWriteHarness(t, script, enabledWrites())

	out := h.call(t, tools.ToolGetDogs, map[string]any{argDogStatus: "active"})

	dogs, _ := out["dogs"].([]any)
	if len(dogs) != 1 {
		t.Fatalf("dogs = %v, want one", out["dogs"])
	}
	dog := dogs[0].(map[string]any)
	if dog["dog_id"] != float64(4242) || dog["primary_breed_id"] != float64(2) || dog["weight_kg"] != 10.5 {
		t.Errorf("dog = %v", dog)
	}
}

func TestGetDogBreedsSortsTheCatalog(t *testing.T) {
	script := testkit.NewScript().
		With(client.PathSocialProfile, okJSON(profileBody)).
		With(client.PathDogBreeds, okJSON(`{"10":"DOG_BREED_B","2":"DOG_BREED_A"}`))
	h := newWriteHarness(t, script, enabledWrites())

	breeds, _ := h.call(t, tools.ToolGetDogBreeds, map[string]any{})["breeds"].([]any)

	if len(breeds) != 2 || breeds[0].(map[string]any)["breed_id"] != float64(2) {
		t.Errorf("breeds = %v, want sorted by id", breeds)
	}
}

func TestGetDogActivityStatsConvertsUnits(t *testing.T) {
	body := `{"countOfActivities":2,"stats":{"all":{"distance":{"count":2,"sum":1000000.0},` +
		`"duration":{"count":2,"sum":7200000.0},"animals":{"animalProfileGroups":[` +
		`{"animalProfileId":4242,"numberOfActivities":2,"distance":{"sum":1000000.0}}]}}}}`
	script := testkit.NewScript().
		With(client.PathSocialProfile, okJSON(profileBody)).
		With(client.PathDogStatsLifetime, okJSON(body))
	h := newWriteHarness(t, script, enabledWrites())

	out := h.call(t, tools.ToolGetDogActivityStats, map[string]any{})

	total := out["total"].(map[string]any)
	if total["distance_meters"] != float64(10000) || total["duration_seconds"] != float64(7200) {
		t.Errorf("total = %v, want 10000 m and 7200 s", total)
	}
	dogs := out["dogs"].([]any)
	if len(dogs) != 1 || dogs[0].(map[string]any)["dog_id"] != float64(4242) {
		t.Errorf("dogs = %v", dogs)
	}
}

func TestCreateDogSendsTheProfile(t *testing.T) {
	script := testkit.NewScript().
		With(client.PathSocialProfile, okJSON(profileBody)).
		With(client.PathPets, okJSON(testDogBody))
	h := newWriteHarness(t, script, enabledWrites())

	out := h.call(t, tools.ToolCreateDog, map[string]any{
		"name": testDogName, "primary_breed_id": 2, "gender": "male", "birthdate": "2020-01-02",
	})

	if out["dog_id"] != float64(4242) {
		t.Errorf("create_dog = %v", out)
	}
	body := h.bodyFor(t, http.MethodPost, client.PathPets)
	if body["name"] != testDogName || body["primaryBreedId"] != float64(2) || body["birthdatePrecision"] != "day" {
		t.Errorf("POST body = %v", body)
	}
}

func TestUpdateDogRefusesACallThatChangesNothing(t *testing.T) {
	h := newWriteHarness(t, testkit.NewScript(), enabledWrites())

	h.callError(t, tools.ToolUpdateDog, map[string]any{argDogID: 4242})

	if len(h.fake.Requests()) != 0 {
		t.Errorf("an empty update reached Garmin: %v", h.recordedMethods())
	}
}

func TestUpdateDogKeepsTheOtherFields(t *testing.T) {
	script := testkit.NewScript().
		With(client.PathSocialProfile, okJSON(profileBody)).
		With(testDogPath(), okJSON(testDogBody), testkit.Behavior{Status: http.StatusNoContent}, okJSON(testDogBody))
	h := newWriteHarness(t, script, enabledWrites())

	h.call(t, tools.ToolUpdateDog, map[string]any{argDogID: 4242, "weight_kg": 11})

	body := h.bodyFor(t, http.MethodPut, testDogPath())
	if body["weight"] != float64(11) || body["name"] != testDogName || body["primaryBreedId"] != float64(2) {
		t.Errorf("PUT body = %v, want the weight changed and the rest kept", body)
	}
}

func TestSetDogStatusAndDeleteDogReportTheOutcome(t *testing.T) {
	script := testkit.NewScript().
		With(client.PathSocialProfile, repeat(okJSON(profileBody), 2)...).
		With(testDogPath()+"/status", testkit.Behavior{Status: http.StatusNoContent}).
		With(testDogPath(), testkit.Behavior{Status: http.StatusNoContent})
	h := newWriteHarness(t, script, enabledWrites())

	retired := h.call(t, tools.ToolSetDogStatus, map[string]any{argDogID: 4242, argDogStatus: dogStatusGone})
	deleted := h.call(t, tools.ToolDeleteDog, map[string]any{argDogID: 4242})

	if retired["outcome"] != "dog_retired" || deleted["outcome"] != "dog_deleted" {
		t.Errorf("outcomes = %v, %v", retired["outcome"], deleted["outcome"])
	}
	if body := h.bodyFor(t, http.MethodPut, testDogPath()+"/status"); body[argDogStatus] != dogStatusGone {
		t.Errorf("status body = %v", body)
	}
}

func TestSetActivityDogsWritesTheMetadata(t *testing.T) {
	h := newWriteHarness(t, writeScript(), enabledWrites())

	out := h.call(t, tools.ToolSetActivityDogs, map[string]any{
		argActivityID: testActivityID, argDogIDs: []int64{4242},
	})

	if out["updated"] != "dog_ids" {
		t.Errorf("updated = %v, want dog_ids", out["updated"])
	}
	metadata, _ := h.bodyFor(t, http.MethodPut, activityWritePath())["metadataDTO"].(map[string]any)
	if metadata["hasDog"] != true {
		t.Errorf("metadataDTO = %v, want hasDog true", metadata)
	}
}

func TestSetActivityDogsRefusesANonPositiveDog(t *testing.T) {
	h := newWriteHarness(t, testkit.NewScript(), enabledWrites())

	h.callError(t, tools.ToolSetActivityDogs, map[string]any{
		argActivityID: testActivityID, argDogIDs: []int64{0},
	})

	if len(h.fake.Requests()) != 0 {
		t.Errorf("a refused write reached Garmin: %v", h.recordedMethods())
	}
}
