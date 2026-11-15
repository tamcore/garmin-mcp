package client

// Device-and-gear-inventory paths. Source: python-garminconnect 0.3.10
// garminconnect/__init__.py, cross-checked against Taxuspt src/garmin_mcp/devices.py
// and src/garmin_mcp/gear_management.py for field spellings and response shapes. A
// Prefix path is completed by the domain client with escaped segments.
const (
	// PathDeviceSettingsPrefix precedes a device id in the per-device settings
	// path. Source: garmin_connect_device_url ("/device-service/deviceservice"),
	// read by get_device_settings as
	// f"{garmin_connect_device_url}/device-info/settings/{device_id}".
	PathDeviceSettingsPrefix = "/device-service/deviceservice/device-info/settings"
	// PathDeviceLastUsed is the most-recently-used device document, keyed to the
	// signed-in account. It takes no segment and no parameter.
	// Source: f"{garmin_connect_device_url}/mylastused" in get_device_last_used.
	PathDeviceLastUsed = "/device-service/deviceservice/mylastused"
	// PathPrimaryTrainingDevice is the primary-training-device document, carrying
	// the designated device alongside every other wearable's priority.
	// Source: garmin_connect_primary_device_url
	// ("/web-gateway/device-info/primary-training-device"), read verbatim by
	// get_primary_training_device.
	PathPrimaryTrainingDevice = "/web-gateway/device-info/primary-training-device"
	// PathDeviceSolarPrefix precedes a device id, a start date and an end date,
	// each its own segment, in the solar-charging path. Source:
	// garmin_connect_solar_url ("/web-gateway/solar"), read by
	// get_device_solar_data as f"{garmin_connect_solar_url}/{device_id}/{startdate}/{enddate}".
	PathDeviceSolarPrefix = "/web-gateway/solar"

	// PathGearStatsPrefix precedes a gear UUID in the per-gear usage-statistics
	// path. Source: garmin_connect_gear_baseurl ("/gear-service/gear"), read by
	// get_gear_stats as f"{garmin_connect_gear_baseurl}/stats/{gearUUID}".
	PathGearStatsPrefix = "/gear-service/gear/stats"
	// PathGearUserDefaultsPrefix precedes a user profile number in the
	// gear-defaults path, itself followed by the fixed "/activityTypes" segment.
	// Source: f"{garmin_connect_gear_baseurl}/user/{userProfileNumber}/activityTypes"
	// in get_gear_defaults.
	PathGearUserDefaultsPrefix = "/gear-service/gear/user"
	// PathGearV2List is the v2 gear list, the only read that carries a gear item's
	// free-text notes. It takes no segment and no parameter: it answers for the
	// signed-in account. Source: GEAR_V2_LIST_ENDPOINT in gear_management.py
	// ("/gear-service/gear/v2/list").
	PathGearV2List = "/gear-service/gear/v2/list"
	// PathGearV2 is the v2 gear create path, and precedes a gear UUID in the v2
	// detail, update, status and delete paths. Source: python-garminconnect
	// db0ddaa441 garminconnect/api/gear.py: create_gear, get_gear_details,
	// update_gear, set_gear_status and delete_gear.
	PathGearV2 = "/gear-service/gear/v2"
	// GearStatusSegment precedes ACTIVE or RETIRED in the v2 status path.
	// Source: set_gear_status's f"{...}/gear/v2/{gear_uuid}/status/{status}".
	GearStatusSegment = "status"
)

// Query parameter names and fixed wire values the device-and-gear reads add.
// Source: the params dicts of the corresponding methods in
// garminconnect/__init__.py.
const (
	// QuerySingleDayView selects the single-day solar view. Source: the params
	// dict of get_device_solar_data, which sends True whenever no enddate is
	// given — the only mode this project's tool contract exposes.
	QuerySingleDayView = "singleDayView"
	// QueryUserProfilePK filters the gear list and gear defaults by account.
	// Source: params={"userProfilePk": userProfileNumber} in get_gear.
	QueryUserProfilePK = "userProfilePk"
)

// ActivityTypesSegment is the fixed segment following a user profile number in
// the gear-defaults path. Source: get_gear_defaults's f-string.
const ActivityTypesSegment = "activityTypes"

// Sanitized endpoint labels for the device-and-gear tier. They never contain a
// host, a credential or a query string.
const (
	EndpointDeviceSettings        = Endpoint("connectapi.device.settings")
	EndpointDeviceLastUsed        = Endpoint("connectapi.device.last_used")
	EndpointPrimaryTrainingDevice = Endpoint("connectapi.device.primary_training_device")
	EndpointDeviceSolar           = Endpoint("connectapi.device.solar")
	EndpointGearDefaults          = Endpoint("connectapi.gear.defaults")
	EndpointGearStats             = Endpoint("connectapi.gear.stats")
	EndpointGearV2List            = Endpoint("connectapi.gear.v2_list")
	EndpointGearV2                = Endpoint("connectapi.gear.v2")
	EndpointGearV2Status          = Endpoint("connectapi.gear.v2_status")
)

// devicesEndpoints returns the device-and-gear labels declared here.
// EndpointGearFilter, declared in endpoints.go, is reused for the gear list by
// account rather than restated: one path serves both an activity filter and a
// profile filter. A function, not a var: AGENTS.md allows no package-level
// mutable state.
func devicesEndpoints() []Endpoint {
	return []Endpoint{
		EndpointDeviceSettings,
		EndpointDeviceLastUsed,
		EndpointPrimaryTrainingDevice,
		EndpointDeviceSolar,
		EndpointGearDefaults,
		EndpointGearStats,
		EndpointGearV2List,
		EndpointGearV2,
		EndpointGearV2Status,
	}
}

// Sanitized operation labels, one per call this tier adds.
const (
	OpGetDeviceSettings        = Op("get_device_settings")
	OpGetDeviceLastUsed        = Op("get_device_last_used")
	OpGetPrimaryTrainingDevice = Op("get_primary_training_device")
	OpGetDeviceSolarData       = Op("get_device_solar_data")
	OpGetGear                  = Op("get_gear")
	OpGetGearDefaults          = Op("get_gear_defaults")
	OpGetGearStats             = Op("get_gear_stats")
	OpGetGearDetails           = Op("get_gear_details")
	OpCreateGear               = Op("create_gear")
	OpUpdateGear               = Op("update_gear")
	OpSetGearStatus            = Op("set_gear_status")
	OpDeleteGear               = Op("delete_gear")
)

// devicesOps returns the device-and-gear operations declared here. OpListDevices
// (endpoints.go) is reused by the alarms walk rather than restated: it is the
// same read get_devices already performs.
func devicesOps() []Op {
	return []Op{
		OpGetDeviceSettings,
		OpGetDeviceLastUsed,
		OpGetPrimaryTrainingDevice,
		OpGetDeviceSolarData,
		OpGetGear,
		OpGetGearDefaults,
		OpGetGearStats,
		OpGetGearDetails,
		OpCreateGear,
		OpUpdateGear,
		OpSetGearStatus,
		OpDeleteGear,
	}
}
