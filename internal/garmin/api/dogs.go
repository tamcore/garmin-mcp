package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"net/http"
	"net/url"
	"strconv"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// Dog profiles.
//
// Source: the Garmin Connect web app (connect.garmin.com 5.31.2.3): the profile
// client in its family-profile module and the dog form's payload builder,
// cross-checked against captured browser traffic for the list, the read, the
// breed catalog, the create and the status change.

// DogStatus is a dog profile lifecycle state.
type DogStatus string

// The two dog states Garmin accepts.
const (
	DogStatusActive  DogStatus = "active"
	DogStatusRetired DogStatus = "retired"
)

// Bounds on the caller-supplied dog fields.
const (
	// MaxDogNameLen bounds a dog name.
	MaxDogNameLen = 64
	// MaxDogSecondaryBreeds bounds the secondary breed list.
	MaxDogSecondaryBreeds = 16
	// MaxDogWeightKG bounds a dog weight.
	MaxDogWeightKG = 200
	// birthdatePrecisionDay marks a full birth date.
	birthdatePrecisionDay = "day"
)

// Dogs reads and writes dog profiles.
type Dogs struct {
	req requester
}

// NewDogs returns a dog client over the request layer.
func NewDogs(rc *client.Client) (*Dogs, error) {
	req, err := newRequester(rc)
	if err != nil {
		return nil, err
	}
	return &Dogs{req: req}, nil
}

// BreedRef is a breed reference. Garmin returns an object with an id; the web app
// also accepts a bare id, so both decode.
type BreedRef struct {
	ID client.Number
}

// UnmarshalJSON accepts {"id": n} or n.
func (b *BreedRef) UnmarshalJSON(data []byte) error {
	var object struct {
		ID client.Number `json:"id"`
	}
	if err := json.Unmarshal(data, &object); err == nil {
		b.ID = object.ID
		return nil
	}
	return json.Unmarshal(data, &b.ID)
}

// Dog is one dog profile. It is account material: returned to the authorized
// caller, never logged.
type Dog struct {
	ID                 client.Number `json:"id"`
	Name               *string       `json:"name"`
	PrimaryBreed       *BreedRef     `json:"primaryBreed"`
	SecondaryBreeds    []BreedRef    `json:"secondaryBreeds"`
	Gender             *string       `json:"gender"`
	IsNeutered         *bool         `json:"isNeutered"`
	WeightKG           client.Number `json:"weight"`
	Birthdate          *string       `json:"birthdate"`
	BirthdatePrecision *string       `json:"birthdatePrecision"`
	Status             *string       `json:"status"`
}

// LogValue reports which fields arrived, never a name or a reading.
func (d Dog) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "dog"),
		slog.String("id", presence(d.ID.IsSet())),
		slog.String("name", presence(d.Name != nil)),
	)
}

// DogFields are the editable fields of a dog profile. A nil field is absent:
// create sends null for it, update keeps the current value.
type DogFields struct {
	Name              *string
	PrimaryBreedID    *int64
	SecondaryBreedIDs *[]int64
	Gender            *string
	IsNeutered        *bool
	WeightKG          *float64
	Birthdate         *client.Date
}

// overlay validates the fields and renders each present one under its wire key.
func (f DogFields) overlay(req client.Request) (map[string]any, error) {
	out := map[string]any{}
	if f.Name != nil {
		name, err := requireBoundedText(req, *f.Name, "name", MaxDogNameLen)
		if err != nil {
			return nil, err
		}
		out["name"] = name
	}
	if f.PrimaryBreedID != nil {
		if *f.PrimaryBreedID <= 0 {
			return nil, invalid(req, fmt.Errorf("%w: primary breed id must be positive", client.ErrValidation))
		}
		out["primaryBreedId"] = *f.PrimaryBreedID
	}
	if f.SecondaryBreedIDs != nil {
		ids, err := secondaryBreedIDs(req, *f.SecondaryBreedIDs)
		if err != nil {
			return nil, err
		}
		out["secondaryBreedIds"] = ids
	}
	return f.overlayRest(req, out)
}

