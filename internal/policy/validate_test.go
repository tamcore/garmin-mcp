package policy_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/policy"
)

func TestNewRejectsAToolNamedInTwoTiers(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.WriteTools = append(cfg.WriteTools, readTool)

	_, err := policy.New(cfg, policy.NoScopes{})
	if !errors.Is(err, policy.ErrDuplicateTool) {
		t.Fatalf("New error = %v, want ErrDuplicateTool", err)
	}
}

func TestNewRejectsAnEmptyToolName(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.ReadOnlyTools = append(cfg.ReadOnlyTools, "  ")

	_, err := policy.New(cfg, policy.NoScopes{})
	if !errors.Is(err, policy.ErrInvalidToolName) {
		t.Fatalf("New error = %v, want ErrInvalidToolName", err)
	}
}

func TestNewRejectsAnUnknownMode(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.Mode = policy.Mode(0)

	_, err := policy.New(cfg, policy.NoScopes{})
	if !errors.Is(err, policy.ErrInvalidMode) {
		t.Fatalf("New error = %v, want ErrInvalidMode", err)
	}
}

// A typo in a tier list must fail at start-up, not at the first call.
func TestValidateRejectsATypoInATierList(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.WriteTools = []string{"add_weigh_inn"}

	p, err := policy.New(cfg, policy.NoScopes{})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	err = p.Validate(registered)
	if !errors.Is(err, policy.ErrToolNotRegistered) {
		t.Fatalf("Validate error = %v, want ErrToolNotRegistered", err)
	}
	if !strings.Contains(err.Error(), "add_weigh_inn") {
		t.Fatalf("Validate error %q does not name the offending entry", err)
	}
}

func TestValidateRejectsATypoInTheAllowlistOrDenylist(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		set  func(*policy.Config)
	}{
		{"allowlist", func(c *policy.Config) { c.Allowlist = []string{"get_activitiez"} }},
		{"denylist", func(c *policy.Config) { c.Denylist = []string{"get_activitiez"} }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := baseConfig()
			tc.set(&cfg)

			p, err := policy.New(cfg, policy.NoScopes{})
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			if err := p.Validate(registered); !errors.Is(err, policy.ErrToolNotRegistered) {
				t.Fatalf("Validate error = %v, want ErrToolNotRegistered", err)
			}
		})
	}
}

// The reverse direction matters just as much: a registered tool with no tier
// would otherwise be silently unreachable.
func TestValidateRejectsARegisteredToolWithNoTier(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.ReadOnlyTools = []string{readTool}

	p, err := policy.New(cfg, policy.NoScopes{})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	err = p.Validate(registered)
	if !errors.Is(err, policy.ErrToolWithoutTier) {
		t.Fatalf("Validate error = %v, want ErrToolWithoutTier", err)
	}
	if !strings.Contains(err.Error(), readTool2) {
		t.Fatalf("Validate error %q does not name the untiered tool", err)
	}
}

func TestValidateAcceptsAMatchingSet(t *testing.T) {
	t.Parallel()

	p, err := policy.New(baseConfig(), policy.NoScopes{})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := p.Validate(registered); err != nil {
		t.Fatalf("Validate returned error for a matching set: %v", err)
	}
}

func TestTierOfReportsTheConfiguredTier(t *testing.T) {
	t.Parallel()

	p := mustNew(t, baseConfig(), policy.NoScopes{})

	cases := map[string]policy.Tier{
		readTool:        policy.TierReadOnly,
		writeTool:       policy.TierWrite,
		destructiveTool: policy.TierDestructive,
	}
	for tool, want := range cases {
		got, ok := p.TierOf(tool)
		if !ok || got != want {
			t.Errorf("TierOf(%q) = %v, %v; want %v, true", tool, got, ok, want)
		}
	}
	if _, ok := p.TierOf("no_such_tool"); ok {
		t.Error("TierOf must report false for an unknown tool")
	}
}

// Config is copied at construction, so mutating the caller's slices afterwards
// cannot change an already-built policy.
func TestPolicyDoesNotAliasTheCallerConfiguration(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	p := mustNew(t, cfg, policy.NoScopes{})

	cfg.Denylist = append(cfg.Denylist, readTool)
	cfg.ReadOnlyTools[0] = "mutated"

	if decision := p.Decide(context.Background(), readTool); !decision.Allowed {
		t.Fatalf("mutating the caller's Config changed an existing policy: %v", decision.Err)
	}
}
