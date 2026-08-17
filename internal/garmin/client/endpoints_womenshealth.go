package client

// Women's-health API-tier paths. Source: python-garminconnect 0.3.10
// garminconnect/__init__.py (garmin_connect_menstrual_calendar_url,
// garmin_connect_menstrual_dayview_url and garmin_connect_pregnancy_snapshot_url,
// __init__.py:508-517), read by get_menstrual_calendar_data,
// get_menstrual_data_for_date and get_pregnancy_summary (__init__.py:3462-3488).
//
// Every one of those three methods returns self.connectapi(url) unmodified, and
// Taxuspt/garmin_mcp's src/garmin_mcp/womens_health.py — the pinned curation these
// three tools are ported from — reads no field of any of the three documents
// either: each tool is json.dumps(<result>, indent=2) on the raw response
// (womens_health.py:52-55, 67-70, 98-112). No pinned source therefore names a
// single field spelling anywhere in any of these three documents; see
// WomensHealth's doc comment in internal/garmin/api/womenshealth.go for what that
// means for the models built on these paths.
const (
	// PathMenstrualCalendarPrefix precedes a start date and an end date, each its
	// own segment, in the menstrual-cycle calendar path.
	// Source: garmin_connect_menstrual_calendar_url (__init__.py:508-510), read as
	// f"{...}/{startdate}/{enddate}" by get_menstrual_calendar_data (__init__.py:3476).
	PathMenstrualCalendarPrefix = "/periodichealth-service/menstrualcycle/calendar"
	// PathMenstrualDayviewPrefix precedes a calendar date in the menstrual-cycle
	// day-view path.
	// Source: garmin_connect_menstrual_dayview_url (__init__.py:512-514), read as
	// f"{...}/{fordate}" by get_menstrual_data_for_date (__init__.py:3465).
	PathMenstrualDayviewPrefix = "/periodichealth-service/menstrualcycle/dayview"
	// PathPregnancySnapshot is the pregnancy-summary path. It takes no parameter.
	// Source: garmin_connect_pregnancy_snapshot_url (__init__.py:515-517), read
	// verbatim by get_pregnancy_summary (__init__.py:3485).
	PathPregnancySnapshot = "/periodichealth-service/menstrualcycle/pregnancysnapshot"
)

// Sanitized endpoint labels for the women's-health tier. They never contain a
// host, a credential or a query string.
const (
	EndpointMenstrualCalendar = Endpoint("connectapi.womenshealth.menstrual_calendar")
	EndpointMenstrualDayview  = Endpoint("connectapi.womenshealth.menstrual_dayview")
	EndpointPregnancySnapshot = Endpoint("connectapi.womenshealth.pregnancy_snapshot")
)

// womensHealthEndpoints returns the women's-health labels. A function, not a var:
// AGENTS.md allows no package-level mutable state.
func womensHealthEndpoints() []Endpoint {
	return []Endpoint{
		EndpointMenstrualCalendar,
		EndpointMenstrualDayview,
		EndpointPregnancySnapshot,
	}
}

// Sanitized operation labels, one per women's-health tool.
const (
	OpGetMenstrualCalendarData = Op("get_menstrual_calendar_data")
	OpGetMenstrualDataForDate  = Op("get_menstrual_data_for_date")
	OpGetPregnancySummary      = Op("get_pregnancy_summary")
)

// womensHealthOps returns the women's-health operations. A function for the same
// reason as womensHealthEndpoints.
func womensHealthOps() []Op {
	return []Op{
		OpGetMenstrualCalendarData,
		OpGetMenstrualDataForDate,
		OpGetPregnancySummary,
	}
}
