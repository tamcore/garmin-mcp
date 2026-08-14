package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Hashed opaque MCP token material, grouped into revocable families.
//
// # What is stored
//
// Never the token. Only hex(HMAC(purposeKey, token)) under a per-kind purpose, so an
// access token and a refresh token with identical bytes produce different lookup
// values and neither can be presented as the other. Alongside it: the family, the
// scopes, the audience, the issue and expiry instants, the consumption instant of a
// rotated refresh token, and the revocation instant.
//
// # Rotation and reuse detection
//
// Every refresh token redeems exactly once. RotateRefreshToken marks the presented
// row consumed and issues the next pair into the same family, in one transaction. If
// a row that is already consumed is presented again, the whole family is revoked and
// ErrRefreshTokenReuse is reported: a replay means either the client is buggy or the
// token leaked, and in the leak case the attacker and the legitimate client both hold
// material from that family, so the only safe answer is to invalidate all of it.

// The two rows a grant produces.
const (
	tokenKindAccess  = "access"
	tokenKindRefresh = "refresh"
)

// maxTokenLifetime bounds an issued lifetime. An unbounded lifetime turns a leaked
// token into a permanent credential.
const maxTokenLifetime = 90 * 24 * time.Hour

// TokenGrant is the material and metadata for one new token family.
//
// The caller generates the token material — at least 256 bits from crypto/rand — and
// this store only ever sees it long enough to hash it.
type TokenGrant struct {
	PrincipalID string
	ClientID    string

	// Scopes are the scopes both tokens carry. Required.
	Scopes []string

	// Audience is the resource the access token is valid for. Required, because an
	// access token with no audience is a token valid everywhere.
	Audience string

	AccessToken  Secret
	RefreshToken Secret

	AccessLifetime  time.Duration
	RefreshLifetime time.Duration
}

// AccessGrant is what a valid presented token proves. It is the value an
// authorization decision is made from, and it deliberately carries the internal
// principal id rather than an email.
type AccessGrant struct {
	PrincipalID string
	ClientID    string
	FamilyID    string
	Scopes      []string
	Audience    string
	ExpiresAt   time.Time
}

// RefreshRotation is the input to RotateRefreshToken.
type RefreshRotation struct {
	// Presented is the refresh token the client sent.
	Presented Secret

	// NextAccessToken and NextRefreshToken are the freshly generated replacements.
	NextAccessToken  Secret
	NextRefreshToken Secret

	AccessLifetime  time.Duration
	RefreshLifetime time.Duration
}

// IssueTokenFamily creates a family and its first access and refresh token, and
// returns the family id.
//
// It requires an active consent for the principal and client: a token issued without
// one would outlive the user's decision. The whole thing is one transaction, so a
// family never exists without its tokens.
func (s *SQLiteStore) IssueTokenFamily(ctx context.Context, grant TokenGrant) (string, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return "", err
	}
	prepared, err := s.prepareGrant(grant)
	if err != nil {
		return "", err
	}
	familyID, err := newPrincipalID()
	if err != nil {
		return "", err
	}

	err = s.inTx(ctx, func(tx *sql.Tx) error {
		if err := requireActiveConsent(ctx, tx, grant.PrincipalID, grant.ClientID); err != nil {
			return err
		}
		if err := s.insertFamily(ctx, tx, familyID, grant.PrincipalID, grant.ClientID); err != nil {
			return err
		}
		return s.insertTokenPair(ctx, tx, familyID, prepared)
	})
	if err != nil {
		return "", err
	}
	return familyID, nil
}

// preparedGrant is a validated grant with its lookup values already computed.
type preparedGrant struct {
	accessHash  string
	refreshHash string
	scopes      string
	audience    string
	accessTTL   time.Duration
	refreshTTL  time.Duration
}

// prepareGrant validates a grant and hashes its material.
func (s *SQLiteStore) prepareGrant(grant TokenGrant) (preparedGrant, error) {
	for kind, value := range map[string]string{
		"principal id": grant.PrincipalID, "client id": grant.ClientID, "audience": grant.Audience,
	} {
		if err := checkIdentifier(kind, value); err != nil {
			return preparedGrant{}, err
		}
	}
	scopes, err := encodeScopes(grant.Scopes)
	if err != nil {
		return preparedGrant{}, err
	}
	if err := checkLifetimes(grant.AccessLifetime, grant.RefreshLifetime); err != nil {
		return preparedGrant{}, err
	}
	accessHash, err := s.keys.requireLookup(purposeAccessToken, grant.AccessToken)
	if err != nil {
		return preparedGrant{}, err
	}
	refreshHash, err := s.keys.requireLookup(purposeRefreshToken, grant.RefreshToken)
	if err != nil {
		return preparedGrant{}, err
	}
	return preparedGrant{
		accessHash:  accessHash,
		refreshHash: refreshHash,
		scopes:      scopes,
		audience:    grant.Audience,
		accessTTL:   grant.AccessLifetime,
		refreshTTL:  grant.RefreshLifetime,
	}, nil
}

