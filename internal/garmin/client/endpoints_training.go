package client

// Training-and-performance paths. Source: python-garminconnect 0.3.10
// garminconnect/__init__.py, cross-checked against Taxuspt src/garmin_mcp/training.py.
// A Prefix path is completed by the domain client with escaped segments.
const (
	// PathFitnessStatsActivity is the aggregated activity progress summary. The
	// window and the metric are query parameters, not segments.
	// Source: garmin_connect_fitnessstats, read by get_progress_summary_between_dates.
	PathFitnessStatsActivity = "/fitnessstats-service/activity"

	// PathHillScore is the single-day hill score, keyed by the calendarDate
	// parameter. Source: garmin_connect_hill_score_url, the enddate-is-None
	// branch of get_hill_score.
	PathHillScore = "/metrics-service/metrics/hillscore"
	// PathHillScoreStats is the hill score over a window, filtered by startDate,
	// endDate and aggregation. Source: f"{garmin_connect_hill_score_url}/stats",
	// the date-range branch of get_hill_score.
	PathHillScoreStats = "/metrics-service/metrics/hillscore/stats"
	// PathEnduranceScore is the single-day endurance score, keyed by the
	// calendarDate parameter. Source: garmin_connect_endurance_score_url, the
	// enddate-is-None branch of get_endurance_score.
	PathEnduranceScore = "/metrics-service/metrics/endurancescore"
	// PathEnduranceScoreStats is the endurance score over a window, filtered by
	// startDate, endDate and aggregation.
	// Source: f"{garmin_connect_endurance_score_url}/stats", the date-range
	// branch of get_endurance_score.
	PathEnduranceScoreStats = "/metrics-service/metrics/endurancescore/stats"
	// PathTrainingStatusPrefix precedes a calendar date in the aggregated
	// training status path. Source: garmin_connect_training_status_url.
	PathTrainingStatusPrefix = "/metrics-service/metrics/trainingstatus/aggregated"
	// PathMaxMetricsPrefix precedes a start date and an end date, each its own
	// segment, in the max-metrics path that carries the VO2 max series. A single
	// day is the same date twice. Source: garmin_connect_metrics_url, used by
	// get_max_metrics and get_max_metrics_range.
	PathMaxMetricsPrefix = "/metrics-service/metrics/maxmet/daily"

	// PathHRVPrefix precedes a calendar date in the heart-rate-variability path.
	// Source: garmin_connect_hrv_url, read by get_hrv_data.
	PathHRVPrefix = "/hrv-service/hrv"
	// PathFitnessAgePrefix precedes a calendar date in the fitness-age path.
	// Source: garmin_connect_fitnessage.
	PathFitnessAgePrefix = "/fitnessage-service/fitnessage"

	// PathLatestFunctionalThresholdPowerPrefix precedes a Garmin sport key as a
	// single escaped segment. Upstream's cycling FTP read appends SportCycling.
	// Source: f"{garmin_connect_biometric_url}/latestFunctionalThresholdPower/CYCLING"
	// in get_cycling_ftp.
	PathLatestFunctionalThresholdPowerPrefix = "/biometric-service/biometric/latestFunctionalThresholdPower"
	// PathLatestLactateThreshold is the latest lactate-threshold speed and heart
	// rate. It takes no segment and no parameter.
	// Source: f"{garmin_connect_biometric_url}/latestLactateThreshold" in the
	// latest branch of get_lactate_threshold.
	PathLatestLactateThreshold = "/biometric-service/biometric/latestLactateThreshold"
	// PathPowerToWeightLatestPrefix precedes a calendar date and is filtered by
	// the sport parameter. Source:
	// f"{garmin_connect_biometric_url}/powerToWeight/latest/{date.today()}" in
	// the latest branch of get_lactate_threshold.
	PathPowerToWeightLatestPrefix = "/biometric-service/biometric/powerToWeight/latest"
	// PathLactateThresholdSpeedRangePrefix precedes a start date and an end date,
	// each its own segment. Source:
	// f"{garmin_connect_biometric_stats_url}/lactateThresholdSpeed/range/{start_date}/{end_date}".
	PathLactateThresholdSpeedRangePrefix = "/biometric-service/stats/lactateThresholdSpeed/range"
	// PathLactateThresholdHeartRateRangePrefix precedes a start date and an end
	// date, each its own segment. Source:
	// f"{garmin_connect_biometric_stats_url}/lactateThresholdHeartRate/range/{start_date}/{end_date}".
	PathLactateThresholdHeartRateRangePrefix = "/biometric-service/stats/lactateThresholdHeartRate/range"
	// PathFunctionalThresholdPowerRangePrefix precedes a start date and an end
	// date, each its own segment. Source:
	// f"{garmin_connect_biometric_stats_url}/functionalThresholdPower/range/{start}/{end}"
	// in get_functional_threshold_power_range, which the range branch of
	// get_lactate_threshold calls for the power series.
	PathFunctionalThresholdPowerRangePrefix = "/biometric-service/stats/functionalThresholdPower/range"

	// PathEpochReloadRequestPrefix precedes a calendar date. It is the only
	// training path this file declares that is written rather than read: upstream
	// POSTs it with no body to make Garmin re-derive an offloaded day.
	// Source: garmin_request_reload_url, posted by request_reload.
	PathEpochReloadRequestPrefix = "/wellness-service/wellness/epoch/request"
)

