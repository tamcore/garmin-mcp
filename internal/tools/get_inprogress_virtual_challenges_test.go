package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// virtualChallengeDistanceDocument is one in-progress virtual challenge with no unit
// id, exercising the meters/km branch (challenges.py:602-608). Every value is
// invented.
const virtualChallengeDistanceDocument = `[{"badgeChallengeName":"Kilimanjaro Trek",` +
	`"uuid":"virt-0001","startDate":"2026-01-01T00:00:00.0","endDate":"2026-06-30T23:59:59.0",` +
	`"badgeProgressValue":42000,"badgeTargetValue":100000}]`

// virtualChallengeNonDistanceDocument carries a non-distance unit id (7, time),
// exercising the formatBadgeValue branch (challenges.py:610-611).
const virtualChallengeNonDistanceDocument = `[{"name":"Endurance Hours",` +
	`"uuid":"virt-0002","startDate":"2026-02-01T00:00:00.0","endDate":"2026-08-31T23:59:59.0",` +
	`"progress":3600,"target":36000,"badgeUnitId":7}]`

func TestGetInProgressVirtualChallengesReportsDistanceInMetersAndKM(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathInProgressVirtualChallenges,
		testkit.JSON(http.StatusOK, virtualChallengeDistanceDocument))
	h := newChallengesHarness(t, script, []string{ToolGetInProgressVirtualChallenges},
		registerGetInProgressVirtualChallenges)

	challenge := entry(t, list(t, h.call(t, ToolGetInProgressVirtualChallenges, nil),
		"challenges"), 0)

	if got, _ := challenge["name"].(string); got != "Kilimanjaro Trek" {
		t.Errorf("name = %q, want %q", got, "Kilimanjaro Trek")
	}
	if got := number(t, challenge, "progress_meters"); got != 42000 {
		t.Errorf("progress_meters = %v, want 42000", got)
	}
	if got := number(t, challenge, "target_meters"); got != 100000 {
		t.Errorf("target_meters = %v, want 100000", got)
	}
	if got, _ := challenge["progress_km"].(string); got != "42.00 km" {
		t.Errorf("progress_km = %q, want 42.00 km", got)
	}
	if got, _ := challenge["target_km"].(string); got != "100.00 km" {
		t.Errorf("target_km = %q, want 100.00 km", got)
	}
	if got, _ := challenge["progress_percent"].(string); got != "42.0%" {
		t.Errorf("progress_percent = %q, want 42.0%%", got)
	}
	for _, key := range []string{keyProgress, keyTarget} {
		if _, present := challenge[key]; present {
			t.Errorf("%s = %v for a distance challenge, want the meters/km fields only",
				key, challenge[key])
		}
	}
}

// TestGetInProgressVirtualChallengesReportsANonDistanceUnitFormatted proves a
// non-distance unit id takes the formatBadgeValue branch instead of meters/km,
// matching challenges.py:609-611, and that the name falls back to the plain "name"
// field when badgeChallengeName is absent (challenges.py:579-581).
func TestGetInProgressVirtualChallengesReportsANonDistanceUnitFormatted(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathInProgressVirtualChallenges,
		testkit.JSON(http.StatusOK, virtualChallengeNonDistanceDocument))
	h := newChallengesHarness(t, script, []string{ToolGetInProgressVirtualChallenges},
		registerGetInProgressVirtualChallenges)

	challenge := entry(t, list(t, h.call(t, ToolGetInProgressVirtualChallenges, nil),
		"challenges"), 0)

	if got, _ := challenge["name"].(string); got != "Endurance Hours" {
		t.Errorf("name = %q, want the plain-name fallback %q", got, "Endurance Hours")
	}
	if got, _ := challenge["progress"].(string); got != "1:00:00" {
		t.Errorf("progress = %q, want 1:00:00 for 3600 seconds", got)
	}
	if got, _ := challenge["target"].(string); got != "10:00:00" {
		t.Errorf("target = %q, want 10:00:00 for 36000 seconds", got)
	}
	for _, key := range []string{"progress_meters", "target_meters", "progress_km", "target_km"} {
		if _, present := challenge[key]; present {
			t.Errorf("%s = %v for a non-distance challenge, want the formatted fields only",
				key, challenge[key])
		}
	}
}

func TestGetInProgressVirtualChallengesUsesTheManifestDefaults(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathInProgressVirtualChallenges,
		testkit.JSON(http.StatusOK, `[]`))
	h := newChallengesHarness(t, script, []string{ToolGetInProgressVirtualChallenges},
		registerGetInProgressVirtualChallenges)

	h.call(t, ToolGetInProgressVirtualChallenges, nil)

	requests := h.fake.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want one", len(requests))
	}
	if got := requests[0].Query.Get(client.QueryStart); got != "1" {
		t.Errorf("start = %q, want the manifest default 1", got)
	}
}

