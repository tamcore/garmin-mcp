package api_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// Every fixture here is synthetic, shaped after the real spellings evidenced by
// Taxuspt/garmin_mcp's src/garmin_mcp/challenges.py. No fixture is a recording
// of a real account.

// adhocChallengesBareArray is a bare-array response using the socialChallenge*
// and adHocChallenge* vocabulary challenges.py:362-409 reads.
const adhocChallengesBareArray = `[{"adHocChallengeName":"Team Steps","adHocChallengeDesc":"Weekly step-off",` +
	`"uuid":"adhoc-uuid-1","socialChallengeActivityTypeId":4,"socialChallengeStatusId":2,` +
	`"startDate":"2026-01-01","endDate":"2026-01-31","userRanking":3,"playerCount":12}]`

// badgeChallengesBareArray is a bare-array response using the badgeChallenge*
// vocabulary _curate_badge_challenge (challenges.py:176-207) reads.
const badgeChallengesBareArray = `[{"uuid":"badge-uuid-1","badgeChallengeName":"Summer Series",` +
	`"challengeCategoryId":1,"badgeChallengeStatusId":3,"badgeUnitId":1,` +
	`"badgeProgressValue":40,"badgeTargetValue":100,"badgePoints":50,` +
	`"startDate":"2026-02-01","endDate":"2026-02-28","userJoined":true,` +
	`"badgeEarnedDate":"2026-02-10"}]`

func newTestChallengesPage(t *testing.T) client.Page {
	t.Helper()

	page, err := client.NewPage(0, 20)
	if err != nil {
		t.Fatalf("NewPage() = %v", err)
	}
	return page
}

func TestChallengesAdhocChallengesDecodesTheEvidencedSpellings(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathAdhocChallenges,
		testkit.JSON(http.StatusOK, adhocChallengesBareArray))
	h := newHarness(t, script, client.Limits{})

	got, err := newChallenges(t, h).AdhocChallenges(t.Context(), h.session, newTestChallengesPage(t))
	if err != nil {
		t.Fatalf("AdhocChallenges() = %v", err)
	}
	if len(got.Challenges) != 1 {
		t.Fatalf("AdhocChallenges() returned %d challenges, want 1", len(got.Challenges))
	}
	item := got.Challenges[0]
	if title, ok := item.Title(); !ok || title != "Team Steps" {
		t.Errorf("Title() = %q (%t), want %q", title, ok, "Team Steps")
	}
	if got, ok := item.UserRanking.Int64(); !ok || got != 3 {
		t.Errorf("userRanking = %v (%t), want 3", got, ok)
	}
	if got, ok := item.PlayerCount.Int64(); !ok || got != 12 {
		t.Errorf("playerCount = %v (%t), want 12", got, ok)
	}
	if got.Payload().Len() == 0 {
		t.Error("AdhocChallenges() retained no payload")
	}

	requests := h.server.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want 1", len(requests))
	}
	if got := requests[0].Query.Get(client.QueryStart); got != "0" {
		t.Errorf("start = %q, want 0", got)
	}
	if got := requests[0].Query.Get(client.QueryLimit); got != "20" {
		t.Errorf("limit = %q, want 20", got)
	}
}

func TestChallengesBadgeChallengesDecodesTheEvidencedSpellings(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathBadgeChallenges,
		testkit.JSON(http.StatusOK, badgeChallengesBareArray))
	h := newHarness(t, script, client.Limits{})

	got, err := newChallenges(t, h).BadgeChallenges(t.Context(), h.session, newTestChallengesPage(t))
	if err != nil {
		t.Fatalf("BadgeChallenges() = %v", err)
	}
	if len(got.Challenges) != 1 {
		t.Fatalf("BadgeChallenges() returned %d challenges, want 1", len(got.Challenges))
	}
	item := got.Challenges[0]
	if title, ok := item.Title(); !ok || title != "Summer Series" {
		t.Errorf("Title() = %q (%t), want %q", title, ok, "Summer Series")
	}
	if got, ok := item.Points.Float64(); !ok || got != 50 {
		t.Errorf("points = %v (%t), want 50", got, ok)
	}
	if got, ok := item.CategoryID.Int64(); !ok || got != 1 {
		t.Errorf("categoryId = %v (%t), want 1", got, ok)
	}
	if got, ok := item.StatusID.Int64(); !ok || got != 3 {
		t.Errorf("statusId = %v (%t), want 3", got, ok)
	}
	if item.UserJoined == nil || !*item.UserJoined {
		t.Errorf("userJoined = %v, want true", item.UserJoined)
	}
}

