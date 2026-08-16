package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// adhocChallengeDocument is two user-created social challenges with every curated
// field present, in an order that requires the start-date-descending sort
// (challenges.py:400-402) to be exercised. Every value is invented.
const adhocChallengeDocument = `[` +
	`{"uuid":"adhoc-0001","adHocChallengeName":"January Steps",` +
	`"adHocChallengeDesc":"Step it up with friends","socialChallengeActivityTypeId":4,` +
	`"socialChallengeStatusId":2,"startDate":"2026-01-01T00:00:00.0",` +
	`"endDate":"2026-01-31T23:59:59.0","userRanking":2,"playerCount":6},` +
	`{"uuid":"adhoc-0002","adHocChallengeName":"February Ride",` +
	`"adHocChallengeDesc":"Cycling club","socialChallengeActivityTypeId":2,` +
	`"socialChallengeStatusId":1,"startDate":"2026-02-01T00:00:00.0",` +
	`"endDate":"2026-02-28T23:59:59.0","userRanking":1,"playerCount":4}` +
	`]`

func TestGetAdhocChallengesCuratesTheChallenge(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathAdhocChallenges,
		testkit.JSON(http.StatusOK, adhocChallengeDocument))
	h := newChallengesHarness(t, script, []string{ToolGetAdhocChallenges}, registerGetAdhocChallenges)

	result := h.call(t, ToolGetAdhocChallenges, nil)
	challenges := list(t, result, "challenges")
	if len(challenges) != 2 {
		t.Fatalf("challenges holds %d entries, want 2", len(challenges))
	}

	// TestGetAdhocChallengesSortsByStartDateDescending below asserts the order;
	// this test asserts the curation of one entry.
	first := entry(t, challenges, 0)
	if got, _ := first["name"].(string); got != "February Ride" {
		t.Fatalf("the first entry is %q, want the sort to put February Ride first", got)
	}
	if got, _ := first["description"].(string); got != "Cycling club" {
		t.Errorf("description = %q, want %q", got, "Cycling club")
	}
	if got, _ := first["activity_type"].(string); got != challengeLabelCycling {
		t.Errorf("activity_type = %q, want Cycling for activity type id 2", got)
	}
	if got, _ := first["status"].(string); got != "Not Started" {
		t.Errorf("status = %q, want Not Started for status id 1", got)
	}
	if got := number(t, first, "your_ranking"); got != 1 {
		t.Errorf("your_ranking = %v, want 1", got)
	}
	if got := number(t, first, "player_count"); got != 4 {
		t.Errorf("player_count = %v, want 4", got)
	}
	if got, _ := first["start_date"].(string); got != challengeWindowEnd {
		t.Errorf("start_date = %q, want the date-only 2026-02-01", got)
	}
}

// TestGetAdhocChallengesSortsByStartDateDescending proves the most-recently-started
// challenge is returned first, matching challenges.py:400-402.
func TestGetAdhocChallengesSortsByStartDateDescending(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathAdhocChallenges,
		testkit.JSON(http.StatusOK, adhocChallengeDocument))
	h := newChallengesHarness(t, script, []string{ToolGetAdhocChallenges}, registerGetAdhocChallenges)

	challenges := list(t, h.call(t, ToolGetAdhocChallenges, nil), "challenges")
	first, _ := entry(t, challenges, 0)["start_date"].(string)
	second, _ := entry(t, challenges, 1)["start_date"].(string)
	if first != challengeWindowEnd || second != scoresStartDate {
		t.Errorf("start dates = [%q, %q], want the February challenge first", first, second)
	}
}

func TestGetAdhocChallengesUsesTheManifestDefaults(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathAdhocChallenges,
		testkit.JSON(http.StatusOK, `[]`))
	h := newChallengesHarness(t, script, []string{ToolGetAdhocChallenges}, registerGetAdhocChallenges)

	h.call(t, ToolGetAdhocChallenges, nil)

	requests := h.fake.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want one", len(requests))
	}
	if got := requests[0].Query.Get(client.QueryStart); got != "0" {
		t.Errorf("start = %q, want the manifest default 0", got)
	}
	if got := requests[0].Query.Get(client.QueryLimit); got != "20" {
		t.Errorf("limit = %q, want the manifest default 20", got)
	}
}

// TestGetAdhocChallengesRejectsANegativeStart proves the schema's default-0 minimum
// (this family, unlike the badge-challenge families, accepts start 0) still refuses
// a negative one before any Garmin call is made.
func TestGetAdhocChallengesRejectsANegativeStart(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathAdhocChallenges,
		testkit.JSON(http.StatusOK, `[]`))
	h := newChallengesHarness(t, script, []string{ToolGetAdhocChallenges}, registerGetAdhocChallenges)

	advice := h.callError(t, ToolGetAdhocChallenges, map[string]any{argStart: -1})
	assertNoRawPayload(t, advice)
	if len(h.fake.Requests()) != 0 {
		t.Error("a negative start reached the fake Garmin service")
	}
}

// TestGetAdhocChallengesCapsAtTheRequestedLimit proves a Garmin response carrying
// more challenges than the requested limit is cut to it and reported as truncated,
// since Garmin does not reliably honor the limit it is asked for.
func TestGetAdhocChallengesCapsAtTheRequestedLimit(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathAdhocChallenges,
		testkit.JSON(http.StatusOK, adhocChallengeDocument))
	h := newChallengesHarness(t, script, []string{ToolGetAdhocChallenges}, registerGetAdhocChallenges)

	result := h.call(t, ToolGetAdhocChallenges, map[string]any{argLimit: 1})
	challenges := list(t, result, "challenges")
	if len(challenges) != 1 {
		t.Fatalf("challenges holds %d entries, want 1: the response should be cut to the limit", len(challenges))
	}
	if truncated, _ := result["truncated"].(bool); !truncated {
		t.Error("truncated = false, want true: Garmin returned more than the requested limit")
	}
}

func TestAdhocChallengeListLogValueReportsShapeOnly(t *testing.T) {
	t.Parallel()

	name := "January Steps"
	ranking := int64(2)
	value := AdhocChallengeList{Challenges: []AdhocChallenge{
		{Name: &name, YourRanking: &ranking},
	}}.LogValue().String()

	if strings.Contains(value, "January Steps") {
		t.Errorf("the log value %q carries the name", value)
	}
	if !strings.Contains(value, "adhocChallengeList") {
		t.Errorf("the log value %q does not name the model", value)
	}
}
