//go:build garminlive

package live

import "github.com/tamcore/garmin-mcp/internal/tools"

// The badge, goal and challenge half of the shape table. Split from shapes_test.go
// only to stay inside the package's 400-line limit; the contract is the one stated
// there.

// keyChallenges is the result key every challenge-list tool in this half of the
// shape table shares.
const keyChallenges = "challenges"

// challengeShapes names, per badge, goal or challenge tool, the result keys its
// answer always carries.
//
// get_race_predictions declares only "predictions": the nested struct itself is
// always present (internal/tools/get_race_predictions.go's RacePredictions), but
// every distance inside it, and prediction_date, is optional — Garmin omits a
// distance it has not modeled for the account.
func challengeShapes() map[string][]string {
	challenges := []string{keyChallenges, "total"}
	return map[string][]string{
		tools.ToolGetEarnedBadges:                {"badges", "total_badges"},
		tools.ToolGetGoals:                       {argGoalType, "goals", keyCount, keyTruncated, "dropped_fields"},
		tools.ToolGetAdhocChallenges:             challenges,
		tools.ToolGetAvailableBadgeChallenges:    challenges,
		tools.ToolGetBadgeChallenges:             challenges,
		tools.ToolGetNonCompletedBadgeChallenges: challenges,
		tools.ToolGetRacePredictions:             {"predictions"},
		tools.ToolGetInProgressVirtualChallenges: challenges,
	}
}
