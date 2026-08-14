package identity

import "errors"

// Sentinel errors this package returns. Every error returned by this package
// wraps exactly one of them, so callers can branch with errors.Is.
var (
	// ErrNoPrincipal reports that no principal was resolved. It is returned both
	// when a request context carries no principal and when process-local
	// configuration names no account at all. There is deliberately no default
	// and no fallback: an unresolved principal is a refusal, never a guess.
	ErrNoPrincipal = errors.New("identity: no principal resolved")

	// ErrAmbiguousPrincipal reports that process-local configuration names more
	// than one account. The stdio transport binds exactly one principal, so an
	// ambiguous configuration is refused at start-up rather than resolved by
	// picking a winner.
	ErrAmbiguousPrincipal = errors.New("identity: ambiguous multi-account configuration")

	// ErrEmailNotAPrincipal reports that an identifier looks like an email
	// address. A principal is an opaque internal identifier; an email is login
	// and display material, is personal data, and must never key isolation.
	ErrEmailNotAPrincipal = errors.New("identity: an email address is not a principal identifier")

	// ErrInvalidPrincipalID reports that an identifier is empty, padded,
	// over-long, or carries a control character.
	ErrInvalidPrincipalID = errors.New("identity: invalid principal identifier")
)
