package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/tamcore/garmin-mcp/internal/cryptostore"
)

// The Garmin DI token set, per principal, in SQLite.
//
// The method set and the sentinels are exactly FileStore's, so both backends
// satisfy the same consumer interface and a caller can be moved from the local file
// store to the multi-user database without touching its error handling. The payload
// inside the envelope is the same document encodeRecordPayload writes and
// decodeTokenDocument reads, and the additional data is the same
// recordAAD(schema, version), so a record stays bound to its principal, to its
// record type, to the payload schema and to the compare-and-set version.

// Load returns the token set for principal and the version that produced it. It
// reports ErrNoTokens when no record exists, which is the signal to log in.
func (s *SQLiteStore) Load(ctx context.Context, principal string) (TokenSet, int64, error) {
	if err := checkRequest(ctx, principal); err != nil {
		return TokenSet{}, 0, err
	}

	var (
		schema  int
		version int64
		sealed  []byte
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT record_schema, version, sealed FROM garmin_token_sets WHERE principal_id = ?`,
		principal).Scan(&schema, &version, &sealed)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return TokenSet{}, 0, fmt.Errorf("store: principal has no token record: %w", ErrNoTokens)
	case err != nil:
		return TokenSet{}, 0, fmt.Errorf("store: read token record: %w", err)
	}

	set, err := s.openTokenRecord(principal, schema, version, sealed)
	if err != nil {
		return TokenSet{}, 0, err
	}
	return set, version, nil
}

// openTokenRecord decrypts and decodes one stored token set.
func (s *SQLiteStore) openTokenRecord(principal string, schema int, version int64,
	sealed []byte,
) (TokenSet, error) {
	if schema != recordSchema {
		return TokenSet{}, fmt.Errorf("store: token record has schema %d, want %d: %w",
			schema, recordSchema, ErrCorruptRecord)
	}
	plaintext, err := cryptostore.Decrypt(s.key, principal, recordAAD(schema, version), sealed)
	if err != nil {
		// The cause reports versions and sizes only, never material.
		return TokenSet{}, fmt.Errorf("store: open token record: %w: %w", ErrCorruptRecord, err)
	}
	return decodeTokenDocument(plaintext, sourceRecord)
}

// Save stores set for principal and returns the new version.
//
// expectedVersion is a compare-and-set precondition: zero means the record must not
// exist yet, and any other value must equal the stored version. A mismatch reports
// ErrVersionConflict and changes nothing, so a caller that raced a token rotation
// reloads and retries instead of overwriting the newer refresh token.
//
// The read of the current version and the write of the next one happen in one
// immediate transaction, and the UPDATE repeats the expected version in its WHERE
// clause, so the check cannot be outrun: two goroutines racing the same rotation
// produce exactly one winner and one ErrVersionConflict.
func (s *SQLiteStore) Save(ctx context.Context, principal string, set TokenSet,
	expectedVersion int64,
) (int64, error) {
	if err := checkRequest(ctx, principal); err != nil {
		return 0, err
	}
	if expectedVersion < 0 || expectedVersion >= maxRecordVersion {
		return 0, fmt.Errorf("store: expected version %d is out of range: %w",
			expectedVersion, ErrInvalidArgument)
	}
	if set.IsZero() {
		return 0, fmt.Errorf("store: refusing to store an empty token set: %w", ErrInvalidArgument)
	}

	next := expectedVersion + 1
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		return s.commitTokenRecord(ctx, tx, principal, set, expectedVersion, next)
	})
	if err != nil {
		return 0, err
	}
	return next, nil
}

// commitTokenRecord is the transactional body of Save.
func (s *SQLiteStore) commitTokenRecord(ctx context.Context, tx *sql.Tx, principal string,
	set TokenSet, expected, next int64,
) error {
	current, err := currentTokenVersion(ctx, tx, principal)
	if err != nil {
		return err
	}
	if current != expected {
		return fmt.Errorf("store: record is at version %d, caller expected %d: %w",
			current, expected, ErrVersionConflict)
	}

	keyVersion, err := keyVersionOf(s.key)
	if err != nil {
		return err
	}
	sealed, err := cryptostore.Encrypt(s.key, principal,
		recordAAD(recordSchema, next), encodeRecordPayload(set))
	if err != nil {
		return fmt.Errorf("store: seal token record: %w", err)
	}

	if expected == 0 {
		return s.insertTokenRecord(ctx, tx, principal, sealed, keyVersion, next)
	}
	return s.updateTokenRecord(ctx, tx, principal, sealed, keyVersion, expected, next)
}

func (s *SQLiteStore) insertTokenRecord(ctx context.Context, tx *sql.Tx, principal string,
	sealed []byte, keyVersion int, next int64,
) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO garmin_token_sets
		     (principal_id, record_schema, version, key_version, sealed, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		principal, recordSchema, next, keyVersion, sealed, formatTime(s.now()))
	switch {
	case isUniqueViolation(err):
		// Another writer inserted between the version read and this insert. That
		// is a lost race, not a corrupt state: report the conflict.
		return fmt.Errorf("store: token record was created concurrently: %w", ErrVersionConflict)
	case isForeignKeyViolation(err):
		return fmt.Errorf("store: principal %s does not exist: %w", principal, ErrPrincipalNotFound)
	case err != nil:
		return fmt.Errorf("store: insert token record: %w", err)
	}
	return nil
}

func (s *SQLiteStore) updateTokenRecord(ctx context.Context, tx *sql.Tx, principal string,
	sealed []byte, keyVersion int, expected, next int64,
) error {
	result, err := tx.ExecContext(ctx,
		`UPDATE garmin_token_sets
		    SET record_schema = ?, version = ?, key_version = ?, sealed = ?, updated_at = ?
		  WHERE principal_id = ? AND version = ?`,
		recordSchema, next, keyVersion, sealed, formatTime(s.now()), principal, expected)
	if err != nil {
		return fmt.Errorf("store: update token record: %w", err)
	}
	return requireOneRow(result, fmt.Errorf(
		"store: record moved away from version %d before the write: %w", expected, ErrVersionConflict))
}

// currentTokenVersion reports the stored version, or 0 when no record exists.
func currentTokenVersion(ctx context.Context, tx *sql.Tx, principal string) (int64, error) {
	var version int64
	err := tx.QueryRowContext(ctx,
		`SELECT version FROM garmin_token_sets WHERE principal_id = ?`, principal).Scan(&version)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("store: read token version: %w", err)
	}
	return version, nil
}

// Delete removes the local token record for principal. An absent record is not an
// error.
//
// This does NOT revoke anything at Garmin. The DI refresh token stays valid at
// Garmin's service until Garmin expires or revokes it, and any copy of the material
// keeps working. Never report a Delete as a revocation. To also revoke this server's
// own tokens for the principal, use UnlinkGarminAccount.
func (s *SQLiteStore) Delete(ctx context.Context, principal string) error {
	if err := checkRequest(ctx, principal); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM garmin_token_sets WHERE principal_id = ?`, principal); err != nil {
		return fmt.Errorf("store: delete token record: %w", err)
	}
	return nil
}
