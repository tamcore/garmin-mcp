package ratelimit_test

import (
	"errors"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/ratelimit"
)

const (
	gateKeyA = "10.0.0.1"
	gateKeyB = "10.0.0.2"
)

// testGateConfig is a small budget so a test exhausts it in a few calls.
func testGateConfig() ratelimit.GateConfig {
	return ratelimit.GateConfig{PerMinute: 60, Burst: 2, MaxKeys: 4}
}

func TestNewGateRefusesANonPositiveBudget(t *testing.T) {
	// Arrange
	cases := map[string]ratelimit.GateConfig{
		"rate":  {PerMinute: 0, Burst: 2, MaxKeys: 4},
		"burst": {PerMinute: 60, Burst: 0, MaxKeys: 4},
	}

	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			// Act
			_, err := ratelimit.NewGate(cfg, nil)

			// Assert
			if !errors.Is(err, ratelimit.ErrInvalidBudget) {
				t.Fatalf("NewGate error = %v, want ErrInvalidBudget", err)
			}
		})
	}
}

func TestNewGateRefusesAnUnboundedKeyTable(t *testing.T) {
	// Arrange
	cfg := ratelimit.GateConfig{PerMinute: 60, Burst: 2, MaxKeys: 0}

	// Act
	_, err := ratelimit.NewGate(cfg, nil)

	// Assert
	if !errors.Is(err, ratelimit.ErrUnboundedKeys) {
		t.Fatalf("NewGate error = %v, want ErrUnboundedKeys", err)
	}
}

func TestGateExhaustsOneKeyWithoutAffectingAnother(t *testing.T) {
	// Arrange
	now := time.Now()
	gate, err := ratelimit.NewGate(testGateConfig(), func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewGate returned error: %v", err)
	}

	// Act
	for i := range 2 {
		if decision := gate.Allow(gateKeyA); !decision.Allowed {
			t.Fatalf("call %d for the first key was refused", i)
		}
	}
	exhausted := gate.Allow(gateKeyA)
	other := gate.Allow(gateKeyB)

	// Assert
	if exhausted.Allowed {
		t.Fatalf("the third call for the first key was allowed")
	}
	if exhausted.RetryAfter <= 0 {
		t.Fatalf("a refusal advertised RetryAfter %v, want a positive delay", exhausted.RetryAfter)
	}
	if !other.Allowed {
		t.Fatalf("the second key was refused because the first key was exhausted")
	}
}

func TestGateRefillsOverTime(t *testing.T) {
	// Arrange
	now := time.Now()
	gate, err := ratelimit.NewGate(testGateConfig(), func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewGate returned error: %v", err)
	}
	for range 2 {
		gate.Allow(gateKeyA)
	}

	// Act
	now = now.Add(time.Minute)
	refilled := gate.Allow(gateKeyA)

	// Assert
	if !refilled.Allowed {
		t.Fatalf("the budget did not refill after a minute")
	}
}

func TestGateKeyTableStaysBounded(t *testing.T) {
	// Arrange
	gate, err := ratelimit.NewGate(testGateConfig(), nil)
	if err != nil {
		t.Fatalf("NewGate returned error: %v", err)
	}

	// Act
	for i := range 100 {
		gate.Allow(gateKeyA + string(rune('a'+i%26)) + string(rune('a'+i/26)))
	}

	// Assert
	if tracked := gate.TrackedKeys(); tracked > 4 {
		t.Fatalf("TrackedKeys = %d, want at most 4", tracked)
	}
}

func TestNilGateAllowsEveryCall(t *testing.T) {
	// Arrange
	var gate *ratelimit.Gate

	// Act
	decision := gate.Allow(gateKeyA)

	// Assert
	if !decision.Allowed {
		t.Fatalf("a nil gate refused a call")
	}
}

func TestGateRefusesAnEmptyKey(t *testing.T) {
	// Arrange
	gate, err := ratelimit.NewGate(testGateConfig(), nil)
	if err != nil {
		t.Fatalf("NewGate returned error: %v", err)
	}

	// Act
	decision := gate.Allow("")

	// Assert
	if decision.Allowed {
		t.Fatalf("an unattributable request was allowed")
	}
}
