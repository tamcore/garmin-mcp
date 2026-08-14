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
// family's resource, the scopes, the audience, the generation within the family, the
// issue and expiry instants, the consumption instant of a rotated refresh token, and
// the revocation instant.
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

// TokenGrant is the material and metadata for one new token family, expressed as
// lifetimes.
//
// The caller generates the token material — at least 256 bits from crypto/rand — and
// this store only ever sees it long enough to hash it.
//
// A caller that holds records rather than lifetimes — one that already knows the
// family id, the generation and the absolute instants, because it minted them —
// wants IssueTokenFamilyRecord instead. Nothing here may be left empty: this shape
// requires a scope list and an audience.
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

	// Generation is how many rotations deep in its family the token is. The first
	// pair of a family is generation 0.
	Generation uint64

	// IssuedAt and ExpiresAt are the stored instants, not a lifetime applied at read
	// time, so a caller can reproduce exactly what was written.
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// IssueTokenFamily creates a family and its first access and refresh token, and
// returns the generated family id.
//
// It requires an active consent for the principal and client: a token issued without
// one would outlive the user's decision. The whole thing is one transaction, so a
// family never exists without its tokens.
func (s *SQLiteStore) IssueTokenFamily(ctx context.Context, grant TokenGrant) (string, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return "", err
	}
	if err := checkIdentifier("audience", grant.Audience); err != nil {
		return "", err
	}
	if _, err := encodeScopes(grant.Scopes); err != nil {
		return "", err
	}
	if err := checkLifetimes(grant.AccessLifetime, grant.RefreshLifetime); err != nil {
		return "", err
	}

	issuedAt := s.now().UTC()
	return s.IssueTokenFamilyRecord(ctx, TokenFamilyGrant{
		PrincipalID:      grant.PrincipalID,
		ClientID:         grant.ClientID,
		Scopes:           grant.Scopes,
		Resource:         grant.Audience,
		AccessToken:      grant.AccessToken,
		RefreshToken:     grant.RefreshToken,
		IssuedAt:         issuedAt,
		AccessExpiresAt:  issuedAt.Add(grant.AccessLifetime),
		RefreshExpiresAt: issuedAt.Add(grant.RefreshLifetime),
	})
}

// preparedGrant is a validated grant with its lookup values already computed and its
// instants already absolute.
type preparedGrant struct {
	accessHash       string
	refreshHash      string
	scopes           string
	audience         string
	generation       uint64
	issuedAt         time.Time
	accessExpiresAt  time.Time
	refreshExpiresAt time.Time
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

// insertFamily writes the family row, including the resource its tokens are for.
func (s *SQLiteStore) insertFamily(ctx context.Context, tx *sql.Tx,
	familyID, principalID, clientID, resource string,
) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO token_families
		     (id, principal_id, client_id, resource, created_at, revoked_at, revocation_reason)
		 VALUES (?, ?, ?, ?, ?, NULL, NULL)`,
		familyID, principalID, clientID, resource, formatTime(s.now()))
	switch {
	case isUniqueViolation(err):
		return fmt.Errorf("store: token family %s already exists: %w", familyID, ErrInvalidArgument)
	case isForeignKeyViolation(err):
		return fmt.Errorf("store: principal %s or client %s does not exist: %w",
			principalID, clientID, ErrPrincipalNotFound)
	case err != nil:
		return fmt.Errorf("store: insert token family: %w", err)
	}
	return nil
}

// insertTokenPair writes the access and refresh rows of one generation.
func (s *SQLiteStore) insertTokenPair(ctx context.Context, tx *sql.Tx, familyID string,
	prepared preparedGrant,
) error {
	rows := []struct {
		hash      string
		kind      string
		expiresAt time.Time
	}{
		{prepared.accessHash, tokenKindAccess, prepared.accessExpiresAt},
		{prepared.refreshHash, tokenKindRefresh, prepared.refreshExpiresAt},
	}

	for _, row := range rows {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO mcp_tokens
			     (token_hash, family_id, kind, scopes, audience, generation, issued_at,
			      expires_at, consumed_at, revoked_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL)`,
			row.hash, familyID, row.kind, prepared.scopes, prepared.audience,
			int64(prepared.generation), formatTime(prepared.issuedAt), formatTime(row.expiresAt))
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
	resource       string
	scopes         string
	audience       string
	generation     uint64
	issuedAt       time.Time
	expiresAt      time.Time
	consumedAt     time.Time
	consumed       bool
	revoked        bool
	familyRevoked  bool
	consentRevoked bool
}

