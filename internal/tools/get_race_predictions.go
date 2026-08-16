package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetRacePredictions is the upstream compatibility name of the race-prediction
// read.
//
// Source: Taxuspt/garmin_mcp's get_race_predictions tool (challenges.py:513-549),
// the zero-parameter branch only; see internal/garmin/api's RacePredictions doc
// comment for why the dated and ranged branches have no caller here.
const ToolGetRacePredictions = "get_race_predictions"

// A RacePrediction is one predicted distance's finish time. Both fields are
// optional together: Garmin omits a distance it has not modeled for the account.
type RacePrediction struct {
	Time        *string  `json:"time,omitempty" jsonschema:"the predicted finish time, human-readable"`
	TimeSeconds *float64 `json:"time_seconds,omitempty" jsonschema:"the predicted finish time, in seconds"`
}

// RacePredictionsByDistance is the four distances get_race_predictions reports,
// keyed the way challenges.py:527-544 keys them.
type RacePredictionsByDistance struct {
	FiveK        RacePrediction `json:"5K" jsonschema:"the predicted 5K finish time"`
	TenK         RacePrediction `json:"10K" jsonschema:"the predicted 10K finish time"`
	HalfMarathon RacePrediction `json:"half_marathon" jsonschema:"the predicted half-marathon finish time"`
	Marathon     RacePrediction `json:"marathon" jsonschema:"the predicted marathon finish time"`
}

// RacePredictions is the account's latest predicted race times. It is health data:
// never log a predicted time.
type RacePredictions struct {
	// PredictionDate is calendarDate: the day Garmin computed the prediction.
	PredictionDate *string                   `json:"prediction_date,omitempty" jsonschema:"the day of the prediction"`
	Predictions    RacePredictionsByDistance `json:"predictions" jsonschema:"the predicted times by distance"`
}

// LogValue reports which distances arrived, never a predicted time.
func (r RacePredictions) LogValue() slog.Value {
	return shape("racePredictions",
		slog.String("5K", presence(r.Predictions.FiveK.TimeSeconds != nil)),
		slog.String("10K", presence(r.Predictions.TenK.TimeSeconds != nil)),
		slog.String("halfMarathon", presence(r.Predictions.HalfMarathon.TimeSeconds != nil)),
		slog.String("marathon", presence(r.Predictions.Marathon.TimeSeconds != nil)),
	)
}

func getRacePredictionsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetRacePredictions,
			Title: "Get race predictions",
			Description: "read Garmin's predicted finish times for 5K, 10K, half " +
				"marathon and marathon, based on the account's recent training and VO2 max",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(),
	}
}

func registerGetRacePredictions(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, _ noArguments) (
		*mcp.CallToolResult, RacePredictions, error,
	) {
		session, err := svc.session(ctx)
		if err != nil {
			return nil, RacePredictions{}, err
		}
		name, err := svc.displayName(ctx, session)
		if err != nil {
			return nil, RacePredictions{}, err
		}
		predictions, err := svc.challenges.RacePredictions(ctx, session, name)
		if err != nil {
			return nil, RacePredictions{}, fail(err)
		}
		return nil, newRacePredictions(predictions), nil
	}
	return mcpserver.AddTool(registry, getRacePredictionsContract().Registration(), handler)
}

// newRacePredictions maps the domain model onto the result. Source:
// challenges.py:525-545, which builds "predictions" with all four keys present
// regardless of whether a distance's own time is unset.
func newRacePredictions(predictions api.RacePredictionSet) RacePredictions {
	return RacePredictions{
		PredictionDate: predictions.CalendarDate,
		Predictions: RacePredictionsByDistance{
			FiveK:        newRacePrediction(predictions.Time5K),
			TenK:         newRacePrediction(predictions.Time10K),
			HalfMarathon: newRacePrediction(predictions.TimeHalfMarathon),
			Marathon:     newRacePrediction(predictions.TimeMarathon),
		},
	}
}

// newRacePrediction formats one predicted time, matching _format_time
// (challenges.py:98-109, applied at 529, 533, 537, 541).
func newRacePrediction(seconds client.Number) RacePrediction {
	value, ok := seconds.Float64()
	if !ok {
		return RacePrediction{}
	}
	formatted := formatClockDuration(value)
	return RacePrediction{Time: &formatted, TimeSeconds: &value}
}
