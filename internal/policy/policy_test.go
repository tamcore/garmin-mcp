package policy_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/policy"
)

// Synthetic tool names shared by these tests.
const (
	readTool        = "get_activities"
	readTool2       = "get_sleep_data"
	writeTool       = "add_weigh_in"
	destructiveTool = "delete_workout"
)

// registered is the pretend set of actually-registered tool names used across
// these tests. Validate is checked against it, so a typo in a tier list fails.
var registered = []string{readTool, readTool2, writeTool, destructiveTool}

func baseConfig() policy.Config {
	return policy.Config{
		Mode:             policy.ModeLocal,
		ReadOnlyTools:    []string{readTool, readTool2},
		WriteTools:       []string{writeTool},
		DestructiveTools: []string{destructiveTool},
	}
}

// grantingScopes stands in for the remote source that reads a verified bearer token.
type grantingScopes struct {
	scopes []policy.Scope
	err    error
}

func (g grantingScopes) GrantedScopes(context.Context) ([]policy.Scope, error) {
	return g.scopes, g.err
}

func mustNew(t *testing.T, cfg policy.Config, scopes policy.ScopeSource) *policy.Policy {
	t.Helper()

	p, err := policy.New(cfg, scopes)
	if err != nil {
		t.Fatalf("policy.New returned error: %v", err)
	}
	if err := p.Validate(registered); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	return p
}

func TestReadOnlyToolIsAllowedWithNoScopesGranted(t *testing.T) {
	t.Parallel()

	p := mustNew(t, baseConfig(), policy.NoScopes{})

	decision := p.Decide(context.Background(), readTool)
	if !decision.Allowed {
		t.Fatalf("read-only tool denied: %v (%s)", decision.Err, decision.Reason)
	}
	if decision.Tier != policy.TierReadOnly {
		t.Fatalf("Tier = %v, want read-only", decision.Tier)
	}
	if decision.RequiresConfirmation {
		t.Fatal("a read-only tool must not require confirmation")
	}
}

func TestLocalOperatorAuthorityAllowsEnabledTiersWithoutOAuthScopes(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.LocalOperatorAuthority = true
	cfg.EnableWrite = true
	cfg.EnableDestructive = true
	p := mustNew(t, cfg, nil)

	write := p.Decide(context.Background(), writeTool)
	if !write.Allowed {
		t.Fatalf("local write denied: %v (%s)", write.Err, write.Reason)
	}
	if write.RequiresConfirmation {
		t.Fatal("local write tool requires confirmation")
	}

	destructive := p.Decide(context.Background(), destructiveTool)
	if !destructive.Allowed {
		t.Fatalf("local destructive call denied: %v (%s)", destructive.Err, destructive.Reason)
	}
	if !destructive.RequiresConfirmation {
		t.Fatal("local destructive tool does not require confirmation")
	}
	if scopes := p.GrantedScopes(context.Background()); len(scopes) != 0 {
		t.Fatalf("GrantedScopes() = %v, want none for local operator authority", scopes)
	}
}

func TestRemoteOperatorEnablementAloneNeverSuffices(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.Mode = policy.ModeRemote
	cfg.EnableWrite = true
	cfg.EnableDestructive = true
	p := mustNew(t, cfg, policy.NoScopes{})

	for _, tool := range []string{writeTool, destructiveTool} {
		decision := p.Decide(context.Background(), tool)
		if decision.Allowed {
			t.Errorf("%s allowed with enablement but no granted scope", tool)
		}
		if !errors.Is(decision.Err, policy.ErrScopeNotGranted) {
			t.Errorf("%s Err = %v, want ErrScopeNotGranted", tool, decision.Err)
		}
	}
}

func TestLocalOperatorAuthorityDoesNotBypassNameLists(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		set  func(*policy.Config)
		err  error
	}{
		{"allowlist", func(c *policy.Config) { c.Allowlist = []string{readTool} }, policy.ErrNotAllowlisted},
		{"denylist", func(c *policy.Config) { c.Denylist = []string{writeTool} }, policy.ErrToolDenied},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := baseConfig()
			cfg.LocalOperatorAuthority = true
			cfg.EnableWrite = true
			tc.set(&cfg)
			p := mustNew(t, cfg, nil)

			decision := p.Decide(context.Background(), writeTool)
			if decision.Allowed {
				t.Fatal("local operator authority bypassed a name list")
			}
			if !errors.Is(decision.Err, tc.err) {
				t.Fatalf("Err = %v, want %v", decision.Err, tc.err)
			}
		})
	}
}

