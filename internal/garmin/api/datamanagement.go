package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// DataManagement writes the data-management surface: add_body_composition,
// set_blood_pressure and add_hydration_data.
//
// Source: python-garminconnect 0.3.10 garminconnect/__init__.py (evidenced
// as 0310-__init__.py): add_body_composition (:1174-1216), set_blood_pressure
// (:1375-1406) and add_hydration_data (:1619-1696), cross-checked against
// the Taxuspt pinned curation at src/garmin_mcp/data_management.py, which is
// the only source for the three tools' argument shapes.
//
// Every value this package writes is health data tied to a person, so no
// model here is ever logged with its content, only its shape — see
// datamanagementsensitive.go.
type DataManagement struct {
	req requester
}

// NewDataManagement returns a data-management client over the request layer.
func NewDataManagement(rc *client.Client) (*DataManagement, error) {
	req, err := newRequester(rc)
	if err != nil {
		return nil, err
	}
	return &DataManagement{req: req}, nil
}

// bodyCompositionFilename is the multipart filename add_body_composition's
// own FIT upload uses.
//
// Source: __init__.py:1214-1215,
// `files = {"file": ("body_composition.fit", fitEncoder.getvalue())}`. The
// requests library defaults an untyped file tuple's content type to
// "application/octet-stream", which is what this package declares
// explicitly instead of leaving implicit.
const bodyCompositionFilename = "body_composition.fit"

// BodyCompositionWriteResult is what AddBodyComposition reports: the ordinary
// write outcome, plus the weight the FIT message actually carries after
// scaledUint16's own truncation. StoredWeightKG lets a caller learn that
// 70.006 was recorded as 70.00, rather than have its own input echoed back as
// if it had round-tripped exactly.
type BodyCompositionWriteResult struct {
	WriteResult
	StoredWeightKG float64
}

// AddBodyComposition encodes entry as a FIT file and uploads it, exactly the
// shape add_body_composition's own FitEncoderWeight produces.
//
// Its effect is EffectUnsafeWrite: Garmin's upload endpoint appends a new
// record with no target identifier, so the retry layer must never replay a
// lost response.
//
// Source: __init__.py:1174-1216.
func (d *DataManagement) AddBodyComposition(
	ctx context.Context, session client.Session, entry BodyCompositionEntry,
) (BodyCompositionWriteResult, error) {
	req := writeRequest(client.OpAddBodyComposition, client.EndpointUpload,
		http.MethodPost, client.PathUpload, client.EffectUnsafeWrite)
	req.FileTransfer = true

	if err := entry.validate(req); err != nil {
		return BodyCompositionWriteResult{}, err
	}
	fitBytes, storedWeightKG, err := buildBodyCompositionFIT(entry)
	if err != nil {
		return BodyCompositionWriteResult{}, invalid(req, err)
	}
	body, contentType, err := buildMultipartFile(
		"file", bodyCompositionFilename, "application/octet-stream", fitBytes)
	if err != nil {
		return BodyCompositionWriteResult{}, invalid(req, err)
	}
	req.Body = body
	req.ContentType = contentType

	payload, err := d.req.write(ctx, session, req, nil)
	if err != nil {
		return BodyCompositionWriteResult{}, err
	}
	return BodyCompositionWriteResult{
		WriteResult:    newWriteResult(payload),
		StoredWeightKG: storedWeightKG,
	}, nil
}

// Blood pressure bounds. Source: __init__.py:1397-1403,
// `for name, val, lo, hi in (("systolic", systolic, 70, 260),
// ("diastolic", diastolic, 40, 150), ("pulse", pulse, 20, 250))`.
const (
	minSystolic, maxSystolic   = 70, 260
	minDiastolic, maxDiastolic = 40, 150
	minPulse, maxPulse         = 20, 250
)

// BloodPressureEntry is the strict request model for set_blood_pressure.
// At is the single instant both the local and the GMT wire timestamps are
// rendered from, matching __init__.py:1386-1387's own
// `dtGMT = dt.astimezone(UTC)`.
type BloodPressureEntry struct {
	Systolic  int
	Diastolic int
	Pulse     int
	Notes     string
	At        time.Time
}

// validate reports whether the entry may be dispatched.
func (e BloodPressureEntry) validate(req client.Request) error {
	switch {
	case e.Systolic < minSystolic || e.Systolic > maxSystolic:
		return invalid(req, fmt.Errorf("%w: systolic must be between %d and %d",
			client.ErrValidation, minSystolic, maxSystolic))
	case e.Diastolic < minDiastolic || e.Diastolic > maxDiastolic:
		return invalid(req, fmt.Errorf("%w: diastolic must be between %d and %d",
			client.ErrValidation, minDiastolic, maxDiastolic))
	case e.Pulse < minPulse || e.Pulse > maxPulse:
		return invalid(req, fmt.Errorf("%w: pulse must be between %d and %d",
			client.ErrValidation, minPulse, maxPulse))
	case e.At.IsZero():
		return invalid(req, fmt.Errorf("%w: a timestamp is required", client.ErrValidation))
	}
	if e.Notes != "" {
		if len(e.Notes) > MaxTextLen {
			return invalid(req, fmt.Errorf("%w: notes is too long", client.ErrValidation))
		}
		if hasControlRune(e.Notes) {
			return invalid(req, fmt.Errorf("%w: notes must not contain control characters",
				client.ErrValidation))
		}
	}
	return nil
}

