package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// The upstream compatibility names of the workout deletes.
const (
	ToolDeleteWorkout  = "delete_workout"
	ToolDeleteWorkouts = "delete_workouts"
)

// identifierListProperty declares a bounded list of positive identifiers.
func identifierListProperty(name, description string) Property {
	return Property{
		Name:        name,
		Types:       []string{typeArray},
		Description: description,
		Items:       map[string]any{keyType: typeInteger, keyMinimum: 1},
		MinItems:    new(1),
		MaxItems:    new(DefaultMaxBatchItems),
		Required:    true,
	}
}

// parseIdentifierList validates a bounded identifier list before anything is
// dispatched, so a malformed entry cannot leave half a batch applied.
func parseIdentifierList(field string, values []int64, limit int) ([]client.ID, error) {
	if err := boundedCount(field, len(values), limit); err != nil {
		return nil, err
	}
	out := make([]client.ID, 0, len(values))
	for _, value := range values {
		id, err := parseIdentifier(field, value)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

// removal is one destructive single-record call: a delete or an unschedule.
type removal func(context.Context, client.Session, client.ID) (api.WriteResult, error)

// removeEach loops a single-record removal, reporting each item separately. One
// item's failure does not abandon the rest, because a batch that stopped halfway
// would leave a caller unable to say what was removed.
func (s *service) removeEach(
	ctx context.Context, session client.Session, ids []client.ID, remove removal,
) BatchResult {
	outcomes := make([]BatchOutcome, 0, len(ids))
	for _, id := range ids {
		result, err := remove(ctx, session, id)
		if err != nil {
			outcomes = append(outcomes, failedOutcome(id.Int64(), err))
			continue
		}
		outcomes = append(outcomes, BatchOutcome{
			ID: id.Int64(), Applied: true, Status: result.Status,
		})
	}
	return newBatchResult(outcomes)
}

// deleteWorkoutInput is the single-delete argument set.
type deleteWorkoutInput struct {
	WorkoutID int64 `json:"workout_id" jsonschema:"the workout to delete"`
}

func deleteWorkoutContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolDeleteWorkout,
			Title: "Delete a workout",
			Description: "permanently remove one workout from the Garmin Connect library. It " +
				"cannot be undone, it removes the template every calendar entry points at, " +
				"and it requires confirmation",
			Tier:        policy.TierDestructive,
			Category:    categoryHealth,
			Annotations: destructiveAnnotations(),
		},
		Schema: NewSchema(workoutIDIntegerProperty()),
	}
}

func registerDeleteWorkout(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in deleteWorkoutInput) (
		*mcp.CallToolResult, DeletionResult, error,
	) {
		id, session, err := svc.resolveWorkoutRead(ctx, in.WorkoutID)
		if err != nil {
			return nil, DeletionResult{}, err
		}

		result, err := svc.workouts.Delete(ctx, session, id)
		if err != nil {
			return nil, DeletionResult{}, fail(err)
		}
		return nil, DeletionResult{ID: id.Int64(), Deleted: true, Status: result.Status}, nil
	}
	return mcpserver.AddTool(registry, deleteWorkoutContract().Registration(), handler)
}

// deleteWorkoutsInput is the batch-delete argument set.
type deleteWorkoutsInput struct {
	WorkoutIDs []int64 `json:"workout_ids" jsonschema:"the workouts to delete"`
}

func deleteWorkoutsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolDeleteWorkouts,
			Title: "Delete several workouts",
			Description: "permanently remove several workouts from the Garmin Connect " +
				"library in one call. It cannot be undone and it requires confirmation. Each " +
				"identifier is deleted on its own and reported separately",
			Tier:        policy.TierDestructive,
			Category:    categoryHealth,
			Annotations: destructiveAnnotations(),
		},
		Schema: NewSchema(identifierListProperty(
			"workout_ids", "the workout identifiers to delete, from get_workouts")),
	}
}

func registerDeleteWorkouts(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in deleteWorkoutsInput) (
		*mcp.CallToolResult, BatchResult, error,
	) {
		ids, err := parseIdentifierList("workout_ids", in.WorkoutIDs, svc.bounds.MaxBatchItems)
		if err != nil {
			return nil, BatchResult{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, BatchResult{}, err
		}
		return nil, svc.removeEach(ctx, session, ids, svc.workouts.Delete), nil
	}
	return mcpserver.AddTool(registry, deleteWorkoutsContract().Registration(), handler)
}
