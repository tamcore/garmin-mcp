package api

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// DailyHydration is one day of fluid intake.
//
// calendarDate and valueInML are pinned: they are the keys add_hydration_data sends to
// the same service upstream. The remaining fields are what the day report is expected
// to carry alongside them; each is optional, so a document without them still decodes.
type DailyHydration struct {
	CalendarDate            *string       `json:"calendarDate"`
	ValueInML               client.Number `json:"valueInML"`
	GoalInML                client.Number `json:"goalInML"`
	DailyAverageInML        client.Number `json:"dailyAverageinML"`
	SweatLossInML           client.Number `json:"sweatLossInML"`
	ActivityIntakeInML      client.Number `json:"activityIntakeInML"`
	LastEntryTimestampLocal client.Text   `json:"lastEntryTimestampLocal"`

	raw client.Payload
}

// Payload is the retained raw response.
func (d DailyHydration) Payload() client.Payload { return d.raw }

// A BloodPressureMeasurement is one reading.
//
// The field names are pinned to the payload set_blood_pressure sends to the same
// service upstream. notes is the account holder's own free text and is carried
// verbatim; like every other field here it is never logged.
type BloodPressureMeasurement struct {
	Systolic                  client.Number `json:"systolic"`
	Diastolic                 client.Number `json:"diastolic"`
	Pulse                     client.Number `json:"pulse"`
	MeasurementTimestampGMT   client.Text   `json:"measurementTimestampGMT"`
	MeasurementTimestampLocal client.Text   `json:"measurementTimestampLocal"`
	SourceType                client.Text   `json:"sourceType"`
	Notes                     client.Text   `json:"notes"`
}

// A BloodPressureSummary is one day-group of measurements.
type BloodPressureSummary struct {
	MeasurementSummaryDate *string                    `json:"measurementSummaryDate"`
	Measurements           []BloodPressureMeasurement `json:"measurements"`
}

// BloodPressureRange is the answer for one date window.
//
// The envelope is decoded as a union because it is not pinned to a source: a document
// that groups measurements by day and one that lists them at the top level are both
// accepted, and Measurements flattens whichever arrived.
type BloodPressureRange struct {
	Summaries []BloodPressureSummary     `json:"measurementSummaries"`
	Direct    []BloodPressureMeasurement `json:"measurements"`

	raw client.Payload
}

// Payload is the retained raw response.
func (b BloodPressureRange) Payload() client.Payload { return b.raw }

// Measurements returns every reading the document carried, in payload order.
func (b BloodPressureRange) Measurements() []BloodPressureMeasurement {
	out := make([]BloodPressureMeasurement, 0, len(b.Direct))
	out = append(out, b.Direct...)
	for _, summary := range b.Summaries {
		out = append(out, summary.Measurements...)
	}
	return out
}

// LifestyleLog is one day of the lifestyle log.
//
// The document's field set is not established by any pinned source: upstream returns
// it whole and names nothing inside it. Rather than invent a schema it is carried
// opaquely, which is also what upstream does. Give it a shape only once a real
// document has been sampled.
type LifestyleLog struct {
	// Document is the response body, verbatim. It is health data.
	Document json.RawMessage

	raw client.Payload
}

// Payload is the retained raw response.
func (l LifestyleLog) Payload() client.Payload { return l.raw }

// HasDocument reports whether Garmin returned a document at all.
func (l LifestyleLog) HasDocument() bool {
	return len(l.Document) > 0 && string(l.Document) != jsonNull
}

// A SleepDigest is the part of the daily sleep document the narrow summary needs and
// the DailySleep model does not carry.
//
// It is decoded from the payload DailySleep already retained, so get_sleep_summary
// reuses the one sleep read rather than issuing a second one. The field set is pinned
// to the summary the upstream MCP server builds from the same document.
type SleepDigest struct {
	Daily           *SleepDigestDaily `json:"dailySleepDTO"`
	SpO2            *SleepDigestSpO2  `json:"wellnessSpO2SleepSummaryDTO"`
	AvgOvernightHRV client.Number     `json:"avgOvernightHrv"`
}

