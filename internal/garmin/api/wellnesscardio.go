package api

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// WellnessCardio reads the cardio, respiration and logging half of the
// health-and-wellness surface: daily heart rate, resting heart rate, respiration,
// pulse ox, hydration, blood pressure and the lifestyle log.
//
// Every document it returns is health data. Never log one, never cache one.
//
// Two of the endpoints answer two tools each: the daily heart-rate document serves
// get_heart_rates and get_heart_rates_summary, and the daily respiration document
// serves get_respiration_data and get_respiration_summary. Each pair is one read with
// two views, so a pair costs one Garmin request, not two. The two reads differ only in
// the sanitized operation label, which is what names the call in a log line.
type WellnessCardio struct {
	req requester
}

// NewWellnessCardio returns a cardio client over the request layer.
func NewWellnessCardio(rc *client.Client) (*WellnessCardio, error) {
	req, err := newRequester(rc)
	if err != nil {
		return nil, err
	}
	return &WellnessCardio{req: req}, nil
}

// Cardio returns the cardio client over the same request layer as w.
//
// The two clients read the same wellness surface and are separated only so that one
// file does not carry every endpoint. Nothing is copied but the request layer, which
// is itself immutable after construction.
func (w *Wellness) Cardio() *WellnessCardio { return &WellnessCardio{req: w.req} }

// DailyHeartRate is one day of heart rate.
//
// userProfilePK is deliberately not decoded: it is an account identifier, and no tool
// returns or logs one. The timestamp fields arrive in the layout
// "2006-01-02T15:04:05.0" — one fractional digit and no zone — which is neither
// RFC 3339 nor a three-digit fraction, so they are carried as text and never parsed.
type DailyHeartRate struct {
	CalendarDate                     *string            `json:"calendarDate"`
	StartTimestampGMT                client.Text        `json:"startTimestampGMT"`
	EndTimestampGMT                  client.Text        `json:"endTimestampGMT"`
	MaxHeartRate                     client.Number      `json:"maxHeartRate"`
	MinHeartRate                     client.Number      `json:"minHeartRate"`
	RestingHeartRate                 client.Number      `json:"restingHeartRate"`
	LastSevenDaysAvgRestingHeartRate client.Number      `json:"lastSevenDaysAvgRestingHeartRate"`
	Descriptors                      []SeriesDescriptor `json:"heartRateValueDescriptors"`
	Values                           json.RawMessage    `json:"heartRateValues"`

	raw client.Payload
}

// Payload is the retained raw response.
func (d DailyHeartRate) Payload() client.Payload { return d.raw }

// Samples decodes the intraday series through its declared layout.
func (d DailyHeartRate) Samples() ([]Sample, error) {
	return ParseSeries(d.Values, d.Descriptors, SeriesKeyHeartRate)
}

// DailyRespiration is one day of breathing rate.
//
// The document carries two series: a per-sample one and an hourly aggregate one, each
// with its own descriptor list and its own descriptor key spelling. It also carries
// last night's and the coming night's sleep windows, which is why a respiration day
// has four timestamp pairs rather than one. userProfilePK is deliberately not decoded:
// it is an account identifier.
//
// Every timestamp field arrives in the layout "2006-01-02T15:04:05.0" and is carried
// as text, never parsed.
type DailyRespiration struct {
	CalendarDate      *string     `json:"calendarDate"`
	StartTimestampGMT client.Text `json:"startTimestampGMT"`
	EndTimestampGMT   client.Text `json:"endTimestampGMT"`

	SleepStartTimestampGMT   client.Text `json:"sleepStartTimestampGMT"`
	SleepEndTimestampGMT     client.Text `json:"sleepEndTimestampGMT"`
	SleepStartTimestampLocal client.Text `json:"sleepStartTimestampLocal"`
	SleepEndTimestampLocal   client.Text `json:"sleepEndTimestampLocal"`

	TomorrowSleepStartTimestampGMT   client.Text `json:"tomorrowSleepStartTimestampGMT"`
	TomorrowSleepEndTimestampGMT     client.Text `json:"tomorrowSleepEndTimestampGMT"`
	TomorrowSleepStartTimestampLocal client.Text `json:"tomorrowSleepStartTimestampLocal"`
	TomorrowSleepEndTimestampLocal   client.Text `json:"tomorrowSleepEndTimestampLocal"`

	LowestRespirationValue           client.Number `json:"lowestRespirationValue"`
	HighestRespirationValue          client.Number `json:"highestRespirationValue"`
	AvgWakingRespirationValue        client.Number `json:"avgWakingRespirationValue"`
	AvgSleepRespirationValue         client.Number `json:"avgSleepRespirationValue"`
	AvgTomorrowSleepRespirationValue client.Number `json:"avgTomorrowSleepRespirationValue"`
	RespirationVersion               client.Number `json:"respirationVersion"`

	Descriptors []SeriesDescriptor `json:"respirationValueDescriptorsDTOList"`
	Values      json.RawMessage    `json:"respirationValuesArray"`

	AverageDescriptors []SeriesDescriptor `json:"respirationAveragesValueDescriptorDTOList"`
	AverageValues      json.RawMessage    `json:"respirationAveragesValuesArray"`

	raw client.Payload
}