// Query parameter names the training-and-performance reads add to the sets in
// endpoints.go and endpoints_health.go. Source: the params dicts of the
// corresponding methods in garminconnect/__init__.py.
const (
	QueryAggregation               = "aggregation"
	QueryAggregationStrategy       = "aggregationStrategy"
	QueryMetric                    = "metric"
	QueryGroupByParentActivityType = "groupByParentActivityType"
	QuerySport                     = "sport"
)

// Fixed parameter values the training reads send. They are Garmin wire values,
// not runtime settings, so they belong here rather than in a handler.
const (
	// AggregationLifetime is the aggregation the progress summary sends.
	// Source: the params dict of get_progress_summary_between_dates.
	AggregationLifetime = "lifetime"
	// AggregationDaily is the aggregation the hill-score window sends, and the
	// default of the lactate-threshold range.
	// Source: get_hill_score and get_lactate_threshold.
	AggregationDaily = "daily"
	// AggregationWeekly is the aggregation the endurance-score window sends.
	// Source: the params dict of get_endurance_score.
	AggregationWeekly = "weekly"
	// AggregationStrategyLatest selects the last sample in each aggregation
	// bucket. Source: the params dicts of the biometric range reads.
	AggregationStrategyLatest = "LATEST"

	// SportRunning is the upper-case Garmin sport key the biometric range reads
	// send. Source: the params dicts of get_lactate_threshold and
	// get_functional_threshold_power_range.
	SportRunning = "RUNNING"
	// SportCycling is the upper-case Garmin sport key appended to
	// PathLatestFunctionalThresholdPowerPrefix. Source: get_cycling_ftp.
	SportCycling = "CYCLING"
	// SportRunningMixedCase is upstream's mixed-case spelling, sent verbatim to
	// the power-to-weight endpoint alone. The casing differs from SportRunning on
	// purpose: it is what upstream sends, and it is not normalized here.
	// Source: params={"sport": "Running"} in the latest branch of
	// get_lactate_threshold.
	SportRunningMixedCase = "Running"
)

