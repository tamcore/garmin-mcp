package api

import (
	"context"
	"encoding/json"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// WomensHealth reads the account's menstrual-cycle and pregnancy records: the
// calendar summary over a date window, one calendar day's detail, and the
// pregnancy snapshot.
//
// Source: python-garminconnect 0.3.10's get_menstrual_calendar_data,
// get_menstrual_data_for_date and get_pregnancy_summary (__init__.py:3462-3488),
// and the three periodichealth-service URLs its constructor assigns
// (__init__.py:508-517). Every one of the three methods returns
// self.connectapi(url) with no field read at all, and Taxuspt/garmin_mcp's
// src/garmin_mcp/womens_health.py — the pinned curation these three tools are
// ported from — reads no field either: each tool is
// json.dumps(<result>, indent=2) on the raw response (womens_health.py:52-55,
// 67-70, 98-112). No pinned source therefore names a single field spelling
// anywhere in any of these three documents, so none is decoded here: each is
// kept as Garmin's own opaque JSON, the same discipline this package already
// applies to LifestyleLog (wellness.go) and FoodLogDay (nutritionread.go) for the
// same reason. Give any of them a typed shape once a real document has been
// sampled.
//
// Every document here is menstrual-cycle or pregnancy data — the most sensitive
// category this project handles. Never log one; see womenshealthsensitive.go.
type WomensHealth struct {
	req requester
}

// NewWomensHealth returns a women's-health client over the request layer.
func NewWomensHealth(rc *client.Client) (*WomensHealth, error) {
	req, err := newRequester(rc)
	if err != nil {
		return nil, err
	}
	return &WomensHealth{req: req}, nil
}

// MenstrualDay is one calendar day of the menstrual-cycle day view.
type MenstrualDay struct {
	// Document is the response body, verbatim. It is menstrual-cycle health data.
	Document json.RawMessage

	raw client.Payload
}

// Payload is the retained raw response.
func (d MenstrualDay) Payload() client.Payload { return d.raw }

// HasDocument reports whether Garmin returned a document at all.
func (d MenstrualDay) HasDocument() bool {
	return len(d.Document) > 0 && string(d.Document) != jsonNull
}

// DayView reads one calendar day of the menstrual-cycle day view.
//
// Source: get_menstrual_data_for_date, GET
// "/periodichealth-service/menstrualcycle/dayview/{fordate}" (__init__.py:3462-3468).
func (w *WomensHealth) DayView(
	ctx context.Context, session client.Session, date client.Date,
) (MenstrualDay, error) {
	req := readRequest(client.OpGetMenstrualDataForDate, client.EndpointMenstrualDayview,
		datedPath(client.PathMenstrualDayviewPrefix, date), nil)
	if err := requireDate(req, date); err != nil {
		return MenstrualDay{}, err
	}

	var document json.RawMessage
	payload, err := w.req.read(ctx, session, req, &document)
	if err != nil {
		return MenstrualDay{}, err
	}
	return MenstrualDay{Document: document, raw: payload}, nil
}

// MenstrualCalendar is the calendar-range summary of the menstrual cycle.
type MenstrualCalendar struct {
	// Document is the response body, verbatim. It is menstrual-cycle health data.
	Document json.RawMessage

	raw client.Payload
}

// Payload is the retained raw response.
func (c MenstrualCalendar) Payload() client.Payload { return c.raw }

// HasDocument reports whether Garmin returned a document at all.
func (c MenstrualCalendar) HasDocument() bool {
	return len(c.Document) > 0 && string(c.Document) != jsonNull
}

// Calendar reads the menstrual-cycle calendar summary over an inclusive date
// window.
//
// Source: get_menstrual_calendar_data, GET
// "/periodichealth-service/menstrualcycle/calendar/{startdate}/{enddate}"
// (__init__.py:3470-3481).
//
// Upstream's own MCP curation (Taxuspt/garmin_mcp's get_menstrual_calendar_data
// tool, womens_health.py:75-114) additionally chunks a window over 92 days into
// successive requests and stitches the results back together, working around
// what its own comment calls "Garmin's server-side limit" — a limit no pinned
// source documents from the server side, only from that client-side workaround.
// This method does not port the chunking: like DailySleepRange (wellness.go) it
// keeps one bound per request instead of a client-guessed, server-defined chunk
// size, enforced the same way every other date window in this package is,
// through Limits.ValidateDateRange. A caller that needs more than the configured
// window asks again with a narrower one; see doc.go's "Not ported from
// upstream" section for the parallel decision on the sleep-stats range.
func (w *WomensHealth) Calendar(
	ctx context.Context, session client.Session, span client.DateRange,
) (MenstrualCalendar, error) {
	path := datedPath(datedPath(client.PathMenstrualCalendarPrefix, span.Start()), span.End())
	req := readRequest(client.OpGetMenstrualCalendarData, client.EndpointMenstrualCalendar, path, nil)

	if span.IsZero() {
		return MenstrualCalendar{}, invalid(req, client.ErrValidation)
	}
	if err := w.req.limits().ValidateDateRange(span); err != nil {
		return MenstrualCalendar{}, invalid(req, err)
	}

	var document json.RawMessage
	payload, err := w.req.read(ctx, session, req, &document)
	if err != nil {
		return MenstrualCalendar{}, err
	}
	return MenstrualCalendar{Document: document, raw: payload}, nil
}

// PregnancySummary is the account's pregnancy snapshot.
type PregnancySummary struct {
	// Document is the response body, verbatim. It is pregnancy health data.
	Document json.RawMessage

	raw client.Payload
}

// Payload is the retained raw response.
func (p PregnancySummary) Payload() client.Payload { return p.raw }

// HasDocument reports whether Garmin returned a document at all.
func (p PregnancySummary) HasDocument() bool {
	return len(p.Document) > 0 && string(p.Document) != jsonNull
}

// PregnancySummary reads the account's pregnancy snapshot. It takes no
// parameter.
//
// Source: get_pregnancy_summary, GET
// "/periodichealth-service/menstrualcycle/pregnancysnapshot" (__init__.py:3483-3488).
func (w *WomensHealth) PregnancySummary(
	ctx context.Context, session client.Session,
) (PregnancySummary, error) {
	req := readRequest(client.OpGetPregnancySummary, client.EndpointPregnancySnapshot,
		client.PathPregnancySnapshot, nil)

	var document json.RawMessage
	payload, err := w.req.read(ctx, session, req, &document)
	if err != nil {
		return PregnancySummary{}, err
	}
	return PregnancySummary{Document: document, raw: payload}, nil
}
