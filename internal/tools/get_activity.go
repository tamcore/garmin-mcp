package tools

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetActivity is the upstream compatibility name of the single-activity read.
const ToolGetActivity = "get_activity"

// ActivityTiming is when one activity happened and how long it lasted.
type ActivityTiming struct {
	StartTimeLocal *string  `json:"start_time_local,omitempty" jsonschema:"the local start time"`
	StartTimeGMT   *string  `json:"start_time_gmt,omitempty" jsonschema:"the UTC start time"`
	DurationSecs   *float64 `json:"duration_seconds,omitempty" jsonschema:"the timed duration in seconds"`
	MovingSecs     *float64 `json:"moving_seconds,omitempty" jsonschema:"the moving duration in seconds"`
	ElapsedSecs    *float64 `json:"elapsed_seconds,omitempty" jsonschema:"the elapsed duration in seconds"`
}

// ActivityDistance is how far and how fast one activity went.
type ActivityDistance struct {
	DistanceMeters *float64 `json:"distance_meters,omitempty" jsonschema:"the distance in meters"`
	AverageSpeed   *float64 `json:"average_speed_mps,omitempty" jsonschema:"the average speed, m/s"`
	MaxSpeed       *float64 `json:"max_speed_mps,omitempty" jsonschema:"the top speed, m/s"`
}

// ActivityHeartRate is the heart-rate summary of one activity. It is health data.
type ActivityHeartRate struct {
	Average *float64 `json:"average_bpm,omitempty" jsonschema:"the average heart rate in bpm"`
	Max     *float64 `json:"max_bpm,omitempty" jsonschema:"the maximum heart rate in bpm"`
	Min     *float64 `json:"min_bpm,omitempty" jsonschema:"the minimum heart rate in bpm"`
}

// ActivityEnergy is the energy summary of one activity.
type ActivityEnergy struct {
	Calories    *float64 `json:"calories,omitempty" jsonschema:"the kilocalories burned"`
	BMRCalories *float64 `json:"bmr_calories,omitempty" jsonschema:"the basal kilocalories"`
}

// ActivityRunMetrics are the running-form measurements Garmin records when the
// activity and the device support them.
type ActivityRunMetrics struct {
	AverageCadence      *float64 `json:"average_cadence,omitempty" jsonschema:"the average run cadence, spm"`
	MaxCadence          *float64 `json:"max_cadence,omitempty" jsonschema:"the maximum run cadence, spm"`
	StrideLength        *float64 `json:"stride_length_cm,omitempty" jsonschema:"the average stride length, cm"`
	GroundContactTime   *float64 `json:"ground_contact_time_ms,omitempty" jsonschema:"the average ground contact time, ms"`
	VerticalOscillation *float64 `json:"vertical_oscillation_cm,omitempty" jsonschema:"the vertical oscillation, cm"`
	Steps               *float64 `json:"steps,omitempty" jsonschema:"the steps recorded"`
}

// ActivityPower is the power summary of one activity.
type ActivityPower struct {
	Average    *float64 `json:"average_watts,omitempty" jsonschema:"the average power in watts"`
	Max        *float64 `json:"max_watts,omitempty" jsonschema:"the maximum power in watts"`
	Normalized *float64 `json:"normalized_watts,omitempty" jsonschema:"the normalized power in watts"`
}

// ActivityTraining is the training-effect summary of one activity. It is health
// data: it describes how one person's body responded to a session.
type ActivityTraining struct {
	AerobicEffect            *float64 `json:"aerobic_training_effect,omitempty" jsonschema:"the aerobic training effect"`
	AnaerobicEffect          *float64 `json:"anaerobic_training_effect,omitempty" jsonschema:"the anaerobic effect"`
	EffectLabel              *string  `json:"training_effect_label,omitempty" jsonschema:"Garmin's training effect label"`
	TrainingLoad             *float64 `json:"training_load,omitempty" jsonschema:"the activity training load"`
	ModerateIntensityMinutes *float64 `json:"moderate_intensity_minutes,omitempty" jsonschema:"the moderate minutes"`
	VigorousIntensityMinutes *float64 `json:"vigorous_intensity_minutes,omitempty" jsonschema:"the vigorous minutes"`
}

