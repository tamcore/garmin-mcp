package cmd

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/cryptostore"
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
