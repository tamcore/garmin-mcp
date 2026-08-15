package tools

import (
	"encoding/json"
	"log/slog"
	"math"
	"strconv"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// Argument bounds this package enforces before anything is dispatched.
const (
	// maxDateArgumentLen is the length of a YYYY-MM-DD date. A longer value is
	// refused before it reaches the date parser.
	maxDateArgumentLen = 10

	// maxIdentifierArgumentLen bounds a decimal identifier, which is int64 at
	// widest.
	maxIdentifierArgumentLen = 19

	// maxActivityTypeArgumentLen bounds an activity-type filter.
	maxActivityTypeArgumentLen = 32

	// defaultActivityStart and defaultActivityLimit are the manifest defaults for
	// get_activities.
	defaultActivityStart = 0
	defaultActivityLimit = 20

	// defaultWindowPage and defaultWindowPageSize are the manifest defaults for
	// get_activities_by_date.
	defaultWindowPage     = 0
	defaultWindowPageSize = 100
)

// parseCalendarDate validates a calendar-date argument. field names the argument, so
// the refusal says which one was wrong without quoting what arrived.
func parseCalendarDate(field, value string) (client.Date, error) {
	if len(value) > maxDateArgumentLen {
		return client.Date{}, invalidArgument(field + " must be exactly ten characters, YYYY-MM-DD")
	}
	date, err := client.ParseDate(value)
	if err != nil {
		return client.Date{}, invalidArgument(
			field + " must be a real calendar date in YYYY-MM-DD form")
	}
	return date, nil
}

// parseWindow validates an inclusive date window against the configured bound, so a
// caller cannot ask for a decade of health data in one call.
func parseWindow(startValue, endValue string, limits client.Limits) (client.DateRange, error) {
	start, err := parseCalendarDate("start_date", startValue)
	if err != nil {
		return client.DateRange{}, err
	}
	end, err := parseCalendarDate("end_date", endValue)
	if err != nil {
		return client.DateRange{}, err
	}

	span, err := client.NewDateRange(start, end)
	if err != nil {
		return client.DateRange{}, invalidArgument("start_date must not be after end_date")
	}
	if maxDays := limits.MaxDateRangeDays; span.Days() > maxDays {
		return client.DateRange{}, invalidArgument(
			"the date window must not exceed " + strconv.Itoa(maxDays) + " days")
	}
	return span, nil
}

// parseActivityTypeFilter validates the optional activity-type filter. An empty value
// is the unfiltered zero value, matching upstream's optional parameter.
func parseActivityTypeFilter(value string) (api.ActivityType, error) {
	if value == "" {
		return api.ActivityType{}, nil
	}
	if len(value) > maxActivityTypeArgumentLen {
		return api.ActivityType{}, invalidArgument("activity_type is too long")
	}
	filter, err := api.ParseActivityType(value)
	if err != nil {
		return api.ActivityType{}, invalidArgument(
			"activity_type must be a lowercase Garmin activity key such as running or cycling")
	}
	return filter, nil
}

// parseActivityIdentifier accepts the two forms the manifest declares — a JSON number
// and a decimal string — and nothing else. Only a validated client.ID ever reaches a
// URL path, so an identifier can carry no path separator and no traversal segment.
func parseActivityIdentifier(value any) (client.ID, error) {
	return parseIdentifier("activity_id", value)
}

// parseIdentifier is parseActivityIdentifier for any identifier argument. field names
// the argument, so the refusal says which one was wrong without quoting what arrived.
func parseIdentifier(field string, value any) (client.ID, error) {
	switch typed := value.(type) {
	case nil:
		return client.ID{}, invalidArgument(field + " is required")
	case string:
		return identifierFromText(field, typed)
	case json.Number:
		return identifierFromText(field, typed.String())
	case float64:
		return identifierFromNumber(field, typed)
	case int64:
		return identifierFromNumber(field, float64(typed))
	default:
		return client.ID{}, invalidArgument(
			field + " must be a positive integer, as a JSON number or as a decimal string")
	}
}

func identifierFromText(field, value string) (client.ID, error) {
	if len(value) > maxIdentifierArgumentLen {
		return client.ID{}, invalidArgument(field + " is too long to be a Garmin identifier")
	}
	id, err := client.ParseID(value)
	if err != nil {
		return client.ID{}, invalidArgument(
			field + " must be decimal digits naming a positive record")
	}
	return id, nil
}

func identifierFromNumber(field string, value float64) (client.ID, error) {
	if value != math.Trunc(value) || value <= 0 || value > math.MaxInt64 {
		return client.ID{}, invalidArgument(field + " must be a positive whole number")
	}
	id, err := client.NewID(int64(value))
	if err != nil {
		return client.ID{}, invalidArgument(field + " must be a positive whole number")
	}
	return id, nil
}

// resolveActivityPage applies the manifest defaults, refuses an out-of-range window,
// and then clamps the page size down to whatever the request layer allows. Clamping
// down is a bound, not data loss: the effective limit is reported back to the caller.
func resolveActivityPage(start, limit *int, limits client.Limits) (client.Page, error) {
	startValue, limitValue := defaultActivityStart, defaultActivityLimit
	if start != nil {
		startValue = *start
	}
	if limit != nil {
		limitValue = *limit
	}

	switch {
	case startValue < 0:
		return client.Page{}, invalidArgument("start must not be negative")
	case startValue > client.MaxPageStartCap:
		return client.Page{}, invalidArgument(
			"start must not exceed " + strconv.Itoa(client.MaxPageStartCap))
	case limitValue < 1:
		return client.Page{}, invalidArgument("limit must be at least 1")
	case limitValue > DefaultMaxActivityPageSize:
		return client.Page{}, invalidArgument(
			"limit must not exceed " + strconv.Itoa(DefaultMaxActivityPageSize))
	}
	if limitValue > limits.MaxPageSize {
		limitValue = limits.MaxPageSize
	}

	page, err := client.NewPage(startValue, limitValue)
	if err != nil {
		return client.Page{}, fail(err)
	}
	return page, nil
}

// resolveWindowPagination applies the manifest defaults for the date-window paging and
// refuses an out-of-range page or page size.
func resolveWindowPagination(page, pageSize *int) (int, int, error) {
	pageValue, sizeValue := defaultWindowPage, defaultWindowPageSize
	if page != nil {
		pageValue = *page
	}
	if pageSize != nil {
		sizeValue = *pageSize
	}

	switch {
	case pageValue < 0:
		return 0, 0, invalidArgument("page must not be negative")
	case pageValue > DefaultMaxWindowPage:
		return 0, 0, invalidArgument("page must not exceed " + strconv.Itoa(DefaultMaxWindowPage))
	case sizeValue < 1:
		return 0, 0, invalidArgument("page_size must be at least 1")
	case sizeValue > DefaultMaxWindowPageSize:
		return 0, 0, invalidArgument(
			"page_size must not exceed " + strconv.Itoa(DefaultMaxWindowPageSize))
	}
	return pageValue, sizeValue, nil
}

// optionalInt renders a union-decoded number as an optional integer.
func optionalInt(number client.Number) *int {
	value, ok := number.Int64()
	if !ok {
		return nil
	}
	out := int(value)
	return &out
}

// optionalInt64 renders a union-decoded number as an optional 64-bit integer.
func optionalInt64(number client.Number) *int64 {
	value, ok := number.Int64()
	if !ok {
		return nil
	}
	return &value
}

// optionalFloat renders a union-decoded number as an optional float.
func optionalFloat(number client.Number) *float64 {
	value, ok := number.Float64()
	if !ok {
		return nil
	}
	return &value
}

// optionalText renders a union-decoded string as an optional string.
func optionalText(text client.Text) *string {
	value, ok := text.Value()
	if !ok || value == "" {
		return nil
	}
	return &value
}

// typeKeyOf reads the type key out of the two shapes Garmin sends for an activity or
// event type: a nested object, and a bare key.
func typeKeyOf(raw json.RawMessage) *string {
	if len(raw) == 0 || string(raw) == jsonNull {
		return nil
	}

	var nested struct {
		TypeKey *string `json:"typeKey"`
	}
	if err := json.Unmarshal(raw, &nested); err == nil && nested.TypeKey != nil {
		return nested.TypeKey
	}

	var bare string
	if err := json.Unmarshal(raw, &bare); err == nil && bare != "" {
		return &bare
	}
	return nil
}

// presence renders whether an optional value is present, without revealing it. Every
// LogValue in this package is built from it.
func presence(present bool) string {
	if present {
		return "set"
	}
	return "unset"
}

// shape is the group every result model logs: the model name, then counts and
// presence flags only.
func shape(model string, attrs ...slog.Attr) slog.Value {
	all := make([]slog.Attr, 0, len(attrs)+1)
	all = append(all, slog.String("model", model))
	all = append(all, attrs...)
	return slog.GroupValue(all...)
}
