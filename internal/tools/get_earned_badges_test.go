package tools

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// earnedBadgesDocument is three earned badges: one with every curated field
// present, one completed challenge badge with a challenge_period and no progress
// (since it carries no target), and one carrying an unrecognized category and
// difficulty code. Every value is invented.
const earnedBadgesDocument = `[` +
	`{"badgeName":"10K Runner","badgeCategoryId":2,"badgeDifficultyId":2,` +
	`"badgePoints":50,"badgeEarnedDate":"2026-03-15T08:00:00.0",` +
	`"badgeProgressValue":10000,"badgeTargetValue":10000,"badgeUnitId":1,` +
	`"badgeAssocType":"activityId","badgeAssocDataId":778899,"badgeSeriesId":12},` +
	`{"badgeName":"Winter Challenge","badgeCategoryId":4,"badgeDifficultyId":3,` +
	`"badgePoints":200,"badgeEarnedDate":"2026-01-10T00:00:00.0",` +
	`"badgeStartDate":"2025-12-01T00:00:00.0","badgeEndDate":"2026-01-31T23:59:59.0"},` +
	`{"badgeName":"Mystery Badge","badgeCategoryId":42,"badgeDifficultyId":9,` +
	`"badgePoints":10,"badgeEarnedDate":"2026-02-01T00:00:00.0"}` +
	`]`

// findBadge returns the one badge whose curated name is name.
func findBadge(t *testing.T, badges []any, name string) map[string]any {
	t.Helper()
	for i := range badges {
		candidate := entry(t, badges, i)
		if got, _ := candidate["name"].(string); got == name {
			return candidate
		}
	}
	t.Fatalf("no badge named %q in the result", name)
	return nil
}

func TestGetEarnedBadgesCuratesEveryField(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathEarnedBadges,
		testkit.JSON(http.StatusOK, earnedBadgesDocument))
	h := newChallengesHarness(t, script, []string{ToolGetEarnedBadges}, registerGetEarnedBadges)

	result := h.call(t, ToolGetEarnedBadges, nil)
	if got := number(t, result, "total_badges"); got != 3 {
		t.Fatalf("total_badges = %v, want 3", got)
	}
	badges := list(t, result, "badges")
	if len(badges) != 3 {
		t.Fatalf("badges holds %d entries, want 3", len(badges))
	}

	runner := findBadge(t, badges, "10K Runner")
	if got, _ := runner["category"].(string); got != challengeLabelRunning {
		t.Errorf("category = %q, want Running for category id 2", got)
	}
	if got, _ := runner["difficulty"].(string); got != "Medium" {
		t.Errorf("difficulty = %q, want Medium for difficulty id 2", got)
	}
	if got := number(t, runner, "points"); got != 50 {
		t.Errorf("points = %v, want 50", got)
	}
	if got, _ := runner["earned_date"].(string); got != "2026-03-15" {
		t.Errorf("earned_date = %q, want the date-only 2026-03-15", got)
	}
	if got, _ := runner["progress"].(string); got != "10.00 km" {
		t.Errorf("progress = %q, want 10.00 km", got)
	}
	if got, _ := runner["target"].(string); got != "10.00 km" {
		t.Errorf("target = %q, want 10.00 km", got)
	}
	if got := number(t, runner, "activity_id"); got != 778899 {
		t.Errorf("activity_id = %v, want 778899", got)
	}
	if got := number(t, runner, "series_id"); got != 12 {
		t.Errorf("series_id = %v, want 12", got)
	}
	if _, present := runner["progress_percent"]; present {
		t.Error("get_earned_badges reported progress_percent, which challenges.py never curates")
	}
}

func TestGetEarnedBadgesReportsAChallengePeriodWithNoProgress(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathEarnedBadges,
		testkit.JSON(http.StatusOK, earnedBadgesDocument))
	h := newChallengesHarness(t, script, []string{ToolGetEarnedBadges}, registerGetEarnedBadges)

	badges := list(t, h.call(t, ToolGetEarnedBadges, nil), "badges")
	winter := findBadge(t, badges, "Winter Challenge")

	if got, _ := winter["challenge_period"].(string); got != "2025-12-01 to 2026-01-31" {
		t.Errorf("challenge_period = %q, want %q", got, "2025-12-01 to 2026-01-31")
	}
	if got, _ := winter["difficulty"].(string); got != "Hard" {
		t.Errorf("difficulty = %q, want Hard for difficulty id 3", got)
	}
	for _, key := range []string{keyProgress, keyTarget, argActivityID, "series_id"} {
		if _, present := winter[key]; present {
			t.Errorf("%s = %v for a badge with no target, want the key absent", key, winter[key])
		}
	}
}