// checkLifetimes refuses a non-positive or unbounded lifetime.
func checkLifetimes(access, refresh time.Duration) error {
	for name, lifetime := range map[string]time.Duration{"access": access, "refresh": refresh} {
		if lifetime <= 0 || lifetime > maxTokenLifetime {
			return fmt.Errorf("store: %s lifetime %s is outside (0, %s]: %w",
				name, lifetime, maxTokenLifetime, ErrInvalidArgument)
		}
	}
	return nil
}

// requireActiveConsent refuses to issue against a withdrawn or absent grant.
func requireActiveConsent(ctx context.Context, tx *sql.Tx, principalID, clientID string) error {
	var revokedAt sql.NullString
	err := tx.QueryRowContext(ctx,
		`SELECT revoked_at FROM consents WHERE principal_id = ? AND client_id = ?`,
		principalID, clientID).Scan(&revokedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("store: principal %s has not granted client %s: %w",
			principalID, clientID, ErrConsentNotFound)
	case err != nil:
		return fmt.Errorf("store: read consent: %w", err)
	case revokedAt.Valid:
		return fmt.Errorf("store: principal %s revoked client %s: %w",
			principalID, clientID, ErrConsentNotFound)
	}
	return nil
}

func (s *SQLiteStore) insertFamily(ctx context.Context, tx *sql.Tx,
	familyID, principalID, clientID string,
) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO token_families
		     (id, principal_id, client_id, created_at, revoked_at, revocation_reason)
		 VALUES (?, ?, ?, ?, NULL, NULL)`,
		familyID, principalID, clientID, formatTime(s.now()))
	if err != nil {
		if isForeignKeyViolation(err) {
			return fmt.Errorf("store: principal %s or client %s does not exist: %w",
				principalID, clientID, ErrPrincipalNotFound)
		}
		return fmt.Errorf("store: insert token family: %w", err)
	}
	return nil
}

// insertTokenPair writes the access and refresh rows of one generation.
func (s *SQLiteStore) insertTokenPair(ctx context.Context, tx *sql.Tx, familyID string,
	prepared preparedGrant,
) error {
	issuedAt := s.now().UTC()
	rows := []struct {
		hash     string
		kind     string
		lifetime time.Duration
	}{
		{prepared.accessHash, tokenKindAccess, prepared.accessTTL},
		{prepared.refreshHash, tokenKindRefresh, prepared.refreshTTL},
	}

	for _, row := range rows {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO mcp_tokens
			     (token_hash, family_id, kind, scopes, audience, issued_at, expires_at,
			      consumed_at, revoked_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, NULL, NULL)`,
			row.hash, familyID, row.kind, prepared.scopes, prepared.audience,
			formatTime(issuedAt), formatTime(issuedAt.Add(row.lifetime)))
		if err != nil {
			if isUniqueViolation(err) {
				return fmt.Errorf("store: %s token material is already stored: %w",
					row.kind, ErrInvalidArgument)
			}
			return fmt.Errorf("store: insert %s token: %w", row.kind, err)
		}
	}
	return nil
}

// storedToken is one mcp_tokens row joined to its family and its consent.
type storedToken struct {
	familyID       string
	principalID    string
	clientID       string
	scopes         string
	audience       string
	expiresAt      time.Time
	consumed       bool
	revoked        bool
	familyRevoked  bool
	consentRevoked bool
}

// selectTokenSQL reads a token with everything a decision needs, in one round trip.
// The consent subquery coalesces to 1 — treated as revoked — when no consent row
// exists at all, so a missing consent fails closed rather than reading as granted.
const selectTokenSQL = `
SELECT f.id, f.principal_id, f.client_id, t.scopes, t.audience, t.expires_at,
       t.consumed_at IS NOT NULL, t.revoked_at IS NOT NULL, f.revoked_at IS NOT NULL,
       coalesce((SELECT c.revoked_at IS NOT NULL FROM consents c
                  WHERE c.principal_id = f.principal_id AND c.client_id = f.client_id), 1)
  FROM mcp_tokens t
  JOIN token_families f ON f.id = t.family_id
 WHERE t.token_hash = ? AND t.kind = ?`

