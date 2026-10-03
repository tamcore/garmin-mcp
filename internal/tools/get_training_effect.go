package tools

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetTrainingEffect is the upstream compatibility name of the training-effect read.
const ToolGetTrainingEffect = "get_training_effect"

// The two documents a training effect can be read from.
const (
	trainingEffectSourceDetails = "activity_details"
	trainingEffectSourceList    = "activity_list"
)

// adviceTrainingEffectNotListed answers a refused details read whose list fallback
// found nothing. It is not the authentication advice: the session is fine.
const adviceTrainingEffectNotListed = "Garmin refused the activity details (HTTP 403), " +
	"and the activity is not among the most recent activities. Its training effect " +
	"cannot be read."

// TrainingEffect is one activity's training effect. It is health data — never log it,
// never cache it.
//
// Garmin serves it inside the activity summary, so this tool reads the same document
// get_activity reads and keeps only the training fields of it.
type TrainingEffect struct {
	ActivityID int64 `json:"activity_id" jsonschema:"the activity this effect belongs to"`

	AerobicEffect        *float64 `json:"aerobic_training_effect,omitempty" jsonschema:"the aerobic training effect"`
	AnaerobicEffect      *float64 `json:"anaerobic_training_effect,omitempty" jsonschema:"the anaerobic training effect"`
	EffectLabel          *string  `json:"training_effect_label,omitempty" jsonschema:"Garmin's own label for the effect"`
	RecoveryTimeHours    *float64 `json:"recovery_time_hours,omitempty" jsonschema:"the advised recovery, hours"`
	TrainingLoad         *float64 `json:"training_load,omitempty" jsonschema:"the activity's training load"`
	PerformanceCondition *float64 `json:"performance_condition,omitempty" jsonschema:"the performance condition"`

	Reported bool   `json:"reported" jsonschema:"whether the activity carried a training summary at all"`
	Source   string `json:"source" jsonschema:"activity_details, or activity_list when Garmin refused the details"`
}

// LogValue reports the shape of the answer, never a reading.
func (t TrainingEffect) LogValue() slog.Value {
	return shape("trainingEffect",
		slog.Bool("reported", t.Reported),
		slog.String("aerobic", presence(t.AerobicEffect != nil)),
		slog.String("anaerobic", presence(t.AnaerobicEffect != nil)),
	)
}

func getTrainingEffectContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetTrainingEffect,
			Title: "Get training effect",
			Description: "read the training effect of one activity: the aerobic and " +
				"anaerobic effect, Garmin's label for them, the advised recovery time, " +
				"the training load and the performance condition. An activity Garmin " +
				"recorded no training summary for — a manual entry, or one from a device " +
				"that does not measure it — answers with reported false and no readings",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(trainingEffectIDProperty()),
	}
}

// trainingEffectIDProperty declares the identifier argument.
//
// It is the shared activity-identifier property narrowed to an integer, which is what
// the manifest states for this tool alone: every other activity read accepts the
// decimal-string form as well.
func trainingEffectIDProperty() Property {
	property := activityIDProperty()
	property.Types = []string{typeInteger}
	property.Description = "the Garmin activity identifier, as a positive whole number"
	return property
}

// registerGetTrainingEffect registers the tool.
func registerGetTrainingEffect(registry *mcpserver.Registry, svc *service) error {
	scores, err := trainingScoresClient(svc)
	if err != nil {
		return err
	}
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in activityIDInput) (
		*mcp.CallToolResult, TrainingEffect, error,
	) {
		id, session, err := svc.resolveActivityRead(ctx, in.ActivityID)
		if err != nil {
			return nil, TrainingEffect{}, err
		}
		effect, err := scores.TrainingEffect(ctx, session, id)
		switch {
		case hasStatus(err, http.StatusForbidden):
			listed, listErr := trainingEffectFromList(ctx, svc, session, id)
			return nil, listed, listErr
		case err != nil:
			return nil, TrainingEffect{}, fail(err)
		}
		return nil, newTrainingEffect(id.Int64(), effect.Summary, trainingEffectSourceDetails), nil
	}
	return mcpserver.AddTool(registry, getTrainingEffectContract().Registration(), handler)
}

// trainingEffectFromList answers from the activity list, which Garmin still serves
// for activities whose details it refuses: first filtered by the identifier, then one
// bounded page of the most recent activities. Source: training.py:163-207.
func trainingEffectFromList(
	ctx context.Context, svc *service, session client.Session, id client.ID,
) (TrainingEffect, error) {
	probe, err := client.NewPage(0, 1)
	if err != nil {
		return TrainingEffect{}, fail(err)
	}
	recent, err := client.NewPage(0, min(DefaultMaxActivityPageSize, svc.limits.MaxPageSize))
	if err != nil {
		return TrainingEffect{}, fail(err)
	}
	for _, query := range []api.ListQuery{{Page: probe, ID: id}, {Page: recent}} {
		result, err := svc.activities.List(ctx, session, query)
		if err != nil {
			return TrainingEffect{}, fail(err)
		}
		i := slices.IndexFunc(result.Activities, func(activity api.Activity) bool {
			return activity.ActivityID != nil && *activity.ActivityID == id.Int64()
		})
		if i < 0 {
			continue
		}
		var summary *api.TrainingEffectSummary
		if listed := result.Activities[i].TrainingEffectSummary; listed != (api.TrainingEffectSummary{}) {
			summary = &listed
		}
		return newTrainingEffect(id.Int64(), summary, trainingEffectSourceList), nil
	}
	return TrainingEffect{}, &ToolError{Advice: adviceTrainingEffectNotListed, Err: client.ErrNotFound}
}

// newTrainingEffect maps the activity summary onto the result.
//
// The identifier is the validated one the caller asked for, never the one the payload
// echoed: a response that names a different activity must not be reported as if it
// answered the question that was asked.
func newTrainingEffect(activityID int64, summary *api.TrainingEffectSummary, source string) TrainingEffect {
	out := TrainingEffect{ActivityID: activityID, Source: source}
	if summary == nil {
		return out
	}

	out.Reported = true
	out.AerobicEffect = optionalFloat(cmp.Or(summary.TrainingEffect, summary.AerobicTrainingEffect))
	out.AnaerobicEffect = optionalFloat(summary.AnaerobicTrainingEffect)
	out.EffectLabel = optionalText(cmp.Or(summary.TrainingEffectLabel, summary.AerobicTrainingEffectMessage))
	out.TrainingLoad = optionalFloat(summary.ActivityTrainingLoad)
	out.PerformanceCondition = optionalFloat(summary.PerformanceCondition)
	if minutes, ok := summary.RecoveryTime.Float64(); ok {
		hours := fitRound(minutes/minutesPerHour, placesOne)
		out.RecoveryTimeHours = &hours
	}
	return out
}
