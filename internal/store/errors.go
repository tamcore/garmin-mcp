package store

import "errors"

// Sentinel errors. Every failure is comparable with errors.Is, because callers
// act on the distinction: an absent record starts a login, a version conflict
// retries the read-modify-write, and an unsafe path or permission is a refusal an
// operator must fix.
var (
	// ErrNoTokens means no token record exists for the principal. It is the signal
	// to run a login, not a malfunction of the store.
	ErrNoTokens = errors.New("store: no tokens for principal")

	// ErrVersionConflict means the record changed since the caller read it, or the
	// must-not-exist precondition (expectedVersion zero) failed. The caller
	// reloads and retries; it must never overwrite blindly, because the record it
	// would replace may hold a newer rotated refresh token.
	ErrVersionConflict = errors.New("store: token record version conflict")

	// ErrInvalidPrincipal means the principal id was empty or unusable as a record
	// key.
	ErrInvalidPrincipal = errors.New("store: invalid principal")

	// ErrInvalidConfig means the store was configured with no directory, no key,
	// or an empty path.
	ErrInvalidConfig = errors.New("store: invalid configuration")

	// ErrInsecurePath means a component of the path is a symlink, or cannot be
	// checked.
	//
	// Source: token_file_path in client.py (0.3.10), which rejects symlinks across
	// the full ancestry, not only the final component.
	ErrInsecurePath = errors.New("store: token path is not safe")

	// ErrForeignHomePath means the path used ~username syntax that expands into
	// another local account's home directory. Bare ~ and ~/... are accepted.
	//
	// Source: _OTHER_USER_HOME_RE in client.py (0.3.10).
	ErrForeignHomePath = errors.New("store: token path points into another user's home")

	// ErrInsecurePermissions means a token file is readable or writable by group
	// or other. A refresh token another local account can read is treated as
	// compromised.
	//
	// Source: the 0o600-inside-0o700 rule in Client.dump (0.3.10,
	// GHSA-wjhr-76vg-2hvc).
	ErrInsecurePermissions = errors.New("store: token file is not owner-only")

	// ErrIncompatibleTokenFile means the file exists but is not a 0.3.x
	// garmin_tokens.json document. Detection is structural, never length-based.
	ErrIncompatibleTokenFile = errors.New("store: not a 0.3.x token document")

	// ErrInlineTokensRefused means inline token JSON was supplied while the
	// insecure compatibility override was off. Inline tokens are refused by
	// default and must stay refused in remote mode.
	ErrInlineTokensRefused = errors.New("store: inline token JSON is refused")

	// ErrCorruptRecord means an encrypted record could not be parsed or opened. It
	// never quotes the record.
	ErrCorruptRecord = errors.New("store: token record is unreadable")
)
