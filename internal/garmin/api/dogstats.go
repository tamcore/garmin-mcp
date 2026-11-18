package api

import (
	"context"
	"log/slog"
	"net/url"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// Lifetime activity stats with dogs.
//
// Source: the Garmin Connect web app (connect.garmin.com 5.31.2.3): the dog
// settings page requests the distance and duration metrics, reads distance sums
// in centimeters and duration sums in milliseconds, and keys each per-dog group by
// animalProfileId. The empty-account shape is confirmed against a real account.

// DogStatSum is one metric aggregate.
type DogStatSum struct {
	Count client.Number `json:"count"`
	Sum   client.Number `json:"sum"`
}

// DogStatsGroup is the aggregate of the activities with one dog.
type DogStatsGroup struct {
	AnimalProfileID     client.Number `json:"animalProfileId"`
	NumberOfActivities  client.Number `json:"numberOfActivities"`
	RecentActivityStart *string       `json:"recentActivityStartTimeGmt"`
	Distance            *DogStatSum   `json:"distance"`
	Duration            *DogStatSum   `json:"duration"`
}

// DogStats is the lifetime aggregate of all activities with dogs. It is account
// material: returned to the authorized caller, never logged.
type DogStats struct {
	CountOfActivities client.Number `json:"countOfActivities"`
	Stats             struct {
		All struct {
			Distance *DogStatSum `json:"distance"`
			Duration *DogStatSum `json:"duration"`
			Animals  struct {
				Groups []DogStatsGroup `json:"animalProfileGroups"`
			} `json:"animals"`
		} `json:"all"`
	} `json:"stats"`
}

// LogValue reports the group count, never a reading.
func (s DogStats) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "dogStats"),
		slog.Int("groups", len(s.Stats.All.Animals.Groups)),
	)
}

// LifetimeStats reads the all-time stats of activities with dogs.
func (d *Dogs) LifetimeStats(ctx context.Context, session client.Session) (DogStats, error) {
	query := url.Values{client.QueryMetric: {"distance", "duration"}}
	req := readRequest(client.OpGetDogStats, client.EndpointDogStatsTotal, client.PathDogStatsLifetime, query)
	var stats DogStats
	if _, err := d.req.read(ctx, session, req, &stats); err != nil {
		return DogStats{}, err
	}
	return stats, nil
}
