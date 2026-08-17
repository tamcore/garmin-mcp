package api

import (
	"context"
	"net/url"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// WeighInMeasurement is one weigh-in reading.
//
// Weight is the raw measurement in grams — weight_management.py divides it by
// 1000 only to build its own curated "weight_kg" figure
// (weight_management.py:57, :112), the field itself is grams. CalendarDate,
// SourceType and TimestampGMT, and the five body-composition figures, are read
// verbatim in both get_weigh_ins' and get_daily_weigh_ins' curation loops
// (weight_management.py:53-65, :109-120). SamplePK is the identifier
// delete_weigh_ins reads off each entry of get_daily_weigh_ins' own
// dateWeightList to build the per-item delete path
// (garminconnect/__init__.py:1342-1343, w["samplePk"]); its presence is
// therefore only established for DailyWeighIns.DateWeightList, not for
// WeighInRange's allWeightMetrics entries — Number tolerates its absence there
// rather than failing the read.
type WeighInMeasurement struct {
	CalendarDate client.Text   `json:"calendarDate"`
	Weight       client.Number `json:"weight"`
	BMI          client.Number `json:"bmi"`
	BodyFat      client.Number `json:"bodyFat"`
	BodyWater    client.Number `json:"bodyWater"`
	BoneMass     client.Number `json:"boneMass"`
	MuscleMass   client.Number `json:"muscleMass"`
	SourceType   client.Text   `json:"sourceType"`
	TimestampGMT client.Text   `json:"timestampGMT"`
	SamplePK     client.Number `json:"samplePk"`
}

// WeighInAverage is the averaged weight over a read's window or day.
// Source: weight_management.py:76-79 and :126-129, total_avg.get("weight").
type WeighInAverage struct {
	Weight client.Number `json:"weight"`
}

// weighInDailySummary is one day-group of measurements inside WeighInRange.
// Source: weight_management.py:41-43, day.get("allWeightMetrics").
type weighInDailySummary struct {
	AllWeightMetrics []WeighInMeasurement `json:"allWeightMetrics"`
}

// maxWeighInMeasurements bounds the measurements this package will flatten out
// of one response, so a malformed or hostile document cannot force an
// unbounded result even though the request layer already bounds the response's
// wire and decompressed byte size. It is generous headroom over any real
// account: a manual weigh-in is at most a handful of entries per day.
const maxWeighInMeasurements = 1000

// WeighInRange is the answer for get_weigh_ins over a date window.
// Source: weight_management.py:34-37 (dailyWeightSummaries), :76-79
// (totalAverage.weight).
type WeighInRange struct {
	DailySummaries []weighInDailySummary `json:"dailyWeightSummaries"`
	TotalAverage   *WeighInAverage       `json:"totalAverage"`

	raw client.Payload
}

// Payload is the retained raw response.
func (r WeighInRange) Payload() client.Payload { return r.raw }

// Measurements flattens every day's allWeightMetrics into payload order,
// bounded by maxWeighInMeasurements. MeasurementsTruncated reports whether the
// bound was reached.
func (r WeighInRange) Measurements() []WeighInMeasurement {
	all := r.allMeasurements()
	if len(all) > maxWeighInMeasurements {
		return all[:maxWeighInMeasurements]
	}
	return all
}

// MeasurementsTruncated reports whether Measurements dropped any entry to stay
// inside maxWeighInMeasurements.
func (r WeighInRange) MeasurementsTruncated() bool {
	return len(r.allMeasurements()) > maxWeighInMeasurements
}

func (r WeighInRange) allMeasurements() []WeighInMeasurement {
	var all []WeighInMeasurement
	for _, day := range r.DailySummaries {
		all = append(all, day.AllWeightMetrics...)
	}
	return all
}

// DailyWeighIns is the answer for get_daily_weigh_ins for one day.
// Source: weight_management.py:97-98 (dateWeightList), :126-129
// (totalAverage.weight).
type DailyWeighIns struct {
	DateWeightList []WeighInMeasurement `json:"dateWeightList"`
	TotalAverage   *WeighInAverage      `json:"totalAverage"`

	raw client.Payload
}

// Payload is the retained raw response.
func (d DailyWeighIns) Payload() client.Payload { return d.raw }

// Measurements returns the day's weigh-ins, bounded by maxWeighInMeasurements.
// MeasurementsTruncated reports whether the bound was reached.
func (d DailyWeighIns) Measurements() []WeighInMeasurement {
	if len(d.DateWeightList) > maxWeighInMeasurements {
		return d.DateWeightList[:maxWeighInMeasurements]
	}
	return d.DateWeightList
}

// MeasurementsTruncated reports whether Measurements dropped any entry to stay
// inside maxWeighInMeasurements.
func (d DailyWeighIns) MeasurementsTruncated() bool {
	return len(d.DateWeightList) > maxWeighInMeasurements
}

// GetWeighIns reads every weigh-in in an inclusive date window.
//
// The window is validated against the request layer's configured bound before
// anything is dispatched, so a caller cannot ask for a decade of readings in
// one call. includeAll is sent exactly as upstream sends it.
//
// Source: get_weigh_ins, GET "/weight-service/weight/range/{startdate}/{enddate}"
// (garminconnect/__init__.py:1292-1300).
func (w *Weight) GetWeighIns(
	ctx context.Context, session client.Session, span client.DateRange,
) (WeighInRange, error) {
	query := url.Values{}
	query.Set(client.QueryIncludeAll, "true")
	path := client.PathWeightRangePrefix + "/" + span.Start().String() + "/" + span.End().String()
	req := readRequest(client.OpGetWeighIns, client.EndpointWeightRange, path, query)

	if err := w.requireWindow(req, span); err != nil {
		return WeighInRange{}, err
	}

	var window WeighInRange
	payload, err := w.req.read(ctx, session, req, &window)
	if err != nil {
		return WeighInRange{}, err
	}
	window.raw = payload
	return window, nil
}

// GetDailyWeighIns reads every weigh-in for one calendar day.
//
// Source: get_daily_weigh_ins, GET "/weight-service/weight/dayview/{cdate}"
// (garminconnect/__init__.py:1302-1309).
func (w *Weight) GetDailyWeighIns(
	ctx context.Context, session client.Session, date client.Date,
) (DailyWeighIns, error) {
	query := url.Values{}
	query.Set(client.QueryIncludeAll, "true")
	req := readRequest(client.OpGetDailyWeighIns, client.EndpointWeightDayview,
		datedPath(client.PathWeightDayviewPrefix, date), query)

	if err := requireDate(req, date); err != nil {
		return DailyWeighIns{}, err
	}

	var day DailyWeighIns
	payload, err := w.req.read(ctx, session, req, &day)
	if err != nil {
		return DailyWeighIns{}, err
	}
	day.raw = payload
	return day, nil
}

// requireWindow refuses an unset or oversized window before anything is
// dispatched, matching WellnessDaily.requireWindow.
func (w *Weight) requireWindow(req client.Request, span client.DateRange) error {
	if span.IsZero() {
		return invalid(req, client.ErrValidation)
	}
	if err := w.req.limits().ValidateDateRange(span); err != nil {
		return invalid(req, err)
	}
	return nil
}
