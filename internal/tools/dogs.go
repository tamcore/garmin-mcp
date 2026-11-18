package tools

import (
	"context"
	"log/slog"
	"sort"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// The dog tools. Source: the Garmin Connect web app (connect.garmin.com
// 5.31.2.3); the pinned upstream has no dog tool.
const (
	ToolGetDogs             = "get_dogs"
	ToolGetDogBreeds        = "get_dog_breeds"
	ToolGetDogActivityStats = "get_dog_activity_stats"
)

// Dog unit conversions. Source: the dog settings page divides a distance sum by
// 100 and a duration sum by 1000.
const (
	dogStatsCentimetersPerMeter = 100
	dogStatsMillisPerSecond     = 1000
	dogStatusArgActive          = "active"
	dogStatusArgRetired         = "retired"
)

// DogRecord is one dog profile. It is account material: returned to the
// authorized caller, never logged.
type DogRecord struct {
	DogID              int64    `json:"dog_id" jsonschema:"the dog profile identifier"`
	Name               *string  `json:"name,omitempty" jsonschema:"the dog's name"`
	PrimaryBreedID     *int64   `json:"primary_breed_id,omitempty" jsonschema:"the primary breed, from get_dog_breeds"`
	SecondaryBreedIDs  []int64  `json:"secondary_breed_ids,omitempty" jsonschema:"other breeds"`
	Gender             *string  `json:"gender,omitempty" jsonschema:"male or female, when set"`
	IsNeutered         *bool    `json:"is_neutered,omitempty" jsonschema:"whether the dog is neutered, when set"`
	WeightKG           *float64 `json:"weight_kg,omitempty" jsonschema:"the weight in kilograms, when set"`
	Birthdate          *string  `json:"birthdate,omitempty" jsonschema:"the birth date, YYYY-MM-DD"`
	BirthdatePrecision *string  `json:"birthdate_precision,omitempty" jsonschema:"day or month"`
	Status             *string  `json:"status,omitempty" jsonschema:"active or retired"`
}

// LogValue reports presence only, never a name or a reading.
func (r DogRecord) LogValue() slog.Value {
	return shape("dogRecord", slog.String("name", presence(r.Name != nil)))
}

func newDogRecord(dog api.Dog) DogRecord {
	id, _ := dog.ID.Int64Exact()
	record := DogRecord{
		DogID: id, Name: dog.Name, Gender: dog.Gender, IsNeutered: dog.IsNeutered,
		Birthdate: dog.Birthdate, BirthdatePrecision: dog.BirthdatePrecision, Status: dog.Status,
	}
	if dog.PrimaryBreed != nil {
		record.PrimaryBreedID = exactInt(dog.PrimaryBreed.ID)
	}
	for _, b := range dog.SecondaryBreeds {
		if breed, ok := b.ID.Int64Exact(); ok {
			record.SecondaryBreedIDs = append(record.SecondaryBreedIDs, breed)
		}
	}
	if weight, ok := dog.WeightKG.Float64(); ok {
		record.WeightKG = &weight
	}
	return record
}

// DogList is the get_dogs result.
type DogList struct {
	Dogs []DogRecord `json:"dogs" jsonschema:"the dog profiles"`
}

// LogValue reports the count only.
func (l DogList) LogValue() slog.Value { return shape("dogList", slog.Int("dogs", len(l.Dogs))) }

type getDogsInput struct {
	Status *string `json:"status,omitempty" jsonschema:"active or retired; omit for all"`
}

func dogStatusProperty(description string, required bool) Property {
	return Property{
		Name: "status", Types: []string{typeString}, Description: description,
		Enum: []any{dogStatusArgActive, dogStatusArgRetired}, Required: required,
	}
}

func getDogsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetDogs,
			Title: "Get dogs",
			Description: "list the dog profiles on the account: identifier, name, breeds, " +
				"gender, weight, birth date and status. Use the identifier with " +
				"set_activity_dogs to record a dog on an activity",
			Tier:        policy.TierReadOnly,
			Category:    categoryProfile,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dogStatusProperty("only dogs with this status; omit for all", false)),
	}
}

func registerGetDogs(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getDogsInput) (
		*mcp.CallToolResult, DogList, error,
	) {
		var status api.DogStatus
		if in.Status != nil {
			parsed, err := parseDogStatus(*in.Status)
			if err != nil {
				return nil, DogList{}, err
			}
			status = parsed
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, DogList{}, err
		}
		dogs, err := svc.dogs.List(ctx, session, status)
		if err != nil {
			return nil, DogList{}, fail(err)
		}
		out := DogList{Dogs: make([]DogRecord, 0, len(dogs))}
		for _, dog := range dogs {
			out.Dogs = append(out.Dogs, newDogRecord(dog))
		}
		return nil, out, nil
	}
	return mcpserver.AddTool(registry, getDogsContract().Registration(), handler)
}

func parseDogStatus(value string) (api.DogStatus, error) {
	switch value {
	case dogStatusArgActive:
		return api.DogStatusActive, nil
	case dogStatusArgRetired:
		return api.DogStatusRetired, nil
	default:
		return "", invalidArgument("status must be active or retired")
	}
}

