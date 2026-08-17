package api

import "log/slog"

// The data-management domain's LogValue implementations. Every model here
// carries a body-composition figure, a blood-pressure reading or a
// hydration volume tied to a person, so each reports its shape only,
// following the same discipline sensitive.go documents for the rest of this
// package. Kept in a file of its own, the same reason weightsensitive.go is.

// LogValue reports which fields a body-composition write carries, never a
// weight, a percentage, a mass or a timestamp.
func (e BodyCompositionEntry) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "bodyCompositionEntry"),
		slog.String("at", presence(!e.At.IsZero())),
		slog.String("weight", presence(e.Weight != 0)),
		slog.String("percentFat", presence(e.PercentFat != nil)),
		slog.String("percentHydration", presence(e.PercentHydration != nil)),
		slog.String("visceralFatMass", presence(e.VisceralFatMass != nil)),
		slog.String("boneMass", presence(e.BoneMass != nil)),
		slog.String("muscleMass", presence(e.MuscleMass != nil)),
		slog.String("basalMet", presence(e.BasalMet != nil)),
		slog.String("activeMet", presence(e.ActiveMet != nil)),
		slog.String("physiqueRating", presence(e.PhysiqueRating != nil)),
		slog.String("metabolicAge", presence(e.MetabolicAge != nil)),
		slog.String("visceralFatRating", presence(e.VisceralFatRating != nil)),
		slog.String("bmi", presence(e.BMI != nil)),
	)
}

// LogValue reports which fields a blood-pressure write carries, never the
// systolic, diastolic or pulse values, the notes or the timestamp.
func (e BloodPressureEntry) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "bloodPressureEntry"),
		slog.String("systolic", presence(e.Systolic != 0)),
		slog.String("diastolic", presence(e.Diastolic != 0)),
		slog.String("pulse", presence(e.Pulse != 0)),
		slog.String("notes", presence(e.Notes != "")),
		slog.String("at", presence(!e.At.IsZero())),
	)
}

// LogValue reports which fields a hydration write carries, never the
// volume, the calendar date or the timestamp.
func (e HydrationEntry) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "hydrationEntry"),
		slog.String("valueInMl", presence(e.ValueInML != 0)),
		slog.String("date", presence(!e.Date.IsZero())),
		slog.String("at", presence(!e.At.IsZero())),
	)
}