func TestChallengesAvailableBadgeChallengesSendsThePage(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathAvailableBadgeChallenges,
		testkit.JSON(http.StatusOK, "[]"))
	h := newHarness(t, script, client.Limits{})

	page, err := client.NewPage(5, 15)
	if err != nil {
		t.Fatalf("NewPage() = %v", err)
	}
	if _, err := newChallenges(t, h).AvailableBadgeChallenges(t.Context(), h.session, page); err != nil {
		t.Fatalf("AvailableBadgeChallenges() = %v", err)
	}

	requests := h.server.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want 1", len(requests))
	}
	if got := requests[0].Query.Get(client.QueryStart); got != "5" {
		t.Errorf("start = %q, want 5", got)
	}
	if got := requests[0].Query.Get(client.QueryLimit); got != "15" {
		t.Errorf("limit = %q, want 15", got)
	}
}

func TestChallengesAvailableBadgeChallengesDecodesJoinable(t *testing.T) {
	t.Parallel()

	body := `[{"uuid":"badge-uuid-2","badgeChallengeName":"Autumn Series","joinable":false}]`
	script := testkit.NewScript().With(client.PathAvailableBadgeChallenges, testkit.JSON(http.StatusOK, body))
	h := newHarness(t, script, client.Limits{})

	got, err := newChallenges(t, h).AvailableBadgeChallenges(t.Context(), h.session, newTestChallengesPage(t))
	if err != nil {
		t.Fatalf("AvailableBadgeChallenges() = %v", err)
	}
	if len(got.Challenges) != 1 {
		t.Fatalf("AvailableBadgeChallenges() returned %d challenges, want 1", len(got.Challenges))
	}
	if item := got.Challenges[0]; item.Joinable == nil || *item.Joinable {
		t.Errorf("joinable = %v, want false", item.Joinable)
	}
}

func TestChallengesNonCompletedBadgeChallengesValidatesThePage(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{MaxPageSize: 5})

	page, err := client.NewPage(0, 20)
	if err != nil {
		t.Fatalf("NewPage() = %v", err)
	}
	_, err = newChallenges(t, h).NonCompletedBadgeChallenges(t.Context(), h.session, page)
	if !errors.Is(err, client.ErrValidation) {
		t.Errorf("NonCompletedBadgeChallenges(oversized page) = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want none", got)
	}
}

func TestChallengesInProgressVirtualChallengesRefusesAZeroStart(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})

	page, err := client.NewPage(0, 20)
	if err != nil {
		t.Fatalf("NewPage() = %v", err)
	}
	_, err = newChallenges(t, h).InProgressVirtualChallenges(t.Context(), h.session, page)
	if !errors.Is(err, client.ErrValidation) {
		t.Errorf("InProgressVirtualChallenges(start=0) = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want none", got)
	}
}

// TestChallengesBareArrayFamiliesToleratesNullAndEmptyObject asserts the two
// shapes challenges.py's own iteration tolerates without raising: a null (or
// empty) body, and an empty object. `for challenge in {}:` iterates zero
// times with no exception, so an empty dict yields the same empty result a
// null response does.
func TestChallengesBareArrayFamiliesToleratesNullAndEmptyObject(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{"null body", nullBody},
		{"empty object", "{}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := testkit.NewScript().With(client.PathAdhocChallenges, testkit.JSON(http.StatusOK, tc.body))
			h := newHarness(t, script, client.Limits{})

			got, err := newChallenges(t, h).AdhocChallenges(t.Context(), h.session, newTestChallengesPage(t))
			if err != nil {
				t.Fatalf("AdhocChallenges() = %v", err)
			}
			if len(got.Challenges) != 0 {
				t.Errorf("AdhocChallenges() returned %d challenges, want 0", len(got.Challenges))
			}
		})
	}
}

// TestChallengesBareArrayFamiliesRejectsANonEmptyObject is the regression test
// for the defect a review found: the decoder used to treat any object it
// could not place — including a non-empty one — as an empty page, masking a
// real shape mismatch behind an indistinguishable "no challenges" result.
//
// A non-empty object is not a shape challenges.py's own curation tolerates
// either: `for challenge in challenges` over a non-empty dict yields its
// string keys, and `challenge.get(...)` on a string raises, which the
// shipped tool's own try/except turns into a reported error. This test
// asserts the same outcome here: an error, not a silent empty page.
func TestChallengesBareArrayFamiliesRejectsANonEmptyObject(t *testing.T) {
	t.Parallel()

	body := `{"total":0,"message":"none"}`
	script := testkit.NewScript().With(client.PathAdhocChallenges, testkit.JSON(http.StatusOK, body))
	h := newHarness(t, script, client.Limits{})

	_, err := newChallenges(t, h).AdhocChallenges(t.Context(), h.session, newTestChallengesPage(t))
	if err == nil {
		t.Fatal("AdhocChallenges() = nil error, want an error for the unrecognized wrapped shape")
	}
}

