package client

import "slices"

// Health-and-wellness API-tier paths. Source: the URL table built in
// GarminConnect.__init__ and the per-method URLs in python-garminconnect 0.3.10,
// file garminconnect/__init__.py.
//
// Like the paths in endpoints.go these are templates with no query string. A
// path that ends in Prefix is completed by the domain client with escaped
// segments — a calendar date, a date range, a week count, or the account's
// display name — so no caller-supplied value is ever concatenated raw.
const (
	// PathDailySummaryChartPrefix is the intraday step chart. The display name is
	// appended as a single escaped segment and the day is the date parameter.
	// Source: garmin_connect_user_summary_chart, read by get_steps_data.
	PathDailySummaryChartPrefix = "/wellness-service/wellness/dailySummaryChart"
	// PathFloorsChartDailyPrefix precedes a calendar date in the daily floors
	// chart path. Source: garmin_connect_floors_chart_daily_url.
	PathFloorsChartDailyPrefix = "/wellness-service/wellness/floorsChartData/daily"
	// PathDailyHeartRatePrefix is the date-keyed daily heart-rate document. The
	// display name is appended as a single escaped segment and the day is the
	// date parameter. Source: garmin_connect_heartrates_daily_url.
	PathDailyHeartRatePrefix = "/wellness-service/wellness/dailyHeartRate"
	// PathDailyStressPrefix precedes a calendar date in the daily stress path.
	// Source: garmin_connect_daily_stress_url.
	PathDailyStressPrefix = "/wellness-service/wellness/dailyStress"
	// PathDailyRespirationPrefix precedes a calendar date in the daily
	// respiration path. Source: garmin_connect_daily_respiration_url.
	PathDailyRespirationPrefix = "/wellness-service/wellness/daily/respiration"
	// PathDailySpO2Prefix precedes a calendar date in the daily pulse-ox path.
	// Source: garmin_connect_daily_spo2_url.
	PathDailySpO2Prefix = "/wellness-service/wellness/daily/spo2"
	// PathDailyEvents is the all-day event list, keyed by the calendarDate
	// parameter rather than by a path segment.
	// Source: garmin_daily_events_url.
	PathDailyEvents = "/wellness-service/wellness/dailyEvents"
	// PathBodyBatteryDaily is the body-battery day report, filtered by startDate
	// and endDate. Source: garmin_connect_daily_body_battery_url.
	PathBodyBatteryDaily = "/wellness-service/wellness/bodyBattery/reports/daily"
	// PathBodyBatteryEventsPrefix precedes a calendar date in the body-battery
	// event path. Source: garmin_connect_body_battery_events_url.
	PathBodyBatteryEventsPrefix = "/wellness-service/wellness/bodyBattery/events"

	// PathDailyHydrationPrefix precedes a calendar date in the daily hydration
	// path. Source: garmin_connect_daily_hydration_url.
	PathDailyHydrationPrefix = "/usersummary-service/usersummary/hydration/daily"
	// PathDailyStepsStatsPrefix precedes a start date and an end date, each its
	// own segment, in the daily step aggregate path. Garmin caps the window at 28
	// days. Source: garmin_connect_daily_stats_steps_url.
	PathDailyStepsStatsPrefix = "/usersummary-service/stats/steps/daily"
	// PathWeeklyStepsStatsPrefix precedes an end date and a week count, each its
	// own segment. Source: garmin_connect_weekly_stats_steps_url.
	PathWeeklyStepsStatsPrefix = "/usersummary-service/stats/steps/weekly"
	// PathWeeklyStressStatsPrefix precedes an end date and a week count, each its
	// own segment. Source: garmin_connect_weekly_stats_stress_url.
	PathWeeklyStressStatsPrefix = "/usersummary-service/stats/stress/weekly"
	// PathWeeklyIntensityMinutesStatsPrefix precedes a start date and an end
	// date, each its own segment. Unlike the two weekly paths above, this one
	// takes a date range rather than a week count.
	// Source: garmin_connect_weekly_stats_intensity_minutes_url.
	PathWeeklyIntensityMinutesStatsPrefix = "/usersummary-service/stats/im/weekly"

	// PathBodyComposition is the body-composition series, filtered by startDate
	// and endDate. Source: f"{garmin_connect_weight_url}/weight/dateRange".
	PathBodyComposition = "/weight-service/weight/dateRange"
	// PathBloodPressureRangePrefix precedes a start date and an end date, each
	// its own segment. Source: garmin_connect_blood_pressure_endpoint.
	PathBloodPressureRangePrefix = "/bloodpressure-service/bloodpressure/range"
	// PathRestingHeartRatePrefix is the user-statistics daily series the resting
	// heart-rate read uses. The display name is appended as a single escaped
	// segment and the day is carried by fromDate, untilDate and metricId.
	// Source: garmin_connect_rhr_url.
	PathRestingHeartRatePrefix = "/userstats-service/wellness/daily"
	// PathTrainingReadinessPrefix precedes a calendar date in the training
	// readiness path. Source: garmin_connect_training_readiness_url.
	PathTrainingReadinessPrefix = "/metrics-service/metrics/trainingreadiness"
	// PathLifestyleLoggingPrefix precedes a calendar date in the lifestyle
	// logging path. Source: garmin_connect_daily_lifestyle_logging_url.
	PathLifestyleLoggingPrefix = "/lifestylelogging-service/dailyLog"
)