// bloodPressureDTO is the POST "/bloodpressure-service/bloodpressure"
// request body.
//
// Source: __init__.py:1388-1396.
type bloodPressureDTO struct {
	MeasurementTimestampLocal string `json:"measurementTimestampLocal"`
	MeasurementTimestampGMT   string `json:"measurementTimestampGMT"`
	Systolic                  int    `json:"systolic"`
	Diastolic                 int    `json:"diastolic"`
	Pulse                     int    `json:"pulse"`
	SourceType                string `json:"sourceType"`
	Notes                     string `json:"notes"`
}

// SetBloodPressure records one blood-pressure reading.
//
// Its effect is EffectUnsafeWrite: the timestamp defaults to now and every
// call appends a new reading, so the retry layer must never replay a lost
// response.
//
// Source: __init__.py:1375-1406.
func (d *DataManagement) SetBloodPressure(
	ctx context.Context, session client.Session, entry BloodPressureEntry,
) (WriteResult, error) {
	req := writeRequest(client.OpSetBloodPressure, client.EndpointBloodPressureSet,
		http.MethodPost, client.PathBloodPressureSet, client.EffectUnsafeWrite)
	if err := entry.validate(req); err != nil {
		return WriteResult{}, err
	}

	body, err := jsonBody(req, bloodPressureDTO{
		MeasurementTimestampLocal: renderLocalWeighInTimestamp(entry.At),
		MeasurementTimestampGMT:   renderGMTWeighInTimestamp(entry.At),
		Systolic:                  entry.Systolic,
		Diastolic:                 entry.Diastolic,
		Pulse:                     entry.Pulse,
		SourceType:                client.WeightSourceManual,
		Notes:                     entry.Notes,
	})
	if err != nil {
		return WriteResult{}, err
	}
	req.Body = body

	payload, err := d.req.write(ctx, session, req, nil)
	if err != nil {
		return WriteResult{}, err
	}
	return newWriteResult(payload), nil
}

// maxHydrationML bounds a hydration write in either direction.
//
// Source: __init__.py:41, MAX_HYDRATION_ML = 10000, and :1636-1639,
// `if abs(value_in_ml) > MAX_HYDRATION_ML: raise ValueError(...)`. Negative
// values are accepted on purpose: __init__.py:1626 documents value_in_ml as
// "positive) or subtract (negative)".
const maxHydrationML = 10000

// HydrationEntry is the strict request model for add_hydration_data. At is
// the instant timestampLocal is rendered from; Date is the calendar day the
// caller's own cdate names. Both are required at this tool's own manifest
// (data_management.py's add_hydration_data has no optional parameter), so
// unlike the underlying library method this package never resolves either
// from "now".
type HydrationEntry struct {
	ValueInML float64
	Date      client.Date
	At        time.Time
}

// validate reports whether the entry may be dispatched.
//
// Source: __init__.py:1670-1686's "both provided" branch, the only one this
// tool ever reaches: cdate and the timestamp's own date part must agree, or
// upstream raises `ValueError(f"timestamp date ({ts_date}) doesn't match
// cdate ({cdate})")`.
func (e HydrationEntry) validate(req client.Request) error {
	if e.ValueInML < -maxHydrationML || e.ValueInML > maxHydrationML {
		return invalid(req, fmt.Errorf("%w: value_in_ml seems unreasonably high",
			client.ErrValidation))
	}
	if e.Date.IsZero() {
		return invalid(req, fmt.Errorf("%w: a calendar date is required", client.ErrValidation))
	}
	if e.At.IsZero() {
		return invalid(req, fmt.Errorf("%w: a timestamp is required", client.ErrValidation))
	}
	if e.At.Format(client.DateLayout) != e.Date.String() {
		return invalid(req, fmt.Errorf("%w: timestamp date does not match cdate",
			client.ErrValidation))
	}
	return nil
}

// hydrationDTO is the PUT "/usersummary-service/usersummary/hydration/log"
// request body.
//
// Source: __init__.py:1691-1695.
type hydrationDTO struct {
	CalendarDate   string  `json:"calendarDate"`
	TimestampLocal string  `json:"timestampLocal"`
	ValueInML      float64 `json:"valueInML"`
}

// AddHydrationData records one hydration entry.
//
// Its effect is EffectUnsafeWrite: PUT accumulates a delta rather than
// replacing a resource at a fixed address, so the retry layer must never
// replay a lost response.
//
// Source: __init__.py:1619-1696.
func (d *DataManagement) AddHydrationData(
	ctx context.Context, session client.Session, entry HydrationEntry,
) (WriteResult, error) {
	req := writeRequest(client.OpAddHydrationData, client.EndpointHydrationSet,
		http.MethodPut, client.PathHydrationSet, client.EffectUnsafeWrite)
	if err := entry.validate(req); err != nil {
		return WriteResult{}, err
	}

	body, err := jsonBody(req, hydrationDTO{
		CalendarDate:   entry.Date.String(),
		TimestampLocal: renderLocalWeighInTimestamp(entry.At),
		ValueInML:      entry.ValueInML,
	})
	if err != nil {
		return WriteResult{}, err
	}
	req.Body = body

	payload, err := d.req.write(ctx, session, req, nil)
	if err != nil {
		return WriteResult{}, err
	}
	return newWriteResult(payload), nil
}