// Payload is the retained raw response.
func (d DailyRespiration) Payload() client.Payload { return d.raw }

// Samples decodes the per-sample series through its declared layout.
func (d DailyRespiration) Samples() ([]Sample, error) {
	return ParseSeries(d.Values, d.Descriptors, SeriesKeyRespiration)
}

// HourlyAverages decodes the hourly aggregate series through its declared layout.
//
// That list declares its positions under different key names than the per-sample list
// does; SeriesDescriptor reads both, so this is not the positional fallback in
// disguise.
func (d DailyRespiration) HourlyAverages() ([]AverageSample, error) {
	return ParseAverageSeries(d.AverageValues, d.AverageDescriptors)
}

// DailySpO2 is one day of pulse oximetry.
//
// Source of the field names: the summary get_spo2_data builds in the pinned upstream
// MCP server, which reads averageSpO2, lowestSpO2, latestSpO2, lastSevenDaysAvgSpO2,
// avgSleepSpO2 and spO2HourlyAverages off the raw document. lastSevenDaysAvgSpO2 is
// documented upstream as occasionally arriving as a string, which client.Number
// already tolerates.
type DailySpO2 struct {
	CalendarDate           *string         `json:"calendarDate"`
	AverageSpO2            client.Number   `json:"averageSpO2"`
	LowestSpO2             client.Number   `json:"lowestSpO2"`
	LatestSpO2             client.Number   `json:"latestSpO2"`
	LatestSpO2TimestampGMT client.Text     `json:"latestSpO2TimestampGMT"`
	LastSevenDaysAvgSpO2   client.Number   `json:"lastSevenDaysAvgSpO2"`
	AvgSleepSpO2           client.Number   `json:"avgSleepSpO2"`
	HourlyAveragesRaw      json.RawMessage `json:"spO2HourlyAverages"`

	raw client.Payload
}

// Payload is the retained raw response.
func (d DailySpO2) Payload() client.Payload { return d.raw }

// HourlyAverages decodes the hourly average series.
//
// The document declares no descriptor list for it, so the tuple positions fall back to
// the observed order: the hour instant first, the average second.
func (d DailySpO2) HourlyAverages() ([]Sample, error) {
	return ParseSeries(d.HourlyAveragesRaw, nil, SeriesKeyTimestamp)
}

// RestingHeartRateDay is the user-statistics answer for one metric over one day.
//
// The metric map is read by key rather than by a hardcoded name: Garmin keys the
// series by an enumeration whose spelling this project has not pinned to a source, so
// the resting heart-rate series is found by substring, and a single-entry map is
// accepted whatever it is called.
type RestingHeartRateDay struct {
	StatisticsStartDate *string     `json:"statisticsStartDate"`
	StatisticsEndDate   *string     `json:"statisticsEndDate"`
	AllMetrics          *MetricSets `json:"allMetrics"`

	raw client.Payload
}

// MetricSets is the keyed metric map of a user-statistics document.
type MetricSets struct {
	MetricsMap map[string][]MetricPoint `json:"metricsMap"`
}

// A MetricPoint is one day of one metric.
type MetricPoint struct {
	CalendarDate *string       `json:"calendarDate"`
	Value        client.Number `json:"value"`
}

// Payload is the retained raw response.
func (r RestingHeartRateDay) Payload() client.Payload { return r.raw }

// restingHeartRateKeyPart identifies the resting heart-rate series among the keys of a
// user-statistics metric map.
const restingHeartRateKeyPart = "RESTING_HEART_RATE"

// RestingHeartRate returns the reading for day, when the document carries one.
//
// A point whose calendar date matches wins. Otherwise the first present value of the
// series is used, because the request asked for a single day and Garmin echoes that
// day even when it holds nothing for it.
func (r RestingHeartRateDay) RestingHeartRate(day client.Date) (client.Number, bool) {
	points, ok := r.series()
	if !ok {
		return client.Number{}, false
	}

	var fallback client.Number
	for _, point := range points {
		if !point.Value.IsSet() {
			continue
		}
		if point.CalendarDate != nil && *point.CalendarDate == day.String() {
			return point.Value, true
		}
		if !fallback.IsSet() {
			fallback = point.Value
		}
	}
	return fallback, fallback.IsSet()
}

// series picks the resting heart-rate series out of the metric map.
func (r RestingHeartRateDay) series() ([]MetricPoint, bool) {
	if r.AllMetrics == nil || len(r.AllMetrics.MetricsMap) == 0 {
		return nil, false
	}
	for key, points := range r.AllMetrics.MetricsMap {
		if strings.Contains(strings.ToUpper(key), restingHeartRateKeyPart) {
			return points, true
		}
	}
	if len(r.AllMetrics.MetricsMap) != 1 {
		return nil, false
	}
	for _, points := range r.AllMetrics.MetricsMap {
		return points, true
	}
	return nil, false
}

