package api

import (
	"log/slog"
	"maps"
	"slices"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// The aggregated training-status document, which four reads share.
//
// It is one Garmin document — /metrics-service/metrics/trainingstatus/aggregated for
// one calendar day — and it is modeled exactly once, here. get_training_status,
// get_training_load_trend, get_training_load_balance and the VO2 max trend's per-day
// fallback all decode these types and differ only in the sanitized operation label
// they are read under. Two models of one document drift into two answers to one
// question.
//
// It is health data: a load, a VO2 max and a training status are readings. Never log
// one, never cache one.
//
// Every field is optional and every section is a pointer, because Garmin sends an
// explicit null for a section the account has no data in. That is a normal state for a
// new account or a rest stretch, never an error.

// AcuteTrainingLoad is the acute-to-chronic workload block of one device's training
// status.
//
// Source: the acuteTrainingLoadDTO fields upstream reads in get_training_status and
// get_training_load_trend. acwrStatus is a phrase and acwrPercent a number; a sampled
// day confirms that split.
type AcuteTrainingLoad struct {
	DailyTrainingLoadAcute         client.Number `json:"dailyTrainingLoadAcute"`
	DailyTrainingLoadChronic       client.Number `json:"dailyTrainingLoadChronic"`
	DailyAcuteChronicWorkloadRatio client.Number `json:"dailyAcuteChronicWorkloadRatio"`
	ACWRStatus                     client.Text   `json:"acwrStatus"`
	ACWRPercent                    client.Number `json:"acwrPercent"`
	MinTrainingLoadChronic         client.Number `json:"minTrainingLoadChronic"`
	MaxTrainingLoadChronic         client.Number `json:"maxTrainingLoadChronic"`
}

// TrainingStatusDevice is one device's view of the training status. Garmin keys these
// by device identifier; the key is never decoded into a result.
//
// The status fields are flat rather than wrapped in a trainingStatusDTO; source: the
// reads in upstream's get_training_load_trend.
//
// trainingStatus and fitnessTrend are decoded as Text rather than as Number, and that
// is evidenced rather than defensive: a sampled day shows both arriving as numeric
// codes, with the human-readable phrase in a separate field. Text accepts a number, a
// string and a boolean, so neither spelling can fail the whole document, and nothing
// here models the codes as a closed set — one day of one account shows a fraction of
// Garmin's range.
type TrainingStatusDevice struct {
	CalendarDate          *string            `json:"calendarDate"`
	PrimaryTrainingDevice *bool              `json:"primaryTrainingDevice"`
	TrainingStatus        client.Text        `json:"trainingStatus"`
	FeedbackPhrase        client.Text        `json:"trainingStatusFeedbackPhrase"`
	Sport                 client.Text        `json:"sport"`
	FitnessTrend          client.Text        `json:"fitnessTrend"`
	AcuteTrainingLoad     *AcuteTrainingLoad `json:"acuteTrainingLoadDTO"`
}

// TrainingLoadBalanceDevice is one device's monthly load balance, with the target range
// Garmin sets for each intensity band.
//
// Source: the monthlyLoad* fields and trainingBalanceFeedbackPhrase upstream reads in
// get_training_status and get_training_load_balance. The six target bounds are what
// let a band be placed below, within or above its range.
type TrainingLoadBalanceDevice struct {
	CalendarDate          *string     `json:"calendarDate"`
	PrimaryTrainingDevice *bool       `json:"primaryTrainingDevice"`
	FeedbackPhrase        client.Text `json:"trainingBalanceFeedbackPhrase"`

	MonthlyLoadAerobicLow           client.Number `json:"monthlyLoadAerobicLow"`
	MonthlyLoadAerobicLowTargetMin  client.Number `json:"monthlyLoadAerobicLowTargetMin"`
	MonthlyLoadAerobicLowTargetMax  client.Number `json:"monthlyLoadAerobicLowTargetMax"`
	MonthlyLoadAerobicHigh          client.Number `json:"monthlyLoadAerobicHigh"`
	MonthlyLoadAerobicHighTargetMin client.Number `json:"monthlyLoadAerobicHighTargetMin"`
	MonthlyLoadAerobicHighTargetMax client.Number `json:"monthlyLoadAerobicHighTargetMax"`
	MonthlyLoadAnaerobic            client.Number `json:"monthlyLoadAnaerobic"`
	MonthlyLoadAnaerobicTargetMin   client.Number `json:"monthlyLoadAnaerobicTargetMin"`
	MonthlyLoadAnaerobicTargetMax   client.Number `json:"monthlyLoadAnaerobicTargetMax"`
}

// VO2MaxEntry is one sport's VO2 max reading.
//
// It carries a calendar date because the max-metrics range keys its readings by the
// date inside the per-sport section rather than on the entry; the aggregated status
// omits that field, which costs its reader nothing.
type VO2MaxEntry struct {
	CalendarDate       *string       `json:"calendarDate"`
	VO2MaxValue        client.Number `json:"vo2MaxValue"`
	VO2MaxPreciseValue client.Number `json:"vo2MaxPreciseValue"`
}

// Value takes the rounded estimate and falls back to the precise one.
//
// That order is upstream's, and it is the whole point of citing it: the candidate
// paths of _extract_vo2_measurements list vo2MaxValue before vo2MaxPreciseValue and
// the first match wins, so a document carrying 52.0 and 52.3 reports 52.0. Preferring
// the precise field reads like an improvement and is a parity break — the same figure
// this tool reports would differ from upstream's by a tenth, silently. A caller that
// wants the precise reading has it: get_training_status returns both, separately.
func (e VO2MaxEntry) Value() client.Number {
	if e.VO2MaxValue.IsSet() {
		return e.VO2MaxValue
	}
	return e.VO2MaxPreciseValue
}

// TrainingStatusLatest is the device-keyed status block.
type TrainingStatusLatest struct {
	LatestData map[string]TrainingStatusDevice `json:"latestTrainingStatusData"`
}

// TrainingStatusVO2Max is the per-sport VO2 max block. Garmin names the running series
// "generic"; source: the candidate paths of _extract_vo2_measurements.
type TrainingStatusVO2Max struct {
	Generic *VO2MaxEntry `json:"generic"`
	Cycling *VO2MaxEntry `json:"cycling"`
}

// TrainingStatusLoadBalance is the device-keyed load-balance block.
type TrainingStatusLoadBalance struct {
	Devices map[string]TrainingLoadBalanceDevice `json:"metricsTrainingLoadBalanceDTOMap"`
}

// TrainingStatus is the aggregated training status of one day.
//
// Each device-keyed map stays a map because Garmin keys it by device identifier: a
// caller is told how many devices reported, never which.
type TrainingStatus struct {
	MostRecentTrainingStatus      *TrainingStatusLatest      `json:"mostRecentTrainingStatus"`
	MostRecentVO2Max              *TrainingStatusVO2Max      `json:"mostRecentVO2Max"`
	MostRecentTrainingLoadBalance *TrainingStatusLoadBalance `json:"mostRecentTrainingLoadBalance"`

	raw client.Payload
}

// Payload is the retained raw response.
func (t TrainingStatus) Payload() client.Payload { return t.raw }

// LogValue reports which sections arrived, never a load, a status or a VO2 max.
func (t TrainingStatus) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "trainingStatus"),
		slog.String("status", presence(t.MostRecentTrainingStatus != nil)),
		slog.String("vo2max", presence(t.MostRecentVO2Max != nil)),
		slog.String("loadBalance", presence(t.MostRecentTrainingLoadBalance != nil)),
		slog.Any("payload", t.raw),
	)
}