// ActivityElevation is the elevation summary of one activity. It carries gains and
// extremes only: no coordinate and no track point.
type ActivityElevation struct {
	GainMeters *float64 `json:"gain_meters,omitempty" jsonschema:"the total elevation gain in meters"`
	LossMeters *float64 `json:"loss_meters,omitempty" jsonschema:"the total elevation loss in meters"`
	MaxMeters  *float64 `json:"max_meters,omitempty" jsonschema:"the highest elevation in meters"`
	MinMeters  *float64 `json:"min_meters,omitempty" jsonschema:"the lowest elevation in meters"`
}

// ActivityFeedback is what the account recorded about the session afterwards,
// together with the recovery reading Garmin derived from it.
type ActivityFeedback struct {
	RecoveryHeartRate  *float64 `json:"recovery_heart_rate_bpm,omitempty" jsonschema:"the recovery heart rate in bpm"`
	BodyBatteryImpact  *float64 `json:"body_battery_impact,omitempty" jsonschema:"the body battery difference"`
	WorkoutFeel        *float64 `json:"workout_feel,omitempty" jsonschema:"how the session felt, 0 to 100"`
	PerceivedExertion  *float64 `json:"workout_rpe,omitempty" jsonschema:"the rate of perceived exertion, 0 to 100"`
	DeviceManufacturer *string  `json:"device_manufacturer,omitempty" jsonschema:"the recording device maker"`
}

// ActivityDetail is one activity's own record.
//
// Like every other activity result on this surface it carries no coordinate: the
// start position of a real outing names where a person lives or works, and nothing
// a caller asked for here needs it.
type ActivityDetail struct {
	ActivityID   int64              `json:"activity_id" jsonschema:"the Garmin activity identifier"`
	Name         *string            `json:"activity_name,omitempty" jsonschema:"the activity name"`
	Description  *string            `json:"description,omitempty" jsonschema:"the activity description"`
	ActivityType *string            `json:"activity_type,omitempty" jsonschema:"the activity type key"`
	ParentTypeID *int               `json:"parent_type_id,omitempty" jsonschema:"the parent activity type identifier"`
	EventType    *string            `json:"event_type,omitempty" jsonschema:"the event type key, e.g. race"`
	LapCount     *int               `json:"lap_count,omitempty" jsonschema:"how many laps the activity holds"`
	HasSplits    *bool              `json:"has_splits,omitempty" jsonschema:"whether split records exist"`
	Timing       ActivityTiming     `json:"timing" jsonschema:"when it happened and how long it lasted"`
	Distance     ActivityDistance   `json:"distance" jsonschema:"how far and how fast the activity went"`
	HeartRate    ActivityHeartRate  `json:"heart_rate" jsonschema:"the heart-rate summary"`
	Energy       ActivityEnergy     `json:"energy" jsonschema:"the energy summary"`
	RunMetrics   ActivityRunMetrics `json:"run_metrics" jsonschema:"the running-form measurements"`
	Power        ActivityPower      `json:"power" jsonschema:"the power summary"`
	Training     ActivityTraining   `json:"training" jsonschema:"the training-effect summary"`
	Elevation    ActivityElevation  `json:"elevation" jsonschema:"the elevation summary"`
	Feedback     ActivityFeedback   `json:"feedback" jsonschema:"the recovery reading and the feedback"`
}

// LogValue reports that one activity was read, never a measurement of it.
func (d ActivityDetail) LogValue() slog.Value {
	return shape("activityDetail",
		slog.String("activityType", presence(d.ActivityType != nil)),
		slog.String("eventType", presence(d.EventType != nil)),
	)
}

func getActivityContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetActivity,
			Title: "Get activity",
			Description: "read one activity's own record: timing, distance, heart rate, " +
				"energy, running form, power, training effect, elevation and the account's " +
				"own feedback. event_type is absent for activities that pre-date event types " +
				"in Garmin's API, and uncategorized means no event type was set. Start " +
				"coordinates are not returned",
			Tier:        policy.TierReadOnly,
			Category:    categoryLocation,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(activityIDProperty()),
	}
}

// registerGetActivity registers the tool.
func registerGetActivity(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in activityIDInput) (
		*mcp.CallToolResult, ActivityDetail, error,
	) {
		id, session, err := svc.resolveActivityRead(ctx, in.ActivityID)
		if err != nil {
			return nil, ActivityDetail{}, err
		}

		summary, err := svc.details.Summary(ctx, session, id)
		if err != nil {
			return nil, ActivityDetail{}, fail(err)
		}
		return nil, newActivityDetail(id, summary), nil
	}
	return mcpserver.AddTool(registry, getActivityContract().Registration(), handler)
}

