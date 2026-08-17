package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// weighInTimestampLayout is the millisecond wall-clock form both weigh-in
// timestamp fields use. Source: garminconnect/__init__.py, _fmt_ts:
// dt.replace(tzinfo=None).strftime("%Y-%m-%dT%H:%M:%S.%f")[:-3] — the
// microsecond field truncated to three digits, with no timezone suffix.
//
// Unlike nutritionwritelog.go's garminTimestampLayout, the fraction here is
// the instant's real millisecond component, not a fixed literal: Go's ".000"
// verb renders the actual fraction padded to three digits, matching Python's
// [:-3] truncation of a six-digit microsecond string.
const weighInTimestampLayout = "2006-01-02T15:04:05.000"

// renderLocalWeighInTimestamp renders instant in its own location, with no
// conversion. Source: _fmt_ts applied to dt, which renders a caller's local
// wall-clock value as given, never converted to another zone.
func renderLocalWeighInTimestamp(instant time.Time) string {
	return instant.Format(weighInTimestampLayout)
}

// renderGMTWeighInTimestamp renders the UTC wall-clock of instant. Source:
// _fmt_ts applied to dtGMT = dt.astimezone(UTC).
func renderGMTWeighInTimestamp(instant time.Time) string {
	return instant.UTC().Format(weighInTimestampLayout)
}

// WeighInEntry is the strict request model for one weigh-in write, shared by
// add_weigh_in and add_weigh_in_with_timestamps: both build the identical
// payload shape (garminconnect/__init__.py:1238-1244 and :1278-1284), differing
// only in how the two timestamps were obtained — this package takes both as
// already-resolved instants and leaves any "use the current time" default to
// its caller, which is where a clock belongs.
type WeighInEntry struct {
	// Weight is the measurement in Unit's scale. Required, must be positive.
	Weight float64
	// Unit is the scale Weight is expressed in.
	Unit WeightUnit
	// LocalAt is the instant to render as the account's own local wall-clock
	// timestamp, in whatever location it carries. Required.
	LocalAt time.Time
	// GMTAt is the instant to render as the UTC wall-clock timestamp. Required.
	// It need not share LocalAt's Location, only its meaning: the same real
	// instant, expressed in UTC.
	GMTAt time.Time
}

// validate reports whether the entry may be dispatched.
func (e WeighInEntry) validate(req client.Request) error {
	if err := requireFiniteNutrient(req, e.Weight, "weight"); err != nil {
		return err
	}
	if e.Weight <= 0 {
		return invalid(req, fmt.Errorf("%w: weight must be positive", client.ErrValidation))
	}
	switch e.Unit {
	case WeightUnitKg, WeightUnitLbs:
	default:
		return invalid(req, fmt.Errorf("%w: unit must be %q or %q",
			client.ErrValidation, WeightUnitKg, WeightUnitLbs))
	}
	if e.LocalAt.IsZero() {
		return invalid(req, fmt.Errorf("%w: a local timestamp is required", client.ErrValidation))
	}
	if e.GMTAt.IsZero() {
		return invalid(req, fmt.Errorf("%w: a gmt timestamp is required", client.ErrValidation))
	}
	return nil
}

// weighInDTO is the wire shape both add_weigh_in and add_weigh_in_with_timestamps
// POST. Source: garminconnect/__init__.py:1238-1244, :1278-1284.
type weighInDTO struct {
	DateTimestamp string  `json:"dateTimestamp"`
	GMTTimestamp  string  `json:"gmtTimestamp"`
	UnitKey       string  `json:"unitKey"`
	SourceType    string  `json:"sourceType"`
	Value         float64 `json:"value"`
}

// AddWeighIn adds a weigh-in, with the caller resolving the local and GMT
// instants it is recorded at.
//
// Its effect is EffectUnsafeWrite, not EffectIdempotentWrite: each POST with no
// target identifier creates a new record rather than replacing one, so the
// retry layer must never replay a lost response.
//
// Source: add_weigh_in, POST "/weight-service/user-weight"
// (garminconnect/__init__.py:1219-1246).
func (w *Weight) AddWeighIn(
	ctx context.Context, session client.Session, entry WeighInEntry,
) (WriteResult, error) {
	return w.addWeighIn(ctx, session, client.OpAddWeighIn, entry)
}

// AddWeighInWithTimestamps adds a weigh-in, dispatching the identical wire
// shape AddWeighIn does. It exists as its own method, rather than an alias, so
// its Op label matches the distinct add_weigh_in_with_timestamps tool.
//
// Source: add_weigh_in_with_timestamps, POST "/weight-service/user-weight"
// (garminconnect/__init__.py:1248-1290).
func (w *Weight) AddWeighInWithTimestamps(
	ctx context.Context, session client.Session, entry WeighInEntry,
) (WriteResult, error) {
	return w.addWeighIn(ctx, session, client.OpAddWeighInWithTimestamps, entry)
}

// addWeighIn performs the POST both AddWeighIn and AddWeighInWithTimestamps
// share, dispatched under the caller's chosen Op.
func (w *Weight) addWeighIn(
	ctx context.Context, session client.Session, op client.Op, entry WeighInEntry,
) (WriteResult, error) {
	req := writeRequest(op, client.EndpointWeightUserWeight,
		http.MethodPost, client.PathWeightUserWeight, client.EffectUnsafeWrite)
	if err := entry.validate(req); err != nil {
		return WriteResult{}, err
	}

	body, err := jsonBody(req, weighInDTO{
		DateTimestamp: renderLocalWeighInTimestamp(entry.LocalAt),
		GMTTimestamp:  renderGMTWeighInTimestamp(entry.GMTAt),
		UnitKey:       string(entry.Unit),
		SourceType:    client.WeightSourceManual,
		Value:         entry.Weight,
	})
	if err != nil {
		return WriteResult{}, err
	}
	req.Body = body

	payload, err := w.req.write(ctx, session, req, nil)
	if err != nil {
		return WriteResult{}, err
	}
	return newWriteResult(payload), nil
}
