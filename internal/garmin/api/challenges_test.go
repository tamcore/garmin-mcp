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

// Every fixture here is a literal JSON document written directly from
// Taxuspt/garmin_mcp's src/garmin_mcp/challenges.py reads at the pinned
// commit docs/upstream-pins.md names, quoted beside each fixture. None is
// derived from this package's own struct tags or a shared fixture builder,
// and none is a recording of a real account.

// earnedBadgesBody carries exactly the fields get_earned_badges's own
// curation reads (challenges.py:296-360):
//
//	badge.get("badgeName")                                    (317)
//	badge.get("badgeCategoryId"), ("badgeDifficultyId"), ("badgeUnitId")  (307-309)
//	badge.get("badgeProgressValue"), ("badgeTargetValue")     (312-313)
//	badge.get("badgePoints")                                  (325)
//	badge.get("badgeEarnedDate")                              (326)
//	badge.get("badgeStartDate"), ("badgeEndDate")              (335-336)
//	badge.get("badgeAssocType"), ("badgeAssocDataId")          (341-344)
//	badge.get("badgeSeriesId")                                 (347-348)
const earnedBadgesBody = `[{"badgeName":"Century Rider","badgeCategoryId":2,` +
	`"badgeDifficultyId":2,"badgeUnitId":1,"badgeProgressValue":80000,` +
	`"badgeTargetValue":100000,"badgePoints":"25","badgeEarnedDate":"2026-01-15",` +
	`"badgeStartDate":"2026-01-01","badgeEndDate":"2026-01-31",` +
	`"badgeAssocType":"activityId","badgeAssocDataId":998877,"badgeSeriesId":42}]`

// goalPageOneBody is one goal-service object. get_goals's own tool
// (challenges.py:236-249) never reads an individual field — it calls
// json.dumps(goals, indent=2) on the raw list — so the shape asserted here
// is only "one JSON object per page", never a field spelling.
const goalPageOneBody = `[{"goalId":1,"goalTypeId":2,"startDate":"2026-01-01",` +
	`"endDate":"2026-12-31","goalValue":5,"targetValue":10}]`

// racePredictionsBody carries exactly the fields get_race_predictions's tool
// reads (challenges.py:513-549): predictions.get("calendarDate"),
// ("time5K"), ("time10K"), ("timeHalfMarathon") and ("timeMarathon").
const racePredictionsBody = `{"calendarDate":"2026-01-31","time5K":"1500.5",` +
	`"time10K":3120,"timeHalfMarathon":6800,"timeMarathon":14200}`

func newChallenges(t *testing.T, h harness) *api.Challenges {
	t.Helper()

	c, err := api.NewChallenges(h.rc)
	if err != nil {
		t.Fatalf("NewChallenges() = %v", err)
	}
	return c
}

func TestNewChallengesRefusesAMissingRequestLayer(t *testing.T) {
	t.Parallel()

	if _, err := api.NewChallenges(nil); !errors.Is(err, client.ErrNotConfigured) {
		t.Fatalf("NewChallenges(nil) = %v, want ErrNotConfigured", err)
	}
}

func TestChallengesEarnedBadgesDecodesTolerantly(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathEarnedBadges, testkit.JSON(http.StatusOK, earnedBadgesBody))
	h := newHarness(t, script, client.Limits{})

	badges, err := newChallenges(t, h).EarnedBadges(t.Context(), h.session)
	if err != nil {
		t.Fatalf("EarnedBadges() = %v", err)
	}
	if len(badges) != 1 {
		t.Fatalf("EarnedBadges() returned %d badges, want 1", len(badges))
	}
	badge := badges[0]
	if got, ok := badge.BadgePoints.Float64(); !ok || got != 25 {
		t.Errorf("badgePoints = %v (%t), want 25 from the string form", got, ok)
	}
	if got, ok := badge.BadgeName.Value(); !ok || got != "Century Rider" {
		t.Errorf("badgeName = %q (%t), want %q", got, ok, "Century Rider")
	}
	if got, ok := badge.BadgeSeriesID.Int64(); !ok || got != 42 {
		t.Errorf("badgeSeriesId = %v (%t), want 42", got, ok)
	}
}

func TestChallengesGoalsRequiresAStatus(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})

	if _, err := newChallenges(t, h).Goals(t.Context(), h.session, ""); !errors.Is(err, client.ErrValidation) {
		t.Errorf("Goals(\"\") = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want none", got)
	}
}

func TestParseGoalStatusValidatesItsInput(t *testing.T) {
	t.Parallel()

	for _, value := range []api.GoalStatus{api.GoalStatusActive, api.GoalStatusFuture, api.GoalStatusPast} {
		if _, err := api.ParseGoalStatus(string(value)); err != nil {
			t.Errorf("ParseGoalStatus(%q) = %v, want nil", value, err)
		}
	}
	for _, value := range []string{"", "Active", "completed", "active "} {
		if _, err := api.ParseGoalStatus(value); !errors.Is(err, client.ErrValidation) {
			t.Errorf("ParseGoalStatus(%q) = %v, want ErrValidation", value, err)
		}
	}
}