// DogBreed is one catalog entry.
type DogBreed struct {
	BreedID int64  `json:"breed_id" jsonschema:"the breed identifier"`
	Key     string `json:"key" jsonschema:"the breed key, for example DOG_BREED_BEAGLE"`
}

// DogBreeds is the get_dog_breeds result.
type DogBreeds struct {
	Breeds []DogBreed `json:"breeds" jsonschema:"the breed catalog, by identifier"`
}

// LogValue reports the count only.
func (b DogBreeds) LogValue() slog.Value {
	return shape("dogBreeds", slog.Int("breeds", len(b.Breeds)))
}

func getDogBreedsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetDogBreeds,
			Title: "Get dog breeds",
			Description: "list Garmin's dog breed catalog: each breed identifier with its key. " +
				"Use an identifier as primary_breed_id or secondary_breed_ids. Takes no arguments",
			Tier:        policy.TierReadOnly,
			Category:    categoryProfile,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(),
	}
}

func registerGetDogBreeds(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, _ noArguments) (
		*mcp.CallToolResult, DogBreeds, error,
	) {
		session, err := svc.session(ctx)
		if err != nil {
			return nil, DogBreeds{}, err
		}
		catalog, err := svc.dogs.Breeds(ctx, session)
		if err != nil {
			return nil, DogBreeds{}, fail(err)
		}
		return nil, newDogBreeds(catalog), nil
	}
	return mcpserver.AddTool(registry, getDogBreedsContract().Registration(), handler)
}

func newDogBreeds(catalog map[string]string) DogBreeds {
	out := DogBreeds{Breeds: make([]DogBreed, 0, len(catalog))}
	for raw, key := range catalog {
		if id, err := strconv.ParseInt(raw, 10, 64); err == nil {
			out.Breeds = append(out.Breeds, DogBreed{BreedID: id, Key: key})
		}
	}
	sort.Slice(out.Breeds, func(i, j int) bool { return out.Breeds[i].BreedID < out.Breeds[j].BreedID })
	return out
}

// DogActivityTotals is one aggregate of activities with dogs.
type DogActivityTotals struct {
	DogID           *int64   `json:"dog_id,omitempty" jsonschema:"the dog; absent on the all-dogs total"`
	Activities      *int64   `json:"activities,omitempty" jsonschema:"the number of activities"`
	DistanceMeters  *float64 `json:"distance_meters,omitempty" jsonschema:"the total distance in meters"`
	DurationSeconds *float64 `json:"duration_seconds,omitempty" jsonschema:"the total duration in seconds"`
	LastActivityGMT *string  `json:"last_activity_gmt,omitempty" jsonschema:"the most recent activity start, GMT"`
}

// DogActivityStats is the get_dog_activity_stats result.
type DogActivityStats struct {
	Total DogActivityTotals   `json:"total" jsonschema:"all activities with any dog"`
	Dogs  []DogActivityTotals `json:"dogs" jsonschema:"per dog"`
}

// LogValue reports the count only, never a reading.
func (s DogActivityStats) LogValue() slog.Value {
	return shape("dogActivityStats", slog.Int("dogs", len(s.Dogs)))
}

func getDogActivityStatsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetDogActivityStats,
			Title: "Get dog activity stats",
			Description: "read the lifetime totals of activities done with dogs: activity count, " +
				"distance and duration, overall and per dog. Takes no arguments",
			Tier:        policy.TierReadOnly,
			Category:    categoryProfile,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(),
	}
}

func registerGetDogActivityStats(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, _ noArguments) (
		*mcp.CallToolResult, DogActivityStats, error,
	) {
		session, err := svc.session(ctx)
		if err != nil {
			return nil, DogActivityStats{}, err
		}
		stats, err := svc.dogs.LifetimeStats(ctx, session)
		if err != nil {
			return nil, DogActivityStats{}, fail(err)
		}
		return nil, newDogActivityStats(stats), nil
	}
	return mcpserver.AddTool(registry, getDogActivityStatsContract().Registration(), handler)
}

func newDogActivityStats(stats api.DogStats) DogActivityStats {
	all := stats.Stats.All
	out := DogActivityStats{
		Total: DogActivityTotals{
			Activities:      exactInt(stats.CountOfActivities),
			DistanceMeters:  statSum(all.Distance, dogStatsCentimetersPerMeter),
			DurationSeconds: statSum(all.Duration, dogStatsMillisPerSecond),
		},
		Dogs: make([]DogActivityTotals, 0, len(all.Animals.Groups)),
	}
	for _, g := range all.Animals.Groups {
		out.Dogs = append(out.Dogs, DogActivityTotals{
			DogID:           exactInt(g.AnimalProfileID),
			Activities:      exactInt(g.NumberOfActivities),
			DistanceMeters:  statSum(g.Distance, dogStatsCentimetersPerMeter),
			DurationSeconds: statSum(g.Duration, dogStatsMillisPerSecond),
			LastActivityGMT: g.RecentActivityStart,
		})
	}
	return out
}

func exactInt(n client.Number) *int64 {
	if v, ok := n.Int64Exact(); ok {
		return &v
	}
	return nil
}

func statSum(sum *api.DogStatSum, divisor float64) *float64 {
	if sum == nil {
		return nil
	}
	v, ok := sum.Sum.Float64()
	if !ok {
		return nil
	}
	scaled := v / divisor
	return &scaled
}