// TestGetEarnedBadgesReportsAnUnrecognizedCategoryAndDifficultyAsCodes proves an
// unmapped id is reported as its own code, matching challenges.py:319-324's
// f"category_{id}" / f"level_{id}" fallback.
func TestGetEarnedBadgesReportsAnUnrecognizedCategoryAndDifficultyAsCodes(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathEarnedBadges,
		testkit.JSON(http.StatusOK, earnedBadgesDocument))
	h := newChallengesHarness(t, script, []string{ToolGetEarnedBadges}, registerGetEarnedBadges)

	badges := list(t, h.call(t, ToolGetEarnedBadges, nil), "badges")
	mystery := findBadge(t, badges, "Mystery Badge")
	if got, _ := mystery["category"].(string); got != "category_42" {
		t.Errorf("category = %q, want the code fallback category_42", got)
	}
	if got, _ := mystery["difficulty"].(string); got != "level_9" {
		t.Errorf("difficulty = %q, want the code fallback level_9", got)
	}
}

// TestGetEarnedBadgesSortsByEarnedDateDescending proves the most recently earned
// badge is returned first, matching challenges.py:353.
func TestGetEarnedBadgesSortsByEarnedDateDescending(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathEarnedBadges,
		testkit.JSON(http.StatusOK, earnedBadgesDocument))
	h := newChallengesHarness(t, script, []string{ToolGetEarnedBadges}, registerGetEarnedBadges)

	badges := list(t, h.call(t, ToolGetEarnedBadges, nil), "badges")
	first, _ := entry(t, badges, 0)["earned_date"].(string)
	if first != "2026-03-15" {
		t.Errorf("the first badge's earned_date = %q, want the most recent 2026-03-15", first)
	}
	last, _ := entry(t, badges, len(badges)-1)["earned_date"].(string)
	if last != "2026-01-10" {
		t.Errorf("the last badge's earned_date = %q, want the oldest 2026-01-10", last)
	}
}

func TestGetEarnedBadgesReportsNoBadgesAsEmpty(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathEarnedBadges,
		testkit.JSON(http.StatusOK, `[]`))
	h := newChallengesHarness(t, script, []string{ToolGetEarnedBadges}, registerGetEarnedBadges)

	result := h.call(t, ToolGetEarnedBadges, nil)
	if got := number(t, result, "total_badges"); got != 0 {
		t.Errorf("total_badges = %v, want 0", got)
	}
	if got := len(list(t, result, "badges")); got != 0 {
		t.Errorf("badges holds %d entries, want none", got)
	}
}

// TestGetEarnedBadgesCapsAtTheServerBound proves a collection larger than
// defaultMaxEarnedBadges is cut to it and reported as truncated: this tool takes no
// pagination argument, so a live account past that bound (486 badges observed on a
// real account, with no limit argument to narrow the call) has no way to ask for a
// narrower slice.
func TestGetEarnedBadgesCapsAtTheServerBound(t *testing.T) {
	t.Parallel()

	var body strings.Builder
	body.WriteByte('[')
	for i := range defaultMaxEarnedBadges + 1 {
		if i > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"badgeName":"Badge %d","badgeEarnedDate":"2026-01-01T00:00:00.0"}`, i)
	}
	body.WriteByte(']')

	script := testkit.NewScript().With(client.PathEarnedBadges,
		testkit.JSON(http.StatusOK, body.String()))
	h := newChallengesHarness(t, script, []string{ToolGetEarnedBadges}, registerGetEarnedBadges)

	result := h.call(t, ToolGetEarnedBadges, nil)
	badges := list(t, result, "badges")
	if len(badges) != defaultMaxEarnedBadges {
		t.Fatalf("badges holds %d entries, want the bound %d", len(badges), defaultMaxEarnedBadges)
	}
	if truncated, _ := result["truncated"].(bool); !truncated {
		t.Error("truncated = false, want true: the collection exceeded the server bound")
	}
}

func TestEarnedBadgeListLogValueReportsShapeOnly(t *testing.T) {
	t.Parallel()

	name := "10K Runner"
	value := EarnedBadgeList{Badges: []EarnedBadge{{Name: &name}}}.LogValue().String()

	if strings.Contains(value, "10K Runner") {
		t.Errorf("the log value %q carries the name", value)
	}
	if !strings.Contains(value, "earnedBadgeList") {
		t.Errorf("the log value %q does not name the model", value)
	}
}
