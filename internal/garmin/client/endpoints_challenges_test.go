package client_test

import (
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// TestChallengesConstantsHaveTheirPinnedValues pins every challenges-and-goals
// constant to its literal value: a shape test passes a path that points at the
// wrong service, and 8 tools are built on these. The values come from
// python-garminconnect at the commit docs/upstream-pins.md names, so changing one
// here without changing the pin is the mistake this test exists to make loud.
func TestChallengesConstantsHaveTheirPinnedValues(t *testing.T) {
	t.Parallel()

	paths := []struct {
		got  string
		want string
	}{
		{client.PathEarnedBadges, "/badge-service/badge/earned"},
		{client.PathAdhocChallenges, "/adhocchallenge-service/adHocChallenge/historical"},
		{client.PathBadgeChallenges, "/badgechallenge-service/badgeChallenge/completed"},
		{client.PathAvailableBadgeChallenges, "/badgechallenge-service/badgeChallenge/available"},
		{client.PathNonCompletedBadgeChallenges, "/badgechallenge-service/badgeChallenge/non-completed"},
		{client.PathInProgressVirtualChallenges, "/badgechallenge-service/virtualChallenge/inProgress"},
		{client.PathGoals, "/goal-service/goal/goals"},
		{client.PathRacePredictionsPrefix, "/metrics-service/metrics/racepredictions"},
	}
	for _, tc := range paths {
		if tc.got != tc.want {
			t.Errorf("path = %q, want %q", tc.got, tc.want)
		}
	}

	labels := []struct {
		got  client.Endpoint
		want string
	}{
		{client.EndpointEarnedBadges, "connectapi.badge.earned"},
		{client.EndpointAdhocChallenges, "connectapi.adhocchallenge.historical"},
		{client.EndpointBadgeChallenges, "connectapi.badgechallenge.completed"},
		{client.EndpointAvailableBadgeChallenges, "connectapi.badgechallenge.available"},
		{client.EndpointNonCompletedBadgeChallenges, "connectapi.badgechallenge.non_completed"},
		{client.EndpointInProgressVirtualChallenges, "connectapi.badgechallenge.virtual_in_progress"},
		{client.EndpointGoals, "connectapi.goal.goals"},
		{client.EndpointRacePredictions, "connectapi.metrics.race_predictions"},
	}
	for _, tc := range labels {
		if string(tc.got) != tc.want {
			t.Errorf("endpoint label = %q, want %q", tc.got, tc.want)
		}
	}

	operations := []struct {
		got  client.Op
		want string
	}{
		{client.OpGetEarnedBadges, "get_earned_badges"},
		{client.OpGetAdhocChallenges, "get_adhoc_challenges"},
		{client.OpGetBadgeChallenges, "get_badge_challenges"},
		{client.OpGetAvailableBadgeChallenges, "get_available_badge_challenges"},
		{client.OpGetNonCompletedBadgeChallenges, "get_non_completed_badge_challenges"},
		{client.OpGetInProgressVirtualChallenges, "get_inprogress_virtual_challenges"},
		{client.OpGetGoals, "get_goals"},
		{client.OpGetRacePredictions, "get_race_predictions"},
	}
	for _, tc := range operations {
		if string(tc.got) != tc.want {
			t.Errorf("op = %q, want %q", tc.got, tc.want)
		}
	}

	wireValues := []struct {
		got  string
		want string
	}{
		{client.QueryStatus, "status"},
		{client.GoalSortAscending, "asc"},
	}
	for _, tc := range wireValues {
		if tc.got != tc.want {
			t.Errorf("wire value = %q, want %q", tc.got, tc.want)
		}
	}
}

// TestChallengesPathsAreTemplates keeps every challenges-and-goals path a
// query-free template, so a display name or a range-type token is always
// appended as an escaped segment by the domain client rather than baked into
// the constant.
func TestChallengesPathsAreTemplates(t *testing.T) {
	t.Parallel()

	paths := []string{
		client.PathEarnedBadges, client.PathAdhocChallenges, client.PathBadgeChallenges,
		client.PathAvailableBadgeChallenges, client.PathNonCompletedBadgeChallenges,
		client.PathInProgressVirtualChallenges, client.PathGoals, client.PathRacePredictionsPrefix,
	}
	seen := make(map[string]struct{})
	for _, path := range paths {
		if !strings.HasPrefix(path, "/") {
			t.Errorf("path %q must be host-relative and start with a slash", path)
		}
		if strings.ContainsAny(path, "?&= {}") || strings.HasSuffix(path, "/") {
			t.Errorf("path %q must be a bare template: no query, no placeholder, no trailing slash", path)
		}
		if _, duplicate := seen[path]; duplicate {
			t.Errorf("path %q is declared twice; one endpoint needs exactly one constant", path)
		}
		seen[path] = struct{}{}
	}
}

// TestEveryChallengesEndpointAndOpIsInTheAllowlist is the regression test for a
// dropped entry.
//
// Request.Validate refuses any endpoint or op outside the allowlists, so an
// entry removed from challengesEndpoints or challengesOps makes its tool
// impossible to call while every other test stays green. Counting is not
// enough — a swap would keep the count — so each one is asserted by name.
func TestEveryChallengesEndpointAndOpIsInTheAllowlist(t *testing.T) {
	t.Parallel()

	endpoints := []client.Endpoint{
		client.EndpointEarnedBadges,
		client.EndpointAdhocChallenges,
		client.EndpointBadgeChallenges,
		client.EndpointAvailableBadgeChallenges,
		client.EndpointNonCompletedBadgeChallenges,
		client.EndpointInProgressVirtualChallenges,
		client.EndpointGoals,
		client.EndpointRacePredictions,
	}
	for _, endpoint := range endpoints {
		if !endpoint.IsKnown() {
			t.Errorf("endpoint %q is not in the allowlist, so Request.Validate refuses it", endpoint)
		}
	}
	if got, want := len(endpoints), 8; got != want {
		t.Errorf("%d challenges endpoints asserted, want %d", got, want)
	}

	operations := []client.Op{
		client.OpGetEarnedBadges,
		client.OpGetAdhocChallenges,
		client.OpGetBadgeChallenges,
		client.OpGetAvailableBadgeChallenges,
		client.OpGetNonCompletedBadgeChallenges,
		client.OpGetInProgressVirtualChallenges,
		client.OpGetGoals,
		client.OpGetRacePredictions,
	}
	for _, op := range operations {
		if !op.IsKnown() {
			t.Errorf("op %q is not in the allowlist, so Request.Validate refuses it", op)
		}
		if op.IsCredentialSubmission() {
			t.Errorf("op %q must not be treated as a credential submission", op)
		}
	}
	if got, want := len(operations), 8; got != want {
		t.Errorf("%d challenges ops asserted, want %d", got, want)
	}
}
