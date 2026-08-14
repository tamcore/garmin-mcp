package ratelimit

import "errors"

// Sentinel errors New returns. Both are start-up failures: a limiter that cannot
// be configured coherently must not be built at all.
var (
	// ErrInvalidBudget reports a non-positive rate or burst. A zero budget is
	// rejected rather than interpreted, because "block everything" and "no limit"
	// are both plausible readings of it and guessing wrong is a security or an
	// availability bug.
	ErrInvalidBudget = errors.New("ratelimit: invalid budget")

	// ErrUnboundedPrincipals reports a non-positive Config.MaxPrincipals. An
	// unbounded principal table is a memory leak a remote caller can drive by
	// presenting a stream of distinct principals.
	ErrUnboundedPrincipals = errors.New("ratelimit: principal table must be bounded")
)
