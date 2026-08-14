package identity

import (
	"context"
	"fmt"
)

// A Resolver resolves the principal for one incoming request.
//
// Resolve takes a context and nothing else. That signature is the enforcement
// point for the rule that a tool argument must never select or influence the
// principal: there is no parameter a decoded argument could travel through.
//
// The interface lives here because both the stdio resolver and the future
// bearer-token resolver implement it, and the server depends on the abstraction
// rather than on either one.
type Resolver interface {
	// Resolve returns the principal for the request carried by ctx, or an error
	// wrapping ErrNoPrincipal when none can be established.
	Resolve(ctx context.Context) (Principal, error)
}

// StdioConfig is the process-local account configuration for the stdio
// transport.
//
// PrincipalIDs must name exactly one account. A longer list is an ambiguous
// configuration and is refused at start-up rather than resolved by picking a
// winner; an empty list means no principal is configured at all.
type StdioConfig struct {
	PrincipalIDs []string
}

// A StdioResolver returns the single principal bound at start-up.
//
// It holds no mutable state after construction, so it is safe for concurrent use
// and cannot be steered by a request. Local stdio has exactly one account,
// protected by OS process and file permissions, and this type is the only place
// that fact is encoded.
type StdioResolver struct {
	principal Principal
}

// NewStdioResolver validates cfg and binds its single principal.
//
// It returns ErrNoPrincipal when cfg names no account, ErrAmbiguousPrincipal
// when it names more than one entry — duplicates included, because a repeated
// entry is still an operator mistake rather than an obviously single-account
// configuration — and the validation errors of NewPrincipal for the entry
// itself.
func NewStdioResolver(cfg StdioConfig) (*StdioResolver, error) {
	switch len(cfg.PrincipalIDs) {
	case 0:
		return nil, fmt.Errorf("stdio configuration names no account: %w", ErrNoPrincipal)
	case 1:
	default:
		return nil, fmt.Errorf("stdio configuration names %d accounts, want exactly 1: %w",
			len(cfg.PrincipalIDs), ErrAmbiguousPrincipal)
	}

	principal, err := NewPrincipal(cfg.PrincipalIDs[0])
	if err != nil {
		return nil, fmt.Errorf("stdio principal: %w", err)
	}
	return &StdioResolver{principal: principal}, nil
}

// Resolve returns the principal bound at start-up.
//
// ctx is accepted to satisfy Resolver and is deliberately not read: for stdio
// the principal is a property of the process, not of the request, so nothing a
// caller can put on the context or in a tool argument changes the answer.
func (r *StdioResolver) Resolve(_ context.Context) (Principal, error) {
	if r == nil || !r.principal.IsValid() {
		return Principal{}, fmt.Errorf("stdio resolver holds no principal: %w", ErrNoPrincipal)
	}
	return r.principal, nil
}
