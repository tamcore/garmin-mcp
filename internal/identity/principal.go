// Package identity models the internal principal and resolves it for a request.
//
// A principal is the single key on which every per-user boundary in this server
// is isolated: the Garmin client, the token set, the cookie jar, the rate-limit
// budget, and the cache entry. It is an opaque internal identifier, never an
// email address and never anything derived from a tool argument.
//
// Two rules are structural rather than advisory:
//
//   - The zero Principal is never valid. A handler that reads a principal it was
//     not given gets an error, not a usable zero value.
//   - Resolver.Resolve takes only a context.Context. There is no parameter
//     through which a tool argument could select or influence the principal.
package identity

import (
	"fmt"
	"strings"
	"unicode"
)

// MaxPrincipalIDLen bounds a principal identifier. The internal identifier is a
// UUID today, so this is generous; the bound exists so a hostile or corrupt
// configuration cannot push an unbounded string into a log line or a map key.
const MaxPrincipalIDLen = 256

// A Principal identifies one Garmin-linked account inside this server.
//
// Principal is a comparable value type, so it is usable directly as a map key
// for per-principal state. Its identifier is unexported and reachable only
// through ID, so a Principal cannot be constructed by struct literal from
// another package: NewPrincipal, and therefore its validation, is the only way
// in. The zero value is invalid and reports so through IsValid.
type Principal struct {
	id string
}

// NewPrincipal validates id and returns the Principal it names.
//
// It returns ErrEmailNotAPrincipal if id looks like an email address, and
// ErrInvalidPrincipalID if id is empty, padded with whitespace, longer than
// MaxPrincipalIDLen, or carries a control character.
func NewPrincipal(id string) (Principal, error) {
	if err := validatePrincipalID(id); err != nil {
		return Principal{}, err
	}
	return Principal{id: id}, nil
}

// ID returns the opaque internal identifier, or the empty string for the zero
// Principal.
func (p Principal) ID() string { return p.id }

// IsValid reports whether p was produced by NewPrincipal. The zero Principal
// reports false, so a caller can never mistake an unresolved principal for a
// resolved one.
func (p Principal) IsValid() bool { return p.id != "" }

// String renders the pseudonymous identifier. The identifier is loggable by
// design — the redaction rules name it as permitted — and it carries no account
// name, because NewPrincipal refuses an email.
func (p Principal) String() string { return p.id }

// validatePrincipalID applies the ordered checks. The email check runs before
// the character checks so that a bare "@" is reported as an email rather than as
// a generic malformed identifier: the more specific diagnosis is the useful one.
func validatePrincipalID(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("principal identifier is empty: %w", ErrInvalidPrincipalID)
	}
	if strings.Contains(id, "@") {
		return fmt.Errorf("principal identifier contains %q: %w", "@", ErrEmailNotAPrincipal)
	}
	if len(id) > MaxPrincipalIDLen {
		return fmt.Errorf("principal identifier is longer than %d bytes: %w",
			MaxPrincipalIDLen, ErrInvalidPrincipalID)
	}
	if idx := strings.IndexFunc(id, isDisallowedRune); idx >= 0 {
		return fmt.Errorf("principal identifier carries a disallowed rune at offset %d: %w",
			idx, ErrInvalidPrincipalID)
	}
	return nil
}

// isDisallowedRune rejects control characters and any space, which also covers
// the padded-identifier case. A principal identifier ends up in log lines and
// map keys, and neither tolerates an embedded newline or an invisible separator.
func isDisallowedRune(r rune) bool {
	return unicode.IsControl(r) || unicode.IsSpace(r) || r == unicode.ReplacementChar
}
