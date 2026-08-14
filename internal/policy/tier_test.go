package policy_test

import (
	"testing"

	"github.com/tamcore/garmin-mcp/internal/policy"
)

func TestTierStringIsStableAndCoarse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		tier policy.Tier
		want string
	}{
		{policy.TierReadOnly, "read-only"},
		{policy.TierWrite, "write"},
		{policy.TierDestructive, "destructive"},
		{policy.Tier(0), "unknown"},
		{policy.Tier(99), "unknown"},
	}

	for _, tc := range cases {
		if got := tc.tier.String(); got != tc.want {
			t.Errorf("Tier(%d).String() = %q, want %q", int(tc.tier), got, tc.want)
		}
	}
}

func TestTierIsValidRejectsTheZeroValue(t *testing.T) {
	t.Parallel()

	if policy.Tier(0).IsValid() {
		t.Fatal("the zero Tier must not read as valid: a tool must declare its tier explicitly")
	}
	for _, tier := range []policy.Tier{policy.TierReadOnly, policy.TierWrite, policy.TierDestructive} {
		if !tier.IsValid() {
			t.Errorf("Tier %v must read as valid", tier)
		}
	}
}

func TestTierRequiredScopeMapsWriteAndDestructiveOnly(t *testing.T) {
	t.Parallel()

	cases := []struct {
		tier      policy.Tier
		wantScope policy.Scope
		wantOK    bool
	}{
		{policy.TierReadOnly, "", false},
		{policy.TierWrite, policy.ScopeWrite, true},
		{policy.TierDestructive, policy.ScopeDestructive, true},
	}

	for _, tc := range cases {
		scope, ok := tc.tier.RequiredScope()
		if ok != tc.wantOK || scope != tc.wantScope {
			t.Errorf("Tier(%v).RequiredScope() = %q, %v; want %q, %v",
				tc.tier, scope, ok, tc.wantScope, tc.wantOK)
		}
	}
}

func TestTierRequiresConfirmationOnlyForDestructive(t *testing.T) {
	t.Parallel()

	if policy.TierReadOnly.RequiresConfirmation() {
		t.Error("a read-only tool must not require confirmation")
	}
	if policy.TierWrite.RequiresConfirmation() {
		t.Error("a write tool must not require confirmation at the tier level")
	}
	if !policy.TierDestructive.RequiresConfirmation() {
		t.Error("a destructive tool must require confirmation")
	}
}
