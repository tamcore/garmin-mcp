package api_test

import (
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func TestDogsLifetimeStatsRequestsBothMetrics(t *testing.T) {
	t.Parallel()

	body := `{"date":"2026-11-18","countOfActivities":1,"stats":{"all":{` +
		`"duration":{"count":1,"sum":3600000.0},"distance":{"count":1,"sum":500000.0},` +
		`"animals":{"animalProfileGroups":[{"animalProfileId":4242,"numberOfActivities":1,` +
		`"distance":{"sum":500000.0},"duration":{"sum":3600000.0}}]}}}}`
	script := testkit.NewScript().With(client.PathDogStatsLifetime, testkit.JSON(http.StatusOK, body))
	h := newHarness(t, script, client.Limits{})

	stats, err := newDogs(t, h).LifetimeStats(t.Context(), h.session)
	if err != nil {
		t.Fatalf("LifetimeStats() = %v", err)
	}
	if got := h.server.Requests()[0].Query[client.QueryMetric]; len(got) != 2 {
		t.Errorf("metric = %v, want distance and duration", got)
	}
	groups := stats.Stats.All.Animals.Groups
	if len(groups) != 1 || groups[0].Distance == nil {
		t.Fatalf("groups = %+v, want one group with a distance", groups)
	}
	if sum, _ := groups[0].Distance.Sum.Float64(); sum != 500000 {
		t.Errorf("distance sum = %v, want 500000", sum)
	}
}