// The mirror of the rule: a granted scope alone must not open the tier either.
func TestGrantedScopeAloneNeverSuffices(t *testing.T) {
	t.Parallel()

	scopes := grantingScopes{scopes: []policy.Scope{policy.ScopeWrite, policy.ScopeDestructive}}
	p := mustNew(t, baseConfig(), scopes)

	for _, tool := range []string{writeTool, destructiveTool} {
		decision := p.Decide(context.Background(), tool)
		if decision.Allowed {
			t.Errorf("%s allowed with a granted scope but no operator enablement", tool)
		}
		if !errors.Is(decision.Err, policy.ErrTierNotEnabled) {
			t.Errorf("%s Err = %v, want ErrTierNotEnabled", tool, decision.Err)
		}
	}
}

func TestIntersectionOfEnablementAndScopeAllowsTheTool(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.EnableWrite = true
	cfg.EnableDestructive = true
	scopes := grantingScopes{scopes: []policy.Scope{policy.ScopeWrite, policy.ScopeDestructive}}
	p := mustNew(t, cfg, scopes)

	write := p.Decide(context.Background(), writeTool)
	if !write.Allowed {
		t.Fatalf("add_weigh_in denied: %v (%s)", write.Err, write.Reason)
	}
	if write.RequiresConfirmation {
		t.Error("a write tool must not require confirmation")
	}

	destructive := p.Decide(context.Background(), destructiveTool)
	if !destructive.Allowed {
		t.Fatalf("delete_workout denied: %v (%s)", destructive.Err, destructive.Reason)
	}
	if !destructive.RequiresConfirmation {
		t.Error("an allowed destructive tool must still require confirmation")
	}
}

// The write scope must not carry the destructive tier, and the reverse.
func TestScopesDoNotImplyEachOther(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.EnableWrite = true
	cfg.EnableDestructive = true

	writeOnly := mustNew(t, cfg, grantingScopes{scopes: []policy.Scope{policy.ScopeWrite}})
	if decision := writeOnly.Decide(context.Background(), destructiveTool); decision.Allowed {
		t.Error("the write scope must not authorize a destructive tool")
	}

	destructiveOnly := mustNew(t, cfg, grantingScopes{scopes: []policy.Scope{policy.ScopeDestructive}})
	if decision := destructiveOnly.Decide(context.Background(), writeTool); decision.Allowed {
		t.Error("the destructive scope must not authorize a write tool")
	}
}

func TestScopeLookupFailureFailsClosed(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.EnableWrite = true
	scopes := grantingScopes{err: errors.New("token store unavailable")}
	p := mustNew(t, cfg, scopes)

	decision := p.Decide(context.Background(), writeTool)
	if decision.Allowed {
		t.Fatal("a scope lookup failure must fail closed")
	}
	if !errors.Is(decision.Err, policy.ErrScopeLookupFailed) {
		t.Fatalf("Err = %v, want ErrScopeLookupFailed", decision.Err)
	}
	// The refusal must not echo the underlying error text, which may carry
	// storage detail the caller has no business seeing.
	if strings.Contains(decision.Reason, "token store unavailable") {
		t.Fatalf("Reason %q echoes the underlying error text", decision.Reason)
	}
}

// A read-only tool never consults the scope source, so a broken source must not
// take the read tier down with it.
func TestScopeLookupFailureDoesNotAffectReadOnlyTools(t *testing.T) {
	t.Parallel()

	p := mustNew(t, baseConfig(), grantingScopes{err: errors.New("token store unavailable")})

	if decision := p.Decide(context.Background(), readTool); !decision.Allowed {
		t.Fatalf("read-only tool denied by an unrelated scope failure: %v", decision.Err)
	}
}

func TestNilScopeSourceGrantsNothing(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.EnableWrite = true
	p := mustNew(t, cfg, nil)

	decision := p.Decide(context.Background(), writeTool)
	if decision.Allowed {
		t.Fatal("a nil scope source must grant nothing rather than everything")
	}
	if !errors.Is(decision.Err, policy.ErrScopeNotGranted) {
		t.Fatalf("Err = %v, want ErrScopeNotGranted", decision.Err)
	}
}

func TestUnknownToolIsRefused(t *testing.T) {
	t.Parallel()

	p := mustNew(t, baseConfig(), policy.NoScopes{})

	decision := p.Decide(context.Background(), "get_menstrual_calendar_data")
	if decision.Allowed {
		t.Fatal("a tool with no tier must be refused")
	}
	if !errors.Is(decision.Err, policy.ErrUnknownTool) {
		t.Fatalf("Err = %v, want ErrUnknownTool", decision.Err)
	}
}

