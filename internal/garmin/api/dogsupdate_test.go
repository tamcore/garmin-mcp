package api_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func TestDogsUpdateCarriesTheCurrentProfileOver(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(dogPath(),
		testkit.JSON(http.StatusOK, dogBody), testkit.JSON(http.StatusOK, dogBody),
		testkit.JSON(http.StatusOK, dogBody))
	h := newHarness(t, script, client.Limits{})
	neutered := true

	fields := api.DogFields{IsNeutered: &neutered}
	if _, err := newDogs(t, h).Update(t.Context(), h.session, testDogID, fields); err != nil {
		t.Fatalf("Update() = %v", err)
	}
	requests := h.server.Requests()
	if len(requests) != 3 || requests[1].Method != http.MethodPut {
		t.Fatalf("requests = %d, want GET, PUT, GET", len(requests))
	}
	body := decodeBody(t, requests[1].Body)
	if body["isNeutered"] != true || body[dogKeyName] != testDogName || body["nickname"] != "R" {
		t.Errorf("PUT body = %v, want the overlay on the current profile", body)
	}
	if body["primaryBreedId"] != float64(2) {
		t.Errorf("primaryBreedId = %v, want 2", body["primaryBreedId"])
	}
	if ids, ok := body["secondaryBreedIds"].([]any); !ok || len(ids) != 2 || ids[1] != float64(7) {
		t.Errorf("secondaryBreedIds = %v, want [5 7]", body["secondaryBreedIds"])
	}
	for _, key := range []string{"species", "status", "adminId", "primaryBreed"} {
		if _, present := body[key]; present {
			t.Errorf("%s was sent, want only editable fields", key)
		}
	}
}

func TestDogsUpdateRefusesABreedItCannotCarryOver(t *testing.T) {
	t.Parallel()

	for name, current := range map[string]string{
		"secondary": strings.Replace(dogBody, `[{"id":5},7]`, `[{"id":5},{"key":"X"}]`, 1),
		"primary":   strings.Replace(dogBody, `{"id":2,"name":"DOG_BREED_ENGLISH_POINTER"}`, `{"name":"X"}`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := testkit.NewScript().With(dogPath(), testkit.JSON(http.StatusOK, current))
			h := newHarness(t, script, client.Limits{})
			neutered := true

			_, err := newDogs(t, h).Update(t.Context(), h.session, testDogID, api.DogFields{IsNeutered: &neutered})
			if !errors.Is(err, client.ErrUnexpectedResponse) {
				t.Errorf("Update() = %v, want ErrUnexpectedResponse", err)
			}
			for _, r := range h.server.Requests() {
				if r.Method == http.MethodPut {
					t.Fatal("an update that would drop a breed reached Garmin")
				}
			}
		})
	}
}