func TestChallengesInProgressVirtualChallengesAcceptsAPositiveStart(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathInProgressVirtualChallenges,
		testkit.JSON(http.StatusOK, "[]"))
	h := newHarness(t, script, client.Limits{})

	page, err := client.NewPage(1, 20)
	if err != nil {
		t.Fatalf("NewPage() = %v", err)
	}
	if _, err := newChallenges(t, h).InProgressVirtualChallenges(t.Context(), h.session, page); err != nil {
		t.Fatalf("InProgressVirtualChallenges() = %v", err)
	}
	if got := len(h.server.Requests()); got != 1 {
		t.Errorf("the fake received %d requests, want 1", got)
	}
}

// TestChallengesInProgressVirtualChallengesDecodesAWrappedObject exercises the
// "challenges" key challenges.py:570-574 reads, the one shape none of the other
// four families is evidenced to receive.
func TestChallengesInProgressVirtualChallengesDecodesAWrappedObject(t *testing.T) {
	t.Parallel()

	body := `{"challenges":[{"badgeChallengeName":"Trail Trek","uuid":"virtual-uuid-1",` +
		`"startDate":"2026-03-01","endDate":"2026-03-31","badgeProgressValue":250000,` +
		`"badgeTargetValue":500000,"badgeUnitId":1}]}`
	script := testkit.NewScript().With(client.PathInProgressVirtualChallenges, testkit.JSON(http.StatusOK, body))
	h := newHarness(t, script, client.Limits{})

	page, err := client.NewPage(1, 20)
	if err != nil {
		t.Fatalf("NewPage() = %v", err)
	}
	got, err := newChallenges(t, h).InProgressVirtualChallenges(t.Context(), h.session, page)
	if err != nil {
		t.Fatalf("InProgressVirtualChallenges() = %v", err)
	}
	if len(got.Challenges) != 1 {
		t.Fatalf("InProgressVirtualChallenges() returned %d challenges, want 1", len(got.Challenges))
	}
	item := got.Challenges[0]
	if title, ok := item.Title(); !ok || title != "Trail Trek" {
		t.Errorf("Title() = %q (%t), want %q", title, ok, "Trail Trek")
	}
	if progress, ok := item.Progress(); !ok || progress != 250000 {
		t.Errorf("Progress() = %v (%t), want 250000", progress, ok)
	}
	if target, ok := item.Target(); !ok || target != 500000 {
		t.Errorf("Target() = %v (%t), want 500000", target, ok)
	}
}

// TestChallengesInProgressVirtualChallengesFallsBackToTheWholeObject ports
// challenges.py's own fallback: a dict response with no "challenges" key
// becomes a single one-item list, per challenges.get("challenges",
// [challenges]).
func TestChallengesInProgressVirtualChallengesFallsBackToTheWholeObject(t *testing.T) {
	t.Parallel()

	body := `{"name":"Solo Expedition","uuid":"virtual-uuid-2","progressValue":10,"targetValue":20}`
	script := testkit.NewScript().With(client.PathInProgressVirtualChallenges, testkit.JSON(http.StatusOK, body))
	h := newHarness(t, script, client.Limits{})

	page, err := client.NewPage(1, 20)
	if err != nil {
		t.Fatalf("NewPage() = %v", err)
	}
	got, err := newChallenges(t, h).InProgressVirtualChallenges(t.Context(), h.session, page)
	if err != nil {
		t.Fatalf("InProgressVirtualChallenges() = %v", err)
	}
	if len(got.Challenges) != 1 {
		t.Fatalf("InProgressVirtualChallenges() returned %d challenges, want 1", len(got.Challenges))
	}
	item := got.Challenges[0]
	if title, ok := item.Title(); !ok || title != "Solo Expedition" {
		t.Errorf("Title() = %q (%t), want %q", title, ok, "Solo Expedition")
	}
	if progress, ok := item.Progress(); !ok || progress != 10 {
		t.Errorf("Progress() = %v (%t), want 10", progress, ok)
	}
}
