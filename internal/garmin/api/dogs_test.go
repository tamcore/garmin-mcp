package api_test

import (
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

const (
	testDogID   = int64(4242)
	testDogName = "Rex"
	dogKeyName  = "name"
	dogBody     = `{"id":4242,"adminId":1,"name":"Rex","nickname":"R","species":"dog",` +
		`"primaryBreed":{"id":2,"name":"DOG_BREED_ENGLISH_POINTER"},"secondaryBreeds":[{"id":5},7],` +
		`"gender":"male","isNeutered":false,"weight":10.5,"birthdate":"2020-01-02",` +
		`"birthdatePrecision":"day","adoptionDate":null,"adoptionDatePrecision":null,` +
		`"status":"active","deviceId":null}`
)

func dogPath() string { return client.PathPets + "/4242" }

func newDogs(t *testing.T, h harness) *api.Dogs {
	t.Helper()

	dogs, err := api.NewDogs(h.rc)
	if err != nil {
		t.Fatalf("NewDogs() = %v", err)
	}
	return dogs
}

func TestDogsListSendsTheStatusFilter(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathPets, testkit.JSON(http.StatusOK, "["+dogBody+"]"))
	h := newHarness(t, script, client.Limits{})

	dogs, err := newDogs(t, h).List(t.Context(), h.session, api.DogStatusRetired)
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	if len(dogs) != 1 || dogs[0].Name == nil || *dogs[0].Name != testDogName {
		t.Fatalf("List() = %+v, want one dog", dogs)
	}
	if got := h.server.Requests()[0].Query.Get(client.QueryPetStatus); got != "retired" {
		t.Errorf("status = %q, want retired", got)
	}
	if id, ok := dogs[0].SecondaryBreeds[1].ID.Int64(); !ok || id != 7 {
		t.Errorf("bare secondary breed id = %v, want 7", dogs[0].SecondaryBreeds[1].ID)
	}
}

func TestDogsListRefusesAnUnknownStatus(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	if _, err := newDogs(t, h).List(t.Context(), h.session, "lost"); !errors.Is(err, client.ErrValidation) {
		t.Errorf("List() = %v, want ErrValidation", err)
	}
	if len(h.server.Requests()) != 0 {
		t.Error("a refused list reached Garmin")
	}
}

func TestDogsBreedsDecodesTheCatalog(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathDogBreeds,
		testkit.JSON(http.StatusOK, `{"1":"DOG_BREED_OTHER","2":"DOG_BREED_ENGLISH_POINTER"}`))
	h := newHarness(t, script, client.Limits{})

	breeds, err := newDogs(t, h).Breeds(t.Context(), h.session)
	if err != nil {
		t.Fatalf("Breeds() = %v", err)
	}
	if breeds["2"] != "DOG_BREED_ENGLISH_POINTER" || len(breeds) != 2 {
		t.Errorf("Breeds() = %v", breeds)
	}
}

func TestDogsCreatePostsNullsForAbsentFields(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathPets, testkit.JSON(http.StatusOK, dogBody))
	h := newHarness(t, script, client.Limits{})
	name, breed, weight := " Rex ", int64(2), 10.5
	birthdate := mustDate(t, "2020-01-02")

	created, err := newDogs(t, h).Create(t.Context(), h.session, api.DogFields{
		Name: &name, PrimaryBreedID: &breed, WeightKG: &weight, Birthdate: &birthdate,
	})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if id, _ := created.ID.Int64(); id != testDogID {
		t.Errorf("Create() id = %d, want %d", id, testDogID)
	}
	request := h.server.Requests()[0]
	if request.Method != http.MethodPost {
		t.Fatalf("method = %s, want POST", request.Method)
	}
	body := decodeBody(t, request.Body)
	want := map[string]any{
		dogKeyName: testDogName, "primaryBreedId": float64(2), "weight": 10.5,
		"birthdate": "2020-01-02", "birthdatePrecision": "day",
	}
	for key, value := range want {
		if body[key] != value {
			t.Errorf("%s = %v, want %v", key, body[key], value)
		}
	}
	for _, key := range []string{"secondaryBreedIds", "gender", "isNeutered"} {
		if value, present := body[key]; !present || value != nil {
			t.Errorf("%s = %v (present %t), want null", key, value, present)
		}
	}
}

