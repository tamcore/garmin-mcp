package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// TrainingTrends reads the trend-and-reload half of the training surface: the
// aggregated progress summary, heart-rate variability, the aggregated training status
// that carries training load and load focus, the max-metrics VO2 max series, and the
// one write of the domain, the epoch reload request.
//
// Every document it returns is health data. Never log one, never cache one.
//
// Several reads share an endpoint and differ only in the sanitized operation label,
// which is what names the call in a log line: the aggregated training status answers
// get_training_load_trend, get_training_load_balance and the VO2 max trend's fallback,
// and the HRV document answers get_hrv_data and get_hrv_trend.
type TrainingTrends struct {
	req requester
}

// NewTrainingTrends returns a training-trend client over the request layer.
func NewTrainingTrends(rc *client.Client) (*TrainingTrends, error) {
	req, err := newRequester(rc)
	if err != nil {
		return nil, err
	}
	return &TrainingTrends{req: req}, nil
}

// TrainingTrends returns the trend client over the same request layer as w.
//
// It hangs off Wellness for the same reason Cardio does: the two read the same Garmin
// surface through the same immutable request layer, and separating them only keeps one
// file from carrying every endpoint. The respiration trend reads the health tier's
// daily respiration document through Cardio rather than duplicating it here.
func (w *Wellness) TrainingTrends() *TrainingTrends { return &TrainingTrends{req: w.req} }

// RespirationTrendDay reads one day of the health tier's daily respiration document
// for get_respiration_trend.
//
// It adds no endpoint: the trend is the same document get_respiration_data reads, once
// per day, and only the sanitized operation label differs. The method is declared here
// rather than beside its two siblings so that the trend slice adds nothing to the
// health tier's own file.
func (c *WellnessCardio) RespirationTrendDay(
	ctx context.Context, session client.Session, date client.Date,
) (DailyRespiration, error) {
	return c.respiration(ctx, session, date, client.OpGetRespirationTrend)
}

// groupByParentActivityTypeValue is what upstream sends for the progress summary's
// grouping flag. Source: params={"groupByParentActivityType": str(groupbyactivities)}
// in get_progress_summary_between_dates, where the default True renders as Python's
// "True". It is sent verbatim rather than normalized, because that spelling is the one
// upstream is known to work with and a lower-case spelling is not.
const groupByParentActivityTypeValue = "True"

// maxProgressMetricLen bounds the metric name. It is a Garmin field name such as
// elevationGain, so the longest name upstream documents is 15 characters and this
// leaves ample room without accepting an unbounded query value.
const maxProgressMetricLen = 32

// A ProgressMetric is a validated progress-summary metric name.
//
// It is not an enumeration on purpose. Upstream documents four metrics —
// elevationGain, duration, distance and movingDuration — but the endpoint takes a
// Garmin field name and this project has no pinned source for the complete set, so a
// closed list would silently refuse a metric Garmin serves. The shape is validated
// instead: a bounded letters-and-digits identifier, which can carry no separator.
type ProgressMetric struct {
	value string
}

// ParseProgressMetric validates a metric name.
func ParseProgressMetric(value string) (ProgressMetric, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || len(trimmed) > maxProgressMetricLen {
		return ProgressMetric{}, fmt.Errorf(
			"%w: metric must be 1 to %d characters", client.ErrValidation, maxProgressMetricLen)
	}
	for index, r := range trimmed {
		letter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		digit := r >= '0' && r <= '9'
		if !letter && (!digit || index == 0) {
			return ProgressMetric{}, fmt.Errorf(
				"%w: metric must be a Garmin field name in letters and digits",
				client.ErrValidation)
		}
	}
	return ProgressMetric{value: trimmed}, nil
}

// String renders the metric, or "" when unset.
func (m ProgressMetric) String() string { return m.value }

// IsZero reports whether the metric is unset.
func (m ProgressMetric) IsZero() bool { return m.value == "" }

// ProgressMetricStats is one metric's aggregate for one activity type.
//
// Source: the count, sum, avg, min and max upstream reads off
// stats[activity_type][metric] in get_progress_summary_between_dates.
type ProgressMetricStats struct {
	Count client.Number `json:"count"`
	Sum   client.Number `json:"sum"`
	Avg   client.Number `json:"avg"`
	Min   client.Number `json:"min"`
	Max   client.Number `json:"max"`
}

