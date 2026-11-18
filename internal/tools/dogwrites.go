package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// The dog write tools. Source: the Garmin Connect web app (connect.garmin.com
// 5.31.2.3).
const (
	ToolCreateDog       = "create_dog"
	ToolUpdateDog       = "update_dog"
	ToolSetDogStatus    = "set_dog_status"
	ToolDeleteDog       = "delete_dog"
	ToolSetActivityDogs = "set_activity_dogs"
)

const (
	argDogID     = "dog_id"
	maxDogBreeds = 1_000_000
)

// dogFieldsInput is the optional field set create_dog and update_dog share.
type dogFieldsInput struct {
	Name              *string  `json:"name,omitempty" jsonschema:"the dog's name"`
	PrimaryBreedID    *int64   `json:"primary_breed_id,omitempty" jsonschema:"the primary breed"`
	SecondaryBreedIDs *[]int64 `json:"secondary_breed_ids,omitempty" jsonschema:"other breeds"`
	Gender            *string  `json:"gender,omitempty" jsonschema:"male or female"`
	IsNeutered        *bool    `json:"is_neutered,omitempty" jsonschema:"whether the dog is neutered"`
	WeightKG          *float64 `json:"weight_kg,omitempty" jsonschema:"the weight in kilograms"`
	Birthdate         *string  `json:"birthdate,omitempty" jsonschema:"the birth date"`
}

func (in dogFieldsInput) toFields() (api.DogFields, error) {
	fields := api.DogFields{
		Name: in.Name, PrimaryBreedID: in.PrimaryBreedID, SecondaryBreedIDs: in.SecondaryBreedIDs,
		Gender: in.Gender, IsNeutered: in.IsNeutered, WeightKG: in.WeightKG,
	}
	if in.Birthdate != nil {
		date, err := parseCalendarDate("birthdate", *in.Birthdate)
		if err != nil {
			return api.DogFields{}, err
		}
		fields.Birthdate = &date
	}
	return fields, nil
}

func dogFieldProperties(nameRequired bool) []Property {
	birthdate := dateProperty("birthdate", "the birth date")
	birthdate.Required = false
	return []Property{
		{
			Name: "name", Types: []string{typeString}, Description: "the dog's name",
			MaxLength: new(api.MaxDogNameLen), Required: nameRequired,
		},
		{
			Name: "primary_breed_id", Types: []string{typeInteger},
			Description: "the primary breed identifier from get_dog_breeds",
			Minimum:     bound(1), Maximum: bound(maxDogBreeds),
		},
		{
			Name: "secondary_breed_ids", Types: []string{typeArray},
			Description: "other breed identifiers from get_dog_breeds. It replaces the current " +
				"list; an empty list clears it",
			MaxItems: new(api.MaxDogSecondaryBreeds),
			Items:    map[string]any{keyType: typeInteger, keyMinimum: 1, keyMaximum: maxDogBreeds},
		},
		{
			Name: "gender", Types: []string{typeString}, Description: "the dog's sex",
			Enum: []any{"male", "female"},
		},
		{Name: "is_neutered", Types: []string{typeBoolean}, Description: "whether the dog is neutered"},
		{
			Name: "weight_kg", Types: []string{typeNumber}, Description: "the weight in kilograms",
			Minimum: bound(0.1), Maximum: bound(api.MaxDogWeightKG),
		},
		birthdate,
	}
}

func dogIDProperty(action string) Property {
	return Property{
		Name: argDogID, Types: []string{typeInteger},
		Description: "the dog profile identifier to " + action + ", from get_dogs",
		Minimum:     bound(1), Required: true,
	}
}

func parseDogID(id int64) (int64, error) {
	if id <= 0 {
		return 0, invalidArgument("dog_id must be a positive integer")
	}
	return id, nil
}

type createDogInput struct {
	dogFieldsInput
}

func createDogContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolCreateDog,
			Title: "Create a dog",
			Description: "add a dog profile to the account, so activities can record the dog. " +
				"Only the name is required. Creates a new profile every time it is called: " +
				"read get_dogs first to avoid a duplicate",
			Tier:        policy.TierWrite,
			Category:    categoryProfile,
			Annotations: writeAnnotations(false),
		},
		Schema: NewSchema(dogFieldProperties(true)...),
	}
}

func registerCreateDog(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in createDogInput) (
		*mcp.CallToolResult, DogRecord, error,
	) {
		fields, err := in.toFields()
		if err != nil {
			return nil, DogRecord{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, DogRecord{}, err
		}
		created, err := svc.dogs.Create(ctx, session, fields)
		if err != nil {
			return nil, DogRecord{}, fail(err)
		}
		return nil, newDogRecord(created), nil
	}
	return mcpserver.AddTool(registry, createDogContract().Registration(), handler)
}

type updateDogInput struct {
	DogID int64 `json:"dog_id" jsonschema:"the dog profile identifier"`
	dogFieldsInput
}

func updateDogContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolUpdateDog,
			Title: "Update a dog",
			Description: "change the given fields of one dog profile; an omitted field keeps " +
				"its value. A retired dog must be made active with set_dog_status first",
			Tier:        policy.TierWrite,
			Category:    categoryProfile,
			Annotations: writeAnnotations(true),
		},
		Schema: NewSchema(append([]Property{dogIDProperty("update")}, dogFieldProperties(false)...)...),
	}
}

// parse validates the identifier and requires at least one change.
func (in updateDogInput) parse() (int64, api.DogFields, error) {
	id, err := parseDogID(in.DogID)
	if err != nil {
		return 0, api.DogFields{}, err
	}
	fields, err := in.toFields()
	if err != nil {
		return 0, api.DogFields{}, err
	}
	if fields == (api.DogFields{}) {
		return 0, api.DogFields{}, invalidArgument("at least one field to change is required")
	}
	return id, fields, nil
}

