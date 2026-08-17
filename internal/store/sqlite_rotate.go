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

	// Scopes is the scope set to persist on the replacement pair. An empty slice
	// inherits the presented token's scopes, which is a refresh that narrowed
	// nothing.
	//
	// This field is why the caller cannot be trusted to merely report a narrowed
	// scope to its client: OAuth lets a refresh narrow scope, and the authorization
	// server does narrow it and returns the narrow set in the response. Before this
	// existed the rotation inherited the CONSUMED token's scopes unconditionally, so
	// a client that deliberately narrowed a token to hand to a lower-trust consumer
	// was told it was read-only while the persisted row still carried write and
	// destructive scope — and verification reads the row, not the response.
	Scopes []string

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

	var (
		issued AccessGrant
		reused familyOwnership
	)
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		grant, revoked, err := s.applyRotation(ctx, tx, presentedHash, rotation)
		issued, reused = grant, revoked
		return err
	})
	if err != nil {
		// A replay is the one path where a failed call still changed state: the
		// family is revoked and committed, so it is announced even though an error
		// is returned. Every other failure rolled back and announces nothing.
		s.publishRevocation(reused.event(reasonRefreshReuse))
		return AccessGrant{}, err
	}
	return issued, nil
}

// applyRotation is the transactional body of RotateRefreshToken.
// The second result names the family a replay revoked, and is the zero value on
// every other path. It is what the caller announces once the commit is known to
// have happened.
func (s *SQLiteStore) applyRotation(ctx context.Context, tx *sql.Tx, presentedHash string,
	rotation RefreshRotation,
) (AccessGrant, familyOwnership, error) {
	stored, err := readToken(ctx, tx, presentedHash, tokenKindRefresh)
	if err != nil {
		return AccessGrant{}, familyOwnership{}, err
	}
	if stored.consumed {
		owner, err := s.revokeOnReuse(ctx, tx, stored.familyID)
		return AccessGrant{}, owner, err
	}
	if err := s.checkUsable(stored); err != nil {
		return AccessGrant{}, familyOwnership{}, err
	}
	grant, err := s.issueNextGeneration(ctx, tx, stored, presentedHash, rotation)
	return grant, familyOwnership{}, err
}

// revokeOnReuse revokes the family of a replayed refresh token and commits, so the
// revocation survives the error this returns.
//
// The owner is read before the revocation and returned with the error, because the
// announcement must name the same family the transaction just took down.
func (s *SQLiteStore) revokeOnReuse(ctx context.Context, tx *sql.Tx, familyID string,
) (familyOwnership, error) {
	owner, err := familyOwner(ctx, tx, familyID)
	if err != nil {
		return familyOwnership{}, err
	}
	if _, err := s.revokeFamilyIn(ctx, tx, familyID, reasonRefreshReuse); err != nil {
		return familyOwnership{}, err
	}
	if err := tx.Commit(); err != nil {
		return familyOwnership{}, fmt.Errorf("store: commit reuse revocation: %w", err)
	}
	return owner, fmt.Errorf(
		"store: refresh token of family %s was already rotated, family revoked: %w",
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
	scopes := stored.scopes
	if len(rotation.Scopes) > 0 {
		// Validated the same way every other scope write is, so a rotation cannot
		// persist a shape the rest of the store would refuse.
		narrowed, err := encodeScopes(rotation.Scopes)
		if err != nil {
			return preparedGrant{}, err
		}
		scopes = narrowed
	}
	return preparedGrant{
		accessHash:       hashes[0],
		refreshHash:      hashes[1],
		scopes:           scopes,
		audience:         stored.audience,
		generation:       generation,
		issuedAt:         issuedAt,
		accessExpiresAt:  accessExpiresAt.UTC(),
		refreshExpiresAt: refreshExpiresAt.UTC(),
	}, nil
}
