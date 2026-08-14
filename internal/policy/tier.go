// Package policy decides whether one tool call may proceed.
//
// The model has three tiers — read-only, write, and destructive — and one rule
// that matters more than the rest: for the write and destructive tiers the
// effective gate is the *intersection* of operator enablement and granted OAuth
// scope. Operator enablement alone is never sufficient, and a granted scope
// alone is never sufficient either.
//
// No scope is issued anywhere in this repository yet. ScopeSource is the seam the
// M2 OAuth work will satisfy, and its default implementation, NoScopes, grants
// nothing. Every write and destructive tool is therefore refused today, however
// the operator configures enablement. That is the intended state, not a gap.
//
// Allowlists and denylists are intersected with the tiers rather than bypassing
// them: an allowlist can only narrow what the tiers already permit.
package policy

// A Tier classifies the effect a tool has on the user's Garmin account.
//
// The zero value is deliberately invalid, so a tool cannot end up in a tier by
// forgetting to declare one.
type Tier int

// The tiers, in increasing order of consequence.
const (
	// TierReadOnly names a tool that only reads. Read-only tools are always
	// registered and are not gated on a tier scope.
	TierReadOnly Tier = iota + 1

	// TierWrite names a tool that adds or updates data.
	TierWrite

	// TierDestructive names a tool that deletes data, pushes to a device,
	// uploads, schedules, or mutates a health record.
	TierDestructive
)

// String returns the coarse, stable label used in logs and refusal reasons. An
// unrecognized tier renders as "unknown" rather than as a number, so a log line
// never leaks an internal enum value.
func (t Tier) String() string {
	switch t {
	case TierReadOnly:
		return "read-only"
	case TierWrite:
		return "write"
	case TierDestructive:
		return "destructive"
	default:
		return "unknown"
	}
}

// IsValid reports whether t is one of the three declared tiers.
func (t Tier) IsValid() bool {
	return t == TierReadOnly || t == TierWrite || t == TierDestructive
}

// A Scope is an OAuth scope string granted to the caller of a request.
type Scope string

// The tier scopes. Per-domain read scopes — profile, activities and location,
// health, devices, nutrition, women's health — are the tool-to-scope map owned
// by the tool registration slice; this package gates at tier granularity only.
const (
	// ScopeWrite authorizes the write tier.
	ScopeWrite Scope = "garmin:write"

	// ScopeDestructive authorizes the destructive tier. It does not imply
	// ScopeWrite, and ScopeWrite does not imply it.
	ScopeDestructive Scope = "garmin:destructive"
)

// RequiredScope returns the scope a caller must hold for this tier, and whether
// the tier is scope-gated at all.
//
// TierReadOnly reports false: the read tier is not gated on a tier scope, because
// a deployment that granted no scope at all must still serve reads. Per-domain
// read scopes are enforced by the M2 bearer-token path, not here.
func (t Tier) RequiredScope() (Scope, bool) {
	switch t {
	case TierWrite:
		return ScopeWrite, true
	case TierDestructive:
		return ScopeDestructive, true
	default:
		return "", false
	}
}

// RequiresConfirmation reports whether a tool in this tier must obtain client
// confirmation before it runs. Only the destructive tier does.
func (t Tier) RequiresConfirmation() bool { return t == TierDestructive }

// A Mode is the deployment shape. It exists because the read-only default for
// remote deployments is a documented property of the deployment, and a policy
// that cannot say which shape it is in cannot claim that default.
type Mode int

// The deployment modes.
const (
	// ModeLocal is single-user stdio, bound to one process-local account.
	ModeLocal Mode = iota + 1

	// ModeRemote is the multi-user Streamable HTTP deployment. It defaults to
	// read-only: both higher tiers start disabled and stay disabled until the
	// operator enables them *and* the caller presents the matching scope.
	ModeRemote
)

// String returns the coarse label used in logs.
func (m Mode) String() string {
	switch m {
	case ModeLocal:
		return "local"
	case ModeRemote:
		return "remote"
	default:
		return "unknown"
	}
}

// IsValid reports whether m is a declared mode.
func (m Mode) IsValid() bool { return m == ModeLocal || m == ModeRemote }
