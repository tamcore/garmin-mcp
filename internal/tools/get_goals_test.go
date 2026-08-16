package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// goalsDocument is one page of raw goal documents, each carrying an identifying
// key that must be dropped before the tool returns it, since get_goals curates
// nothing (challenges.py:236-249: `json.dumps(goals, indent=2)` on the raw result,
// with no field read). Every value is invented.
const goalsDocument = `[{"goalTypeId":1,"goalValue":10,"userProfilePK":900001},` +
	`{"goalTypeId":2,"goalValue":5}]`

func TestGetGoalsSanitizesTheRawDocument(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathGoals,
		testkit.JSON(http.StatusOK, goalsDocument), testkit.JSON(http.StatusOK, `[]`))
	h := newChallengesHarness(t, script, []string{ToolGetGoals}, registerGetGoals)

	result := h.call(t, ToolGetGoals, nil)
	if got, _ := result[argGoalType].(string); got != valueActive {
		t.Errorf("goal_type = %q, want the manifest default active", got)
	}
	if got := number(t, result, "count"); got != 2 {
		t.Errorf("count = %v, want 2", got)
	}

	goals := list(t, result, "goals")
	if len(goals) != 2 {
		t.Fatalf("goals holds %d entries, want 2", len(goals))
	}
	first := entry(t, goals, 0)
	document, ok := first["document"].(map[string]any)
	if !ok {
		t.Fatalf("document = %#v, want a structured object", first["document"])
	}
	if _, present := document["userProfilePK"]; present {
		t.Errorf("document %v still carries the identifying key userProfilePK", document)
	}
	if document["goalTypeId"] != float64(1) {
		t.Errorf("document = %v, want goalTypeId 1 preserved", document)
	}

	if got := number(t, result, "dropped_fields"); got != 1 {
		t.Errorf("dropped_fields = %v, want 1", got)
	}
}

// TestGetGoalsSendsTheRequestedStatus proves goal_type reaches Garmin as the exact
// query value get_goals sends, and that the sort order is fixed ascending.
func TestGetGoalsSendsTheRequestedStatus(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathGoals, testkit.JSON(http.StatusOK, `[]`))
	h := newChallengesHarness(t, script, []string{ToolGetGoals}, registerGetGoals)

	h.call(t, ToolGetGoals, map[string]any{argGoalType: valueFuture})

	requests := h.fake.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want one", len(requests))
	}
	if got := requests[0].Query.Get(client.QueryStatus); got != valueFuture {
		t.Errorf("status = %q, want future", got)
	}
	if got := requests[0].Query.Get(client.QuerySortOrder); got != client.GoalSortAscending {
		t.Errorf("sortOrder = %q, want %q", got, client.GoalSortAscending)
	}
}

// TestGetGoalsRejectsAnUnknownGoalType proves the exact three-value set is enforced
// before any Garmin call is made, matching get_goals's own valid_statuses check.
func TestGetGoalsRejectsAnUnknownGoalType(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathGoals, testkit.JSON(http.StatusOK, `[]`))
	h := newChallengesHarness(t, script, []string{ToolGetGoals}, registerGetGoals)

	advice := h.callError(t, ToolGetGoals, map[string]any{argGoalType: "someday"})
	assertNoRawPayload(t, advice)
	if len(h.fake.Requests()) != 0 {
		t.Error("an invalid goal_type reached the fake Garmin service")
	}
}

// TestGetGoalsRefusesAGoalListOverTheByteBudget proves the defaultMaxGoalListBytes
// refusal path this tool has always declared but never exercised: a goal document
// large enough to push the running total past the budget is refused rather than
// silently cut, the same discipline get_lifestyle_logging_data's own byte-bound
// test exercises for a single document.
func TestGetGoalsRefusesAGoalListOverTheByteBudget(t *testing.T) {
	t.Parallel()

	oversizedGoal := `{"note":"` + strings.Repeat("x", defaultMaxGoalListBytes) + `"}`
	script := testkit.NewScript().With(client.PathGoals,
		testkit.JSON(http.StatusOK, "["+oversizedGoal+"]"), testkit.JSON(http.StatusOK, `[]`))
	h := newChallengesHarness(t, script, []string{ToolGetGoals}, registerGetGoals)

	advice := h.callError(t, ToolGetGoals, nil)
	assertNoRawPayload(t, advice)
	if strings.Contains(advice, "xxxx") {
		t.Errorf("the refusal %q quotes the oversized document, want authored advice only", advice)
	}
}

func TestGetGoalsReportsNoGoalsAsEmpty(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathGoals, testkit.JSON(http.StatusOK, `[]`))
	h := newChallengesHarness(t, script, []string{ToolGetGoals}, registerGetGoals)

	result := h.call(t, ToolGetGoals, nil)
	if got := number(t, result, "count"); got != 0 {
		t.Errorf("count = %v, want 0", got)
	}
	if got := len(list(t, result, "goals")); got != 0 {
		t.Errorf("goals holds %d entries, want none", got)
	}
}

func TestGoalListLogValueReportsShapeOnly(t *testing.T) {
	t.Parallel()

	value := GoalList{
		GoalType: valueActive,
		Goals:    []Goal{{Document: map[string]any{"goalValue": 10}}},
		Count:    1,
	}.LogValue().String()

	if strings.Contains(value, "goalValue") || strings.Contains(value, "10") {
		t.Errorf("the log value %q carries goal content", value)
	}
	if !strings.Contains(value, "goalList") {
		t.Errorf("the log value %q does not name the model", value)
	}
}
