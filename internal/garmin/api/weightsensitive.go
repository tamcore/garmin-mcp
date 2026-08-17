package api

import "log/slog"

// The weight-management domain's LogValue implementations. Every model here
// carries a body weight, a body-composition figure or a timestamp tied to a
// person, so each reports its shape only, following the same discipline
// sensitive.go documents for the rest of this package. Kept in a file of its
// own rather than folded into sensitive.go, the same reason
// nutritionsensitive.go and challengessensitive.go give.

// LogValue reports which fields one weigh-in measurement carries, never a
// weight, a body-composition figure, a date or a timestamp.
func (m WeighInMeasurement) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "weighInMeasurement"),
		slog.String("calendarDate", presence(m.CalendarDate.IsSet())),
		slog.String("weight", presence(m.Weight.IsSet())),
		slog.String("bmi", presence(m.BMI.IsSet())),
		slog.String("bodyFat", presence(m.BodyFat.IsSet())),
		slog.String("bodyWater", presence(m.BodyWater.IsSet())),
		slog.String("boneMass", presence(m.BoneMass.IsSet())),
		slog.String("muscleMass", presence(m.MuscleMass.IsSet())),
		slog.String("sourceType", presence(m.SourceType.IsSet())),
		slog.String("timestampGMT", presence(m.TimestampGMT.IsSet())),
		slog.String("samplePk", presence(m.SamplePK.IsSet())),
	)
}

// LogValue reports whether an averaged weight arrived, never its value.
func (a WeighInAverage) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "weighInAverage"),
		slog.String("weight", presence(a.Weight.IsSet())),
	)
}

// LogValue reports how many days and measurements the window carries, never a
// measurement's own fields: those are reported by WeighInMeasurement.LogValue,
// which slog reaches on its own when a caller logs one directly.
func (r WeighInRange) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "weighInRange"),
		slog.Int("days", len(r.DailySummaries)),
		slog.Int("measurements", len(r.allMeasurements())),
		slog.Bool("truncated", r.MeasurementsTruncated()),
		slog.String("totalAverage", presence(r.TotalAverage != nil)),
	)
}

// LogValue reports how many measurements the day carries, never their fields.
func (d DailyWeighIns) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "dailyWeighIns"),
		slog.Int("measurements", len(d.DateWeightList)),
		slog.Bool("truncated", d.MeasurementsTruncated()),
		slog.String("totalAverage", presence(d.TotalAverage != nil)),
	)
}

// LogValue reports the shape of a weigh-in write request, never the weight or
// either timestamp it carries.
func (e WeighInEntry) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "weighInEntry"),
		slog.String("unit", presence(e.Unit != "")),
		slog.String("weight", presence(e.Weight != 0)),
		slog.String("localAt", presence(!e.LocalAt.IsZero())),
		slog.String("gmtAt", presence(!e.GMTAt.IsZero())),
	)
}

// LogValue reports how many weigh-ins were deleted, never their identifiers or
// the write outcome's own retained payload.
func (r DeleteWeighInsResult) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "deleteWeighInsResult"),
		slog.Int("deleted", len(r.Deleted)),
	)
}
