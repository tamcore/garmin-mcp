package api

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/muktihari/fit/decoder"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/typedef"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// TestBuildBodyCompositionFITRoundTrips proves the encoded FIT file decodes
// back to the same weight-scale fields the request carried, at the scales
// fit.py:489-503 and the SDK's generated mesgdef.WeightScale both document.
func TestBuildBodyCompositionFITRoundTrips(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 3, 1, 7, 0, 0, 0, time.UTC)
	entry := BodyCompositionEntry{
		At: at, Weight: 72.5,
		PercentFat:        new(18.25),
		PercentHydration:  new(55.5),
		VisceralFatMass:   new(3.4),
		BoneMass:          new(3.1),
		MuscleMass:        new(30.2),
		BasalMet:          new(1600.0),
		ActiveMet:         new(400.0),
		PhysiqueRating:    new(int64(5)),
		MetabolicAge:      new(30.0),
		VisceralFatRating: new(int64(8)),
		BMI:               new(22.5),
	}

	raw, storedWeightKG, err := buildBodyCompositionFIT(entry)
	if err != nil {
		t.Fatalf("buildBodyCompositionFIT() = %v", err)
	}
	if storedWeightKG != 72.5 {
		t.Errorf("storedWeightKG = %v, want 72.5", storedWeightKG)
	}

	dec := decoder.New(bytes.NewReader(raw))
	if !dec.Next() {
		t.Fatalf("decoder.Next() = false, want true")
	}
	fit, err := dec.Decode()
	if err != nil {
		t.Fatalf("Decode() = %v", err)
	}

	var found bool
	for _, mesg := range fit.Messages {
		if mesg.Num != typedef.MesgNumWeightScale {
			continue
		}
		found = true
		scale := mesgdef.NewWeightScale(&mesg)
		if scale.Weight != typedef.Weight(7250) {
			t.Errorf("Weight = %v, want 7250", scale.Weight)
		}
		if scale.PercentFat != 1825 {
			t.Errorf("PercentFat = %v, want 1825", scale.PercentFat)
		}
		if scale.BasalMet != 6400 {
			t.Errorf("BasalMet = %v, want 6400", scale.BasalMet)
		}
		if scale.PhysiqueRating != 5 {
			t.Errorf("PhysiqueRating = %v, want 5", scale.PhysiqueRating)
		}
		if scale.Bmi != 225 {
			t.Errorf("Bmi = %v, want 225", scale.Bmi)
		}
	}
	if !found {
		t.Fatalf("no WeightScale message was decoded back")
	}
}

// TestBuildBodyCompositionFITLeavesOmittedFieldsInvalid proves an absent
// optional field is never encoded as a fabricated zero.
func TestBuildBodyCompositionFITLeavesOmittedFieldsInvalid(t *testing.T) {
	t.Parallel()

	entry := BodyCompositionEntry{At: time.Now(), Weight: 70}
	raw, _, err := buildBodyCompositionFIT(entry)
	if err != nil {
		t.Fatalf("buildBodyCompositionFIT() = %v", err)
	}

	dec := decoder.New(bytes.NewReader(raw))
	dec.Next()
	fit, err := dec.Decode()
	if err != nil {
		t.Fatalf("Decode() = %v", err)
	}
	for _, mesg := range fit.Messages {
		if mesg.Num != typedef.MesgNumWeightScale {
			continue
		}
		scale := mesgdef.NewWeightScale(&mesg)
		if scale.PercentFat != 0xFFFF {
			t.Errorf("PercentFat = %v, want the invalid sentinel 0xFFFF", scale.PercentFat)
		}
	}
}

func TestBodyCompositionEntryValidateRefusesOutOfRangeWeight(t *testing.T) {
	t.Parallel()

	req := client.Request{}
	for _, weight := range []float64{0, -1, 501} {
		entry := BodyCompositionEntry{At: time.Now(), Weight: weight}
		if err := entry.validate(req); !errors.Is(err, client.ErrValidation) {
			t.Errorf("validate() weight=%v = %v, want ErrValidation", weight, err)
		}
	}
}

