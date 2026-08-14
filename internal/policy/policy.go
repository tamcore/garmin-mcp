package policy

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// A ScopeSource reports the OAuth scopes granted to the caller of a request.
//
// This is the seam the M2 OAuth work satisfies: the remote implementation will
// read the scopes from the verified bearer-token context and nothing else. The
// interface lives here, with its consumer, rather than in a shared interface
// package.
//
// An implementation must fail rather than guess. Returning an error makes Decide
// fail closed, which is the required behavior for an unknown grant.
type ScopeSource interface {
	GrantedScopes(ctx context.Context) ([]Scope, error)
}

// NoScopes is the default ScopeSource and grants nothing.
//
// It is the honest representation of the current state: this repository issues no
// OAuth scope anywhere, so every write and destructive tool is refused. A nil
// ScopeSource behaves identically.
type NoScopes struct{}

// GrantedScopes returns no scopes and no error.
func (NoScopes) GrantedScopes(context.Context) ([]Scope, error) { return nil, nil }

// Config is the operator's tool policy. It is copied by New, so a later mutation
// of the caller's slices cannot change a built Policy.
//
// The three tier lists are explicit name lists, exactly as the house servers do
// it, and Validate checks them against the actually-registered tool set so a typo
// fails at start-up instead of silently never matching.
type Config struct {
	// Mode is the deployment shape and must be set explicitly.
	Mode Mode

	// ReadOnlyTools, WriteTools and DestructiveTools name every tool in each
	// tier. A name may appear in exactly one of them.
	ReadOnlyTools    []string
	WriteTools       []string
	DestructiveTools []string

	// EnableWrite and EnableDestructive are the operator half of the gate. They
	// are necessary and never sufficient: the caller must also hold the tier's
	// scope. Both default to false, which is what makes a remote deployment
	// read-only until the operator says otherwise.
	EnableWrite       bool
	EnableDestructive bool

	// Allowlist, when non-empty, restricts calls to the names it contains. It is
	// intersected with the tiers: an allowlisted write tool still needs
	// enablement and scope.
	Allowlist []string

	// Denylist refuses the names it contains before any tier check runs, so it
	// beats enablement and scope.
	Denylist []string
}

// A Decision is the outcome of one policy evaluation.
//
// Err carries the machine-readable cause and wraps exactly one package sentinel,
// so a caller branches with errors.Is. Reason carries the caller-facing text and
// deliberately names only the tier and the cause — never the tool name, which can
// itself be sensitive.
type Decision struct {
	Tool                 string
	Tier                 Tier
	Allowed              bool
	RequiresConfirmation bool
	Reason               string
	Err                  error
}

// A Policy evaluates tool calls against the operator configuration and the
// caller's granted scopes. It is immutable after New and safe for concurrent use.
type Policy struct {
	mode      Mode
	tiers     map[string]Tier
	enabled   map[Tier]bool
	allowlist map[string]struct{}
	denylist  map[string]struct{}
	// configured preserves every name the operator wrote, in order, so Validate
	// can report the first offender deterministically.
	configured []string
	scopes     ScopeSource
}

// New validates cfg and returns the Policy it describes.
//
// scopes may be nil, which is treated as NoScopes. New checks only what it can
// see on its own: the mode, the shape of every name, and that no name appears in
// two tiers. Checking the names against reality is Validate's job, because the
// registered set does not exist until the tools are registered.
func New(cfg Config, scopes ScopeSource) (*Policy, error) {
	if !cfg.Mode.IsValid() {
		return nil, fmt.Errorf("mode %d: %w", int(cfg.Mode), ErrInvalidMode)
	}
	if scopes == nil {
		scopes = NoScopes{}
	}

	tiers, configured, err := buildTiers(cfg)
	if err != nil {
		return nil, err
	}
	allowlist, err := nameSet(cfg.Allowlist, "allowlist")
	if err != nil {
		return nil, err
	}
	denylist, err := nameSet(cfg.Denylist, "denylist")
	if err != nil {
		return nil, err
	}

	return &Policy{
		mode:      cfg.Mode,
		tiers:     tiers,
		enabled:   map[Tier]bool{TierWrite: cfg.EnableWrite, TierDestructive: cfg.EnableDestructive},
		allowlist: allowlist,
		denylist:  denylist,
		configured: slices.Concat(configured,
			slices.Clone(cfg.Allowlist), slices.Clone(cfg.Denylist)),
		scopes: scopes,
	}, nil
}

// buildTiers indexes the three tier lists, rejecting a malformed name and a name
// that appears in two tiers. It also returns the names in configuration order for
// Validate's deterministic reporting.
func buildTiers(cfg Config) (map[string]Tier, []string, error) {
	lists := []struct {
		tier  Tier
		names []string
	}{
		{TierReadOnly, cfg.ReadOnlyTools},
		{TierWrite, cfg.WriteTools},
		{TierDestructive, cfg.DestructiveTools},
	}

	tiers := make(map[string]Tier)
	ordered := make([]string, 0, len(cfg.ReadOnlyTools)+len(cfg.WriteTools)+len(cfg.DestructiveTools))
	for _, list := range lists {
		for _, name := range list.names {
			if err := validateToolName(name); err != nil {
				return nil, nil, fmt.Errorf("%s tier: %w", list.tier, err)
			}
			if existing, ok := tiers[name]; ok {
				return nil, nil, fmt.Errorf("tool %q is in both the %s and the %s tier: %w",
					name, existing, list.tier, ErrDuplicateTool)
			}
			tiers[name] = list.tier
			ordered = append(ordered, name)
		}
	}
	return tiers, ordered, nil
}