// activityRecordDTO is the summaryDTO of one activity. Every measurement is a union
// decoder, because Garmin sends several of these fields as numbers on one activity
// type and as numeric strings on another.
type activityRecordDTO struct {
	StartTimeLocal  *string       `json:"startTimeLocal"`
	StartTimeGMT    *string       `json:"startTimeGMT"`
	Duration        client.Number `json:"duration"`
	MovingDuration  client.Number `json:"movingDuration"`
	ElapsedDuration client.Number `json:"elapsedDuration"`

	Distance     client.Number `json:"distance"`
	AverageSpeed client.Number `json:"averageSpeed"`
	MaxSpeed     client.Number `json:"maxSpeed"`

	AverageHR client.Number `json:"averageHR"`
	MaxHR     client.Number `json:"maxHR"`
	MinHR     client.Number `json:"minHR"`

	Calories    client.Number `json:"calories"`
	BMRCalories client.Number `json:"bmrCalories"`

	AverageRunCadence   client.Number `json:"averageRunCadence"`
	MaxRunCadence       client.Number `json:"maxRunCadence"`
	StrideLength        client.Number `json:"strideLength"`
	GroundContactTime   client.Number `json:"groundContactTime"`
	VerticalOscillation client.Number `json:"verticalOscillation"`
	Steps               client.Number `json:"steps"`

	AveragePower    client.Number `json:"averagePower"`
	MaxPower        client.Number `json:"maxPower"`
	NormalizedPower client.Number `json:"normalizedPower"`

	TrainingEffect           client.Number `json:"trainingEffect"`
	AnaerobicTrainingEffect  client.Number `json:"anaerobicTrainingEffect"`
	TrainingEffectLabel      client.Text   `json:"trainingEffectLabel"`
	ActivityTrainingLoad     client.Number `json:"activityTrainingLoad"`
	ModerateIntensityMinutes client.Number `json:"moderateIntensityMinutes"`
	VigorousIntensityMinutes client.Number `json:"vigorousIntensityMinutes"`

	ElevationGain client.Number `json:"elevationGain"`
	ElevationLoss client.Number `json:"elevationLoss"`
	MaxElevation  client.Number `json:"maxElevation"`
	MinElevation  client.Number `json:"minElevation"`

	RecoveryHeartRate     client.Number `json:"recoveryHeartRate"`
	DifferenceBodyBattery client.Number `json:"differenceBodyBattery"`
	DirectWorkoutFeel     client.Number `json:"directWorkoutFeel"`
	DirectWorkoutRpe      client.Number `json:"directWorkoutRpe"`
}

// activityMetadataDTO is the metadataDTO of one activity, reduced to the three
// fields this result reports.
type activityMetadataDTO struct {
	LapCount     client.Number `json:"lapCount"`
	HasSplits    *bool         `json:"hasSplits"`
	Manufacturer client.Text   `json:"manufacturer"`
}

// activityTypeDTO is the type object, which carries the parent type identifier the
// type key alone does not.
type activityTypeDTO struct {
	ParentTypeID client.Number `json:"parentTypeId"`
}

// newActivityDetail maps the domain model onto the bounded result.
func newActivityDetail(id client.ID, summary api.ActivitySummary) ActivityDetail {
	var record activityRecordDTO
	decodeSubDocument(summary.Summary, &record)
	var metadata activityMetadataDTO
	decodeSubDocument(summary.Metadata, &metadata)
	var activityType activityTypeDTO
	decodeSubDocument(summary.ActivityType, &activityType)

	return ActivityDetail{
		ActivityID:   id.Int64(),
		Name:         summary.ActivityName,
		Description:  summary.Description,
		ActivityType: typeKeyOf(summary.ActivityType),
		ParentTypeID: optionalInt(activityType.ParentTypeID),
		EventType:    typeKeyOf(summary.EventType),
		LapCount:     optionalInt(metadata.LapCount),
		HasSplits:    metadata.HasSplits,
		Timing:       newActivityTiming(record),
		Distance:     newActivityDistance(record),
		HeartRate:    newActivityHeartRate(record),
		Energy:       newActivityEnergy(record),
		RunMetrics:   newActivityRunMetrics(record),
		Power:        newActivityPower(record),
		Training:     newActivityTraining(record),
		Elevation:    newActivityElevation(record),
		Feedback:     newActivityFeedback(record, metadata),
	}
}

