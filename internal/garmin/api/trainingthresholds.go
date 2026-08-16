package api

import (
	"context"
	"net/url"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// The threshold reads of the training surface: the latest cycling functional
// threshold power, and the five endpoints get_lactate_threshold spans. They share the
// TrainingScores client declared in trainingscores.go, and they are split off only so
// that neither file grows past this project's file-length rule.
//
// Source: python-garminconnect 0.3.10 get_cycling_ftp,
// get_functional_threshold_power_range and get_lactate_threshold.

// A ThresholdPower is one functional-threshold-power record. The same shape serves
// the latest cycling FTP and the latest running power-to-weight.
type ThresholdPower struct {
	Sport                    client.Text   `json:"sport"`
	FunctionalThresholdPower client.Number `json:"functionalThresholdPower"`
	CalendarDate             *string       `json:"calendarDate"`
	IsStale                  *bool         `json:"isStale"`
	BiometricSourceType      client.Text   `json:"biometricSourceType"`
	Weight                   client.Number `json:"weight"`
	PowerToWeight            client.Number `json:"powerToWeight"`
}

// CyclingFTP reads the latest cycling functional threshold power.
//
// Garmin answers with either one object or a list of them, which is why the result is
// a union-decoded list. Source: get_cycling_ftp, whose return type is dict or list.
func (t *TrainingScores) CyclingFTP(
	ctx context.Context, session client.Session,
) ([]ThresholdPower, error) {
	path := client.PathLatestFunctionalThresholdPowerPrefix + "/" + client.SportCycling
	req := readRequest(client.OpGetCyclingFTP, client.EndpointLatestFunctionalThresholdPower,
		path, nil)

	var list client.List[ThresholdPower]
	if _, err := t.req.read(ctx, session, req, &list); err != nil {
		return nil, err
	}
	return list.Items(), nil
}

// A LactateThresholdEntry is one entry of the latest lactate-threshold document.
//
// Garmin answers with several nearly identical entries, one carrying the speed and
// another the heart rate, which is why upstream folds them together. HeartRateTypo is
// Garmin's historical misspelling, which upstream still falls back to.
type LactateThresholdEntry struct {
	CalendarDate     *string       `json:"calendarDate"`
	Sequence         client.Number `json:"sequence"`
	Version          client.Number `json:"version"`
	Speed            client.Number `json:"speed"`
	HeartRate        client.Number `json:"heartRate"`
	HeartRateTypo    client.Number `json:"hearRate"`
	HeartRateCycling client.Number `json:"heartRateCycling"`
}

// LatestLactateThreshold reads the latest lactate-threshold speed and heart rate.
func (t *TrainingScores) LatestLactateThreshold(
	ctx context.Context, session client.Session,
) ([]LactateThresholdEntry, error) {
	req := readRequest(client.OpGetLactateThreshold, client.EndpointLatestLactateThreshold,
		client.PathLatestLactateThreshold, nil)

	var list client.List[LactateThresholdEntry]
	if _, err := t.req.read(ctx, session, req, &list); err != nil {
		return nil, err
	}
	return list.Items(), nil
}

// LatestPowerToWeight reads the latest running power-to-weight record as of asOf.
//
// The sport parameter is sent in upstream's mixed-case spelling, "Running", which is
// the only place that spelling appears. It is deliberate and is not normalized.
func (t *TrainingScores) LatestPowerToWeight(
	ctx context.Context, session client.Session, asOf client.Date,
) ([]ThresholdPower, error) {
	query := url.Values{}
	query.Set(client.QuerySport, client.SportRunningMixedCase)
	req := readRequest(client.OpGetLactateThreshold, client.EndpointPowerToWeightLatest,
		datedPath(client.PathPowerToWeightLatestPrefix, asOf), query)
	if err := requireDate(req, asOf); err != nil {
		return nil, err
	}

	var list client.List[ThresholdPower]
	if _, err := t.req.read(ctx, session, req, &list); err != nil {
		return nil, err
	}
	return list.Items(), nil
}

// A ThresholdSample is one point of a biometric statistics range.
//
// Series names the sport the sample belongs to. It is a plain string on the wire —
// sampled, not assumed — and the value set is treated as open: any string is carried
// through, none is rejected, and no enumeration is modelled over it. It decodes
// through client.Text, so a value Garmin sends as a number or a boolean still reads,
// and a shape no sample has ever shown — an object, an array — is refused as a
// malformed payload rather than passed on unread.
type ThresholdSample struct {
	From   client.Text   `json:"from"`
	Value  client.Number `json:"value"`
	Series client.Text   `json:"series"`
}

// LactateThresholdSpeedRange reads the lactate-threshold speed series.
func (t *TrainingScores) LactateThresholdSpeedRange(
	ctx context.Context, session client.Session, span client.DateRange,
) ([]ThresholdSample, error) {
	return t.thresholdRange(ctx, session, client.EndpointLactateThresholdSpeedRange,
		client.PathLactateThresholdSpeedRangePrefix, span)
}

// LactateThresholdHeartRateRange reads the lactate-threshold heart-rate series.
func (t *TrainingScores) LactateThresholdHeartRateRange(
	ctx context.Context, session client.Session, span client.DateRange,
) ([]ThresholdSample, error) {
	return t.thresholdRange(ctx, session, client.EndpointLactateThresholdHeartRateRange,
		client.PathLactateThresholdHeartRateRangePrefix, span)
}

// FunctionalThresholdPowerRange reads the running functional-threshold-power series.
func (t *TrainingScores) FunctionalThresholdPowerRange(
	ctx context.Context, session client.Session, span client.DateRange,
) ([]ThresholdSample, error) {
	return t.thresholdRange(ctx, session, client.EndpointFunctionalThresholdPowerRange,
		client.PathFunctionalThresholdPowerRangePrefix, span)
}

// thresholdRange reads one biometric statistics range. The window is two path
// segments, each from a validated client.Date, so neither can carry a separator, and
// the three fixed parameters are the ones upstream sends.
func (t *TrainingScores) thresholdRange(
	ctx context.Context, session client.Session, endpoint client.Endpoint,
	prefix string, span client.DateRange,
) ([]ThresholdSample, error) {
	query := url.Values{}
	query.Set(client.QuerySport, client.SportRunning)
	query.Set(client.QueryAggregation, client.AggregationDaily)
	query.Set(client.QueryAggregationStrategy, client.AggregationStrategyLatest)
	path := datedPath(datedPath(prefix, span.Start()), span.End())
	req := readRequest(client.OpGetLactateThreshold, endpoint, path, query)

	var list client.List[ThresholdSample]
	if err := t.readWindow(ctx, session, req, span, &list); err != nil {
		return nil, err
	}
	return list.Items(), nil
}