// readToken loads one token row. It reports ErrTokenNotFound for an unknown hash.
func readToken(ctx context.Context, q Querier, hash, kind string) (storedToken, error) {
	var (
		token       storedToken
		expiresText string
	)
	err := q.QueryRowContext(ctx, selectTokenSQL, hash, kind).Scan(
		&token.familyID, &token.principalID, &token.clientID, &token.scopes,
		&token.audience, &expiresText, &token.consumed, &token.revoked,
		&token.familyRevoked, &token.consentRevoked)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return storedToken{}, fmt.Errorf("store: no %s token matches: %w", kind, ErrTokenNotFound)
	case err != nil:
		return storedToken{}, fmt.Errorf("store: read %s token: %w", kind, err)
	}

	token.expiresAt, err = parseTime(expiresText)
	if err != nil {
		return storedToken{}, err
	}
	return token, nil
}

// grant renders a stored token as an AccessGrant.
func (t storedToken) grant() AccessGrant {
	return AccessGrant{
		PrincipalID: t.principalID,
		ClientID:    t.clientID,
		FamilyID:    t.familyID,
		Scopes:      decodeScopes(t.scopes),
		Audience:    t.audience,
		ExpiresAt:   t.expiresAt,
	}
}

// LookupAccessToken validates a presented access token and returns what it proves.
//
// Every check happens here, on access, and none of them depends on the cleanup job
// having run: an expired token reports ErrTokenExpired, a revoked token or a token in
// a revoked family reports ErrTokenRevoked, and a token whose consent has since been
// withdrawn reports ErrTokenRevoked too, because a live access token must not outlive
// the consent that justified it.
func (s *SQLiteStore) LookupAccessToken(ctx context.Context, token Secret) (AccessGrant, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return AccessGrant{}, err
	}
	hash, err := s.keys.requireLookup(purposeAccessToken, token)
	if err != nil {
		return AccessGrant{}, err
	}
	stored, err := readToken(ctx, s.db, hash, tokenKindAccess)
	if err != nil {
		return AccessGrant{}, err
	}
	if err := s.checkUsable(stored); err != nil {
		return AccessGrant{}, err
	}
	return stored.grant(), nil
}

// checkUsable applies the on-access revocation and expiry rules.
func (s *SQLiteStore) checkUsable(stored storedToken) error {
	switch {
	case stored.revoked || stored.familyRevoked:
		return fmt.Errorf("store: token family %s is revoked: %w", stored.familyID, ErrTokenRevoked)
	case stored.consentRevoked:
		return fmt.Errorf("store: consent for client %s is withdrawn: %w",
			stored.clientID, ErrTokenRevoked)
	case !stored.expiresAt.After(s.now().UTC()):
		return fmt.Errorf("store: token expired at %s: %w",
			stored.expiresAt.Format(timeLayout), ErrTokenExpired)
	}
	return nil
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
	result, err := tx.ExecContext(ctx,
		`UPDATE mcp_tokens SET consumed_at = ? WHERE token_hash = ? AND consumed_at IS NULL`,
		formatTime(s.now()), presentedHash)
	if err != nil {
		return AccessGrant{}, fmt.Errorf("store: consume refresh token: %w", err)
	}
	// The WHERE clause repeats the not-consumed precondition, so two goroutines
	// racing the same refresh token cannot both proceed: the loser matches no row.
	err = requireOneRow(result, fmt.Errorf(
		"store: refresh token of family %s was rotated concurrently: %w",
		stored.familyID, ErrRefreshTokenReuse))
	if err != nil {
		return AccessGrant{}, err
	}

	prepared, err := s.prepareGrant(TokenGrant{
		PrincipalID:     stored.principalID,
		ClientID:        stored.clientID,
		Scopes:          decodeScopes(stored.scopes),
		Audience:        stored.audience,
		AccessToken:     rotation.NextAccessToken,
		RefreshToken:    rotation.NextRefreshToken,
		AccessLifetime:  rotation.AccessLifetime,
		RefreshLifetime: rotation.RefreshLifetime,
	})
	if err != nil {
		return AccessGrant{}, err
	}
	if err := s.insertTokenPair(ctx, tx, stored.familyID, prepared); err != nil {
		return AccessGrant{}, err
	}

	issued := stored.grant()
	issued.ExpiresAt = s.now().UTC().Add(prepared.accessTTL)
	return issued, nil
}