// SleepDigestDaily is the nested per-day part of the digest.
type SleepDigestDaily struct {
	NapTimeSeconds       client.Number  `json:"napTimeSeconds"`
	AwakeCount           client.Number  `json:"awakeCount"`
	RestlessMomentsCount client.Number  `json:"restlessMomentsCount"`
	AvgSleepStress       client.Number  `json:"avgSleepStress"`
	RestingHeartRate     client.Number  `json:"restingHeartRate"`
	SleepScores          *SleepScoreSet `json:"sleepScores"`
}

// SleepScoreSet is the score group of a sleep document.
type SleepScoreSet struct {
	Overall *SleepScoreEntry `json:"overall"`
}

// A SleepScoreEntry is one scored dimension.
type SleepScoreEntry struct {
	Value        client.Number `json:"value"`
	QualifierKey client.Text   `json:"qualifierKey"`
}

// SleepDigestSpO2 is the overnight pulse-ox part of the digest. Garmin spells these
// two keys with a lowercase "o", unlike the daily pulse-ox document.
type SleepDigestSpO2 struct {
	AverageSpO2 client.Number `json:"averageSpo2"`
	LowestSpO2  client.Number `json:"lowestSpo2"`
}

// Overall returns the overall sleep score, when the document carries one.
func (d SleepDigest) Overall() (SleepScoreEntry, bool) {
	if d.Daily == nil || d.Daily.SleepScores == nil || d.Daily.SleepScores.Overall == nil {
		return SleepScoreEntry{}, false
	}
	return *d.Daily.SleepScores.Overall, true
}

// NewSleepDigest decodes the digest out of a sleep document that was already read.
//
// It performs no request. An empty payload decodes to the zero digest and no error,
// because a day with no wearable data is a normal state.
func NewSleepDigest(sleep DailySleep) (SleepDigest, error) {
	var digest SleepDigest
	if err := client.DecodeJSON(sleep.Payload(), &digest); err != nil {
		return SleepDigest{}, err
	}
	return digest, nil
}

// Hydration reads one day of fluid intake.
func (c *WellnessCardio) Hydration(
	ctx context.Context, session client.Session, date client.Date,
) (DailyHydration, error) {
	req := readRequest(client.OpGetHydrationData, client.EndpointDailyHydration,
		datedPath(client.PathDailyHydrationPrefix, date), nil)
	if err := requireDate(req, date); err != nil {
		return DailyHydration{}, err
	}

	var day DailyHydration
	payload, err := c.req.read(ctx, session, req, &day)
	if err != nil {
		return DailyHydration{}, err
	}
	day.raw = payload
	return day, nil
}

// LifestyleLogging reads one day of the lifestyle log.
func (c *WellnessCardio) LifestyleLogging(
	ctx context.Context, session client.Session, date client.Date,
) (LifestyleLog, error) {
	req := readRequest(client.OpGetLifestyleLoggingData, client.EndpointLifestyleLogging,
		datedPath(client.PathLifestyleLoggingPrefix, date), nil)
	if err := requireDate(req, date); err != nil {
		return LifestyleLog{}, err
	}

	var document json.RawMessage
	payload, err := c.req.read(ctx, session, req, &document)
	if err != nil {
		return LifestyleLog{}, err
	}
	return LifestyleLog{Document: document, raw: payload}, nil
}

// BloodPressure reads every measurement in an inclusive date window.
//
// The window is validated against the request layer's bound before anything is
// dispatched, so a caller cannot ask for a decade of readings in one call. includeAll
// is sent exactly as upstream sends it.
func (c *WellnessCardio) BloodPressure(
	ctx context.Context, session client.Session, span client.DateRange,
) (BloodPressureRange, error) {
	query := url.Values{}
	query.Set(client.QueryIncludeAll, strconv.FormatBool(true))
	path := datedPath(datedPath(client.PathBloodPressureRangePrefix, span.Start()), span.End())
	req := readRequest(client.OpGetBloodPressure, client.EndpointBloodPressure, path, query)

	if span.IsZero() {
		return BloodPressureRange{}, invalid(req, client.ErrValidation)
	}
	if err := c.req.limits().ValidateDateRange(span); err != nil {
		return BloodPressureRange{}, invalid(req, err)
	}

	var window BloodPressureRange
	payload, err := c.req.read(ctx, session, req, &window)
	if err != nil {
		return BloodPressureRange{}, err
	}
	window.raw = payload
	return window, nil
}
