package api

import (
	"context"
	"fmt"
	"strconv"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// The monthly calendar feed. It is the one calendar read that is not GraphQL: Garmin
// serves a whole month of calendar items from the REST calendar service, and races and
// events arrive there rather than in the workout-schedule query calendar.go asks.
//
// Source: get_scheduled_workouts in python-garminconnect 0.3.10, which reads
// f"/calendar-service/year/{year}/month/{month - 1}" — the month is zero-based on the
// wire — and upstream's get_calendar_events (calendar_events.py), which filters the
// same feed by itemType.

// The fixed segments of the monthly calendar path.
const (
	calendarYearSegment  = "year"
	calendarMonthSegment = "month"
)

// CalendarItemTypeEvent is the itemType a race or event carries. Workouts, weigh-ins
// and badges share the feed under other types. Source: EVENT_ITEM_TYPE in
// calendar_events.py.
const CalendarItemTypeEvent = "event"

// calendarDistanceUnit is the completion-target unit a distance goal carries.
// Source: target.get("unitType") != "distance" in _target_distance_meters.
const calendarDistanceUnit = "distance"

// The year range the feed accepts. The lower bound is upstream's own
// (_validate_positive_integer plus "year must be 2000 or later"); the upper bound is
// this package's, so a caller cannot ask for a year no calendar can hold.
const (
	calendarMinYear = 2000
	calendarMaxYear = 2100
)

// A CalendarTarget is one calendar item's completion goal.
type CalendarTarget struct {
	UnitType *string       `json:"unitType"`
	Unit     *string       `json:"unit"`
	Value    client.Number `json:"value"`
}

// metersPerDistanceUnit scales a completion-target distance unit to metres.
// Source: _DISTANCE_UNIT_TO_METERS in calendar_events.py.
func metersPerDistanceUnit(unit string) (float64, bool) {
	switch unit {
	case "meter":
		return 1, true
	case "kilometer":
		return 1000, true
	case "yard":
		return 0.9144, true
	case "mile":
		return 1609.344, true
	}
	return 0, false
}

// A CalendarEventTime is the local start time an organiser published.
type CalendarEventTime struct {
	StartTimeHhMm *string `json:"startTimeHhMm"`
	TimeZoneID    *string `json:"timeZoneId"`
}

// A CalendarItem is one entry of the monthly calendar feed.
//
// It is personal material: a race entry says where a person will be on a given day, so
// it is never logged. Every field is optional and an unknown field never fails the
// response.
//
// Source: the fields upstream's _curate_event reads (calendar_events.py:75-90).
type CalendarItem struct {
	ItemType     *string            `json:"itemType"`
	Title        *string            `json:"title"`
	Date         *string            `json:"date"`
	IsRace       *bool              `json:"isRace"`
	PrimaryEvent *bool              `json:"primaryEvent"`
	Subscribed   *bool              `json:"subscribed"`
	Location     *string            `json:"location"`
	URL          *string            `json:"url"`
	EventUUID    *string            `json:"shareableEventUuid"`
	Target       *CalendarTarget    `json:"completionTarget"`
	EventTime    *CalendarEventTime `json:"eventTimeLocal"`
}

// IsEvent reports whether the item is a race or event rather than a workout, a
// weigh-in or a badge.
func (c CalendarItem) IsEvent() bool {
	return c.ItemType != nil && *c.ItemType == CalendarItemTypeEvent
}

// DistanceMeters reports the item's goal in metres when it is expressed as a
// distance in a known unit.
func (c CalendarItem) DistanceMeters() (float64, bool) {
	if c.Target == nil || c.Target.UnitType == nil || c.Target.Unit == nil ||
		*c.Target.UnitType != calendarDistanceUnit {
		return 0, false
	}
	scale, known := metersPerDistanceUnit(*c.Target.Unit)
	value, ok := c.Target.Value.Float64()
	if !known || !ok {
		return 0, false
	}
	return value * scale, true
}

// calendarMonth is the month document the feed answers with.
type calendarMonth struct {
	Items []CalendarItem `json:"calendarItems"`
}

// MonthItems reads one month of the calendar feed, workouts and events alike.
//
// Month is one-based, the way a caller counts months; Garmin numbers it from zero on
// the wire, and the conversion happens here so no caller has to remember it. Both
// segments are rendered from validated integers, so neither can carry a path
// separator.
func (c *Calendar) MonthItems(
	ctx context.Context, session client.Session, year, month int,
) ([]CalendarItem, error) {
	req := readRequest(client.OpGetCalendarEvents, client.EndpointCalendarMonth,
		calendarMonthPath(year, month), nil)

	if year < calendarMinYear || year > calendarMaxYear {
		return nil, invalid(req, fmt.Errorf("%w: year must be between %d and %d",
			client.ErrValidation, calendarMinYear, calendarMaxYear))
	}
	if month < 1 || month > 12 {
		return nil, invalid(req, fmt.Errorf("%w: month must be between 1 and 12",
			client.ErrValidation))
	}

	var document calendarMonth
	if _, err := c.req.read(ctx, session, req, &document); err != nil {
		return nil, err
	}
	return document.Items, nil
}

// calendarMonthPath renders the month path with Garmin's zero-based month.
func calendarMonthPath(year, month int) string {
	return client.PathCalendarService + "/" + calendarYearSegment + "/" +
		strconv.Itoa(year) + "/" + calendarMonthSegment + "/" + strconv.Itoa(month-1)
}
