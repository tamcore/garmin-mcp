package store

import (
	"fmt"
	"strings"
)

// Inline token JSON is an explicitly insecure compatibility override.
//
// Upstream accepts a tokenstore argument that is either a path or the token JSON
// itself. Passing the JSON inline means the refresh token travels as a command-line
// argument, an environment variable or a configuration value, where it is visible
// to `ps`, to process inspection, to shell history, to container inspect output and
// to crash reports. Prefer a mounted owner-only file.
//
// Therefore:
//
//   - it is refused unless the caller passes allowInsecure explicitly, which comes
//     from Config.AllowInsecureInlineTokens;
//   - remote mode must never enable it;
//   - no error built here ever contains the value. Only the source kind and the
//     length are reported.
//
// Source: the 0.3.10 login() failure path, which logs source and length precisely
// because the value may be the inline token JSON.

// LooksLikeInlineTokenJSON reports whether value is inline JSON rather than a
// filesystem path.
//
// The test is structural — a leading { or [ after trimming — not a length
// threshold, because a long legitimate path would be misread as JSON and a short
// JSON document as a path.
//
// Source: _looks_like_json in __init__.py (0.3.10).
func LooksLikeInlineTokenJSON(value string) bool {
	trimmed := strings.TrimSpace(value)
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

// ParseInlineTokenJSON parses inline 0.3.x token JSON.
//
// It reports ErrInlineTokensRefused unless allowInsecure is true, and
// ErrIncompatibleTokenFile when the value is not a 0.3.x document. No error names
// the value.
func ParseInlineTokenJSON(value string, allowInsecure bool) (TokenSet, error) {
	if !allowInsecure {
		return TokenSet{}, fmt.Errorf("store: inline token JSON is disabled "+
			"(source=%s, %d bytes); mount an owner-only token file instead: %w",
			sourceInline, len(value), ErrInlineTokensRefused)
	}

	raw := []byte(strings.TrimSpace(value))
	if !IsLegacyTokenDocument(raw) {
		return TokenSet{}, fmt.Errorf("store: inline value is not a 0.3.x token document "+
			"(source=%s, %d bytes): %w", sourceInline, len(raw), ErrIncompatibleTokenFile)
	}
	return decodeTokenDocument(raw, sourceInline)
}