func TestBodyCompositionEntryValidateRefusesAnAbsurdPercentage(t *testing.T) {
	t.Parallel()

	req := client.Request{}
	entry := BodyCompositionEntry{At: time.Now(), Weight: 70, PercentFat: new(150.0)}
	if err := entry.validate(req); !errors.Is(err, client.ErrValidation) {
		t.Errorf("validate() = %v, want ErrValidation", err)
	}
}

func TestScaledUint16RefusesAnOverflowingValue(t *testing.T) {
	t.Parallel()

	if _, ok := scaledUint16(1000, 100); ok {
		t.Errorf("scaledUint16(1000, 100) = ok, want overflow refusal")
	}
	if _, ok := scaledUint16(-1, 100); ok {
		t.Errorf("scaledUint16(-1, 100) = ok, want negative refusal")
	}
}

// TestScaledUint16TruncatesRatherThanRounds pins fit.py:178-183's own
// FitBaseType.pack behavior, `int(value)`, which truncates toward zero. A
// rounding implementation would store 7001 (70.01 kg) for this input; the
// correct, truncating one stores 7000 (70.00 kg).
func TestScaledUint16TruncatesRatherThanRounds(t *testing.T) {
	t.Parallel()

	scaled, ok := scaledUint16(70.006, bodyCompositionScaleWeight)
	if !ok {
		t.Fatalf("scaledUint16(70.006, 100) = not ok, want ok")
	}
	if scaled != 7000 {
		t.Errorf("scaledUint16(70.006, 100) = %d, want 7000 (truncated, not rounded to 7001)", scaled)
	}
}

// TestBuildBodyCompositionFITReportsTheStoredWeight proves the reported
// storedWeightKG reflects the truncated FIT encoding rather than echoing the
// caller's own more-precise input back unchanged.
func TestBuildBodyCompositionFITReportsTheStoredWeight(t *testing.T) {
	t.Parallel()

	entry := BodyCompositionEntry{At: time.Now(), Weight: 70.006}
	_, storedWeightKG, err := buildBodyCompositionFIT(entry)
	if err != nil {
		t.Fatalf("buildBodyCompositionFIT() = %v", err)
	}
	if storedWeightKG != 70.0 {
		t.Errorf("storedWeightKG = %v, want 70 (70.006 truncates to the FIT format's own precision)",
			storedWeightKG)
	}
}

// TestBodyCompositionEntryValidateRefusesAPreEpochTimestamp proves a
// pre-1990 instant is refused as an ordinary validation error naming the
// real cause, rather than reaching the encoder and surfacing its internal
// "message validation: ... no fields" error.
func TestBodyCompositionEntryValidateRefusesAPreEpochTimestamp(t *testing.T) {
	t.Parallel()

	req := client.Request{}
	entry := BodyCompositionEntry{At: time.Date(1985, 1, 1, 0, 0, 0, 0, time.UTC), Weight: 70}
	err := entry.validate(req)
	if !errors.Is(err, client.ErrValidation) {
		t.Fatalf("validate() = %v, want ErrValidation", err)
	}
}

// TestBodyCompositionEntryValidateRefusesATimestampBeyondTheFITRange proves
// a fabricated far-future timestamp (a typo'd year, for example) is refused
// before it can silently wrap to an arbitrary encoded date.
func TestBodyCompositionEntryValidateRefusesATimestampBeyondTheFITRange(t *testing.T) {
	t.Parallel()

	req := client.Request{}
	entry := BodyCompositionEntry{At: time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC), Weight: 70}
	err := entry.validate(req)
	if !errors.Is(err, client.ErrValidation) {
		t.Fatalf("validate() = %v, want ErrValidation", err)
	}
}

// TestBodyCompositionEntryValidateAcceptsAFractionalMetabolicAge proves
// metabolic_age accepts a manifest-valid fractional value: compat/tools.json
// types it "number", not "integer".
func TestBodyCompositionEntryValidateAcceptsAFractionalMetabolicAge(t *testing.T) {
	t.Parallel()

	req := client.Request{}
	entry := BodyCompositionEntry{At: time.Now(), Weight: 70, MetabolicAge: new(30.5)}
	if err := entry.validate(req); err != nil {
		t.Errorf("validate() = %v, want nil for a fractional metabolic_age", err)
	}
}