// Query parameter names the health-and-wellness reads add to the set in
// endpoints.go. Source: the params dicts of the corresponding methods in
// garminconnect/__init__.py.
const (
	QueryFromDate   = "fromDate"
	QueryUntilDate  = "untilDate"
	QueryMetricID   = "metricId"
	QueryIncludeAll = "includeAll"
)

// MetricIDRestingHeartRate selects the resting heart-rate series from the
// user-statistics daily endpoint, which answers one metric per request.
// Source: the metricId of get_rhr_day.
const MetricIDRestingHeartRate = 60

// Sanitized endpoint labels for the health-and-wellness tier. They never contain
// a host, a credential or a query string.
const (
	EndpointDailySummaryChart           = Endpoint("connectapi.wellness.daily_summary_chart")
	EndpointFloorsChartDaily            = Endpoint("connectapi.wellness.floors_chart_daily")
	EndpointDailyHeartRate              = Endpoint("connectapi.wellness.daily_heart_rate")
	EndpointDailyStress                 = Endpoint("connectapi.wellness.daily_stress")
	EndpointDailyRespiration            = Endpoint("connectapi.wellness.daily_respiration")
	EndpointDailySpO2                   = Endpoint("connectapi.wellness.daily_spo2")
	EndpointDailyEvents                 = Endpoint("connectapi.wellness.daily_events")
	EndpointBodyBatteryDaily            = Endpoint("connectapi.wellness.body_battery_daily")
	EndpointBodyBatteryEvents           = Endpoint("connectapi.wellness.body_battery_events")
	EndpointDailyHydration              = Endpoint("connectapi.usersummary.hydration_daily")
	EndpointDailyStepsStats             = Endpoint("connectapi.usersummary.steps_daily")
	EndpointWeeklyStepsStats            = Endpoint("connectapi.usersummary.steps_weekly")
	EndpointWeeklyStressStats           = Endpoint("connectapi.usersummary.stress_weekly")
	EndpointWeeklyIntensityMinutesStats = Endpoint("connectapi.usersummary.intensity_minutes_weekly")
	EndpointBodyComposition             = Endpoint("connectapi.weight.date_range")
	EndpointBloodPressure               = Endpoint("connectapi.bloodpressure.range")
	EndpointRestingHeartRate            = Endpoint("connectapi.userstats.resting_heart_rate")
	EndpointTrainingReadiness           = Endpoint("connectapi.metrics.training_readiness")
	EndpointLifestyleLogging            = Endpoint("connectapi.lifestylelogging.daily_log")
)

// healthEndpoints returns the health labels. A function, not a var: AGENTS.md
// allows no package-level mutable state.
func healthEndpoints() []Endpoint {
	return []Endpoint{
		EndpointDailySummaryChart,
		EndpointFloorsChartDaily,
		EndpointDailyHeartRate,
		EndpointDailyStress,
		EndpointDailyRespiration,
		EndpointDailySpO2,
		EndpointDailyEvents,
		EndpointBodyBatteryDaily,
		EndpointBodyBatteryEvents,
		EndpointDailyHydration,
		EndpointDailyStepsStats,
		EndpointWeeklyStepsStats,
		EndpointWeeklyStressStats,
		EndpointWeeklyIntensityMinutesStats,
		EndpointBodyComposition,
		EndpointBloodPressure,
		EndpointRestingHeartRate,
		EndpointTrainingReadiness,
		EndpointLifestyleLogging,
	}
}

