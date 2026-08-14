package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Refresh rotation and reuse detection.
//
// Rotation is one transaction: the presented row is marked consumed and the next
// generation is inserted into the same family. Detection and revocation are not two
// steps, because a racing attacker would slip between them.

// RefreshRotation is the input to RotateRefreshToken.
type RefreshRotation struct {
	// Presented is the refresh token the client sent.
	Presented Secret

	// NextAccessToken and NextRefreshToken are the freshly generated replacements.
	NextAccessToken  Secret
	NextRefreshToken Secret

	// AccessLifetime and RefreshLifetime are used only when the matching absolute
	// instant below is zero.
	AccessLifetime  time.Duration
	RefreshLifetime time.Duration

	// NextGeneration is the generation to stamp on the replacement pair. Zero means
	// one past the generation of the presented token, which is what an unbroken chain
	// is; a caller that mints its own records passes what it minted.
	NextGeneration uint64

	// IssuedAt, AccessExpiresAt and RefreshExpiresAt are the absolute instants of the
	// replacement pair. Each zero value falls back to the store's clock and the
	// matching lifetime above, so a caller that has records writes exactly what it
	// returned to the client and a caller that has lifetimes keeps its old behavior.
	IssuedAt         time.Time
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
}

// RotateRefreshToken redeems a refresh token and issues the next generation into the
// same family.
//
// Replaying a refresh token that was already rotated revokes the entire family and
// reports ErrRefreshTokenReuse. The revocation is committed even though the call
// returns an error, because the whole point is that the family must not survive the
// replay. That is the one place in this package where a failed call still changes
// state, and it is deliberate.
func (s *SQLiteStore) RotateRefreshToken(ctx context.Context, rotation RefreshRotation,
) (AccessGrant, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return AccessGrant{}, err
	}
	presentedHash, err := s.keys.requireLookup(purposeRefreshToken, rotation.Presented)
	if err != nil {
		return AccessGrant{}, err
	}

	var issued AccessGrant
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		grant, err := s.applyRotation(ctx, tx, presentedHash, rotation)
		issued = grant
		return err
	})
	if err != nil {
		return AccessGrant{}, err
	}
	return issued, nil
}

// applyRotation is the transactional body of RotateRefreshToken.
func (s *SQLiteStore) applyRotation(ctx context.Context, tx *sql.Tx, presentedHash string,
	rotation RefreshRotation,
) (AccessGrant, error) {
	stored, err := readToken(ctx, tx, presentedHash, tokenKindRefresh)
	if err != nil {
		return AccessGrant{}, err
	}
	if stored.consumed {
		return AccessGrant{}, s.revokeOnReuse(ctx, tx, stored.familyID)
	}
	if err := s.checkUsable(stored); err != nil {
		return AccessGrant{}, err
	}
	return s.issueNextGeneration(ctx, tx, stored, presentedHash, rotation)
}

// revokeOnReuse revokes the family of a replayed refresh token and commits, so the
// revocation survives the error this returns.
func (s *SQLiteStore) revokeOnReuse(ctx context.Context, tx *sql.Tx, familyID string) error {
	if _, err := s.revokeFamilyIn(ctx, tx, familyID, reasonRefreshReuse); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit reuse revocation: %w", err)
	}
	return fmt.Errorf("store: refresh token of family %s was already rotated, family revoked: %w",
		familyID, ErrRefreshTokenReuse)
}

// issueNextGeneration consumes the presented row and writes the replacement pair.
func (s *SQLiteStore) issueNextGeneration(ctx context.Context, tx *sql.Tx, stored storedToken,
	presentedHash string, rotation RefreshRotation,
) (AccessGrant, error) {
	if err := s.consumePresented(ctx, tx, stored, presentedHash); err != nil {
		return AccessGrant{}, err
	}
	prepared, err := s.prepareRotation(stored, rotation)
	if err != nil {
		return AccessGrant{}, err
	}
	if err := s.insertTokenPair(ctx, tx, stored.familyID, prepared); err != nil {
		return AccessGrant{}, err
	}

	issued := stored.grant()
	issued.Generation = prepared.generation
	issued.IssuedAt = prepared.issuedAt
	issued.ExpiresAt = prepared.accessExpiresAt
	return issued, nil
}

// consumePresented marks the presented row consumed, repeating the not-consumed
// precondition in the WHERE clause so two goroutines racing the same refresh token
// cannot both proceed: the loser matches no row.
func (s *SQLiteStore) consumePresented(ctx context.Context, tx *sql.Tx, stored storedToken,
	presentedHash string,
) error {
	result, err := tx.ExecContext(ctx,
		`UPDATE mcp_tokens SET consumed_at = ? WHERE token_hash = ? AND consumed_at IS NULL`,
		formatTime(s.now()), presentedHash)
	if err != nil {
		return fmt.Errorf("store: consume refresh token: %w", err)
	}
	return requireOneRow(result, fmt.Errorf(
		"store: refresh token of family %s was rotated concurrently: %w",
		stored.familyID, ErrRefreshTokenReuse))
}

// prepareRotation builds the replacement pair, inheriting the scopes and the audience
// of the consumed token so a rotation can never widen a grant.
func (s *SQLiteStore) prepareRotation(stored storedToken, rotation RefreshRotation,
) (preparedGrant, error) {
	issuedAt := rotation.IssuedAt
	if issuedAt.IsZero() {
		issuedAt = s.now()
	}
	issuedAt = issuedAt.UTC()

	accessExpiresAt := rotation.AccessExpiresAt
	if accessExpiresAt.IsZero() {
		accessExpiresAt = issuedAt.Add(rotation.AccessLifetime)
	}
	refreshExpiresAt := rotation.RefreshExpiresAt
	if refreshExpiresAt.IsZero() {
		refreshExpiresAt = issuedAt.Add(rotation.RefreshLifetime)
	}
	if err := checkTokenWindow(issuedAt, accessExpiresAt, refreshExpiresAt); err != nil {
		return preparedGrant{}, err
	}

	hashes, err := s.hashTokenPair(rotation.NextAccessToken, rotation.NextRefreshToken)
	if err != nil {
		return preparedGrant{}, err
	}
	generation := rotation.NextGeneration
	if generation == 0 {
		generation = stored.generation + 1
	}
	return preparedGrant{
		accessHash:       hashes[0],
		refreshHash:      hashes[1],
		scopes:           stored.scopes,
		audience:         stored.audience,
		generation:       generation,
		issuedAt:         issuedAt,
		accessExpiresAt:  accessExpiresAt.UTC(),
		refreshExpiresAt: refreshExpiresAt.UTC(),
	}, nil
}
