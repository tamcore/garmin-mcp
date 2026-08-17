package api

import (
	"bytes"
	"fmt"
	"time"

	"github.com/muktihari/fit/encoder"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/typedef"
	"github.com/muktihari/fit/proto"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// bodyCompositionScaleWeight, bodyCompositionScalePercent,
// bodyCompositionScaleMET and bodyCompositionScaleBMI are the FIT scale
// factors add_body_composition's own FIT encoder applies before writing each
// field. They are not hand-picked: they are the same scales the SDK's
// generated mesgdef.WeightScale documents on each field (Weight, PercentFat,
// PercentHydration, VisceralFatMass, BoneMass and MuscleMass: "Scale: 100";
// BasalMet and ActiveMet: "Scale: 4"; Bmi: "Scale: 10"), cross-checked
// against fit.py:489-503's own content list — (0, weight, 100),
// (1, percent_fat, 100), (2, percent_hydration, 100), (3, visceral_fat_mass,
// 100), (4, bone_mass, 100), (5, muscle_mass, 100), (7, basal_met, 4),
// (9, active_met, 4), (13, bmi, 10) — which names the identical scale for
// every one of these fields. Field 253 in that same list is the message
// timestamp, not weight; it carries scale 1 and is rendered through
// FitEncoder.timestamp, never through one of these constants.
// PhysiqueRating, MetabolicAge and VisceralFatRating carry no scale in
// either source.
const (
	bodyCompositionScaleWeight  = 100
	bodyCompositionScalePercent = 100
	bodyCompositionScaleMET     = 4
	bodyCompositionScaleBMI     = 10
)

// fitEpoch is the FIT protocol's own epoch: seconds since UTC 00:00 Dec 31
// 1989. A function, not a var: AGENTS.md allows no package-level mutable
// state, and a time.Date literal is exactly the case it names.
//
// Source: fit.py:410-416's own timestamp() docstring, "seconds since UTC
// 00:00 Dec 31 1989 (631065600)", and the SDK's
// github.com/muktihari/fit/kit/datetime.Epoch(), the same instant.
func fitEpoch() time.Time {
	return time.Date(1989, time.December, 31, 0, 0, 0, 0, time.UTC)
}

// fitMaxTimestampSeconds is the largest second count a FIT uint32 timestamp
// can carry. 0xFFFFFFFF (4294967295) is reserved as the SDK's own "invalid"
// sentinel (github.com/muktihari/fit/profile/basetype.Uint32Invalid), so the
// representable ceiling is one second short of it.
const fitMaxTimestampSeconds = 0xFFFFFFFE

// fitMaxTimestamp is the latest instant a FIT uint32 timestamp can represent.
// A function for the same reason fitEpoch is.
func fitMaxTimestamp() time.Time {
	return fitEpoch().Add(fitMaxTimestampSeconds * time.Second)
}

// requireFITTimestamp refuses an instant the FIT uint32 timestamp field
// cannot represent, before it ever reaches the encoder.
//
// Two failure modes this guards against, both observed rather than
// theoretical: a pre-epoch instant leaves the SDK's own Timestamp field at
// its invalid sentinel, so a message with no other field set encodes as
// empty and the SDK refuses it with an internal "message validation: ...
// no fields" error that names no real cause; a post-ceiling instant (for
// example a typo'd year like 2200) does not error at all — converting a
// float64 second count above the uint32 range is undefined by the Go
// language spec, and in practice wraps to a fabricated date decades away
// from the intended one, which is silently written into a permanent Garmin
// record. Both are refused here as an ordinary validation error instead.
func requireFITTimestamp(req client.Request, at time.Time) error {
	switch {
	case at.Before(fitEpoch()):
		return invalid(req, fmt.Errorf(
			"%w: timestamp predates the FIT format's epoch (1989-12-31T00:00:00Z)",
			client.ErrValidation))
	case at.After(fitMaxTimestamp()):
		return invalid(req, fmt.Errorf(
			"%w: timestamp exceeds the FIT format's representable range", client.ErrValidation))
	}
	return nil
}

// BodyCompositionEntry is the strict request model for add_body_composition.
// Weight is required; every other field is optional, matching
// fit.py:473-487's own Optional[int | float] parameters, and an absent field
// is written to the FIT message as the SDK's own invalid sentinel rather
// than a fabricated zero.
type BodyCompositionEntry struct {
	At               time.Time
	Weight           float64
	PercentFat       *float64
	PercentHydration *float64
	VisceralFatMass  *float64
	BoneMass         *float64
	MuscleMass       *float64
	BasalMet         *float64
	ActiveMet        *float64
	PhysiqueRating   *int64
	// MetabolicAge is a number, not an integer: compat/tools.json's
	// add_body_composition types metabolic_age "number" (unlike
	// physique_rating and visceral_fat_rating, which really are integers),
	// and __init__.py:1186's own metabolic_age parameter is float | None too.
	MetabolicAge      *float64
	VisceralFatRating *int64
	BMI               *float64
}

// bodyCompositionBounds are sanity ceilings this package rejects a reading
// beyond, before any of it reaches a FIT message. They are not values
// fit.py or __init__.py state — the upstream release performs no such
// check — but AGENTS.md requires health data to be checked for an absurd
// magnitude before dispatch, and these are generous enough to admit any real
// human measurement.
const (
	minBodyWeightKG = 1.0
	maxBodyWeightKG = 500.0
	maxPercent      = 100.0
	maxMassKG       = 200.0
	maxMET          = 10000.0
	minPhysique     = 1
	maxPhysique     = 9
	minMetabolicAge = 1.0
	maxMetabolicAge = 120.0
	minFatRating    = 1
	maxFatRating    = 59
	maxBMI          = 100.0
)

// validate reports whether the entry may be encoded and dispatched.
func (e BodyCompositionEntry) validate(req client.Request) error {
	if err := requireFiniteNutrient(req, e.Weight, "weight"); err != nil {
		return err
	}
	if e.Weight < minBodyWeightKG || e.Weight > maxBodyWeightKG {
		return invalid(req, fmt.Errorf("%w: weight is outside a plausible range",
			client.ErrValidation))
	}
	if e.At.IsZero() {
		return invalid(req, fmt.Errorf("%w: a timestamp is required", client.ErrValidation))
	}
	if err := requireFITTimestamp(req, e.At); err != nil {
		return err
	}
	for _, field := range []struct {
		name     string
		value    *float64
		min, max float64
	}{
		{"percent_fat", e.PercentFat, 0, maxPercent},
		{"percent_hydration", e.PercentHydration, 0, maxPercent},
		{"visceral_fat_mass", e.VisceralFatMass, 0, maxMassKG},
		{"bone_mass", e.BoneMass, 0, maxMassKG},
		{"muscle_mass", e.MuscleMass, 0, maxMassKG},
		{"basal_met", e.BasalMet, 0, maxMET},
		{"active_met", e.ActiveMet, 0, maxMET},
		{"bmi", e.BMI, 0, maxBMI},
		{"metabolic_age", e.MetabolicAge, minMetabolicAge, maxMetabolicAge},
	} {
		if field.value == nil {
			continue
		}
		if err := requireFiniteNutrient(req, *field.value, field.name); err != nil {
			return err
		}
		if *field.value < field.min || *field.value > field.max {
			return invalid(req, fmt.Errorf("%w: %s is outside a plausible range",
				client.ErrValidation, field.name))
		}
	}
	for _, field := range []struct {
		name     string
		value    *int64
		min, max int64
	}{
		{"physique_rating", e.PhysiqueRating, minPhysique, maxPhysique},
		{"visceral_fat_rating", e.VisceralFatRating, minFatRating, maxFatRating},
	} {
		if field.value == nil {
			continue
		}
		if *field.value < field.min || *field.value > field.max {
			return invalid(req, fmt.Errorf("%w: %s is outside a plausible range",
				client.ErrValidation, field.name))
		}
	}
	return nil
}

// scaledUint16 renders value at scale as the FIT uint16 encoding, or reports
// that it does not fit. It truncates rather than rounds, matching fit.py's
// own FitBaseType.pack (fit.py:178-183, `value = int(value)` applied to
// `value * scale`, and Python's int() truncates toward zero exactly as Go's
// float-to-integer conversion does): a caller that writes 70.006 gets 70.00
// stored, not 70.01.
func scaledUint16(value, scale float64) (uint16, bool) {
	scaled := value * scale
	if scaled < 0 || scaled > 0xFFFE {
		return 0, false
	}
	return uint16(scaled), true
}

// buildBodyCompositionFIT encodes entry as a minimal weight-scale FIT file:
// a FileId, a DeviceInfo and a WeightScale message, in that order. Every
// field the SDK's generated mesgdef structs do not receive a value for is
// left at NewX(nil)'s own invalid sentinel, which is what a caller-omitted
// Optional field renders as in fit.py's own encoder (a missing content
// tuple element is written as its FitBaseType's own "invalid" literal,
// fit.py:247-249's own `if value is None: value = basetype["invalid"]`).
//
// Source: garminconnect/__init__.py:1174-1216 (add_body_composition) and
// fit.py:466-518 (FitEncoderWeight, write_weight_scale). Manufacturer,
// product, serial number and every device-info field beyond its own
// timestamp are never set by add_body_composition, which calls
// write_file_info(), write_file_creator() and write_device_info(dt) with no
// further arguments, so this port leaves the identical fields at their SDK
// invalid sentinel rather than inventing a value.
//
// Deliberate deviation: fit.py:296-320's write_file_creator() always writes
// its two fields, even when both are None, because its hand-rolled encoder
// writes an explicit "invalid" literal for every declared field
// unconditionally. The SDK's generated FileCreator.ToMesg omits a field
// still at its invalid sentinel, and add_body_composition never supplies
// software_version or hardware_version, so a ported FileCreator message
// would always encode zero fields — which this SDK's encoder refuses to
// write ("message validation: ... no fields"), because a message with no
// fields is not valid FIT framing. This port therefore omits the
// file_creator message entirely rather than fabricating a value for a field
// upstream itself never sets: FileCreator carries no weight data, and its
// absence changes nothing Garmin's ingestion reads.
//
// It reports storedWeightKG, the weight actually written to the FIT message
// after scaledUint16's own truncation: a caller who writes 70.006 must be
// told 70.00 was stored, not have their own 70.006 echoed back as if it had
// round-tripped exactly.
func buildBodyCompositionFIT(entry BodyCompositionEntry) (fitBytes []byte, storedWeightKG float64, err error) {
	fileID := mesgdef.NewFileId(nil)
	fileID.Type = typedef.FileWeight
	fileID.TimeCreated = entry.At

	deviceInfo := mesgdef.NewDeviceInfo(nil)
	deviceInfo.Timestamp = entry.At

	weightScale := mesgdef.NewWeightScale(nil)
	weightScale.Timestamp = entry.At
	if scaled, ok := scaledUint16(entry.Weight, bodyCompositionScaleWeight); ok {
		weightScale.Weight = typedef.Weight(scaled)
		storedWeightKG = float64(scaled) / bodyCompositionScaleWeight
	}
	setOptionalScaledField(&weightScale.PercentFat, entry.PercentFat, bodyCompositionScalePercent)
	setOptionalScaledField(&weightScale.PercentHydration, entry.PercentHydration, bodyCompositionScalePercent)
	setOptionalScaledField(&weightScale.VisceralFatMass, entry.VisceralFatMass, bodyCompositionScalePercent)
	setOptionalScaledField(&weightScale.BoneMass, entry.BoneMass, bodyCompositionScalePercent)
	setOptionalScaledField(&weightScale.MuscleMass, entry.MuscleMass, bodyCompositionScalePercent)
	setOptionalScaledField(&weightScale.BasalMet, entry.BasalMet, bodyCompositionScaleMET)
	setOptionalScaledField(&weightScale.ActiveMet, entry.ActiveMet, bodyCompositionScaleMET)
	setOptionalScaledField(&weightScale.Bmi, entry.BMI, bodyCompositionScaleBMI)
	if entry.PhysiqueRating != nil {
		weightScale.PhysiqueRating = uint8(*entry.PhysiqueRating)
	}
	if entry.MetabolicAge != nil {
		weightScale.MetabolicAge = uint8(*entry.MetabolicAge)
	}
	if entry.VisceralFatRating != nil {
		weightScale.VisceralFatRating = uint8(*entry.VisceralFatRating)
	}

	fit := &proto.FIT{Messages: []proto.Message{
		fileID.ToMesg(nil),
		deviceInfo.ToMesg(nil),
		weightScale.ToMesg(nil),
	}}

	var buf bytes.Buffer
	if encErr := encoder.New(&buf).Encode(fit); encErr != nil {
		return nil, 0, fmt.Errorf("garmin api: encoding the body-composition FIT file: %w", encErr)
	}
	return buf.Bytes(), storedWeightKG, nil
}

// setOptionalScaledField writes value*scale into field when value is
// present, leaving field at its NewWeightScale(nil) invalid sentinel
// otherwise. A value that cannot be represented at scale is also left
// invalid rather than silently clamped, matching validate's own refusal of
// anything out of a plausible range before encoding is ever attempted.
func setOptionalScaledField(field *uint16, value *float64, scale float64) {
	if value == nil {
		return
	}
	if scaled, ok := scaledUint16(*value, scale); ok {
		*field = scaled
	}
}
