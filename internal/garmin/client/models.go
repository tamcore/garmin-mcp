package client

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DateLayout is Garmin's calendar-date form. Source: DATE_FORMAT_STR "%Y-%m-%d"
// and DATE_FORMAT_REGEX in python-garminconnect 0.3.10.
const DateLayout = "2006-01-02"

// MaxPageStartCap bounds a pagination offset, so a caller cannot ask Garmin to
// skip an absurd number of records. It is MaxPagesCap pages of MaxPageSizeCap
// records, which is far past any real account.
const MaxPageStartCap = MaxPagesCap * MaxPageSizeCap

// Date is a validated Garmin calendar date. The zero value is unset, and every
// accessor reports its zero result for it.
//
// It is a strict request model: a date reaches a query string only after it has
// been parsed here, so a query parameter can never carry arbitrary caller text.
type Date struct {
	day time.Time
}

// ParseDate validates a YYYY-MM-DD calendar date. Surrounding space is trimmed,
// matching upstream; anything else — a partial match, an unreal date, a date with
// a suffix — is rejected with an error matching ErrValidation.
func ParseDate(value string) (Date, error) {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) != len(DateLayout) {
		return Date{}, validationError("date must be in YYYY-MM-DD form")
	}

	day, err := time.ParseInLocation(DateLayout, trimmed, time.UTC)
	if err != nil {
		return Date{}, validationError("date must be a real YYYY-MM-DD calendar date")
	}
	// A round trip is the real check: parsing normalizes an out-of-range day
	// rather than rejecting it.
	if day.Format(DateLayout) != trimmed {
		return Date{}, validationError("date must be a real YYYY-MM-DD calendar date")
	}
	// The zero time.Time is this type's "unset" sentinel, and it is exactly
	// 0001-01-01 UTC. Accepting that date would return a Date that reports itself
	// unset: String would be "", IsZero would be true, and DateRange.Contains
	// would silently drop the day rather than place it. A fuzz target caught this
	// (ParseDate succeeded but did not round-trip), and refusing at the boundary
	// keeps the sentinel unambiguous. No Garmin record carries year 1, so nothing
	// real is rejected.
	if day.IsZero() {
		return Date{}, validationError(
			"date must not be 0001-01-01, which this package reserves as the unset date")
	}
	return Date{day: day}, nil
}

// NewCalendarDay takes the calendar day instant already sits in, in its own
// location, rather than converting it to UTC first.
//
// This is the constructor a Garmin calendar date wants. Garmin keys a day's
// documents by the account's own day, so at 00:30 in a zone ahead of UTC the UTC
// day is still yesterday and NewDate would ask for the wrong one. The resulting
// Date is still held at UTC midnight, because that is only how a Date is
// represented; the day it names is the one the caller was standing in.
func NewCalendarDay(instant time.Time) Date {
	if instant.IsZero() {
		return Date{}
	}
	day := time.Date(instant.Year(), instant.Month(), instant.Day(), 0, 0, 0, 0, time.UTC)
	if day.IsZero() {
		// Truncating a year-1 instant to midnight lands exactly on the unset
		// sentinel; see ParseDate. Report unset rather than a Date that lies
		// about being set.
		return Date{}
	}
	return Date{day: day}
}

// NewDate takes the UTC calendar day of instant. A zero instant yields the zero
// Date.
func NewDate(instant time.Time) Date {
	if instant.IsZero() {
		return Date{}
	}
	utc := instant.UTC()
	day := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
	if day.IsZero() {
		return Date{}
	}
	return Date{day: day}
}

// IsZero reports whether the date is unset.
func (d Date) IsZero() bool { return d.day.IsZero() }

// String renders the date in Garmin's form, or "" when unset. A calendar date is
// not sensitive on its own; the health payload it selects is.
func (d Date) String() string {
	if d.IsZero() {
		return ""
	}
	return d.day.Format(DateLayout)
}

// Time is the UTC midnight instant of the calendar day.
func (d Date) Time() time.Time { return d.day }

// AddDays returns the date shifted by days. The receiver is not modified.
func (d Date) AddDays(days int) Date {
	if d.IsZero() {
		return Date{}
	}
	return Date{day: d.day.AddDate(0, 0, days)}
}

// DateRange is an inclusive, ordered pair of calendar dates.
type DateRange struct {
	start Date
	end   Date
}

// NewDateRange validates that both dates are set and start is not after end.
// Source: _validate_date_range.
func NewDateRange(start, end Date) (DateRange, error) {
	if start.IsZero() || end.IsZero() {
		return DateRange{}, validationError("a date range needs both a start and an end date")
	}
	if start.Time().After(end.Time()) {
		return DateRange{}, validationError("a date range must not start after it ends")
	}
	return DateRange{start: start, end: end}, nil
}

// Start is the inclusive first day.
func (r DateRange) Start() Date { return r.start }

// End is the inclusive last day.
func (r DateRange) End() Date { return r.end }

// IsZero reports whether the range is unset.
func (r DateRange) IsZero() bool { return r.start.IsZero() || r.end.IsZero() }

// Contains reports whether day falls inside the inclusive range. An unset range
// contains nothing, and so does an unset day: a caller filtering a Garmin response
// against a window must drop what it cannot place, not keep it.
func (r DateRange) Contains(day Date) bool {
	if r.IsZero() || day.IsZero() {
		return false
	}
	return !day.Time().Before(r.start.Time()) && !day.Time().After(r.end.Time())
}

