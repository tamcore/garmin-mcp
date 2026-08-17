package cmd

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/cryptostore"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// TestCheckRotationTargetRefusesATargetAboveTheEnvelopeMaximum is the
// off-by-one defect: a target above cryptostore.MaxKeyVersion cannot be
// sealed at all (the envelope header is a uint32), so it must be refused
// regardless of anything else about the rotation.
//
// A target that does not equal the active version or active+1 is already
// refused today for an unrelated reason (the one-version-at-a-time rule), so
// that alone would not prove this defect is fixed: the marker here is
// planted directly at cryptostore.MaxKeyVersion so that target ==
// active+1 exactly, the one shape the pre-existing skip-check accepts as a
// legitimate new rotation. Only an explicit bound on the target itself
// catches it.
func TestCheckRotationTargetRefusesATargetAboveTheEnvelopeMaximum(t *testing.T) {
	paths := statePaths{root: activeVersionTestDir(t)}
	paths.keys = filepath.Join(paths.root, "keys")
	if err := writeActiveKeyVersion(paths, cryptostore.MaxKeyVersion); err != nil {
		t.Fatalf("plant an active marker at the envelope maximum: %v", err)
	}

	if _, err := checkRotationTarget(paths, cryptostore.MaxKeyVersion+1); !errors.Is(err, ErrRotationTargetInvalid) {
		t.Fatalf("checkRotationTarget(MaxKeyVersion+1) with active at the maximum: err = %v, want ErrRotationTargetInvalid",
			err)
	}
}

// TestReportFileStoreResealDoesNotClaimActiveKeyAfterALostRace is the MEDIUM
// item: a lost race must not be reported the same way as "nothing needed
// resealing". Before this fix, both store.ResealAlreadyCurrent and what is
// now store.ResealRaced collapsed into a single changed=false, so the
// affirmative "the bound principal's record is at the active key version"
// line printed even when the record had actually just changed under a
// concurrent writer.
func TestReportFileStoreResealDoesNotClaimActiveKeyAfterALostRace(t *testing.T) {
	var out bytes.Buffer
	err := reportFileStoreReseal(&out, store.ResealRaced)
	if !errors.Is(err, ErrRotationIncomplete) {
		t.Fatalf("reportFileStoreReseal(ResealRaced): err = %v, want ErrRotationIncomplete", err)
	}
	if strings.Contains(out.String(), "is at the active key version") {
		t.Fatalf("reportFileStoreReseal(ResealRaced) printed the affirmative active-key-version line: %q", out.String())
	}
}

// TestReportFileStoreResealClaimsActiveKeyOnlyWhenTrue is the companion
// property: the affirmative line must still print for the two outcomes that
// actually leave the record at the active key.
func TestReportFileStoreResealClaimsActiveKeyOnlyWhenTrue(t *testing.T) {
	for _, outcome := range []store.ResealOutcome{store.ResealRewrote, store.ResealAlreadyCurrent, store.ResealNoRecord} {
		var out bytes.Buffer
		if err := reportFileStoreReseal(&out, outcome); err != nil {
			t.Fatalf("reportFileStoreReseal(%v): %v", outcome, err)
		}
		if !strings.Contains(out.String(), "is at the active key version") {
			t.Fatalf("reportFileStoreReseal(%v) did not print the affirmative active-key-version line: %q",
				outcome, out.String())
		}
	}
}