// decodeSubDocument decodes one of the sub-documents Garmin nests inside an
// activity record.
//
// A sub-document this server cannot decode leaves out at its zero value rather than
// failing the whole read: these endpoints are undocumented and they drift, and an
// activity whose summaryDTO changed shape is still worth returning with the fields
// that did decode.
func decodeSubDocument(raw json.RawMessage, out any) {
	if len(raw) == 0 || string(raw) == jsonNull {
		return
	}
	_ = json.Unmarshal(raw, out)
}

func newActivityTiming(record activityRecordDTO) ActivityTiming {
	return ActivityTiming{
		StartTimeLocal: record.StartTimeLocal,
		StartTimeGMT:   record.StartTimeGMT,
		DurationSecs:   optionalFloat(record.Duration),
		MovingSecs:     optionalFloat(record.MovingDuration),
		ElapsedSecs:    optionalFloat(record.ElapsedDuration),
	}
}

func newActivityDistance(record activityRecordDTO) ActivityDistance {
	return ActivityDistance{
		DistanceMeters: optionalFloat(record.Distance),
		AverageSpeed:   optionalFloat(record.AverageSpeed),
		MaxSpeed:       optionalFloat(record.MaxSpeed),
	}
}

func newActivityHeartRate(record activityRecordDTO) ActivityHeartRate {
	return ActivityHeartRate{
		Average: optionalFloat(record.AverageHR),
		Max:     optionalFloat(record.MaxHR),
		Min:     optionalFloat(record.MinHR),
	}
}

func newActivityEnergy(record activityRecordDTO) ActivityEnergy {
	return ActivityEnergy{
		Calories:    optionalFloat(record.Calories),
		BMRCalories: optionalFloat(record.BMRCalories),
	}
}

func newActivityRunMetrics(record activityRecordDTO) ActivityRunMetrics {
	return ActivityRunMetrics{
		AverageCadence:      optionalFloat(record.AverageRunCadence),
		MaxCadence:          optionalFloat(record.MaxRunCadence),
		StrideLength:        optionalFloat(record.StrideLength),
		GroundContactTime:   optionalFloat(record.GroundContactTime),
		VerticalOscillation: optionalFloat(record.VerticalOscillation),
		Steps:               optionalFloat(record.Steps),
	}
}

func newActivityPower(record activityRecordDTO) ActivityPower {
	return ActivityPower{
		Average:    optionalFloat(record.AveragePower),
		Max:        optionalFloat(record.MaxPower),
		Normalized: optionalFloat(record.NormalizedPower),
	}
}

func newActivityTraining(record activityRecordDTO) ActivityTraining {
	return ActivityTraining{
		AerobicEffect:            optionalFloat(record.TrainingEffect),
		AnaerobicEffect:          optionalFloat(record.AnaerobicTrainingEffect),
		EffectLabel:              optionalText(record.TrainingEffectLabel),
		TrainingLoad:             optionalFloat(record.ActivityTrainingLoad),
		ModerateIntensityMinutes: optionalFloat(record.ModerateIntensityMinutes),
		VigorousIntensityMinutes: optionalFloat(record.VigorousIntensityMinutes),
	}
}

func newActivityElevation(record activityRecordDTO) ActivityElevation {
	return ActivityElevation{
		GainMeters: optionalFloat(record.ElevationGain),
		LossMeters: optionalFloat(record.ElevationLoss),
		MaxMeters:  optionalFloat(record.MaxElevation),
		MinMeters:  optionalFloat(record.MinElevation),
	}
}

func newActivityFeedback(
	record activityRecordDTO, metadata activityMetadataDTO,
) ActivityFeedback {
	return ActivityFeedback{
		RecoveryHeartRate:  optionalFloat(record.RecoveryHeartRate),
		BodyBatteryImpact:  optionalFloat(record.DifferenceBodyBattery),
		WorkoutFeel:        optionalFloat(record.DirectWorkoutFeel),
		PerceivedExertion:  optionalFloat(record.DirectWorkoutRpe),
		DeviceManufacturer: optionalText(metadata.Manufacturer),
	}
}