// Sanitized endpoint labels for the training-and-performance tier. They never
// contain a host, a credential or a query string.
const (
	EndpointFitnessStats                   = Endpoint("connectapi.fitnessstats.activity")
	EndpointHillScore                      = Endpoint("connectapi.metrics.hill_score")
	EndpointHillScoreStats                 = Endpoint("connectapi.metrics.hill_score_stats")
	EndpointEnduranceScore                 = Endpoint("connectapi.metrics.endurance_score")
	EndpointEnduranceScoreStats            = Endpoint("connectapi.metrics.endurance_score_stats")
	EndpointTrainingStatus                 = Endpoint("connectapi.metrics.training_status")
	EndpointMaxMetrics                     = Endpoint("connectapi.metrics.max_metrics")
	EndpointHRV                            = Endpoint("connectapi.hrv.daily")
	EndpointFitnessAge                     = Endpoint("connectapi.fitnessage.daily")
	EndpointLatestFunctionalThresholdPower = Endpoint("connectapi.biometric.latest_ftp")
	EndpointLatestLactateThreshold         = Endpoint("connectapi.biometric.latest_lactate_threshold")
	EndpointPowerToWeightLatest            = Endpoint("connectapi.biometric.power_to_weight_latest")
	EndpointLactateThresholdSpeedRange     = Endpoint("connectapi.biometric.lactate_threshold_speed_range")
	EndpointLactateThresholdHeartRateRange = Endpoint("connectapi.biometric.lactate_threshold_heart_rate_range")
	EndpointFunctionalThresholdPowerRange  = Endpoint("connectapi.biometric.ftp_range")
	EndpointEpochReloadRequest             = Endpoint("connectapi.wellness.epoch_reload_request")
)

// trainingEndpoints returns the training labels. A function, not a var: AGENTS.md
// allows no package-level mutable state, and a constant that cannot be a const is
// a function, never a var.
func trainingEndpoints() []Endpoint {
	return []Endpoint{
		EndpointFitnessStats,
		EndpointHillScore,
		EndpointHillScoreStats,
		EndpointEnduranceScore,
		EndpointEnduranceScoreStats,
		EndpointTrainingStatus,
		EndpointMaxMetrics,
		EndpointHRV,
		EndpointFitnessAge,
		EndpointLatestFunctionalThresholdPower,
		EndpointLatestLactateThreshold,
		EndpointPowerToWeightLatest,
		EndpointLactateThresholdSpeedRange,
		EndpointLactateThresholdHeartRateRange,
		EndpointFunctionalThresholdPowerRange,
		EndpointEpochReloadRequest,
	}
}

// Sanitized operation labels, one per tool. Several share an endpoint — four read
// training status, counting the VO2 max trend's fallback — so the operation, not
// the endpoint, identifies a read in a log line.
//
// OpRequestReload is the one write, and carries no credential. Whether it may be
// replayed follows from the Effect its caller assigns, not from its method:
// retryDelay refuses a replay only for a credential submission or a
// non-repeatable Effect, and EffectIdempotentWrite is repeatable.
const (
	OpGetProgressSummaryBetweenDates = Op("get_progress_summary_between_dates")
	OpGetHillScore                   = Op("get_hill_score")
	OpGetEnduranceScore              = Op("get_endurance_score")
	OpGetTrainingEffect              = Op("get_training_effect")
	OpGetHRVData                     = Op("get_hrv_data")
	OpGetFitnessAgeData              = Op("get_fitnessage_data")
	OpGetTrainingStatus              = Op("get_training_status")
	OpGetCyclingFTP                  = Op("get_cycling_ftp")
	OpGetLactateThreshold            = Op("get_lactate_threshold")
	OpRequestReload                  = Op("request_reload")
	OpGetTrainingLoadTrend           = Op("get_training_load_trend")
	OpGetTrainingLoadBalance         = Op("get_training_load_balance")
	OpGetHRVTrend                    = Op("get_hrv_trend")
	OpGetVO2MaxTrend                 = Op("get_vo2max_trend")
	OpGetRespirationTrend            = Op("get_respiration_trend")
)

// trainingOps returns the training operations. A function for the same reason.
func trainingOps() []Op {
	return []Op{
		OpGetProgressSummaryBetweenDates,
		OpGetHillScore,
		OpGetEnduranceScore,
		OpGetTrainingEffect,
		OpGetHRVData,
		OpGetFitnessAgeData,
		OpGetTrainingStatus,
		OpGetCyclingFTP,
		OpGetLactateThreshold,
		OpRequestReload,
		OpGetTrainingLoadTrend,
		OpGetTrainingLoadBalance,
		OpGetHRVTrend,
		OpGetVO2MaxTrend,
		OpGetRespirationTrend,
	}
}