// ProgressSummary is one aggregated progress entry.
//
// Source: the date, countOfActivities and stats upstream reads off the first element
// of the response.
type ProgressSummary struct {
	Date              *string                                   `json:"date"`
	CountOfActivities client.Number                             `json:"countOfActivities"`
	Stats             map[string]map[string]ProgressMetricStats `json:"stats"`
}

// ProgressSummaries is the progress-summary response.
//
// Garmin answers with a list whose first element carries the aggregate, and upstream
// refuses anything else. A bare object is accepted here as well, because tolerating a
// singular document costs nothing and a strict list decoder would turn a shape drift
// into a decode failure.
type ProgressSummaries struct {
	entries []ProgressSummary

	raw client.Payload
}

// UnmarshalJSON accepts the list form and the single-object form.
func (s *ProgressSummaries) UnmarshalJSON(data []byte) error {
	var list []ProgressSummary
	if err := json.Unmarshal(data, &list); err == nil {
		s.entries = list
		return nil
	}
	var single ProgressSummary
	if err := json.Unmarshal(data, &single); err != nil {
		return err
	}
	s.entries = []ProgressSummary{single}
	return nil
}

// Payload is the retained raw response.
func (s ProgressSummaries) Payload() client.Payload { return s.raw }

// Len is how many entries the response carried.
func (s ProgressSummaries) Len() int { return len(s.entries) }

// First returns the aggregate entry, which is the one upstream reads.
func (s ProgressSummaries) First() (ProgressSummary, bool) {
	if len(s.entries) == 0 {
		return ProgressSummary{}, false
	}
	return s.entries[0], true
}

// LogValue reports the shape of the response, never an aggregate.
func (s ProgressSummaries) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "progressSummaries"),
		slog.Int("entries", len(s.entries)),
		slog.Any("payload", s.raw),
	)
}

// ProgressSummary reads the aggregated activity progress for a date window.
//
// Source: get_progress_summary_between_dates, which sends startDate, endDate,
// aggregation=lifetime, groupByParentActivityType and metric.
func (t *TrainingTrends) ProgressSummary(
	ctx context.Context, session client.Session, span client.DateRange, metric ProgressMetric,
) (ProgressSummaries, error) {
	query := url.Values{}
	query.Set(client.QueryStartDate, span.Start().String())
	query.Set(client.QueryEndDate, span.End().String())
	query.Set(client.QueryAggregation, client.AggregationLifetime)
	query.Set(client.QueryGroupByParentActivityType, groupByParentActivityTypeValue)
	query.Set(client.QueryMetric, metric.String())
	req := readRequest(client.OpGetProgressSummaryBetweenDates, client.EndpointFitnessStats,
		client.PathFitnessStatsActivity, query)

	if err := t.req.limits().ValidateDateRange(span); err != nil {
		return ProgressSummaries{}, invalid(req, err)
	}
	if metric.IsZero() {
		return ProgressSummaries{}, invalid(req, fmt.Errorf(
			"%w: a metric is required for this endpoint", client.ErrValidation))
	}

	var summaries ProgressSummaries
	payload, err := t.req.read(ctx, session, req, &summaries)
	if err != nil {
		return ProgressSummaries{}, err
	}
	summaries.raw = payload
	return summaries, nil
}

// HRV reads one day of heart-rate variability for get_hrv_data.
func (t *TrainingTrends) HRV(
	ctx context.Context, session client.Session, date client.Date,
) (HRVDay, error) {
	return t.hrv(ctx, session, date, client.OpGetHRVData)
}

// HRVForTrend reads the same document for one day of get_hrv_trend.
func (t *TrainingTrends) HRVForTrend(
	ctx context.Context, session client.Session, date client.Date,
) (HRVDay, error) {
	return t.hrv(ctx, session, date, client.OpGetHRVTrend)
}

func (t *TrainingTrends) hrv(
	ctx context.Context, session client.Session, date client.Date, op client.Op,
) (HRVDay, error) {
	req := readRequest(op, client.EndpointHRV, datedPath(client.PathHRVPrefix, date), nil)
	if err := requireDate(req, date); err != nil {
		return HRVDay{}, err
	}

	var day HRVDay
	payload, err := t.req.read(ctx, session, req, &day)
	if err != nil {
		return HRVDay{}, err
	}
	day.raw = payload
	return day, nil
}

