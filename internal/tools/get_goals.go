package tools

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetGoals is the upstream compatibility name of the goal-list read.
//
// Source: Taxuspt/garmin_mcp's get_goals tool (challenges.py:236-249), which applies
// no curation at all: `json.dumps(goals, indent=2)` on the raw result. That is why
// internal/garmin/api's Goal is kept as json.RawMessage rather than a typed struct
// (see its doc comment in challenges.go) and why this tool passes each goal through
// as a sanitized, structured document rather than typed fields: no pinned source
// names a single goal field. The document is emitted as structured JSON, not as a
// JSON-encoded string, so the live leak sweep's decoded-key walk can see into it the
// same way it sees every other field this package returns.
const ToolGetGoals = "get_goals"

// defaultMaxGoalListBytes bounds the total sanitized size of one goal-list result.
// A goal document is small, so this budget comfortably covers maxGoalWalkItems
// (internal/garmin/api's own accumulation bound) worth of them; a call that still
// exceeds it is refused rather than cut, for the same reason
// get_lifestyle_logging_data refuses rather than cuts: half a JSON document is not a
// JSON document.
const defaultMaxGoalListBytes = 512 << 10

// goalTypeEnum is the exact three lifecycle filters get_goals accepts. Source: the
// valid_statuses set of get_goals (challenges.py), checked by exact string equality.
func goalTypeEnum() []any { return []any{valueActive, valueFuture, "past"} }

// A Goal is one goal document, kept as sanitized structured JSON under Garmin's own
// field names because no pinned source names a single field inside it. It is health
// data and identity material together: never log it.
type Goal struct {
	Document any `json:"document" jsonschema:"one goal, as Garmin returns it, sanitized"`
}

// A GoalList is the bounded goal collection matching one lifecycle filter.
type GoalList struct {
	GoalType string `json:"goal_type" jsonschema:"the lifecycle filter that was read"`
	Goals    []Goal `json:"goals" jsonschema:"the goals matching goal_type"`
	Count    int    `json:"count" jsonschema:"how many goals this result carries"`

	// Truncated is set both when internal/garmin/api's own accumulation bound was
	// reached and when this tool stopped early to stay inside its byte budget.
	Truncated bool `json:"truncated" jsonschema:"whether this result was cut at a server bound"`

	// DroppedFields is how many identifying keys were removed across every goal
	// document before it was returned. It is a count and never a list of names: see
	// sanitizeUntyped for why naming them would disclose what removing them hid.
	DroppedFields int `json:"dropped_fields" jsonschema:"identifying keys removed across every goal document"`
}

// LogValue reports the goal count and never a goal's own content.
func (l GoalList) LogValue() slog.Value {
	return shape("goalList",
		slog.String("goalType", l.GoalType),
		slog.Int("goals", l.Count),
		slog.Bool("truncated", l.Truncated),
	)
}

// getGoalsInput is the strict argument set: an optional lifecycle filter.
type getGoalsInput struct {
	GoalType string `json:"goal_type,omitempty" jsonschema:"the lifecycle filter, active, future or past"`
}

func getGoalsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetGoals,
			Title: "Get goals",
			Description: "read the account's active, future or past goals. Each goal is " +
				"returned as JSON text under Garmin's own field names, because no field " +
				"in it is pinned",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(Property{
			Name:        argGoalType,
			Types:       []string{typeString},
			Description: "which goals to read: active, future or past",
			Enum:        goalTypeEnum(),
			Default:     valueActive,
		}),
	}
}

func registerGetGoals(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getGoalsInput) (
		*mcp.CallToolResult, GoalList, error,
	) {
		goalType := in.GoalType
		if goalType == "" {
			goalType = valueActive
		}
		status, err := api.ParseGoalStatus(goalType)
		if err != nil {
			return nil, GoalList{}, fail(err)
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, GoalList{}, err
		}
		result, err := svc.challenges.Goals(ctx, session, status)
		if err != nil {
			return nil, GoalList{}, fail(err)
		}
		out, err := newGoalList(goalType, result)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getGoalsContract().Registration(), handler)
}

// newGoalList sanitizes every goal document and bounds the total, refusing rather
// than cutting when the budget is exceeded.
func newGoalList(goalType string, result api.GoalResult) (GoalList, error) {
	goals := make([]Goal, 0, len(result.Goals))
	totalBytes, droppedTotal := 0, 0

	for _, raw := range result.Goals {
		document, dropped, err := sanitizedGoalDocument(raw)
		if err != nil {
			return GoalList{}, err
		}
		encoded, err := json.Marshal(document)
		if err != nil {
			return GoalList{}, tooLarge("a goal in this list cannot be rendered")
		}
		totalBytes += len(encoded)
		if totalBytes > defaultMaxGoalListBytes {
			return GoalList{}, tooLarge("the goal list is larger than " +
				strconv.Itoa(defaultMaxGoalListBytes) +
				" bytes; ask for a narrower goal_type")
		}
		droppedTotal += dropped
		goals = append(goals, Goal{Document: document})
	}

	return GoalList{
		GoalType:      goalType,
		Goals:         goals,
		Count:         len(goals),
		Truncated:     result.Truncated,
		DroppedFields: droppedTotal,
	}, nil
}

// sanitizedGoalDocument strips the identifying keys out of one raw goal document and
// returns the sanitized structure, refusing a document this server cannot walk whole
// or read as JSON.
func sanitizedGoalDocument(raw json.RawMessage) (any, int, error) {
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, 0, tooLarge("a goal in this list is not readable JSON")
	}

	outcome := sanitizeUntyped(decoded)
	if outcome.Truncated {
		return nil, 0, tooLarge(
			"a goal in this list is nested deeper than " + strconv.Itoa(maxSanitizeDepth) +
				" levels or holds more than " + strconv.Itoa(maxSanitizeNodes) + " values")
	}

	return outcome.Value, outcome.Dropped, nil
}
