package policy_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/policy"
)

// TestEffectiveTiersAlwaysIncludesReadOnly proves the tier a deployment can never
// take away is always reported, whatever the operator configured.
func TestEffectiveTiersAlwaysIncludesReadOnly(t *testing.T) {
	t.Parallel()

	pol, err := policy.New(baseConfig(), nil)
	if err != nil {
		t.Fatalf("policy.New() = %v", err)
	}

	got := pol.EffectiveTiers(context.Background())
	if !slices.Contains(got, policy.TierReadOnly) {
		t.Fatalf("EffectiveTiers() = %v, want it to contain TierReadOnly", got)
	}
}

func TestEffectiveTiersIncludesLocallyAuthorizedTiers(t *testing.T) {
	t.Parallel()

	cfg := withEnable(baseConfig(), true)
	cfg.LocalOperatorAuthority = true
	pol, err := policy.New(cfg, nil)
	if err != nil {
		t.Fatalf("policy.New() = %v", err)
	}

	got := pol.EffectiveTiers(context.Background())
	want := []policy.Tier{policy.TierReadOnly, policy.TierWrite, policy.TierDestructive}
	if !slices.Equal(got, want) {
		t.Fatalf("EffectiveTiers() = %v, want %v", got, want)
	}
}

// TestEffectiveTiersMatchesDecideForEveryRegisteredTool is the property the
// tools/list filter and server_info must never disagree on: a tier EffectiveTiers
// reports absent must never be a tier Decide allows for a registered tool, and a
// tier Decide allows must always be present.
func TestEffectiveTiersMatchesDecideForEveryRegisteredTool(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		cfg    policy.Config
		scopes policy.ScopeSource
	}{
		{"no scopes, nothing enabled", baseConfig(), nil},
		{"write enabled, no scopes", withEnable(baseConfig(), false), nil},
		{
			"write enabled and granted", withEnable(baseConfig(), false),
			grantingScopes{scopes: []policy.Scope{policy.ScopeWrite}},
		},
		{
			"destructive enabled and granted", withEnable(baseConfig(), true),
			grantingScopes{scopes: []policy.Scope{policy.ScopeWrite, policy.ScopeDestructive}},
		},
		{
			"destructive enabled but only write granted", withEnable(baseConfig(), true),
			grantingScopes{scopes: []policy.Scope{policy.ScopeWrite}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pol, err := policy.New(tc.cfg, tc.scopes)
			if err != nil {
				t.Fatalf("policy.New() = %v", err)
			}
			ctx := context.Background()
			effective := pol.EffectiveTiers(ctx)

			for _, tool := range registered {
				decision := pol.Decide(ctx, tool)
				if decision.Allowed && !slices.Contains(effective, decision.Tier) {
					t.Errorf("Decide(%q) allowed tier %v, but EffectiveTiers() = %v does not contain it",
						tool, decision.Tier, effective)
				}
			}
		})
	}
}

// withEnable returns cfg with write enabled and destructive set as given: every
// case here needs write on, so that is the one always-true flag this signature
// does not bother parameterizing.
func withEnable(cfg policy.Config, destructive bool) policy.Config {
	cfg.EnableWrite = true
	cfg.EnableDestructive = destructive
	return cfg
}

// TestEffectiveTiersFailsClosedOnAScopeLookupError proves a broken scope source
// narrows the report rather than defaulting to open.
func TestEffectiveTiersFailsClosedOnAScopeLookupError(t *testing.T) {
	t.Parallel()

	cfg := withEnable(baseConfig(), true)
	pol, err := policy.New(cfg, grantingScopes{err: errors.New("boom")})
	if err != nil {
		t.Fatalf("policy.New() = %v", err)
	}

	got := pol.EffectiveTiers(context.Background())
	if !slices.Equal(got, []policy.Tier{policy.TierReadOnly}) {
		t.Fatalf("EffectiveTiers() = %v, want only TierReadOnly on a scope lookup failure", got)
	}
}

// TestGrantedScopesReturnsWhatTheSourceGrants proves server_info's scope report
// is the raw grant, not the tier-gated subset — a client may hold a domain read
// scope this package's tier gate never checks.
func TestGrantedScopesReturnsWhatTheSourceGrants(t *testing.T) {
	t.Parallel()

	pol, err := policy.New(baseConfig(), grantingScopes{
		scopes: []policy.Scope{policy.ScopeWrite, "garmin:read:health"},
	})
	if err != nil {
		t.Fatalf("policy.New() = %v", err)
	}

	got := pol.GrantedScopes(context.Background())
	want := []policy.Scope{policy.ScopeWrite, "garmin:read:health"}
	if !slices.Equal(got, want) {
		t.Fatalf("GrantedScopes() = %v, want %v", got, want)
	}
}

// TestEffectiveTiersExcludesATierTheAllowlistLeavesWithNoUsableTool is the P2
// self-consistency proof: with only a read-only tool allowlisted, write and
// destructive must not be reported even though both are enabled and granted,
// because Decide would refuse every tool in either tier — the allowlist
// narrows below the tier gate. Reporting them anyway is what let server_info
// answer enabledTiers=[write,destructive] alongside visibleToolCount=1 in the
// same response.
func TestEffectiveTiersExcludesATierTheAllowlistLeavesWithNoUsableTool(t *testing.T) {
	t.Parallel()

	cfg := withEnable(baseConfig(), true)
	cfg.Allowlist = []string{readTool}
	pol, err := policy.New(cfg, grantingScopes{
		scopes: []policy.Scope{policy.ScopeWrite, policy.ScopeDestructive},
	})
	if err != nil {
		t.Fatalf("policy.New() = %v", err)
	}

	got := pol.EffectiveTiers(context.Background())
	if !slices.Equal(got, []policy.Tier{policy.TierReadOnly}) {
		t.Fatalf("EffectiveTiers() = %v, want only [read-only]: the allowlist "+
			"leaves no usable tool in either higher tier", got)
	}
}

// TestEffectiveTiersExcludesATierTheDenylistLeavesWithNoUsableTool is the
// denylist half of the same property: denylisting the one write tool must drop
// "write" from EffectiveTiers even though the tier itself is enabled and granted.
func TestEffectiveTiersExcludesATierTheDenylistLeavesWithNoUsableTool(t *testing.T) {
	t.Parallel()

	cfg := withEnable(baseConfig(), false)
	cfg.Denylist = []string{writeTool}
	pol, err := policy.New(cfg, grantingScopes{scopes: []policy.Scope{policy.ScopeWrite}})
	if err != nil {
		t.Fatalf("policy.New() = %v", err)
	}

	got := pol.EffectiveTiers(context.Background())
	if !slices.Equal(got, []policy.Tier{policy.TierReadOnly}) {
		t.Fatalf("EffectiveTiers() = %v, want only [read-only]: the denylist "+
			"removes the only tool the write tier has", got)
	}
}

// TestGrantedScopesReturnsNoneOnALookupError proves this is a fail-safe report
// rather than a refusal: server_info is read-only and must always answer.
func TestGrantedScopesReturnsNoneOnALookupError(t *testing.T) {
	t.Parallel()

	pol, err := policy.New(baseConfig(), grantingScopes{err: errors.New("boom")})
	if err != nil {
		t.Fatalf("policy.New() = %v", err)
	}

	if got := pol.GrantedScopes(context.Background()); got != nil {
		t.Fatalf("GrantedScopes() = %v, want nil on a lookup error", got)
	}
}
