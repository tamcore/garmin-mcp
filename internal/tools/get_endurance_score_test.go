package tools

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// enduranceDocument is one current score with its classification limits, one
// contributor, and two weekly buckets. Every value is invented.
const enduranceDocument = `{"avg":7100,"max":7400,"enduranceScoreDTO":{` +
	`"calendarDate":"` + scoresEndDate + `","overallScore":7250,"classification":4,` +
	`"classificationLowerLimitIntermediate":4000,"classificationLowerLimitTrained":6000,` +
	`"classificationLowerLimitWellTrained":7000,"classificationLowerLimitExpert":8000,` +
	`"classificationLowerLimitSuperior":9000,"classificationLowerLimitElite":9500,` +
	`"contributors":[{"activityTypeId":1,"contribution":72.4567}]},` +
	`"groupMap":{"2026-01-26":{"groupAverage":7200,"groupMax":7300,` +
	`"enduranceContributorDTOList":[{"group":8,"contribution":27.5}]},` +
	`"2026-01-19":{"groupAverage":7000,"groupMax":7100}}}`

func TestGetEnduranceScoreReturnsTheCurrentScoreAndItsBuckets(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathEnduranceScoreStats,
		testkit.JSON(http.StatusOK, enduranceDocument))
	h := newScoresHarness(t, script)

	result := h.call(t, ToolGetEnduranceScore, scoresWindowArgs())

	if got := number(t, result, "current_score"); got != 7250 {
		t.Errorf("current_score = %v, want 7250", got)
	}
	if got, _ := result["classification"].(string); got != "well_trained" {
		t.Errorf("classification = %q, want well_trained for code 4", got)
	}
	if got := number(t, result, "classification_id"); got != 4 {
		t.Errorf("classification_id = %v, want 4", got)
	}
	if got := number(t, object(t, result, "thresholds"), "elite"); got != 9500 {
		t.Errorf("thresholds.elite = %v, want 9500", got)
	}

	contributor := entry(t, list(t, result, "contributors"), 0)
	if got := number(t, contributor, "activity_type_id"); got != 1 {
		t.Errorf("activity_type_id = %v, want 1", got)
	}
	// Upstream rounds a contribution to two decimals.
	if got := number(t, contributor, "contribution_percent"); got != 72.46 {
		t.Errorf("contribution_percent = %v, want 72.46", got)
	}
}

// TestGetEnduranceScoreOrdersTheBucketsByDate proves the answer does not depend on Go's
// map iteration order.
func TestGetEnduranceScoreOrdersTheBucketsByDate(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathEnduranceScoreStats,
		testkit.JSON(http.StatusOK, enduranceDocument))
	h := newScoresHarness(t, script)

	weeks := list(t, h.call(t, ToolGetEnduranceScore, scoresWindowArgs()), "weekly_breakdown")
	if len(weeks) != 2 {
		t.Fatalf("weekly_breakdown holds %d buckets, want two", len(weeks))
	}
	if got, _ := entry(t, weeks, 0)["week_start"].(string); got != "2026-01-19" {
		t.Errorf("the first bucket starts %q, want the older 2026-01-19", got)
	}
}

func TestGetEnduranceScoreSendsTheWeeklyAggregation(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathEnduranceScoreStats,
		testkit.JSON(http.StatusOK, enduranceDocument))
	h := newScoresHarness(t, script)

	h.call(t, ToolGetEnduranceScore, scoresWindowArgs())

	requests := h.fake.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want one", len(requests))
	}
	if got := requests[0].Query.Get(client.QueryAggregation); got != client.AggregationWeekly {
		t.Errorf("aggregation = %q, want %q", got, client.AggregationWeekly)
	}
}

// TestGetEnduranceScoreKeepsAnUnknownClassificationAsACode proves an unmapped code is
// reported rather than given an invented label.
func TestGetEnduranceScoreKeepsAnUnknownClassificationAsACode(t *testing.T) {
	t.Parallel()

	body := `{"enduranceScoreDTO":{"overallScore":100,"classification":42}}`
	script := testkit.NewScript().With(client.PathEnduranceScoreStats,
		testkit.JSON(http.StatusOK, body))
	h := newScoresHarness(t, script)

	result := h.call(t, ToolGetEnduranceScore, scoresWindowArgs())
	if got := number(t, result, "classification_id"); got != 42 {
		t.Errorf("classification_id = %v, want 42", got)
	}
	if _, present := result["classification"]; present {
		t.Error("an unknown classification code was given a label")
	}
}

// TestGetEnduranceScoreCutsAnImplausibleContributorList proves the contributor bound —
// one entry per Garmin activity type — is applied and stated.
func TestGetEnduranceScoreCutsAnImplausibleContributorList(t *testing.T) {
	t.Parallel()

	contributors := make([]string, 0, defaultMaxActivityTypes+2)
	for i := range defaultMaxActivityTypes + 2 {
		contributors = append(contributors,
			`{"activityTypeId":`+strconv.Itoa(i+1)+`,"contribution":0.1}`)
	}
	body := `{"enduranceScoreDTO":{"overallScore":100,"contributors":[` +
		strings.Join(contributors, ",") + `]}}`
	script := testkit.NewScript().With(client.PathEnduranceScoreStats,
		testkit.JSON(http.StatusOK, body))
	h := newScoresHarness(t, script)

	result := h.call(t, ToolGetEnduranceScore, scoresWindowArgs())

	if got := len(list(t, result, "contributors")); got != defaultMaxActivityTypes {
		t.Errorf("contributors holds %d entries, want the bound %d", got, defaultMaxActivityTypes)
	}
	if truncated, _ := result["truncated"].(bool); !truncated {
		t.Error("truncated = false, want true for a cut contributor list")
	}
}

func TestGetEnduranceScoreReportsAQuietWindowAsEmpty(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathEnduranceScoreStats,
		testkit.JSON(http.StatusOK, `{}`))
	h := newScoresHarness(t, script)

	result := h.call(t, ToolGetEnduranceScore, scoresWindowArgs())
	if got := len(list(t, result, "weekly_breakdown")); got != 0 {
		t.Errorf("weekly_breakdown holds %d buckets, want none", got)
	}
	if _, present := result["current_score"]; present {
		t.Error("an empty document produced a current score")
	}
}

// TestEnduranceScoreLogValueReportsShapeOnly proves the log record carries no reading.
func TestEnduranceScoreLogValueReportsShapeOnly(t *testing.T) {
	t.Parallel()

	score := 7250.0
	value := EnduranceScore{
		CurrentScore:    &score,
		Contributors:    []EnduranceContribution{{}},
		WeeklyBreakdown: []EnduranceWeek{{WeekStart: "2026-01-26"}},
	}.LogValue().String()

	if strings.Contains(value, "7250") || strings.Contains(value, "2026-01-26") {
		t.Errorf("the log value %q carries a reading", value)
	}
	if !strings.Contains(value, "enduranceScore") {
		t.Errorf("the log value %q does not name the model", value)
	}
}