func registerUpdateDog(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in updateDogInput) (
		*mcp.CallToolResult, DogRecord, error,
	) {
		id, fields, err := in.parse()
		if err != nil {
			return nil, DogRecord{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, DogRecord{}, err
		}
		updated, err := svc.dogs.Update(ctx, session, id, fields)
		if err != nil {
			return nil, DogRecord{}, fail(err)
		}
		return nil, newDogRecord(updated), nil
	}
	return mcpserver.AddTool(registry, updateDogContract().Registration(), handler)
}

// DogChange reports a status change or a removal.
type DogChange struct {
	DogID   int64  `json:"dog_id" jsonschema:"the dog profile identifier"`
	Outcome string `json:"outcome" jsonschema:"what happened, for example dog_retired"`
	Status  int    `json:"status" jsonschema:"the HTTP status Garmin answered with"`
}

// LogValue reports the outcome, never which dog it named.
func (c DogChange) LogValue() slog.Value {
	return shape("dogChange", slog.String("outcome", c.Outcome), slog.Int("status", c.Status))
}

type setDogStatusInput struct {
	DogID  int64  `json:"dog_id" jsonschema:"the dog profile identifier"`
	Status string `json:"status" jsonschema:"active or retired"`
}

func setDogStatusContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolSetDogStatus,
			Title: "Retire or reactivate a dog",
			Description: "retire one dog profile or make it active again. A retired dog keeps " +
				"its history but cannot be added to new activities",
			Tier:        policy.TierWrite,
			Category:    categoryProfile,
			Annotations: writeAnnotations(true),
		},
		Schema: NewSchema(dogIDProperty("change"), dogStatusProperty("the new dog status", true)),
	}
}

func registerSetDogStatus(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in setDogStatusInput) (
		*mcp.CallToolResult, DogChange, error,
	) {
		id, err := parseDogID(in.DogID)
		if err != nil {
			return nil, DogChange{}, err
		}
		status, err := parseDogStatus(in.Status)
		if err != nil {
			return nil, DogChange{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, DogChange{}, err
		}
		result, err := svc.dogs.SetStatus(ctx, session, id, status)
		if err != nil {
			return nil, DogChange{}, fail(err)
		}
		outcome := "dog_activated"
		if status == api.DogStatusRetired {
			outcome = "dog_retired"
		}
		return nil, DogChange{DogID: id, Outcome: outcome, Status: result.Status}, nil
	}
	return mcpserver.AddTool(registry, setDogStatusContract().Registration(), handler)
}

type deleteDogInput struct {
	DogID int64 `json:"dog_id" jsonschema:"the dog profile identifier"`
}

func deleteDogContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolDeleteDog,
			Title: "Delete a dog",
			Description: "permanently remove one dog profile. It cannot be undone and it " +
				"requires confirmation. To keep the dog's history, retire it with set_dog_status",
			Tier:        policy.TierDestructive,
			Category:    categoryProfile,
			Annotations: destructiveAnnotations(),
		},
		Schema: NewSchema(dogIDProperty("delete")),
	}
}

func registerDeleteDog(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in deleteDogInput) (
		*mcp.CallToolResult, DogChange, error,
	) {
		id, err := parseDogID(in.DogID)
		if err != nil {
			return nil, DogChange{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, DogChange{}, err
		}
		result, err := svc.dogs.Delete(ctx, session, id)
		if err != nil {
			return nil, DogChange{}, fail(err)
		}
		return nil, DogChange{DogID: id, Outcome: "dog_deleted", Status: result.Status}, nil
	}
	return mcpserver.AddTool(registry, deleteDogContract().Registration(), handler)
}

type setActivityDogsInput struct {
	ActivityID any     `json:"activity_id" jsonschema:"the Garmin activity identifier"`
	DogIDs     []int64 `json:"dog_ids" jsonschema:"the dogs on the activity"`
}

func setActivityDogsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolSetActivityDogs,
			Title: "Set the dogs on an activity",
			Description: "record which dogs joined one activity, such as a run. It replaces " +
				"the current list; an empty list records no dog. Dog identifiers come from get_dogs",
			Tier:        policy.TierWrite,
			Category:    categoryOrdinary,
			Annotations: writeAnnotations(true),
		},
		Schema: NewSchema(activityIDProperty(), Property{
			Name: "dog_ids", Types: []string{typeArray},
			Description: "the dog profile identifiers, from get_dogs; empty for none",
			MaxItems:    new(api.MaxDogsPerActivity), Required: true,
			Items: map[string]any{keyType: typeInteger, keyMinimum: 1},
		}),
	}
}

func registerSetActivityDogs(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in setActivityDogsInput) (
		*mcp.CallToolResult, ActivityUpdate, error,
	) {
		if len(in.DogIDs) > api.MaxDogsPerActivity {
			return nil, ActivityUpdate{}, invalidArgument("dog_ids has too many entries")
		}
		for _, id := range in.DogIDs {
			if _, err := parseDogID(id); err != nil {
				return nil, ActivityUpdate{}, err
			}
		}
		write, err := svc.resolveActivityWrite(ctx, in.ActivityID)
		if err != nil {
			return nil, ActivityUpdate{}, err
		}
		result, err := svc.writes.SetDogs(ctx, write.session, write.id, in.DogIDs)
		if err != nil {
			return nil, ActivityUpdate{}, fail(err)
		}
		return nil, newActivityUpdate(write.id, "dog_ids", result), nil
	}
	return mcpserver.AddTool(registry, setActivityDogsContract().Registration(), handler)
}
