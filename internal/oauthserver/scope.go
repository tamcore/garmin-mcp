package oauthserver

import (
	"fmt"
	"slices"
	"strings"
)

// Bounds on a scope request. They exist so a hostile client cannot push an
// unbounded string into a consent record, a log line or a token row.
const (
	// MaxScopeCount bounds how many scope tokens one request may name.
	MaxScopeCount = 32
	// MaxScopeLen bounds the length of one scope token.
	MaxScopeLen = 128
)

// A Scope is one OAuth scope token, for example "garmin.health.read". The
// tool-to-scope map lives in internal/policy; this package only carries scopes
// through the protocol and never interprets their meaning.
type Scope string

// A ScopeSet is an immutable, deduplicated, lexically ordered set of scopes.
//
// Ordering is normalized so "b a" and "a b" are the same set, which is what makes
// a consent record comparable and a granted-scope check exact. The zero ScopeSet
// is the empty set and is valid.
type ScopeSet struct {
	scopes []Scope
}

// ParseScopeSet parses the space-delimited scope syntax of RFC 6749 §3.3. Empty
// or whitespace-only input yields the empty set, which is not an error: a request
// that names no scope is a request for no scope.
//
// A repeated scope token is folded rather than refused, because scope is a set by
// definition and repeating a member cannot widen it. Every other deviation is an
// error wrapping ErrInvalidScope.
func ParseScopeSet(raw string) (ScopeSet, error) {
	if len(raw) > MaxScopeCount*(MaxScopeLen+1) {
		return ScopeSet{}, fmt.Errorf("scope parameter is %d bytes: %w", len(raw), ErrInvalidScope)
	}
	fields := strings.Split(raw, " ")
	tokens := make([]Scope, 0, len(fields))
	for _, field := range fields {
		if field == "" {
			continue
		}
		tokens = append(tokens, Scope(field))
	}
	return NewScopeSet(tokens...)
}

// NewScopeSet validates, deduplicates and orders the given scopes.
func NewScopeSet(scopes ...Scope) (ScopeSet, error) {
	if len(scopes) > MaxScopeCount {
		return ScopeSet{}, fmt.Errorf("request names %d scopes, the limit is %d: %w",
			len(scopes), MaxScopeCount, ErrInvalidScope)
	}
	normalized := make([]Scope, 0, len(scopes))
	for _, scope := range scopes {
		if err := validateScope(scope); err != nil {
			return ScopeSet{}, err
		}
		if !slices.Contains(normalized, scope) {
			normalized = append(normalized, scope)
		}
	}
	slices.Sort(normalized)
	return ScopeSet{scopes: normalized}, nil
}

// validateScope applies the scope-token grammar of RFC 6749 §3.3: one or more
// characters drawn from %x21 and %x23-5B and %x5D-7E, which excludes the space,
// the double quote, the backslash, every control character and everything
// non-ASCII.
func validateScope(scope Scope) error {
	if scope == "" {
		return fmt.Errorf("scope token is empty: %w", ErrInvalidScope)
	}
	if len(scope) > MaxScopeLen {
		return fmt.Errorf("scope token is %d bytes, the limit is %d: %w",
			len(scope), MaxScopeLen, ErrInvalidScope)
	}
	for i := range len(scope) {
		if !isScopeChar(scope[i]) {
			return fmt.Errorf("scope token carries a disallowed byte at offset %d: %w",
				i, ErrInvalidScope)
		}
	}
	return nil
}

func isScopeChar(b byte) bool {
	return b == 0x21 || (b >= 0x23 && b <= 0x5B) || (b >= 0x5D && b <= 0x7E)
}

// String renders the set in the space-delimited wire syntax, in normalized order.
func (s ScopeSet) String() string {
	return strings.Join(s.Strings(), " ")
}

// Slice returns a copy of the members, so a caller cannot mutate the set.
func (s ScopeSet) Slice() []Scope { return slices.Clone(s.scopes) }

// Strings returns the members as plain strings, for a caller that has to hand
// them to a metadata document or an SDK option.
func (s ScopeSet) Strings() []string {
	out := make([]string, len(s.scopes))
	for i, scope := range s.scopes {
		out[i] = string(scope)
	}
	return out
}

// Len reports the number of members.
func (s ScopeSet) Len() int { return len(s.scopes) }

// IsEmpty reports whether the set has no members.
func (s ScopeSet) IsEmpty() bool { return len(s.scopes) == 0 }

// Contains reports whether scope is a member.
func (s ScopeSet) Contains(scope Scope) bool { return slices.Contains(s.scopes, scope) }

// IsSubsetOf reports whether every member of s is a member of other. It is the
// check that keeps a refresh or a re-authorization from widening a grant.
func (s ScopeSet) IsSubsetOf(other ScopeSet) bool {
	for _, scope := range s.scopes {
		if !other.Contains(scope) {
			return false
		}
	}
	return true
}

// Equal reports exact set equality, independent of the order the sets were built
// in.
func (s ScopeSet) Equal(other ScopeSet) bool { return slices.Equal(s.scopes, other.scopes) }
