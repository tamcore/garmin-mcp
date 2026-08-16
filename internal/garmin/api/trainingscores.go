package api

import (
	"context"
	"fmt"
	"net/url"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// The scores-and-thresholds half of the training surface. Source:
// python-garminconnect 0.3.10 get_hill_score, get_endurance_score,
// get_fitnessage_data, get_training_status, get_cycling_ftp,
// get_functional_threshold_power_range and get_lactate_threshold, cross-checked
// against the curation in Taxuspt src/garmin_mcp/training.py, which names Garmin's
// own field spellings.
//
// Every document here is health data: a score, a threshold, a VO2 figure and a
// fitness age are all readings. Never log one and never cache one.
//
// No model decodes userProfilePK, userId or any other account identifier, and no
// method takes one: the principal comes from the session.

// TrainingScores reads the scores and thresholds. Every field of every model is
// optional — a new account, an unsupported device and a quiet window all produce an
// empty or partial document, which is a normal state and never an error.
type TrainingScores struct {
	req requester
}

// NewTrainingScores returns a training-scores client over the request layer.
func NewTrainingScores(rc *client.Client) (*TrainingScores, error) {
	req, err := newRequester(rc)
	if err != nil {
		return nil, err
	}
	return &TrainingScores{req: req}, nil
}

// NewTrainingScoresFrom returns a client sharing the request layer of an existing
// wellness client, for a caller holding a domain client rather than a layer.
func NewTrainingScoresFrom(w *Wellness) (*TrainingScores, error) {
	if w == nil {
		return nil, fmt.Errorf("garmin api: training-scores client needs a wellness client: %w",
			client.ErrNotConfigured)
	}
	return &TrainingScores{req: w.req}, nil
}

// A HillScoreDay is one day of the hill score.
type HillScoreDay struct {
	CalendarDate     *string       `json:"calendarDate"`
	OverallScore     client.Number `json:"overallScore"`
	StrengthScore    client.Number `json:"strengthScore"`
	EnduranceScore   client.Number `json:"enduranceScore"`
	ClassificationID client.Number `json:"hillScoreClassificationId"`
}

// HillScoreWindow is the hill score aggregated over an inclusive window.
//
// PeriodAvgScore is a keyed object whose key this project has not sourced. It is
// decoded as a map so the value can be read without the key ever being exposed:
// upstream reads one arbitrary value out of it and never the key.
type HillScoreWindow struct {
	PeriodAvgScore map[string]client.Number  `json:"periodAvgScore"`
	MaxScore       client.Number             `json:"maxScore"`
	Days           client.List[HillScoreDay] `json:"hillScoreDTOList"`
}

// HillScore reads the hill score over an inclusive window, with the daily
// aggregation upstream sends. Source: the enddate branch of get_hill_score.
func (t *TrainingScores) HillScore(
	ctx context.Context, session client.Session, span client.DateRange,
) (HillScoreWindow, error) {
	query := windowQuery(span)
	query.Set(client.QueryAggregation, client.AggregationDaily)
	req := readRequest(client.OpGetHillScore, client.EndpointHillScoreStats,
		client.PathHillScoreStats, query)

	var window HillScoreWindow
	if err := t.readWindow(ctx, session, req, span, &window); err != nil {
		return HillScoreWindow{}, err
	}
	return window, nil
}

// An EnduranceContributor is one activity type's share of the endurance score.
// Source: _map_contributor, which reads exactly these three keys.
type EnduranceContributor struct {
	ActivityTypeID client.Number `json:"activityTypeId"`
	Group          client.Number `json:"group"`
	Contribution   client.Number `json:"contribution"`
}

// EnduranceScoreDTO is the current endurance score and its classification limits.
type EnduranceScoreDTO struct {
	CalendarDate                    *string                           `json:"calendarDate"`
	OverallScore                    client.Number                     `json:"overallScore"`
	Classification                  client.Number                     `json:"classification"`
	ClassificationLowerIntermediate client.Number                     `json:"classificationLowerLimitIntermediate"`
	ClassificationLowerTrained      client.Number                     `json:"classificationLowerLimitTrained"`
	ClassificationLowerWellTrained  client.Number                     `json:"classificationLowerLimitWellTrained"`
	ClassificationLowerExpert       client.Number                     `json:"classificationLowerLimitExpert"`
	ClassificationLowerSuperior     client.Number                     `json:"classificationLowerLimitSuperior"`
	ClassificationLowerElite        client.Number                     `json:"classificationLowerLimitElite"`
	Contributors                    client.List[EnduranceContributor] `json:"contributors"`
}

// An EnduranceGroup is one aggregation bucket of the endurance window.
type EnduranceGroup struct {
	GroupAverage client.Number                     `json:"groupAverage"`
	GroupMax     client.Number                     `json:"groupMax"`
	Contributors client.List[EnduranceContributor] `json:"enduranceContributorDTOList"`
}

// EnduranceScoreWindow is the endurance score over an inclusive window.
type EnduranceScoreWindow struct {
	Avg      client.Number             `json:"avg"`
	Max      client.Number             `json:"max"`
	Score    *EnduranceScoreDTO        `json:"enduranceScoreDTO"`
	GroupMap map[string]EnduranceGroup `json:"groupMap"`
}

// EnduranceScore reads the endurance score over an inclusive window, with the weekly
// aggregation upstream sends. Source: the enddate branch of get_endurance_score.
func (t *TrainingScores) EnduranceScore(
	ctx context.Context, session client.Session, span client.DateRange,
) (EnduranceScoreWindow, error) {
	query := windowQuery(span)
	query.Set(client.QueryAggregation, client.AggregationWeekly)
	req := readRequest(client.OpGetEnduranceScore, client.EndpointEnduranceScoreStats,
		client.PathEnduranceScoreStats, query)

	var window EnduranceScoreWindow
	if err := t.readWindow(ctx, session, req, span, &window); err != nil {
		return EnduranceScoreWindow{}, err
	}
	return window, nil
}

// TrainingEffectSummary is the training-effect part of an activity summary. Source:
// the summaryDTO keys the upstream curation reads for get_training_effect.
type TrainingEffectSummary struct {
	TrainingEffect          client.Number `json:"trainingEffect"`
	AnaerobicTrainingEffect client.Number `json:"anaerobicTrainingEffect"`
	TrainingEffectLabel     client.Text   `json:"trainingEffectLabel"`
	RecoveryTime            client.Number `json:"recoveryTime"`
	ActivityTrainingLoad    client.Number `json:"activityTrainingLoad"`
	PerformanceCondition    client.Number `json:"performanceCondition"`
}

// ActivityTrainingEffect is one activity's training effect. Garmin serves it inside
// the activity summary, which is why this read has no endpoint of its own.
type ActivityTrainingEffect struct {
	ActivityID client.Number          `json:"activityId"`
	Summary    *TrainingEffectSummary `json:"summaryDTO"`
}

// TrainingEffect reads one activity's training effect off the activity summary.
func (t *TrainingScores) TrainingEffect(
	ctx context.Context, session client.Session, id client.ID,
) (ActivityTrainingEffect, error) {
	req := readRequest(client.OpGetTrainingEffect, client.EndpointActivity, activityPath(id), nil)
	if err := requireID(req, id); err != nil {
		return ActivityTrainingEffect{}, err
	}

	var effect ActivityTrainingEffect
	if _, err := t.req.read(ctx, session, req, &effect); err != nil {
		return ActivityTrainingEffect{}, err
	}
	return effect, nil
}

// A FitnessAgeComponent is one contributor to the fitness age.
type FitnessAgeComponent struct {
	Value               client.Number `json:"value"`
	TargetValue         client.Number `json:"targetValue"`
	ImprovementValue    client.Number `json:"improvementValue"`
	PotentialAge        client.Number `json:"potentialAge"`
	Priority            client.Number `json:"priority"`
	Stale               *bool         `json:"stale"`
	LastMeasurementDate *string       `json:"lastMeasurementDate"`
}

// FitnessAge is one day of the fitness-age document.
type FitnessAge struct {
	ChronologicalAge     client.Number                  `json:"chronologicalAge"`
	FitnessAge           client.Number                  `json:"fitnessAge"`
	AchievableFitnessAge client.Number                  `json:"achievableFitnessAge"`
	PreviousFitnessAge   client.Number                  `json:"previousFitnessAge"`
	LastUpdated          client.Text                    `json:"lastUpdated"`
	Components           map[string]FitnessAgeComponent `json:"components"`
}

// FitnessAgeData reads the fitness age for one day.
func (t *TrainingScores) FitnessAgeData(
	ctx context.Context, session client.Session, date client.Date,
) (FitnessAge, error) {
	req := readRequest(client.OpGetFitnessAgeData, client.EndpointFitnessAge,
		datedPath(client.PathFitnessAgePrefix, date), nil)
	if err := requireDate(req, date); err != nil {
		return FitnessAge{}, err
	}

	var day FitnessAge
	if _, err := t.req.read(ctx, session, req, &day); err != nil {
		return FitnessAge{}, err
	}
	return day, nil
}

// TrainingStatusData reads the aggregated training status for one day.
//
// The document it decodes lives in trainingstatus.go, the same model the trend reads
// use, and both pick their device through api.SelectStatusDevice. One model and one
// selector are what stop this read and get_training_load_trend disagreeing about a
// load; the shared model alone would not, because two callers could still choose two
// devices out of it.
func (t *TrainingScores) TrainingStatusData(
	ctx context.Context, session client.Session, date client.Date,
) (TrainingStatus, error) {
	req := readRequest(client.OpGetTrainingStatus, client.EndpointTrainingStatus,
		datedPath(client.PathTrainingStatusPrefix, date), nil)
	if err := requireDate(req, date); err != nil {
		return TrainingStatus{}, err
	}

	var status TrainingStatus
	payload, err := t.req.read(ctx, session, req, &status)
	if err != nil {
		return TrainingStatus{}, err
	}
	status.raw = payload
	return status, nil
}

// readWindow refuses an unset or oversized window before anything is dispatched, then
// performs the read.
func (t *TrainingScores) readWindow(
	ctx context.Context, session client.Session, req client.Request,
	span client.DateRange, out any,
) error {
	if span.IsZero() {
		return invalid(req, client.ErrValidation)
	}
	if err := t.req.limits().ValidateDateRange(span); err != nil {
		return invalid(req, err)
	}
	_, err := t.req.read(ctx, session, req, out)
	return err
}

// windowQuery renders an inclusive window as the two parameters the metrics service
// filters by.
func windowQuery(span client.DateRange) url.Values {
	query := url.Values{}
	query.Set(client.QueryStartDate, span.Start().String())
	query.Set(client.QueryEndDate, span.End().String())
	return query
}
