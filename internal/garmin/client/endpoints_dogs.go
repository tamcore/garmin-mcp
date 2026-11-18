package client

// Dog profile and dog activity paths. Source: the Garmin Connect web app
// (connect.garmin.com 5.31.2.3), whose gcx client reaches the profile paths on the
// GCX host and whose connectapi client reaches the stats path, cross-checked
// against captured browser traffic.
const (
	// PathPets lists and creates dog profiles, and precedes a profile id in the
	// per-profile read, update and delete paths. It is served by the GCX host.
	PathPets = "/family-api/profile-service/profile/pets"
	// PathDogBreeds is the dog breed catalog, an object of id to breed key. It is
	// served by the GCX host.
	PathDogBreeds = "/family-api/profile-service/profile/pets/dog/breeds"
	// PetStatusSegment follows a profile id in the status path.
	PetStatusSegment = "status"
	// PathDogStatsLifetime is the all-time activity stats with dogs, grouped by
	// dog profile.
	PathDogStatsLifetime = "/fitnessstats-service/activity-animal/dog/stats/lifetime"
)

// Query parameter names the dog reads add. The lifetime stats repeat
// QueryMetric, declared in endpoints_training.go.
const (
	// QueryPetStatus filters the profile list by active or retired.
	QueryPetStatus = "status"
)

// Sanitized endpoint labels for the dog tier.
const (
	EndpointPets          = Endpoint("gcx.pets")
	EndpointPetStatus     = Endpoint("gcx.pets.status")
	EndpointDogBreeds     = Endpoint("gcx.pets.dog_breeds")
	EndpointDogStatsTotal = Endpoint("connectapi.dog_stats.lifetime")
)

func dogsEndpoints() []Endpoint {
	return []Endpoint{EndpointPets, EndpointPetStatus, EndpointDogBreeds, EndpointDogStatsTotal}
}

// Sanitized operation labels, one per call this tier adds.
const (
	OpGetDogs      = Op("get_dogs")
	OpGetDog       = Op("get_dog")
	OpGetDogBreeds = Op("get_dog_breeds")
	OpGetDogStats  = Op("get_dog_activity_stats")
	OpCreateDog    = Op("create_dog")
	OpUpdateDog    = Op("update_dog")
	OpSetDogStatus = Op("set_dog_status")
	OpDeleteDog    = Op("delete_dog")
	// OpSetActivityDogs writes metadataDTO.hasDog and dogProfileIds on the
	// activity path, which EndpointActivity already labels.
	OpSetActivityDogs = Op("set_activity_dogs")
)

func dogsOps() []Op {
	return []Op{
		OpGetDogs, OpGetDog, OpGetDogBreeds, OpGetDogStats,
		OpCreateDog, OpUpdateDog, OpSetDogStatus, OpDeleteDog, OpSetActivityDogs,
	}
}
