package client

// Weight-management API-tier paths. Source: python-garminconnect 0.3.10
// garminconnect/__init__.py (self.garmin_connect_weight_url and the paths built
// from it for add_weigh_in, add_weigh_in_with_timestamps, get_weigh_ins,
// get_daily_weigh_ins, delete_weigh_in and delete_weigh_ins), plus the Taxuspt
// pinned curation at src/garmin_mcp/weight_management.py, which is the only
// source for the get_weigh_ins/get_daily_weigh_ins response field spellings.
const (
	// PathWeightUserWeight is the single-weigh-in write target, shared verbatim
	// by add_weigh_in and add_weigh_in_with_timestamps.
	// Source: garminconnect/__init__.py, url = f"{self.garmin_connect_weight_url}/user-weight"
	// (add_weigh_in) and the identical build in add_weigh_in_with_timestamps.
	PathWeightUserWeight = "/weight-service/user-weight"
	// PathWeightRangePrefix precedes a start date and an end date, each its own
	// path segment, in the weigh-in range read.
	// Source: get_weigh_ins, url = f"{self.garmin_connect_weight_url}/weight/range/{startdate}/{enddate}".
	PathWeightRangePrefix = "/weight-service/weight/range"
	// PathWeightDayviewPrefix precedes a calendar date in the single-day
	// weigh-in read.
	// Source: get_daily_weigh_ins, url = f"{self.garmin_connect_weight_url}/weight/dayview/{cdate}".
	PathWeightDayviewPrefix = "/weight-service/weight/dayview"
	// PathWeightDeletePrefix precedes a calendar date, PathWeightByVersionSegment
	// and a weigh-in sample identifier, each its own path segment, in the
	// single-weigh-in delete.
	// Source: delete_weigh_in, url = f"{self.garmin_connect_weight_url}/weight/{cdate}/byversion/{weight_pk}".
	PathWeightDeletePrefix = "/weight-service/weight"
	// PathWeightByVersionSegment is the literal path segment between the
	// calendar date and the sample identifier in the delete path. Source: the
	// same f-string as PathWeightDeletePrefix.
	PathWeightByVersionSegment = "byversion"
)

// Fixed Garmin wire values the weigh-in writes send, and the unit tokens both
// the writes and the caller-facing unit key are validated against.
const (
	// WeightUnitKg and WeightUnitLbs are the only two unit tokens
	// VALID_WEIGHT_UNITS accepts. Source: garminconnect/__init__.py,
	// VALID_WEIGHT_UNITS = {"kg", "lbs"}, checked in both add_weigh_in and
	// add_weigh_in_with_timestamps before either dispatches anything.
	//
	// The Taxuspt weight_management.py tool docstrings advertise "kg or lb" for
	// unit_key, but the library those tools call enforces "lbs", not "lb"; a
	// caller passing "lb" is refused by the library regardless of what the tool
	// description says. This package follows the library, which is what
	// actually reaches Garmin's wire.
	WeightUnitKg  = "kg"
	WeightUnitLbs = "lbs"
	// WeightSourceManual is the literal sourceType every weigh-in write carries.
	// Source: add_weigh_in and add_weigh_in_with_timestamps,
	// "sourceType": "MANUAL".
	WeightSourceManual = "MANUAL"
)

// Sanitized endpoint labels for the weight-management tier. They never contain
// a host, a credential or a query string.
const (
	EndpointWeightUserWeight = Endpoint("connectapi.weight.user_weight")
	EndpointWeightRange      = Endpoint("connectapi.weight.range")
	EndpointWeightDayview    = Endpoint("connectapi.weight.dayview")
	EndpointWeightDelete     = Endpoint("connectapi.weight.delete")
)

// weightEndpoints returns the weight-management labels. A function, not a var:
// AGENTS.md allows no package-level mutable state, and a constant that cannot
// be a const is a function, never a var.
func weightEndpoints() []Endpoint {
	return []Endpoint{
		EndpointWeightUserWeight,
		EndpointWeightRange,
		EndpointWeightDayview,
		EndpointWeightDelete,
	}
}

// Sanitized operation labels, one per weight-management tool. The single-item
// delete_weigh_in call delete_weigh_ins fans out to Garmin carries the same
// OpDeleteWeighIns label as the fan-out itself: both belong to the one
// delete_weigh_ins tool, and no upstream tool exposes the single-item delete on
// its own.
const (
	OpAddWeighIn               = Op("add_weigh_in")
	OpAddWeighInWithTimestamps = Op("add_weigh_in_with_timestamps")
	OpGetWeighIns              = Op("get_weigh_ins")
	OpGetDailyWeighIns         = Op("get_daily_weigh_ins")
	OpDeleteWeighIns           = Op("delete_weigh_ins")
)

// weightOps returns the weight-management operations. A function for the same
// reason as weightEndpoints.
func weightOps() []Op {
	return []Op{
		OpAddWeighIn,
		OpAddWeighInWithTimestamps,
		OpGetWeighIns,
		OpGetDailyWeighIns,
		OpDeleteWeighIns,
	}
}
