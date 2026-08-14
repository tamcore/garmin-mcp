package store

import "errors"

// Sentinel errors for the SQLite backend. They extend the file-store sentinels in
// errors.go rather than replacing them: ErrNoTokens, ErrVersionConflict,
// ErrInvalidPrincipal, ErrInvalidConfig and ErrCorruptRecord mean exactly the same
// thing for both backends, so a caller that already handles a FileStore keeps
// working against a SQLiteStore.
//
// Every failure below is comparable with errors.Is, and the OAuth-shaped ones are
// distinct on purpose: an unknown token, an expired token, a revoked token and a
// replayed refresh token demand four different responses, and collapsing them
// would hide the replay.
var (
	// ErrMigrationSet means the migration set itself is unusable: empty, a name
	// that does not parse, a duplicated version, a gap, or a version below 1. It
	// is a build defect, not an operator error.
	ErrMigrationSet = errors.New("store: migration set is malformed")

	// ErrMigrationFailed means a migration's SQL failed. The migration ran inside
	// a transaction that has been rolled back, so the database still holds the
	// last successfully applied version and nothing partial.
	ErrMigrationFailed = errors.New("store: migration failed")

	// ErrMigrationChanged means an already applied migration's content no longer
	// matches what was recorded when it ran. Migrations are immutable once
	// shipped; the fix is a new migration, never an edit.
	ErrMigrationChanged = errors.New("store: applied migration was altered")

	// ErrSchemaTooNew means the database was migrated by a newer build. Running an
	// older binary against it would read a schema it does not understand, so the
	// store refuses to open instead of guessing.
	ErrSchemaTooNew = errors.New("store: database schema is newer than this build")

	// ErrPrincipalNotFound means no principal has that internal id, email or
	// Garmin account linkage.
	ErrPrincipalNotFound = errors.New("store: principal not found")

	// ErrPrincipalExists means the normalized email is already registered. Email
	// is a login handle, so it must be unique — but it is never the isolation key.
	ErrPrincipalExists = errors.New("store: principal already exists")

	// ErrGarminAccountLinked means the Garmin account is already linked to a
	// different principal. This is the fail-closed outcome of two login flows
	// racing for the same Garmin account: the first writer keeps the linkage and
	// the second is refused, so one Garmin account can never become two
	// principals.
	ErrGarminAccountLinked = errors.New("store: garmin account is linked to another principal")

	// ErrClientNotFound means no OAuth client has that id, or the client is
	// disabled.
	ErrClientNotFound = errors.New("store: oauth client not found")

	// ErrRedirectURIMismatch means the presented redirect URI is not one of the
	// client's registered URIs. Matching is exact: no prefix rule, no wildcard.
	ErrRedirectURIMismatch = errors.New("store: redirect uri is not registered for the client")

	// ErrConsentNotFound means the principal has never granted this client, or the
	// consent has been revoked.
	ErrConsentNotFound = errors.New("store: no active consent for principal and client")

	// ErrTransactionNotFound means the authorization transaction handle is unknown
	// or has expired. Expiry is checked on access, so an expired transaction is
	// never returned even before the cleanup job runs.
	ErrTransactionNotFound = errors.New("store: authorization transaction not found")

	// ErrCodeNotFound means the authorization code is unknown or has expired.
	ErrCodeNotFound = errors.New("store: authorization code not found")

	// ErrCodeAlreadyUsed means the authorization code was already redeemed. It is
	// distinct from ErrCodeNotFound because a replay is a security event.
	ErrCodeAlreadyUsed = errors.New("store: authorization code was already used")

	// ErrTokenNotFound means the presented token material matches no stored
	// lookup value.
	ErrTokenNotFound = errors.New("store: token not found")

	// ErrTokenExpired means the token exists but its expiry has passed. Checked on
	// every access, independently of the cleanup job.
	ErrTokenExpired = errors.New("store: token has expired")

	// ErrTokenRevoked means the token, or the family it belongs to, is revoked.
	ErrTokenRevoked = errors.New("store: token is revoked")

	// ErrRefreshTokenReuse means a refresh token that had already been rotated was
	// presented again. The whole family is revoked before this error is returned,
	// so the caller does not have to remember to do it.
	ErrRefreshTokenReuse = errors.New("store: refresh token was replayed")

	// ErrIncompleteUnlink means an unlink or revocation cascade could not remove
	// or revoke everything it must. The transaction is rolled back, so the caller
	// sees the previous state rather than a half-unlinked principal.
	ErrIncompleteUnlink = errors.New("store: unlink did not complete")

	// ErrInvalidAuditDetail means an audit detail was not a short reason code. The
	// audit log must never become a place where a token or a health payload lands
	// by accident, so anything outside the reason-code grammar is refused.
	ErrInvalidAuditDetail = errors.New("store: audit detail is not a reason code")

	// ErrInvalidArgument means a caller-supplied value was empty, too long, or
	// otherwise unusable. It is the boundary-validation refusal.
	ErrInvalidArgument = errors.New("store: invalid argument")
)