// TrainingLoadDay reads one day of the aggregated training status for the training
// load trend.
func (t *TrainingTrends) TrainingLoadDay(
	ctx context.Context, session client.Session, date client.Date,
) (TrainingStatus, error) {
	return t.trainingStatus(ctx, session, date, client.OpGetTrainingLoadTrend)
}

// TrainingLoadBalance reads the same document for the load-focus tool.
func (t *TrainingTrends) TrainingLoadBalance(
	ctx context.Context, session client.Session, date client.Date,
) (TrainingStatus, error) {
	return t.trainingStatus(ctx, session, date, client.OpGetTrainingLoadBalance)
}

// VO2MaxFromTrainingStatus reads the same document as the VO2 max trend's per-day
// fallback, for a day the max-metrics range did not cover.
func (t *TrainingTrends) VO2MaxFromTrainingStatus(
	ctx context.Context, session client.Session, date client.Date,
) (TrainingStatus, error) {
	return t.trainingStatus(ctx, session, date, client.OpGetVO2MaxTrend)
}

func (t *TrainingTrends) trainingStatus(
	ctx context.Context, session client.Session, date client.Date, op client.Op,
) (TrainingStatus, error) {
	req := readRequest(op, client.EndpointTrainingStatus,
		datedPath(client.PathTrainingStatusPrefix, date), nil)
	if err := requireDate(req, date); err != nil {
		return TrainingStatus{}, err
	}

	var day TrainingStatus
	payload, err := t.req.read(ctx, session, req, &day)
	if err != nil {
		return TrainingStatus{}, err
	}
	day.raw = payload
	return day, nil
}

// MaxMetrics reads the VO2 max series for a whole window in one request.
//
// Source: get_max_metrics_range, which asks the same endpoint as the single-day read
// with a distinct start and end segment.
func (t *TrainingTrends) MaxMetrics(
	ctx context.Context, session client.Session, span client.DateRange,
) (MaxMetrics, error) {
	path := client.PathMaxMetricsPrefix + "/" + span.Start().String() + "/" + span.End().String()
	req := readRequest(client.OpGetVO2MaxTrend, client.EndpointMaxMetrics, path, nil)

	if err := t.req.limits().ValidateDateRange(span); err != nil {
		return MaxMetrics{}, invalid(req, err)
	}

	var metrics MaxMetrics
	payload, err := t.req.read(ctx, session, req, &metrics)
	if err != nil {
		return MaxMetrics{}, err
	}
	metrics.raw = payload
	return metrics, nil
}

// ProfileVO2Max reads the account settings document for its VO2 max estimate, which is
// the VO2 max trend's last fallback.
func (t *TrainingTrends) ProfileVO2Max(
	ctx context.Context, session client.Session,
) (ProfileVO2Max, error) {
	req := readRequest(client.OpGetVO2MaxTrend, client.EndpointUserSettings,
		client.PathUserSettings, nil)

	var profile ProfileVO2Max
	payload, err := t.req.read(ctx, session, req, &profile)
	if err != nil {
		return ProfileVO2Max{}, err
	}
	profile.raw = payload
	return profile, nil
}

// RequestReload asks Garmin to re-derive the offloaded epoch data of one day.
//
// The declared effect is EffectIdempotentWrite, which the request layer's retry
// predicate reads as repeatable. That is deliberate. The request carries no body and
// no credential, it is keyed by the account and the calendar date alone, and it
// creates no user record: a second request re-triggers the same server-side recompute
// and converges on the same end state, which is the classification the pinned manifest
// records for request_reload. The cost of a repeat is one more recompute upstream,
// bounded by the request layer's attempt limit; the cost of declaring it unsafe would
// be reporting a transient 503 as a failed reload the caller has to repeat by hand.
// The post-401 replay is refused regardless, because the request layer never replays a
// POST.
func (t *TrainingTrends) RequestReload(
	ctx context.Context, session client.Session, date client.Date,
) (WriteResult, error) {
	req := writeRequest(client.OpRequestReload, client.EndpointEpochReloadRequest,
		http.MethodPost, datedPath(client.PathEpochReloadRequestPrefix, date),
		client.EffectIdempotentWrite)
	if err := requireDate(req, date); err != nil {
		return WriteResult{}, err
	}

	payload, err := t.req.write(ctx, session, req, nil)
	if err != nil {
		return WriteResult{}, err
	}
	return newWriteResult(payload), nil
}
