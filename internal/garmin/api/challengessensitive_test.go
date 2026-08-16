package api_test

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
)

// challengesLogNeedles reports the fixture values a challenges/goals log
// line must never contain: a badge name, a challenge name, a display name, a
// full name and several distinctive figures and dates. A function, not a
// package-level var: AGENTS.md allows no package-level mutable state.
func challengesLogNeedles() []string {
	return []string{
		"Century Rider", "Summer Series", "Team Steps", "Trail Trek",
		fakeDisplayName, fakeFullName,
		"778899", "2026-01-15", "2026-02-10", "14200",
	}
}

// mustDecodeModel decodes body into a zero value of T the way a JSON response
// would, so every fixture below is a literal document rather than a struct
// literal that shares this package's own field spellings.
func mustDecodeModel[T any](t *testing.T, body string) T {
	t.Helper()

	var value T
	if err := json.Unmarshal([]byte(body), &value); err != nil {
		t.Fatalf("json.Unmarshal(%T) = %v", value, err)
	}
	return value
}

// collectChallengesModels builds one fixture of every challenges/goals model
// that carries a name, an identity field or a figure. Every fixture is
// decoded from a literal JSON document quoting the challenges.py line that
// evidences its field spellings, never built from this package's own struct
// tags.
func collectChallengesModels(t *testing.T) map[string]any {
	t.Helper()

	// badge.get("badgeName") (challenges.py:317-318),
	// badge.get("badgeEarnedDate") (challenges.py:326),
	// EarnedBadge's own doc comment for userProfileId/displayName/fullName.
	badge := mustDecodeModel[api.EarnedBadge](t, fmt.Sprintf(
		`{"badgeName":"Century Rider","badgeEarnedDate":"2026-01-15",`+
			`"userProfileId":778899,"displayName":%q,"fullName":%q}`,
		fakeDisplayName, fakeFullName))

	// get_goals's own tool (challenges.py:236-249) reads no individual goal
	// field, so Goal carries none either; this fixture is a bare object with
	// no evidenced spelling to cite, matching Goal's doc comment in
	// challenges.go.
	goal := api.GoalResult{Goals: []api.Goal{json.RawMessage(`{"goalId":1}`)}}

	// predictions.get("timeMarathon") (challenges.py:541-542).
	predictions := mustDecodeModel[api.RacePredictionSet](t, `{"timeMarathon":14200}`)

	badgeChallenge := mustBadgeChallengeItem(t)

	// challenge.get("adHocChallengeName") (challenges.py:384).
	adhocChallenge := mustDecodeModel[api.AdhocChallengeItem](t, `{"adHocChallengeName":"Team Steps"}`)

	// challenge.get("badgeChallengeName") preferred first (challenges.py:579-581).
	virtualChallenge := mustDecodeModel[api.VirtualChallengeItem](t, `{"badgeChallengeName":"Trail Trek"}`)

	return map[string]any{
		"EarnedBadge":          badge,
		"GoalResult":           goal,
		"RacePredictionSet":    predictions,
		"BadgeChallengeItem":   badgeChallenge,
		"BadgeChallengePage":   api.BadgeChallengePage{Challenges: []api.BadgeChallengeItem{badgeChallenge}},
		"AdhocChallengeItem":   adhocChallenge,
		"AdhocChallengePage":   api.AdhocChallengePage{Challenges: []api.AdhocChallengeItem{adhocChallenge}},
		"VirtualChallengeItem": virtualChallenge,
		"VirtualChallengePage": api.VirtualChallengePage{Challenges: []api.VirtualChallengeItem{virtualChallenge}},
	}
}

// mustBadgeChallengeItem decodes a badge-challenge fixture the way a JSON
// response would. Field spellings are cited from _curate_badge_challenge
// (challenges.py:176-207): "badgeChallengeName" (186), "badgePoints" (190),
// "badgeEarnedDate" (203).
func mustBadgeChallengeItem(t *testing.T) api.BadgeChallengeItem {
	t.Helper()

	body := `{"uuid":"badge-uuid-1","badgeChallengeName":"Summer Series","badgePoints":50,` +
		`"badgeEarnedDate":"2026-02-10"}`
	var item api.BadgeChallengeItem
	if err := json.Unmarshal([]byte(body), &item); err != nil {
		t.Fatalf("json.Unmarshal(BadgeChallengeItem) = %v", err)
	}
	return item
}

// TestChallengesModelsAreNotLoggable proves that handing a challenges or goals
// model to slog reports its shape only, never a name, a figure or an identity
// field.
func TestChallengesModelsAreNotLoggable(t *testing.T) {
	t.Parallel()

	for name, value := range collectChallengesModels(t) {
		var logged strings.Builder
		slog.New(slog.NewTextHandler(&logged, leakLogOptions())).Info("garmin read", "model", value)

		rendered := logged.String()
		for _, needle := range challengesLogNeedles() {
			if strings.Contains(rendered, needle) {
				t.Errorf("logging %s leaks %q: %s", name, needle, rendered)
			}
		}
		if !strings.Contains(rendered, "model") {
			t.Errorf("logging %s produced no model group: %s", name, rendered)
		}
	}
}