// overlayRest renders the gender, neuter state, weight and birth date into out,
// which overlay owns.
func (f DogFields) overlayRest(req client.Request, out map[string]any) (map[string]any, error) {
	if f.Gender != nil {
		if *f.Gender != "male" && *f.Gender != "female" {
			return nil, invalid(req, fmt.Errorf("%w: gender must be male or female", client.ErrValidation))
		}
		out["gender"] = *f.Gender
	}
	if f.IsNeutered != nil {
		out["isNeutered"] = *f.IsNeutered
	}
	if f.WeightKG != nil {
		w := *f.WeightKG
		if math.IsNaN(w) || w <= 0 || w > MaxDogWeightKG {
			return nil, invalid(req, fmt.Errorf("%w: weight is out of range", client.ErrValidation))
		}
		out["weight"] = w
	}
	if f.Birthdate != nil {
		out["birthdate"] = f.Birthdate.String()
		out["birthdatePrecision"] = birthdatePrecisionDay
	}
	return out, nil
}

// secondaryBreedIDs renders the list; an empty list clears it and is sent as
// null, as the web app does.
func secondaryBreedIDs(req client.Request, ids []int64) (any, error) {
	if len(ids) > MaxDogSecondaryBreeds {
		return nil, invalid(req, fmt.Errorf("%w: too many secondary breeds", client.ErrValidation))
	}
	for _, id := range ids {
		if id <= 0 {
			return nil, invalid(req, fmt.Errorf("%w: breed ids must be positive", client.ErrValidation))
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return ids, nil
}

// dogPath is the path of one dog profile.
func dogPath(id int64) string {
	return client.PathPets + "/" + strconv.FormatInt(id, 10)
}

func gcxRead(op client.Op, endpoint client.Endpoint, path string, query url.Values) client.Request {
	req := readRequest(op, endpoint, path, query)
	req.GCX = true
	return req
}

func gcxWrite(op client.Op, endpoint client.Endpoint, method, path string, effect client.Effect) client.Request {
	req := writeRequest(op, endpoint, method, path, effect)
	req.GCX = true
	return req
}

func requireDogID(req client.Request, id int64) error {
	if id <= 0 {
		return invalid(req, fmt.Errorf("%w: a dog id is required", client.ErrValidation))
	}
	return nil
}

// List reads the dog profiles; a non-empty status keeps only that state.
func (d *Dogs) List(ctx context.Context, session client.Session, status DogStatus) ([]Dog, error) {
	query := url.Values{}
	if status != "" {
		query.Set(client.QueryPetStatus, string(status))
	}
	req := gcxRead(client.OpGetDogs, client.EndpointPets, client.PathPets, query)
	if status != "" && status != DogStatusActive && status != DogStatusRetired {
		return nil, invalid(req, fmt.Errorf("%w: status must be active or retired", client.ErrValidation))
	}
	var dogs []Dog
	if _, err := d.req.read(ctx, session, req, &dogs); err != nil {
		return nil, err
	}
	return dogs, nil
}

// Get reads one dog profile.
func (d *Dogs) Get(ctx context.Context, session client.Session, id int64) (Dog, error) {
	var dog Dog
	if err := d.readDog(ctx, session, id, &dog); err != nil {
		return Dog{}, err
	}
	return dog, nil
}

func (d *Dogs) readDog(ctx context.Context, session client.Session, id int64, out any) error {
	req := gcxRead(client.OpGetDog, client.EndpointPets, dogPath(id), nil)
	if err := requireDogID(req, id); err != nil {
		return err
	}
	_, err := d.req.read(ctx, session, req, out)
	return err
}

// Breeds reads the breed catalog: breed id to breed key.
func (d *Dogs) Breeds(ctx context.Context, session client.Session) (map[string]string, error) {
	req := gcxRead(client.OpGetDogBreeds, client.EndpointDogBreeds, client.PathDogBreeds, nil)
	var breeds map[string]string
	if _, err := d.req.read(ctx, session, req, &breeds); err != nil {
		return nil, err
	}
	return breeds, nil
}

// Create creates a dog profile and returns it. The name is required.
//
// It is EffectUnsafeWrite: each POST creates a new profile.
func (d *Dogs) Create(ctx context.Context, session client.Session, fields DogFields) (Dog, error) {
	req := gcxWrite(client.OpCreateDog, client.EndpointPets, http.MethodPost, client.PathPets,
		client.EffectUnsafeWrite)
	if fields.Name == nil {
		return Dog{}, invalid(req, fmt.Errorf("%w: a name is required", client.ErrValidation))
	}
	set, err := fields.overlay(req)
	if err != nil {
		return Dog{}, err
	}
	payload := map[string]any{
		"primaryBreedId": nil, "secondaryBreedIds": nil, "gender": nil, "isNeutered": nil,
		"weight": nil, "birthdate": nil, "birthdatePrecision": nil,
	}
	maps.Copy(payload, set)
	if req.Body, err = jsonBody(req, payload); err != nil {
		return Dog{}, err
	}
	var created Dog
	if _, err := d.req.write(ctx, session, req, &created); err != nil {
		return Dog{}, err
	}
	return created, nil
}

// Update changes the present fields of one dog profile and returns the profile as
// Garmin then reports it.
//
// The web app sends the whole editable set, so the current profile is read, its
// editable fields are carried over and the changes are overlaid.
func (d *Dogs) Update(ctx context.Context, session client.Session, id int64, fields DogFields) (Dog, error) {
	req := gcxWrite(client.OpUpdateDog, client.EndpointPets, http.MethodPut, dogPath(id),
		client.EffectIdempotentWrite)
	if err := requireDogID(req, id); err != nil {
		return Dog{}, err
	}
	set, err := fields.overlay(req)
	if err != nil {
		return Dog{}, err
	}
	var current map[string]json.RawMessage
	if err := d.readDog(ctx, session, id, &current); err != nil {
		return Dog{}, err
	}
	if len(current) == 0 {
		return Dog{}, invalid(req, fmt.Errorf("%w: dog not found", client.ErrNotFound))
	}
	payload, err := editablePayload(req, current)
	if err != nil {
		return Dog{}, err
	}
	maps.Copy(payload, set)
	if req.Body, err = jsonBody(req, payload); err != nil {
		return Dog{}, err
	}
	if _, err := d.req.write(ctx, session, req, nil); err != nil {
		return Dog{}, err
	}
	return d.Get(ctx, session, id)
}

// SetStatus retires or re-activates one dog profile.
func (d *Dogs) SetStatus(
	ctx context.Context, session client.Session, id int64, status DogStatus,
) (WriteResult, error) {
	req := gcxWrite(client.OpSetDogStatus, client.EndpointPetStatus, http.MethodPut,
		dogPath(id)+"/"+client.PetStatusSegment, client.EffectIdempotentWrite)
	if err := requireDogID(req, id); err != nil {
		return WriteResult{}, err
	}
	if status != DogStatusActive && status != DogStatusRetired {
		return WriteResult{}, invalid(req, fmt.Errorf("%w: status must be active or retired", client.ErrValidation))
	}
	body, err := jsonBody(req, map[string]string{"status": string(status)})
	if err != nil {
		return WriteResult{}, err
	}
	req.Body = body
	payload, err := d.req.write(ctx, session, req, nil)
	if err != nil {
		return WriteResult{}, err
	}
	return newWriteResult(payload), nil
}

// Delete removes one dog profile permanently.
func (d *Dogs) Delete(ctx context.Context, session client.Session, id int64) (WriteResult, error) {
	req := gcxWrite(client.OpDeleteDog, client.EndpointPets, http.MethodDelete, dogPath(id), client.EffectDelete)
	if err := requireDogID(req, id); err != nil {
		return WriteResult{}, err
	}
	payload, err := d.req.write(ctx, session, req, nil)
	if err != nil {
		return WriteResult{}, err
	}
	return newWriteResult(payload), nil
}
