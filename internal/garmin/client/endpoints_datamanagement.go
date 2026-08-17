package client

// Data-management API-tier paths. Source: python-garminconnect 0.3.10
// garminconnect/__init__.py (0310-__init__.py in the evidence bundle):
// self.garmin_connect_upload (:568), self.garmin_connect_set_hydration_url
// (:445-447) and self.garmin_connect_set_blood_pressure_endpoint (:497-499),
// cross-checked against the Taxuspt pinned curation at
// src/garmin_mcp/data_management.py, which is the only source for the
// add_body_composition/set_blood_pressure/add_hydration_data argument
// shapes exposed as MCP tools.
const (
	// PathUpload is the generic file-upload target add_body_composition posts
	// its encoded FIT file to. Source: __init__.py:568,
	// self.garmin_connect_upload = "/upload-service/upload", and
	// add_body_composition's own `url = self.garmin_connect_upload`
	// (__init__.py:1213).
	PathUpload = "/upload-service/upload"

	// PathBloodPressureSet is set_blood_pressure's write target. Source:
	// __init__.py:497-499,
	// self.garmin_connect_set_blood_pressure_endpoint =
	// "/bloodpressure-service/bloodpressure", and set_blood_pressure's own
	// `url = f"{self.garmin_connect_set_blood_pressure_endpoint}"`
	// (__init__.py:1384).
	PathBloodPressureSet = "/bloodpressure-service/bloodpressure"

	// PathHydrationSet is add_hydration_data's write target. Source:
	// __init__.py:445-447,
	// self.garmin_connect_set_hydration_url =
	// "/usersummary-service/usersummary/hydration/log", and
	// add_hydration_data's own `url = self.garmin_connect_set_hydration_url`
	// (__init__.py:1641).
	PathHydrationSet = "/usersummary-service/usersummary/hydration/log"
)

// Sanitized endpoint labels for the data-management write tier. They never
// contain a host, a credential or a query string.
const (
	EndpointUpload           = Endpoint("connectapi.upload")
	EndpointBloodPressureSet = Endpoint("connectapi.bloodpressure.set")
	EndpointHydrationSet     = Endpoint("connectapi.hydration.set")
)

// dataManagementEndpoints returns the data-management labels. A function,
// not a var: AGENTS.md allows no package-level mutable state.
func dataManagementEndpoints() []Endpoint {
	return []Endpoint{
		EndpointUpload,
		EndpointBloodPressureSet,
		EndpointHydrationSet,
	}
}

// Sanitized operation labels, one per data-management tool.
const (
	OpAddBodyComposition = Op("add_body_composition")
	OpSetBloodPressure   = Op("set_blood_pressure")
	OpAddHydrationData   = Op("add_hydration_data")
)

// dataManagementOps returns the data-management operations. A function for
// the same reason as dataManagementEndpoints.
func dataManagementOps() []Op {
	return []Op{
		OpAddBodyComposition,
		OpSetBloodPressure,
		OpAddHydrationData,
	}
}
