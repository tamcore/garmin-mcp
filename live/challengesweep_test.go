//go:build garminlive

package live

import (
	"testing"

	"github.com/tamcore/garmin-mcp/internal/tools"
)

// The badge, goal and challenge half of the read-only sweep. It obeys the same
// contract as surface_test.go and is split off only to stay inside the package's
// 400-line limit. None of these tools takes a day or a window: each reads the
// account's current badge, goal or challenge state as a whole.

// argGoalType is the lifecycle filter get_goals takes.
const argGoalType = "goal_type"

// challengeCalls are the badge, goal and challenge tools.
func challengeCalls() []sweepCall {
	return []sweepCall{
		{tools.ToolGetEarnedBadges, map[string]any{}},
		{tools.ToolGetGoals, map[string]any{argGoalType: "active"}},
		{tools.ToolGetAdhocChallenges, map[string]any{}},
		{tools.ToolGetAvailableBadgeChallenges, map[string]any{}},
		{tools.ToolGetBadgeChallenges, map[string]any{}},
		{tools.ToolGetNonCompletedBadgeChallenges, map[string]any{}},
		{tools.ToolGetRacePredictions, map[string]any{}},
		{tools.ToolGetInProgressVirtualChallenges, map[string]any{}},
	}
}

// TestChallengeToolsAnswerOverTheLiveAccount drives every badge, goal and challenge
// read-only tool against the real account. An empty collection is a pass: the
// account may have earned no badge, joined no challenge or set no goal, and the
// assertions are that a request was dispatched, the answer carries this tool's shape
// and bounds, and nothing leaked.
func TestChallengeToolsAnswerOverTheLiveAccount(t *testing.T) {
	e := liveEnv(t)

	for _, call := range challengeCalls() {
		t.Run(call.tool, func(t *testing.T) { e.assertToolAnswers(t, call) })
	}
}