// Days is the inclusive day count, so a single-day range reports 1.
func (r DateRange) Days() int {
	if r.IsZero() {
		return 0
	}
	return int(r.end.Time().Sub(r.start.Time()).Hours()/24) + 1
}

// ValidateDateRange reports whether span fits inside the configured window
// bound. An unset range is rejected, so a bound cannot be skipped by omission.
func (l Limits) ValidateDateRange(span DateRange) error {
	if span.IsZero() {
		return validationError("a date range needs both a start and an end date")
	}
	if maxDays := l.Resolved().MaxDateRangeDays; span.Days() > maxDays {
		return validationError("date range exceeds the configured window of " +
			strconv.Itoa(maxDays) + " days")
	}
	return nil
}

// Page is a validated pagination window: a non-negative offset and a positive
// page size. Source: the start/limit parameters of get_activities.
type Page struct {
	start int
	limit int
}

// NewPage validates an offset and a page size against the package caps. The
// configured, possibly stricter, bound is applied by Limits.ValidatePage.
func NewPage(start, limit int) (Page, error) {
	switch {
	case start < 0:
		return Page{}, validationError("page start must not be negative")
	case start > MaxPageStartCap:
		return Page{}, validationError("page start exceeds its cap")
	case limit <= 0:
		return Page{}, validationError("page limit must be positive")
	case limit > MaxPageSizeCap:
		return Page{}, validationError("page limit exceeds its cap")
	}
	return Page{start: start, limit: limit}, nil
}

// Start is the record offset, where 0 is the most recent record.
func (p Page) Start() int { return p.start }

// Limit is the page size.
func (p Page) Limit() int { return p.limit }

// Next returns the following page. The receiver is not modified.
func (p Page) Next() Page { return Page{start: p.start + p.limit, limit: p.limit} }

// ValidatePage reports whether p fits inside the configured page-size bound.
func (l Limits) ValidatePage(p Page) error {
	if maxSize := l.Resolved().MaxPageSize; p.limit > maxSize {
		return validationError("page limit exceeds the configured maximum of " +
			strconv.Itoa(maxSize))
	}
	return nil
}

// ID is a validated positive Garmin numeric identifier: an activity id, a device
// id, a gear id. Source: _validate_positive_integer(int(activity_id)).
//
// Only a parsed ID reaches a URL path, so an identifier can never carry a path
// separator or a traversal segment.
type ID struct {
	value int64
}

// NewID validates a positive identifier.
func NewID(value int64) (ID, error) {
	if value <= 0 {
		return ID{}, validationError("identifier must be a positive integer")
	}
	return ID{value: value}, nil
}

// ParseID validates a decimal positive identifier. No sign, no space, no radix
// prefix and no non-ASCII digit is accepted.
func ParseID(value string) (ID, error) {
	if value == "" {
		return ID{}, validationError("identifier must be a positive integer")
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return ID{}, validationError("identifier must be decimal digits only")
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return ID{}, validationError("identifier must be a positive integer")
	}
	return NewID(parsed)
}

// IsZero reports whether the identifier is unset.
func (i ID) IsZero() bool { return i.value == 0 }

// Int64 is the numeric identifier.
func (i ID) Int64() int64 { return i.value }

// String renders the identifier as decimal digits, or "" when unset.
func (i ID) String() string {
	if i.IsZero() {
		return ""
	}
	return strconv.FormatInt(i.value, 10)
}

// DisplayName is a validated Garmin display name, which several date-keyed
// wellness paths take as a path segment.
//
// It is identity material, so it is not printable: the value sits behind a
// pointer one level deeper than this type, where fmt renders it as an address
// even through a method-stripping alias, and String, GoString, MarshalJSON and
// LogValue report presence rather than content. Value hands the real string to a
// caller that asks for it deliberately, which is how a URL path gets built.
type DisplayName struct {
	// sealed is a pointer on purpose; see protocol.Response for the reasoning.
	sealed *sealedName
}

// sealedName is the extra indirection that keeps the name out of reach of fmt's
// badVerb path.
type sealedName struct {
	inner *string
}

// ParseDisplayName validates a display name for use as a single URL path
// segment. Source: _require_display_name, which refuses an unset display name
// because "None" in a path yields a 403, and percent-encodes the value so a
// hostile profile response cannot inject a path separator or a query.
func ParseDisplayName(value string) (DisplayName, error) {
	trimmed := strings.TrimSpace(value)
	switch {
	case trimmed == "":
		return DisplayName{}, validationError("display name is not set")
	case trimmed == "." || trimmed == "..":
		return DisplayName{}, validationError("display name must not be a path traversal segment")
	case strings.ContainsAny(trimmed, "/\\?#%"):
		return DisplayName{}, validationError("display name must not contain URL separators")
	}
	for _, r := range trimmed {
		if r < 0x20 || r == 0x7f {
			return DisplayName{}, validationError("display name must not contain control characters")
		}
	}

	name := trimmed
	return DisplayName{sealed: &sealedName{inner: &name}}, nil
}

// IsZero reports whether the display name is unset.
func (n DisplayName) IsZero() bool {
	return n.sealed == nil || n.sealed.inner == nil
}

// Value is the validated display name. It is identity material: never log it.
func (n DisplayName) Value() string {
	if n.IsZero() {
		return ""
	}
	return *n.sealed.inner
}

// validationError builds a rejection that matches ErrValidation. It names the
// rule, never the rejected value, so a hostile input cannot copy itself into a
// log line.
func validationError(reason string) error {
	return fmt.Errorf("%w: %s", ErrValidation, reason)
}