// PrimaryStatus picks the device entry to report.
func (t TrainingStatus) PrimaryStatus() (TrainingStatusDevice, bool) {
	if t.MostRecentTrainingStatus == nil {
		return TrainingStatusDevice{}, false
	}
	_, device, ok := SelectStatusDevice(t.MostRecentTrainingStatus.LatestData)
	return device, ok
}

// SelectStatusDevice picks the one device a reader of this document must describe,
// and returns its key so every other block can describe the same device.
//
// The rule, in order: the device Garmin marks primary; then the most recently dated
// entry; then the lowest key. Every step is deterministic, which is a deliberate
// departure from upstream — upstream takes "the first device" out of a Python dict,
// and taking it out of a Go map would make the answer depend on map iteration order
// and differ between two identical calls.
//
// It is exported and shared because two readers choosing separately is not a style
// question: it produced one result whose training status came from one watch and
// whose monthly load came from another, with nothing in the answer saying so.
func SelectStatusDevice(
	devices map[string]TrainingStatusDevice,
) (string, TrainingStatusDevice, bool) {
	if len(devices) == 0 {
		return "", TrainingStatusDevice{}, false
	}
	keys := slices.Sorted(maps.Keys(devices))
	for _, key := range keys {
		if entry := devices[key]; entry.PrimaryTrainingDevice != nil &&
			*entry.PrimaryTrainingDevice {
			return key, entry, true
		}
	}

	chosen := keys[0]
	for _, key := range keys[1:] {
		if laterDay(devices[key].CalendarDate, devices[chosen].CalendarDate) {
			chosen = key
		}
	}
	return chosen, devices[chosen], true
}

// laterDay reports whether candidate names a later calendar day than current. A day
// Garmin did not name never wins.
func laterDay(candidate, current *string) bool {
	if candidate == nil {
		return false
	}
	return current == nil || *candidate > *current
}

// PrimaryLoadBalance picks the load-focus entry to report, by the same rule.
func (t TrainingStatus) PrimaryLoadBalance() (TrainingLoadBalanceDevice, bool) {
	if t.MostRecentTrainingLoadBalance == nil {
		return TrainingLoadBalanceDevice{}, false
	}
	devices := t.MostRecentTrainingLoadBalance.Devices
	key, ok := primaryKey(devices, func(entry TrainingLoadBalanceDevice) bool {
		return entry.PrimaryTrainingDevice != nil && *entry.PrimaryTrainingDevice
	})
	if !ok {
		return TrainingLoadBalanceDevice{}, false
	}
	return devices[key], true
}

// primaryKey returns the key of the entry marked primary, or the lowest key.
func primaryKey[T any](devices map[string]T, isPrimary func(T) bool) (string, bool) {
	if len(devices) == 0 {
		return "", false
	}
	keys := slices.Sorted(maps.Keys(devices))
	for _, key := range keys {
		if isPrimary(devices[key]) {
			return key, true
		}
	}
	return keys[0], true
}
