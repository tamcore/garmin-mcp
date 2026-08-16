package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetTrainingEffect is the upstream compatibility name of the training-effect read.
const ToolGetTrainingEffect = "get_training_effect"

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

	Reported bool `json:"reported" jsonschema:"whether the activity carried a training summary at all"`
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
		if err != nil {
			return nil, TrainingEffect{}, fail(err)
		}
		return nil, newTrainingEffect(id.Int64(), effect), nil
	}
	return mcpserver.AddTool(registry, getTrainingEffectContract().Registration(), handler)
}

// newTrainingEffect maps the activity summary onto the result.
//
// The identifier is the validated one the caller asked for, never the one the payload
// echoed: a response that names a different activity must not be reported as if it
// answered the question that was asked.
func newTrainingEffect(activityID int64, effect api.ActivityTrainingEffect) TrainingEffect {
	out := TrainingEffect{ActivityID: activityID}
	if effect.Summary == nil {
		return out
	}

	summary := effect.Summary
	out.Reported = true
	out.AerobicEffect = optionalFloat(summary.TrainingEffect)
	out.AnaerobicEffect = optionalFloat(summary.AnaerobicTrainingEffect)
	out.EffectLabel = optionalText(summary.TrainingEffectLabel)
	out.TrainingLoad = optionalFloat(summary.ActivityTrainingLoad)
	out.PerformanceCondition = optionalFloat(summary.PerformanceCondition)
	if minutes, ok := summary.RecoveryTime.Float64(); ok {
		hours := fitRound(minutes/minutesPerHour, placesOne)
		out.RecoveryTimeHours = &hours
	}
	return out
}