func TestChallengesGoalsWalksUntilAnEmptyPage(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathGoals,
		testkit.JSON(http.StatusOK, goalPageOneBody),
		testkit.JSON(http.StatusOK, "[]"))
	h := newHarness(t, script, client.Limits{})

	result, err := newChallenges(t, h).Goals(t.Context(), h.session, api.GoalStatusActive)
	if err != nil {
		t.Fatalf("Goals() = %v", err)
	}
	if len(result.Goals) != 1 {
		t.Fatalf("Goals() returned %d goals, want 1", len(result.Goals))
	}
	if result.Truncated {
		t.Error("Goals() reported Truncated, want false")
	}

	requests := h.server.Requests()
	if len(requests) != 2 {
		t.Fatalf("the fake received %d requests, want 2", len(requests))
	}
	if got := requests[0].Query.Get(client.QueryStatus); got != "active" {
		t.Errorf("status = %q, want active", got)
	}
	if got := requests[0].Query.Get(client.QuerySortOrder); got != client.GoalSortAscending {
		t.Errorf("sortOrder = %q, want %q", got, client.GoalSortAscending)
	}
	if got := requests[0].Query.Get(client.QueryStart); got != "0" {
		t.Errorf("first request start = %q, want 0", got)
	}
	if got := requests[1].Query.Get(client.QueryStart); got != "20" {
		t.Errorf("second request start = %q, want 20", got)
	}
}

func TestChallengesGoalsFailsLoudlyOnEndlessPagination(t *testing.T) {
	t.Parallel()

	// Source: the MAX_PAGINATED_REQUESTS guard get_goals itself uses, ported here
	// as the configured Limits.MaxPages bound instead.
	script := testkit.NewScript().With(client.PathGoals, testkit.JSON(http.StatusOK, goalPageOneBody))
	h := newHarness(t, script, client.Limits{MaxPages: 3})

	_, err := newChallenges(t, h).Goals(t.Context(), h.session, api.GoalStatusActive)
	if !errors.Is(err, client.ErrPaginationExhausted) {
		t.Fatalf("Goals() = %v, want ErrPaginationExhausted", err)
	}
	if got := len(h.server.Requests()); got != 3 {
		t.Errorf("the fake received %d requests, want the 3-page bound", got)
	}
}

// TestChallengesGoalsBoundsTheAccumulatedTotal is the regression test for the
// defect a review found: the walk bounded the page count but not the size of
// an individual page or the total accumulated across pages, so a server that
// ignores the requested limit and keeps answering large, never-empty pages
// could grow the in-memory result without bound until Limits.MaxPages
// finally errored. A server behaving this way now stops the walk once
// maxGoalWalkItems items have accumulated and reports Truncated, rather than
// growing further or erroring.
func TestChallengesGoalsBoundsTheAccumulatedTotal(t *testing.T) {
	t.Parallel()

	// One oversized page of 4000 goal objects, repeated on every request: a
	// server that never returns a short or empty page and ignores limit.
	const oversizedPageSize = 4000
	oversizedPage := buildGoalArray(t, oversizedPageSize)

	script := testkit.NewScript().With(client.PathGoals, testkit.JSON(http.StatusOK, oversizedPage))
	h := newHarness(t, script, client.Limits{MaxPages: 100})

	result, err := newChallenges(t, h).Goals(t.Context(), h.session, api.GoalStatusActive)
	if err != nil {
		t.Fatalf("Goals() = %v, want no error (a bounded, truncated result)", err)
	}
	if !result.Truncated {
		t.Error("Goals() did not report Truncated against an endlessly-oversized server")
	}
	if len(result.Goals) > 5000 {
		t.Errorf("Goals() accumulated %d goals, want at most the maxGoalWalkItems bound of 5000", len(result.Goals))
	}
	if len(result.Goals) == 0 {
		t.Error("Goals() accumulated no goals at all, want the first page's worth before truncating")
	}

	// The walk must have stopped well short of the 100-page bound: it hit the
	// item cap, not the page-count cap.
	if got := len(h.server.Requests()); got >= 100 {
		t.Errorf("the fake received %d requests, want fewer than the 100-page bound", got)
	}
}

// buildGoalArray writes a literal JSON array of n minimal goal objects.
func buildGoalArray(t *testing.T, n int) string {
	t.Helper()

	var body strings.Builder
	body.WriteString("[")
	for i := range n {
		if i > 0 {
			body.WriteString(",")
		}
		body.WriteString(`{"goalId":1}`)
	}
	body.WriteString("]")
	return body.String()
}

func TestChallengesRacePredictionsDecodesTolerantly(t *testing.T) {
	t.Parallel()

	path := client.PathRacePredictionsPrefix + "/latest/" + fakeDisplayName
	script := testkit.NewScript().With(path, testkit.JSON(http.StatusOK, racePredictionsBody))
	h := newHarness(t, script, client.Limits{})

	predictions, err := newChallenges(t, h).RacePredictions(t.Context(), h.session, mustDisplayName(t))
	if err != nil {
		t.Fatalf("RacePredictions() = %v", err)
	}
	if got, ok := predictions.Time5K.Float64(); !ok || got != 1500.5 {
		t.Errorf("time5K = %v (%t), want 1500.5 from the string form", got, ok)
	}
	if got, ok := predictions.TimeMarathon.Float64(); !ok || got != 14200 {
		t.Errorf("timeMarathon = %v (%t), want 14200", got, ok)
	}
	if predictions.Payload().Len() == 0 {
		t.Error("RacePredictions() retained no payload")
	}
}

func TestChallengesRacePredictionsRefusesAnUnsetDisplayName(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})

	_, err := newChallenges(t, h).RacePredictions(t.Context(), h.session, client.DisplayName{})
	if !errors.Is(err, client.ErrValidation) {
		t.Errorf("RacePredictions(unset name) = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want none", got)
	}
}