// Mode reports the deployment shape this policy was built for.
func (p *Policy) Mode() Mode { return p.mode }

// TierOf reports the configured tier of name, and whether it has one.
func (p *Policy) TierOf(name string) (Tier, bool) {
	tier, ok := p.tiers[name]
	return tier, ok
}

// Validate checks every configured name against the actually-registered tool set
// and reports the first inconsistency.
//
// Two directions are checked, and both matter. A configured name that is not
// registered is a typo, and it must fail at start-up rather than become a rule
// that never fires. A registered name that is in no tier would be permanently
// unreachable, which is equally a configuration bug.
func (p *Policy) Validate(registered []string) error {
	known := make(map[string]struct{}, len(registered))
	for _, name := range registered {
		known[name] = struct{}{}
	}

	for _, name := range p.configured {
		if _, ok := known[name]; !ok {
			return fmt.Errorf("configured tool %q is not among the %d registered tools: %w",
				name, len(registered), ErrToolNotRegistered)
		}
	}
	for _, name := range registered {
		if _, ok := p.tiers[name]; !ok {
			return fmt.Errorf("registered tool %q is in no tier list: %w", name, ErrToolWithoutTier)
		}
	}
	return nil
}

// Decide evaluates one tool call.
//
// The evaluation order is deliberate: the denylist first, so an operator's
// refusal cannot be reopened by anything downstream; then the allowlist, which
// narrows but never widens; then the tier gate, which is the intersection of
// operator enablement and granted scope.
func (p *Policy) Decide(ctx context.Context, tool string) Decision {
	tier, ok := p.tiers[tool]
	if !ok {
		return refuse(tool, Tier(0), ErrUnknownTool, "the tool is not registered with a policy tier")
	}
	if decision, refused := p.checkLists(tool, tier); refused {
		return decision
	}
	if decision, refused := p.checkTier(ctx, tool, tier); refused {
		return decision
	}
	return Decision{
		Tool:                 tool,
		Tier:                 tier,
		Allowed:              true,
		RequiresConfirmation: tier.RequiresConfirmation(),
	}
}

// checkLists applies the denylist and then the allowlist. It reports whether the
// call was refused.
func (p *Policy) checkLists(tool string, tier Tier) (Decision, bool) {
	if _, denied := p.denylist[tool]; denied {
		return refuse(tool, tier, ErrToolDenied, "the operator denylisted this tool"), true
	}
	if len(p.allowlist) > 0 {
		if _, allowed := p.allowlist[tool]; !allowed {
			return refuse(tool, tier, ErrNotAllowlisted,
				"the operator allowlist does not include this tool"), true
		}
	}
	return Decision{}, false
}

// checkTier applies the intersection gate. A read-only tool passes without
// consulting the scope source at all, so a broken source cannot take reads down.
func (p *Policy) checkTier(ctx context.Context, tool string, tier Tier) (Decision, bool) {
	scope, gated := tier.RequiredScope()
	if !gated {
		return Decision{}, false
	}
	if !p.enabled[tier] {
		return refuse(tool, tier, ErrTierNotEnabled,
			"the "+tier.String()+" tier is not enabled for this deployment"), true
	}

	granted, err := p.scopes.GrantedScopes(ctx)
	if err != nil {
		// The underlying text is not propagated: it comes from the token store
		// or the transport and may carry material the caller must not see.
		return refuse(tool, tier, ErrScopeLookupFailed,
			"the granted scopes for the "+tier.String()+" tier could not be determined"), true
	}
	if !slices.Contains(granted, scope) {
		return refuse(tool, tier, ErrScopeNotGranted,
			"the "+tier.String()+" tier requires an OAuth scope that was not granted"), true
	}
	return Decision{}, false
}

// refuse builds a denied Decision. Reason never contains the tool name, because
// a tool name can itself disclose a sensitive domain.
func refuse(tool string, tier Tier, cause error, reason string) Decision {
	return Decision{
		Tool:   tool,
		Tier:   tier,
		Reason: reason,
		Err:    fmt.Errorf("%s: %w", reason, cause),
	}
}

// nameSet builds a lookup set, validating every entry.
func nameSet(names []string, label string) (map[string]struct{}, error) {
	if len(names) == 0 {
		return nil, nil
	}
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		if err := validateToolName(name); err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		set[name] = struct{}{}
	}
	return set, nil
}

func validateToolName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("tool name is empty: %w", ErrInvalidToolName)
	}
	if name != strings.TrimSpace(name) {
		return fmt.Errorf("tool name %q is padded with whitespace: %w", name, ErrInvalidToolName)
	}
	return nil
}
