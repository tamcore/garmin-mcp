package api_test

import (
	"errors"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

func newWeight(t *testing.T, h harness) *api.Weight {
	t.Helper()

	w, err := api.NewWeight(h.rc)
	if err != nil {
		t.Fatalf("NewWeight() = %v", err)
	}
	return w
}

func TestNewWeightRefusesANilRequestLayer(t *testing.T) {
	t.Parallel()

	if _, err := api.NewWeight(nil); !errors.Is(err, client.ErrNotConfigured) {
		t.Fatalf("NewWeight(nil) = %v, want %v", err, client.ErrNotConfigured)
	}
}

func TestParseWeightUnitAcceptsKgAndLbs(t *testing.T) {
	t.Parallel()

	for _, tc := range []string{"kg", "lbs"} {
		unit, err := api.ParseWeightUnit(tc)
		if err != nil {
			t.Fatalf("ParseWeightUnit(%q) = %v", tc, err)
		}
		if string(unit) != tc {
			t.Errorf("ParseWeightUnit(%q) = %q, want %q", tc, unit, tc)
		}
	}
}

func TestParseWeightUnitRefusesAnUnrecognizedToken(t *testing.T) {
	t.Parallel()

	for _, tc := range []string{"", "lb", "KG", "pounds", " kg"} {
		if _, err := api.ParseWeightUnit(tc); !errors.Is(err, client.ErrValidation) {
			t.Errorf("ParseWeightUnit(%q) = %v, want %v", tc, err, client.ErrValidation)
		}
	}
}
