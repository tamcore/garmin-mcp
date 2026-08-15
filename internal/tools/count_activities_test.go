package tools

import (
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// TestCountActivitiesReportsTheAccountTotal pins the happy path: one read of the
// count endpoint, and the total Garmin reported.
func TestCountActivitiesReportsTheAccountTotal(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathActivitiesCount,
		testkit.JSON(http.StatusOK, `{"totalCount":1234}`))
	h := newToolHarness(t, script)

	result := h.call(t, ToolCountActivities, nil)

	if got := number(t, result, "total_activities"); got != 1234 {
		t.Errorf("total_activities = %v, want 1234", got)
	}
	if note, _ := result["note"].(string); note == "" {
		t.Error("note is empty, want the authored advice on how to read the activities")
	}
}

// TestCountActivitiesAcceptsANumericString is the tolerance test: Garmin has sent
// the same field both as a number and as a numeric string.
func TestCountActivitiesAcceptsANumericString(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathActivitiesCount,
		testkit.JSON(http.StatusOK, `{"totalCount":"77","unknownField":true}`))
	h := newToolHarness(t, script)

	if got := number(t, h.call(t, ToolCountActivities, nil), "total_activities"); got != 77 {
		t.Errorf("total_activities = %v, want 77", got)
	}
}

// TestCountActivitiesRefusesAnAnswerWithoutACount is the honesty test: a response
// that carries no total must not be reported as an account with no activities.
func TestCountActivitiesRefusesAnAnswerWithoutACount(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"no totalCount":   `{"somethingElse":1}`,
		"negative total":  `{"totalCount":-3}`,
		"total is a word": `{"totalCount":"many"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := testkit.NewScript().With(client.PathActivitiesCount,
				testkit.JSON(http.StatusOK, body))
			h := newToolHarness(t, script)

			if advice := h.callError(t, ToolCountActivities, nil); advice == "" {
				t.Error("the refusal carries no advice")
			}
		})
	}
}

// TestCountActivitiesTakesNoArguments proves the tool declares an empty strict
// schema, so no caller can name an account through it.
func TestCountActivitiesTakesNoArguments(t *testing.T) {
	t.Parallel()

	schema := countActivitiesContract().Schema
	if got := len(schema.Properties()); got != 0 {
		t.Errorf("count_activities declares %d properties, want 0", got)
	}
	if got := countActivitiesContract().Spec.Name; got != "count_activities" {
		t.Errorf("registered name = %q, want the upstream name", got)
	}
}