// HeartRates reads one day of heart rate for get_heart_rates.
func (c *WellnessCardio) HeartRates(
	ctx context.Context, session client.Session, name client.DisplayName, date client.Date,
) (DailyHeartRate, error) {
	return c.heartRates(ctx, session, name, date, client.OpGetHeartRates)
}

// HeartRatesSummary reads the same document for get_heart_rates_summary.
func (c *WellnessCardio) HeartRatesSummary(
	ctx context.Context, session client.Session, name client.DisplayName, date client.Date,
) (DailyHeartRate, error) {
	return c.heartRates(ctx, session, name, date, client.OpGetHeartRatesSummary)
}

func (c *WellnessCardio) heartRates(
	ctx context.Context, session client.Session, name client.DisplayName,
	date client.Date, op client.Op,
) (DailyHeartRate, error) {
	query := url.Values{}
	query.Set(client.QueryDate, date.String())
	req := readRequest(op, client.EndpointDailyHeartRate,
		displayNamePath(client.PathDailyHeartRatePrefix, name), query)

	if err := requireDisplayName(req, name); err != nil {
		return DailyHeartRate{}, err
	}
	if err := requireDate(req, date); err != nil {
		return DailyHeartRate{}, err
	}

	var day DailyHeartRate
	payload, err := c.req.read(ctx, session, req, &day)
	if err != nil {
		return DailyHeartRate{}, err
	}
	day.raw = payload
	return day, nil
}

// RestingHeartRateDay reads the one-day resting heart-rate series.
//
// Source: get_rhr_day, which asks the user-statistics daily endpoint for metric 60
// with fromDate and untilDate both set to the requested day.
func (c *WellnessCardio) RestingHeartRateDay(
	ctx context.Context, session client.Session, name client.DisplayName, date client.Date,
) (RestingHeartRateDay, error) {
	query := url.Values{}
	query.Set(client.QueryFromDate, date.String())
	query.Set(client.QueryUntilDate, date.String())
	query.Set(client.QueryMetricID, strconv.Itoa(client.MetricIDRestingHeartRate))
	req := readRequest(client.OpGetRestingHeartRateDay, client.EndpointRestingHeartRate,
		displayNamePath(client.PathRestingHeartRatePrefix, name), query)

	if err := requireDisplayName(req, name); err != nil {
		return RestingHeartRateDay{}, err
	}
	if err := requireDate(req, date); err != nil {
		return RestingHeartRateDay{}, err
	}

	var day RestingHeartRateDay
	payload, err := c.req.read(ctx, session, req, &day)
	if err != nil {
		return RestingHeartRateDay{}, err
	}
	day.raw = payload
	return day, nil
}

// Respiration reads one day of breathing rate for get_respiration_data.
func (c *WellnessCardio) Respiration(
	ctx context.Context, session client.Session, date client.Date,
) (DailyRespiration, error) {
	return c.respiration(ctx, session, date, client.OpGetRespirationData)
}

// RespirationSummary reads the same document for get_respiration_summary.
func (c *WellnessCardio) RespirationSummary(
	ctx context.Context, session client.Session, date client.Date,
) (DailyRespiration, error) {
	return c.respiration(ctx, session, date, client.OpGetRespirationSummary)
}

func (c *WellnessCardio) respiration(
	ctx context.Context, session client.Session, date client.Date, op client.Op,
) (DailyRespiration, error) {
	req := readRequest(op, client.EndpointDailyRespiration,
		datedPath(client.PathDailyRespirationPrefix, date), nil)
	if err := requireDate(req, date); err != nil {
		return DailyRespiration{}, err
	}

	var day DailyRespiration
	payload, err := c.req.read(ctx, session, req, &day)
	if err != nil {
		return DailyRespiration{}, err
	}
	day.raw = payload
	return day, nil
}

// SpO2 reads one day of pulse oximetry.
func (c *WellnessCardio) SpO2(
	ctx context.Context, session client.Session, date client.Date,
) (DailySpO2, error) {
	req := readRequest(client.OpGetSpO2Data, client.EndpointDailySpO2,
		datedPath(client.PathDailySpO2Prefix, date), nil)
	if err := requireDate(req, date); err != nil {
		return DailySpO2{}, err
	}

	var day DailySpO2
	payload, err := c.req.read(ctx, session, req, &day)
	if err != nil {
		return DailySpO2{}, err
	}
	day.raw = payload
	return day, nil
}

// datedPath appends a validated calendar date as one path segment. A client.Date
// renders as YYYY-MM-DD and can carry no separator, so nothing needs escaping away.
func datedPath(prefix string, date client.Date) string {
	return prefix + "/" + date.String()
}