// selectTokenSQL reads a token with everything a decision needs, in one round trip.
// The consent test is "no active consent row exists for this principal and client",
// so a missing consent and a fully revoked one both read as revoked and fail closed.
const selectTokenSQL = `
SELECT f.id, f.principal_id, f.client_id, f.resource, t.scopes, t.audience, t.generation,
       t.issued_at, t.expires_at, t.consumed_at, t.revoked_at IS NOT NULL,
       f.revoked_at IS NOT NULL,
       NOT EXISTS (SELECT 1 FROM consents c
                    WHERE c.principal_id = f.principal_id AND c.client_id = f.client_id
                      AND c.revoked_at IS NULL)
  FROM mcp_tokens t
  JOIN token_families f ON f.id = t.family_id
 WHERE t.token_hash = ? AND t.kind = ?`

// readToken loads one token row. It reports ErrTokenNotFound for an unknown hash.
func readToken(ctx context.Context, q Querier, hash, kind string) (storedToken, error) {
	var (
		token       storedToken
		generation  int64
		issuedText  string
		expiresText string
		consumedAt  sql.NullString
	)
	err := q.QueryRowContext(ctx, selectTokenSQL, hash, kind).Scan(
		&token.familyID, &token.principalID, &token.clientID, &token.resource, &token.scopes,
		&token.audience, &generation, &issuedText, &expiresText, &consumedAt, &token.revoked,
		&token.familyRevoked, &token.consentRevoked)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return storedToken{}, fmt.Errorf("store: no %s token matches: %w", kind, ErrTokenNotFound)
	case err != nil:
		return storedToken{}, fmt.Errorf("store: read %s token: %w", kind, err)
	case generation < 0:
		return storedToken{}, fmt.Errorf("store: token generation %d is negative: %w",
			generation, ErrCorruptRecord)
	}
	token.generation = uint64(generation)

	if token.issuedAt, err = parseTime(issuedText); err != nil {
		return storedToken{}, err
	}
	if token.expiresAt, err = parseTime(expiresText); err != nil {
		return storedToken{}, err
	}
	if consumedAt.Valid {
		token.consumed = true
		if token.consumedAt, err = parseTime(consumedAt.String); err != nil {
			return storedToken{}, err
		}
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
		Generation:  t.generation,
		IssuedAt:    t.issuedAt,
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
//
// A caller that has to judge expiry itself — one whose interface says the record is
// returned and the decision is the caller's — wants ReadAccessToken.
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
	if err := checkNotRevoked(stored); err != nil {
		return err
	}
	if !stored.expiresAt.After(s.now().UTC()) {
		return fmt.Errorf("store: token expired at %s: %w",
			stored.expiresAt.Format(timeLayout), ErrTokenExpired)
	}
	return nil
}

// checkNotRevoked is the revocation half of checkUsable, without the expiry half. The
// reads that hand the expiry decision to the caller still refuse a revoked token,
// because a revoked token is not a record a caller may judge for itself.
func checkNotRevoked(stored storedToken) error {
	switch {
	case stored.revoked || stored.familyRevoked:
		return fmt.Errorf("store: token family %s is revoked: %w", stored.familyID, ErrTokenRevoked)
	case stored.consentRevoked:
		return fmt.Errorf("store: consent for client %s is withdrawn: %w",
			stored.clientID, ErrTokenRevoked)
	}
	return nil
}
