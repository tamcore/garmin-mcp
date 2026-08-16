package client

// Challenge, badge and goal paths. Source: python-garminconnect 0.3.10
// garminconnect/__init__.py: each URL is assigned once in GarminConnect.__init__
// (lines 463-480 for the badge and challenge family, 518 for goals, 528 for race
// predictions, of the pinned commit) and read verbatim, with no interpolation
// beyond query parameters, by the method the doc comment below names.
//
// Taxuspt/garmin_mcp's own curation module for these reads,
// src/garmin_mcp/challenges.py at the pinned commit docs/upstream-pins.md
// names, is now available and is cited in api/challenges.go and
// api/challengeslist.go for field spellings and response shapes. It never
// constructs or overrides a path itself — every URL it reads comes from
// calling the garminconnect method named below — so this file's own citations
// still name garminconnect/__init__.py alone.
const (
	// PathEarnedBadges lists the badges the account has earned. It takes no
	// query parameter. Source: garmin_connect_earned_badges_url
	// (__init__.py:463), read verbatim by get_earned_badges (__init__.py:1769),
	// which forwards it to connectapi with no params.
	PathEarnedBadges = "/badge-service/badge/earned"

	// PathAdhocChallenges lists user-created social challenges, filtered by
	// start and limit. Source: garmin_connect_adhoc_challenges_url
	// (__init__.py:465), read by get_adhoc_challenges (__init__.py:1814).
	PathAdhocChallenges = "/adhocchallenge-service/adHocChallenge/historical"

	// PathBadgeChallenges lists the badge challenges the account has joined.
	// Source: garmin_connect_badge_challenges_url (__init__.py:468), read by
	// get_badge_challenges (__init__.py:1824). Upstream's method name speaks of
	// badge challenges in general; the URL it reads names the "completed"
	// collection. Both are cited as evidenced, without reconciling the naming.
	PathBadgeChallenges = "/badgechallenge-service/badgeChallenge/completed"

	// PathAvailableBadgeChallenges lists the official challenges open to join.
	// Source: garmin_connect_available_badge_challenges_url (__init__.py:471),
	// read by get_available_badge_challenges (__init__.py:1834).
	PathAvailableBadgeChallenges = "/badgechallenge-service/badgeChallenge/available"

	// PathNonCompletedBadgeChallenges lists badge challenges joined but not yet
	// completed. Source: garmin_connect_non_completed_badge_challenges_url
	// (__init__.py:474), read by get_non_completed_badge_challenges
	// (__init__.py:1844).
	PathNonCompletedBadgeChallenges = "/badgechallenge-service/badgeChallenge/non-completed"

	// PathInProgressVirtualChallenges lists virtual/expedition challenges in
	// progress. Source: garmin_connect_inprogress_virtual_challenges_url
	// (__init__.py:477), read by get_inprogress_virtual_challenges
	// (__init__.py:1856), whose start validates as strictly positive
	// (_validate_positive_integer, not the non-negative check every other
	// listing here uses).
	PathInProgressVirtualChallenges = "/badgechallenge-service/virtualChallenge/inProgress"

	// PathGoals lists the account's goals, filtered by status, start and limit.
	// Source: garmin_connect_goals_url (__init__.py:518), read by get_goals
	// (__init__.py:2709).
	PathGoals = "/goal-service/goal/goals"

	// PathRacePredictionsPrefix precedes "/latest" or a range-type token, then the
	// escaped display name, in the race-prediction path.
	// Source: garmin_connect_race_predictor_url (__init__.py:528), read by
	// get_race_predictions (__init__.py:2158).
	PathRacePredictionsPrefix = "/metrics-service/metrics/racepredictions"
)

// QueryStatus filters get_goals by lifecycle status. Source: the params dict of
// get_goals.
const QueryStatus = "status"

// GoalSortAscending is the fixed sortOrder value get_goals always sends.
// Source: the params dict of get_goals, which hardcodes sortOrder to "asc".
const GoalSortAscending = "asc"

// Sanitized endpoint labels for the challenges-and-goals tier. They never
// contain a host, a credential or a query string.
const (
	EndpointEarnedBadges                = Endpoint("connectapi.badge.earned")
	EndpointAdhocChallenges             = Endpoint("connectapi.adhocchallenge.historical")
	EndpointBadgeChallenges             = Endpoint("connectapi.badgechallenge.completed")
	EndpointAvailableBadgeChallenges    = Endpoint("connectapi.badgechallenge.available")
	EndpointNonCompletedBadgeChallenges = Endpoint("connectapi.badgechallenge.non_completed")
	EndpointInProgressVirtualChallenges = Endpoint("connectapi.badgechallenge.virtual_in_progress")
	EndpointGoals                       = Endpoint("connectapi.goal.goals")
	EndpointRacePredictions             = Endpoint("connectapi.metrics.race_predictions")
)

// challengesEndpoints returns the challenges-and-goals labels. A function, not a
// var: AGENTS.md allows no package-level mutable state.
func challengesEndpoints() []Endpoint {
	return []Endpoint{
		EndpointEarnedBadges,
		EndpointAdhocChallenges,
		EndpointBadgeChallenges,
		EndpointAvailableBadgeChallenges,
		EndpointNonCompletedBadgeChallenges,
		EndpointInProgressVirtualChallenges,
		EndpointGoals,
		EndpointRacePredictions,
	}
}

// Sanitized operation labels, one per tool.
const (
	OpGetEarnedBadges                = Op("get_earned_badges")
	OpGetAdhocChallenges             = Op("get_adhoc_challenges")
	OpGetBadgeChallenges             = Op("get_badge_challenges")
	OpGetAvailableBadgeChallenges    = Op("get_available_badge_challenges")
	OpGetNonCompletedBadgeChallenges = Op("get_non_completed_badge_challenges")
	OpGetInProgressVirtualChallenges = Op("get_inprogress_virtual_challenges")
	OpGetGoals                       = Op("get_goals")
	OpGetRacePredictions             = Op("get_race_predictions")
)

// challengesOps returns the challenges-and-goals operations. A function for the
// same reason.
func challengesOps() []Op {
	return []Op{
		OpGetEarnedBadges,
		OpGetAdhocChallenges,
		OpGetBadgeChallenges,
		OpGetAvailableBadgeChallenges,
		OpGetNonCompletedBadgeChallenges,
		OpGetInProgressVirtualChallenges,
		OpGetGoals,
		OpGetRacePredictions,
	}
}