// Sanitized operation labels for the health-and-wellness reads, one per tool.
// Several of them share an endpoint and differ only in what the domain client
// asks for or keeps, so the operation, not the endpoint, is what identifies the
// read in a log line.
const (
	OpGetStats                    = Op("get_stats")
	OpGetStatsAndBody             = Op("get_stats_and_body")
	OpGetBodyComposition          = Op("get_body_composition")
	OpGetStepsData                = Op("get_steps_data")
	OpGetDailySteps               = Op("get_daily_steps")
	OpGetWeeklySteps              = Op("get_weekly_steps")
	OpGetWeeklyStress             = Op("get_weekly_stress")
	OpGetWeeklyIntensityMinutes   = Op("get_weekly_intensity_minutes")
	OpGetTrainingReadiness        = Op("get_training_readiness")
	OpGetMorningTrainingReadiness = Op("get_morning_training_readiness")
	OpGetBodyBattery              = Op("get_body_battery")
	OpGetBodyBatteryEvents        = Op("get_body_battery_events")
	OpGetBloodPressure            = Op("get_blood_pressure")
	OpGetFloors                   = Op("get_floors")
	OpGetRestingHeartRateDay      = Op("get_rhr_day")
	OpGetHeartRates               = Op("get_heart_rates")
	OpGetHeartRatesSummary        = Op("get_heart_rates_summary")
	OpGetHydrationData            = Op("get_hydration_data")
	OpGetSleepSummary             = Op("get_sleep_summary")
	OpGetStressData               = Op("get_stress_data")
	OpGetStressSummary            = Op("get_stress_summary")
	OpGetAllDayStress             = Op("get_all_day_stress")
	OpGetAllDayEvents             = Op("get_all_day_events")
	OpGetRespirationData          = Op("get_respiration_data")
	OpGetRespirationSummary       = Op("get_respiration_summary")
	OpGetSpO2Data                 = Op("get_spo2_data")
	OpGetLifestyleLoggingData     = Op("get_lifestyle_logging_data")
)

// healthOps returns the health operations. A function for the same reason.
func healthOps() []Op {
	return []Op{
		OpGetStats,
		OpGetStatsAndBody,
		OpGetBodyComposition,
		OpGetStepsData,
		OpGetDailySteps,
		OpGetWeeklySteps,
		OpGetWeeklyStress,
		OpGetWeeklyIntensityMinutes,
		OpGetTrainingReadiness,
		OpGetMorningTrainingReadiness,
		OpGetBodyBattery,
		OpGetBodyBatteryEvents,
		OpGetBloodPressure,
		OpGetFloors,
		OpGetRestingHeartRateDay,
		OpGetHeartRates,
		OpGetHeartRatesSummary,
		OpGetHydrationData,
		OpGetSleepSummary,
		OpGetStressData,
		OpGetStressSummary,
		OpGetAllDayStress,
		OpGetAllDayEvents,
		OpGetRespirationData,
		OpGetRespirationSummary,
		OpGetSpO2Data,
		OpGetLifestyleLoggingData,
	}
}

// knownEndpoints and knownOps are the allowlists Request.Validate checks, assembled
// from the core, health, training and nutrition halves. Functions, not vars:
// AGENTS.md allows no package-level mutable state, and the per-request slice costs
// nothing beside the round trip.
func knownEndpoints() []Endpoint {
	return slices.Concat(
		coreEndpoints(), healthEndpoints(), trainingEndpoints(), nutritionEndpoints(), challengesEndpoints(),
		devicesEndpoints(), weightEndpoints(), womensHealthEndpoints(),
		courseEndpoints(), dataManagementEndpoints(),
	)
}

func knownOps() []Op {
	return slices.Concat(
		coreOps(), healthOps(), trainingOps(), nutritionOps(), challengesOps(), devicesOps(), weightOps(),
		womensHealthOps(), courseOps(), dataManagementOps(),
	)
}
