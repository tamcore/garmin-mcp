package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Column encodings shared by every table in the SQLite schema.
//
// Timestamps are stored as RFC 3339 in UTC. That makes string comparison equal to
// time comparison, so an expiry check is a plain SQL predicate and does not need a
// second round trip, and it keeps the database readable to an operator with a
// sqlite3 shell. A numeric epoch would be smaller and would lose both.
//
// Scope lists are stored space separated, which is the encoding OAuth already uses
// on the wire, so nothing has to be re-encoded on the way in or out.

// timeLayout is the one timestamp format the schema uses.
const timeLayout = time.RFC3339Nano

// maxScopeCount and maxScopeLength bound a scope list. An unbounded list is a way
// to push arbitrary text into a row that is later echoed to a client.
const (
	maxScopeCount  = 32
	maxScopeLength = 128
)

// formatTime renders t for storage, in UTC, so two timestamps written in different
// zones still compare correctly as strings.
func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// parseTime reads a stored timestamp.
func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(timeLayout, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("store: timestamp %q is unreadable: %w", value, ErrCorruptRecord)
	}
	return parsed.UTC(), nil
}

// Optional timestamp columns — revoked_at, consumed_at, disabled_at — are read as a
// sql.NullString and interpreted at the point of use, because what NULL means differs per
// column: an unrevoked consent, an unrotated refresh token, an enabled client. A shared
// "read an optional time" helper would invite a caller to treat those as one thing.

// nullableString renders an optional text column: "" becomes SQL NULL, so a unique
// index treats two unset values as distinct rather than as a collision.
func nullableString(value string) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

// encodeScopes renders a scope list for storage, after validating it.
//
// The order the caller granted is preserved, because a consent screen and an
// introspection response should show the same order every time. Duplicates are
// refused rather than deduplicated: a duplicate means the caller built the list
// wrong, and silently accepting it hides that.
func encodeScopes(scopes []string) (string, error) {
	if len(scopes) == 0 {
		return "", fmt.Errorf("store: empty scope list: %w", ErrInvalidArgument)
	}
	if len(scopes) > maxScopeCount {
		return "", fmt.Errorf("store: %d scopes, over the %d bound: %w",
			len(scopes), maxScopeCount, ErrInvalidArgument)
	}

	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if err := checkScope(scope); err != nil {
			return "", err
		}
		if _, duplicate := seen[scope]; duplicate {
			return "", fmt.Errorf("store: scope %q appears twice: %w", scope, ErrInvalidArgument)
		}
		seen[scope] = struct{}{}
	}
	return strings.Join(scopes, " "), nil
}

// checkScope refuses a scope that is empty, oversized, or carries a character that
// would change how the list parses.
func checkScope(scope string) error {
	if scope == "" || len(scope) > maxScopeLength {
		return fmt.Errorf("store: scope has length %d: %w", len(scope), ErrInvalidArgument)
	}
	for _, char := range scope {
		if char <= ' ' || char == '"' || char == '\\' || char == 0x7f {
			return fmt.Errorf("store: scope %q holds a character that is not allowed: %w",
				scope, ErrInvalidArgument)
		}
	}
	return nil
}

// decodeScopes splits a stored scope list. The returned slice is fresh on every
// call, so a caller cannot mutate stored state through it.
func decodeScopes(encoded string) []string {
	if encoded == "" {
		return nil
	}
	return strings.Split(encoded, " ")
}

// maxIdentifierLength bounds an identifier a caller supplies.
const maxIdentifierLength = 256

// checkIdentifier refuses an empty or oversized identifier a caller supplied.
func checkIdentifier(kind, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("store: empty %s: %w", kind, ErrInvalidArgument)
	}
	if len(value) > maxIdentifierLength {
		return fmt.Errorf("store: %s has length %d, over the %d bound: %w",
			kind, len(value), maxIdentifierLength, ErrInvalidArgument)
	}
	return nil
}