// A refusal reason is shown to the caller, so it must never echo a tool name
// that is itself sensitive. The tier and the cause are enough.
func TestRefusalReasonDoesNotEchoTheToolName(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.EnableDestructive = true
	p := mustNew(t, cfg, policy.NoScopes{})

	decision := p.Decide(context.Background(), destructiveTool)
	if decision.Reason == "" {
		t.Fatal("a refusal must carry a reason")
	}
	if strings.Contains(decision.Reason, destructiveTool) {
		t.Fatalf("Reason %q echoes the tool name", decision.Reason)
	}
	if !strings.Contains(decision.Reason, "destructive") {
		t.Fatalf("Reason %q does not name the tier it refused", decision.Reason)
	}
}

func TestRemoteModeDefaultsToReadOnly(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.Mode = policy.ModeRemote
	p := mustNew(t, cfg, grantingScopes{
		scopes: []policy.Scope{policy.ScopeWrite, policy.ScopeDestructive},
	})

	if decision := p.Decide(context.Background(), readTool); !decision.Allowed {
		t.Fatalf("remote read-only tool denied: %v", decision.Err)
	}
	for _, tool := range []string{writeTool, destructiveTool} {
		decision := p.Decide(context.Background(), tool)
		if decision.Allowed {
			t.Errorf("%s allowed in a remote deployment that did not enable its tier", tool)
		}
		if !errors.Is(decision.Err, policy.ErrTierNotEnabled) {
			t.Errorf("%s Err = %v, want ErrTierNotEnabled", tool, decision.Err)
		}
	}
}

func TestDenylistRefusesEvenAReadOnlyTool(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.Denylist = []string{readTool2}
	p := mustNew(t, cfg, policy.NoScopes{})

	decision := p.Decide(context.Background(), readTool2)
	if decision.Allowed {
		t.Fatal("a denylisted tool must be refused")
	}
	if !errors.Is(decision.Err, policy.ErrToolDenied) {
		t.Fatalf("Err = %v, want ErrToolDenied", decision.Err)
	}
	if decision := p.Decide(context.Background(), readTool); !decision.Allowed {
		t.Fatal("the denylist must not affect an unlisted tool")
	}
}

// The denylist is evaluated before the tier gate, so a fully enabled and scoped
// write tool is still refused.
func TestDenylistBeatsEnablementAndScope(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.EnableWrite = true
	cfg.Denylist = []string{writeTool}
	p := mustNew(t, cfg, grantingScopes{scopes: []policy.Scope{policy.ScopeWrite}})

	decision := p.Decide(context.Background(), writeTool)
	if decision.Allowed {
		t.Fatal("the denylist must beat enablement and scope")
	}
	if !errors.Is(decision.Err, policy.ErrToolDenied) {
		t.Fatalf("Err = %v, want ErrToolDenied", decision.Err)
	}
}

func TestAllowlistRestrictsWithoutBypassingTiers(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.Allowlist = []string{readTool, writeTool, destructiveTool}
	p := mustNew(t, cfg, policy.NoScopes{})

	if decision := p.Decide(context.Background(), readTool); !decision.Allowed {
		t.Fatalf("an allowlisted read-only tool must be allowed: %v", decision.Err)
	}

	decision := p.Decide(context.Background(), readTool2)
	if decision.Allowed {
		t.Fatal("a tool absent from a non-empty allowlist must be refused")
	}
	if !errors.Is(decision.Err, policy.ErrNotAllowlisted) {
		t.Fatalf("Err = %v, want ErrNotAllowlisted", decision.Err)
	}

	// The allowlist grants nothing: the write tier is still shut.
	write := p.Decide(context.Background(), writeTool)
	if write.Allowed {
		t.Fatal("the allowlist must be intersected with the tiers, not bypass them")
	}
	if !errors.Is(write.Err, policy.ErrTierNotEnabled) {
		t.Fatalf("Err = %v, want ErrTierNotEnabled", write.Err)
	}
}

func TestEmptyAllowlistMeansNoRestriction(t *testing.T) {
	t.Parallel()

	p := mustNew(t, baseConfig(), policy.NoScopes{})

	for _, tool := range []string{readTool, readTool2} {
		if decision := p.Decide(context.Background(), tool); !decision.Allowed {
			t.Errorf("%s refused although no allowlist is configured: %v", tool, decision.Err)
		}
	}
}