func TestDogsRefuseInvalidFieldsBeforeAnyCall(t *testing.T) {
	t.Parallel()

	gender, zero, heavy, nan := "unknown", int64(0), 500.0, math.NaN()
	control := "a\x00b"
	tooMany := make([]int64, api.MaxDogSecondaryBreeds+1)
	cases := map[string]api.DogFields{
		"gender":       {Gender: &gender},
		"breed":        {PrimaryBreedID: &zero},
		"weight":       {WeightKG: &heavy},
		"nan weight":   {WeightKG: &nan},
		"control name": {Name: &control},
		"breeds":       {SecondaryBreedIDs: &tooMany},
	}
	for name, fields := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, testkit.NewScript(), client.Limits{})
			if _, err := newDogs(t, h).Update(t.Context(), h.session, testDogID, fields); !errors.Is(err, client.ErrValidation) {
				t.Errorf("Update() = %v, want ErrValidation", err)
			}
			if len(h.server.Requests()) != 0 {
				t.Error("a refused update reached Garmin")
			}
		})
	}
}

func TestDogsCreateRequiresAName(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	if _, err := newDogs(t, h).Create(t.Context(), h.session, api.DogFields{}); !errors.Is(err, client.ErrValidation) {
		t.Errorf("Create() = %v, want ErrValidation", err)
	}
}

func TestDogsSetStatusPutsTheStatusDocument(t *testing.T) {
	t.Parallel()

	path := dogPath() + "/" + client.PetStatusSegment
	script := testkit.NewScript().With(path, testkit.JSON(http.StatusNoContent, ""))
	h := newHarness(t, script, client.Limits{})

	result, err := newDogs(t, h).SetStatus(t.Context(), h.session, testDogID, api.DogStatusRetired)
	if err != nil {
		t.Fatalf("SetStatus() = %v", err)
	}
	if result.Status != http.StatusNoContent {
		t.Errorf("status = %d, want 204", result.Status)
	}
	request := h.server.Requests()[0]
	if request.Method != http.MethodPut || decodeBody(t, request.Body)["status"] != "retired" {
		t.Errorf("request = %s %s, want PUT {status: retired}", request.Method, request.Body)
	}
	_, err = newDogs(t, h).SetStatus(t.Context(), h.session, testDogID, "lost")
	if !errors.Is(err, client.ErrValidation) {
		t.Errorf("SetStatus(lost) = %v, want ErrValidation", err)
	}
}

func TestDogsDeleteSendsDelete(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(dogPath(), testkit.JSON(http.StatusNoContent, ""))
	h := newHarness(t, script, client.Limits{})

	if _, err := newDogs(t, h).Delete(t.Context(), h.session, testDogID); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if got := h.server.Requests()[0].Method; got != http.MethodDelete {
		t.Errorf("method = %s, want DELETE", got)
	}
	if _, err := newDogs(t, h).Delete(t.Context(), h.session, 0); !errors.Is(err, client.ErrValidation) {
		t.Errorf("Delete(0) = %v, want ErrValidation", err)
	}
}

func TestDogLogValueNamesNoReading(t *testing.T) {
	t.Parallel()

	name := "Secretdog"
	rendered := slog.AnyValue(api.Dog{Name: &name}).Resolve().String()
	if strings.Contains(rendered, name) {
		t.Errorf("LogValue() = %q, want no name", rendered)
	}
	stats := slog.AnyValue(api.DogStats{}).Resolve().String()
	if !strings.Contains(stats, "groups") {
		t.Errorf("DogStats LogValue() = %q, want the group count", stats)
	}
}