// TestGetInProgressVirtualChallengesRejectsAZeroStart proves the start-at-1 minimum
// this endpoint alone requires (internal/garmin/api's own InProgressVirtualChallenges
// refuses 0), matching garminconnect 0.3.2's rejection of a zero start here.
func TestGetInProgressVirtualChallengesRejectsAZeroStart(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathInProgressVirtualChallenges,
		testkit.JSON(http.StatusOK, `[]`))
	h := newChallengesHarness(t, script, []string{ToolGetInProgressVirtualChallenges},
		registerGetInProgressVirtualChallenges)

	advice := h.callError(t, ToolGetInProgressVirtualChallenges, map[string]any{argStart: 0})
	assertNoRawPayload(t, advice)
	if len(h.fake.Requests()) != 0 {
		t.Error("a zero start reached the fake Garmin service")
	}
}

// TestGetInProgressVirtualChallengesSchemaMinimumMatchesTheHandler drives the
// start property's own advertised Minimum through the handler, one below it,
// so the manifest's schema and resolveChallengePage's minStart cannot drift
// apart again the way they did before: the schema declared Minimum 0 while the
// handler refused start 0.
func TestGetInProgressVirtualChallengesSchemaMinimumMatchesTheHandler(t *testing.T) {
	t.Parallel()

	properties := getInProgressVirtualChallengesContract().Schema.Properties()
	var minimum *float64
	for _, property := range properties {
		if property.Name == argStart {
			minimum = property.Minimum
		}
	}
	if minimum == nil {
		t.Fatal("the start property declares no minimum")
	}

	script := testkit.NewScript().With(client.PathInProgressVirtualChallenges,
		testkit.JSON(http.StatusOK, `[]`))
	h := newChallengesHarness(t, script, []string{ToolGetInProgressVirtualChallenges},
		registerGetInProgressVirtualChallenges)

	belowMinimum := int(*minimum) - 1
	advice := h.callError(t, ToolGetInProgressVirtualChallenges, map[string]any{argStart: belowMinimum})
	assertNoRawPayload(t, advice)
	if len(h.fake.Requests()) != 0 {
		t.Error("a start below the schema's own minimum reached the fake Garmin service")
	}
}

func TestGetInProgressVirtualChallengesReportsNoChallengesAsEmpty(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathInProgressVirtualChallenges,
		testkit.JSON(http.StatusOK, `[]`))
	h := newChallengesHarness(t, script, []string{ToolGetInProgressVirtualChallenges},
		registerGetInProgressVirtualChallenges)

	result := h.call(t, ToolGetInProgressVirtualChallenges, nil)
	if got := number(t, result, "total"); got != 0 {
		t.Errorf("total = %v, want 0", got)
	}
	if got := len(list(t, result, "challenges")); got != 0 {
		t.Errorf("challenges holds %d entries, want none", got)
	}
}

// TestGetInProgressVirtualChallengesCapsAtTheRequestedLimit proves a Garmin
// response carrying more challenges than the requested limit is cut to it and
// reported as truncated, since Garmin does not reliably honor the limit it is
// asked for.
func TestGetInProgressVirtualChallengesCapsAtTheRequestedLimit(t *testing.T) {
	t.Parallel()

	body := `[` +
		`{"badgeChallengeName":"First","uuid":"virt-a"},` +
		`{"badgeChallengeName":"Second","uuid":"virt-b"}` +
		`]`
	script := testkit.NewScript().With(client.PathInProgressVirtualChallenges,
		testkit.JSON(http.StatusOK, body))
	h := newChallengesHarness(t, script, []string{ToolGetInProgressVirtualChallenges},
		registerGetInProgressVirtualChallenges)

	result := h.call(t, ToolGetInProgressVirtualChallenges, map[string]any{argLimit: 1})
	challenges := list(t, result, "challenges")
	if len(challenges) != 1 {
		t.Fatalf("challenges holds %d entries, want 1: the response should be cut to the limit", len(challenges))
	}
	if truncated, _ := result["truncated"].(bool); !truncated {
		t.Error("truncated = false, want true: Garmin returned more than the requested limit")
	}
}

func TestVirtualChallengeListLogValueReportsShapeOnly(t *testing.T) {
	t.Parallel()

	name := "Kilimanjaro Trek"
	value := VirtualChallengeList{Challenges: []VirtualChallenge{
		{Name: &name},
	}}.LogValue().String()

	if strings.Contains(value, "Kilimanjaro Trek") {
		t.Errorf("the log value %q carries the name", value)
	}
	if !strings.Contains(value, "virtualChallengeList") {
		t.Errorf("the log value %q does not name the model", value)
	}
}
