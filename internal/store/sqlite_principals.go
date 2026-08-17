package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// garminIdentityRecordType is the record type bound into the AEAD additional data
// of a sealed Garmin identity, so the envelope cannot be opened as a token set.
const garminIdentityRecordType = "garmin_identity"

// maxEmailLength bounds a login handle.
const maxEmailLength = 254

// Principal is one isolated user of this server.
//
// ID is the only isolation key. It is a random UUID minted here and is not derived
// from the email or from the Garmin account, so changing either does not move the
// boundary and neither can be used to reach another principal's rows. Email is a
// login handle and a display string; it must never appear in an authorization
// decision.
type Principal struct {
	// ID is the internal UUID and the primary key.
	ID string

	// Email is the normalized login handle: trimmed and lower-cased.
	Email string

	// GarminLinked reports whether a Garmin account is linked. The linkage itself
	// is encrypted; read it with GarminIdentity.
	GarminLinked bool

	CreatedAt time.Time
	UpdatedAt time.Time
}

// GarminIdentity is the linkage between a principal and a Garmin account.
//
// AccountID is a Secret because it is a stable, guessable, account-scoped
// identifier: it is not a credential, but it is the value an attacker with the
// database would want in order to correlate users, so it is stored twice over — as
// a keyed HMAC for uniqueness and inside an AEAD envelope for retrieval — and never
// in the clear.
type GarminIdentity struct {
	// AccountID is Garmin's stable account identifier.
	AccountID Secret

	// DisplayName is what Garmin reports as the account's name. It is unverified
	// remote text, so a caller must escape it before rendering.
	DisplayName string
}

// garminIdentityPayload is the sealed shape of a linkage.
type garminIdentityPayload struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName,omitempty"`
}

// CreatePrincipal mints a principal for a normalized email.
//
// It reports ErrPrincipalExists when the email is already registered, which the
// unique index enforces, so two concurrent registrations of one email produce one
// principal and one refusal.
func (s *SQLiteStore) CreatePrincipal(ctx context.Context, email string) (Principal, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return Principal{}, err
	}
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return Principal{}, err
	}
	id, err := newPrincipalID()
	if err != nil {
		return Principal{}, err
	}
	version, err := s.crypt.activeVersion()
	if err != nil {
		return Principal{}, err
	}

	now := s.now().UTC()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO principals (id, email_normalized, garmin_account_hash, garmin_identity_sealed,
		     key_version, created_at, updated_at)
		 VALUES (?, ?, NULL, NULL, ?, ?, ?)`,
		id, nullableString(normalized), version, formatTime(now), formatTime(now))
	if err != nil {
		if isUniqueViolation(err) {
			return Principal{}, fmt.Errorf("store: email is already registered: %w", ErrPrincipalExists)
		}
		return Principal{}, fmt.Errorf("store: create principal: %w", err)
	}
	return Principal{ID: id, Email: normalized, CreatedAt: now, UpdatedAt: now}, nil
}

// NormalizeEmail trims and lower-cases a login handle and refuses an unusable one.
//
// Normalization is for lookup and display only. The result is never an isolation
// key: two principals whose emails point at the same underlying mailbox spelled
// differently would still be two principals, and that is correct, because the
// isolation key is the UUID.
func NormalizeEmail(email string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" || len(normalized) > maxEmailLength {
		return "", fmt.Errorf("store: email has length %d: %w", len(normalized), ErrInvalidArgument)
	}
	at := strings.IndexByte(normalized, '@')
	if at <= 0 || at != strings.LastIndexByte(normalized, '@') || at == len(normalized)-1 {
		return "", fmt.Errorf("store: email must hold exactly one interior @: %w", ErrInvalidArgument)
	}
	if strings.ContainsAny(normalized, " \t\r\n") {
		return "", fmt.Errorf("store: email holds whitespace: %w", ErrInvalidArgument)
	}
	return normalized, nil
}

// newPrincipalID mints a random UUID (version 4, variant 1) from crypto/rand.
func newPrincipalID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("store: generate principal id: %w", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80

	encoded := hex.EncodeToString(raw)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" +
		encoded[16:20] + "-" + encoded[20:32], nil
}

// principalColumns is the select list every principal read shares.
const principalColumns = `id, email_normalized, garmin_account_hash, created_at, updated_at`

// PrincipalByID returns the principal with that internal id.
func (s *SQLiteStore) PrincipalByID(ctx context.Context, id string) (Principal, error) {
	return s.principalWhere(ctx, `id = ?`, id)
}

// PrincipalByEmail returns the principal registered under that email, after
// normalizing it the same way CreatePrincipal did.
func (s *SQLiteStore) PrincipalByEmail(ctx context.Context, email string) (Principal, error) {
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return Principal{}, err
	}
	return s.principalWhere(ctx, `email_normalized = ?`, normalized)
}

// PrincipalByGarminAccount returns the principal a Garmin account is linked to.
func (s *SQLiteStore) PrincipalByGarminAccount(ctx context.Context, accountID Secret) (Principal, error) {
	hash, err := s.keys.requireLookup(purposeGarminAccount, accountID)
	if err != nil {
		return Principal{}, err
	}
	return s.principalWhere(ctx, `garmin_account_hash = ?`, hash)
}

// principalWhere runs one principal lookup. The predicate is a constant from this
// file; only the bound value comes from a caller.
func (s *SQLiteStore) principalWhere(ctx context.Context, predicate string, value any) (Principal, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return Principal{}, err
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+principalColumns+` FROM principals WHERE `+predicate, value)
	principal, err := scanPrincipal(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, fmt.Errorf("store: no principal matches: %w", ErrPrincipalNotFound)
	}
	return principal, err
}

// rowScanner is what a *sql.Row and a *sql.Rows have in common.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanPrincipal(row rowScanner) (Principal, error) {
	var (
		id          string
		email       sql.NullString
		garminHash  sql.NullString
		createdText string
		updatedText string
	)
	if err := row.Scan(&id, &email, &garminHash, &createdText, &updatedText); err != nil {
		return Principal{}, err
	}
	createdAt, err := parseTime(createdText)
	if err != nil {
		return Principal{}, err
	}
	updatedAt, err := parseTime(updatedText)
	if err != nil {
		return Principal{}, err
	}
	return Principal{
		ID:           id,
		Email:        email.String,
		GarminLinked: garminHash.Valid,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}, nil
}

// LinkGarminAccount attaches a Garmin account to a principal.
//
// # Concurrency contract
//
// Two login flows that reach this method for the same Garmin account at the same
// time cannot both win. The garmin_account_hash column is UNIQUE and the whole
// check-then-write runs in one immediate transaction, so exactly one flow links the
// account and the other reports ErrGarminAccountLinked. One Garmin account
// therefore cannot silently become two principals — the failure is loud, and the
// caller resolves it by using the principal that already owns the account.
//
// Linking the same account to the same principal again is idempotent: it refreshes
// the sealed identity and reports no error, so a repeated login is not a failure.
func (s *SQLiteStore) LinkGarminAccount(ctx context.Context, principalID string,
	identity GarminIdentity,
) error {
	if err := checkStoreRequest(ctx); err != nil {
		return err
	}
	if err := checkIdentifier("principal id", principalID); err != nil {
		return err
	}
	hash, err := s.keys.requireLookup(purposeGarminAccount, identity.AccountID)
	if err != nil {
		return err
	}
	sealed, err := s.sealGarminIdentity(principalID, identity)
	if err != nil {
		return err
	}
	version, err := s.crypt.activeVersion()
	if err != nil {
		return err
	}

	return s.inTx(ctx, func(tx *sql.Tx) error {
		return s.applyGarminLink(ctx, tx, principalID, hash, sealed, version)
	})
}

// applyGarminLink is the transactional body of LinkGarminAccount.
func (s *SQLiteStore) applyGarminLink(ctx context.Context, tx *sql.Tx,
	principalID, hash string, sealed []byte, version int,
) error {
	var owner string
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM principals WHERE garmin_account_hash = ?`, hash).Scan(&owner)
	switch {
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("store: check garmin linkage: %w", err)
	case err == nil && owner != principalID:
		return fmt.Errorf("store: garmin account is linked to principal %s: %w",
			owner, ErrGarminAccountLinked)
	}

	result, err := tx.ExecContext(ctx,
		`UPDATE principals
		    SET garmin_account_hash = ?, garmin_identity_sealed = ?, key_version = ?, updated_at = ?
		  WHERE id = ?`,
		hash, sealed, version, formatTime(s.now()), principalID)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("store: garmin account was linked concurrently: %w", ErrGarminAccountLinked)
		}
		return fmt.Errorf("store: link garmin account: %w", err)
	}
	return requireOneRow(result, fmt.Errorf("store: principal %s does not exist: %w",
		principalID, ErrPrincipalNotFound))
}

// sealGarminIdentity encrypts a linkage, binding it to the principal and to the
// record type, exactly as the file store binds a token record.
func (s *SQLiteStore) sealGarminIdentity(principalID string, identity GarminIdentity) ([]byte, error) {
	if identity.AccountID.IsZero() {
		return nil, fmt.Errorf("store: no garmin account id: %w", ErrInvalidArgument)
	}
	if len(identity.DisplayName) > maxIdentifierLength {
		return nil, fmt.Errorf("store: garmin display name has length %d: %w",
			len(identity.DisplayName), ErrInvalidArgument)
	}
	payload, err := json.Marshal(garminIdentityPayload{
		AccountID:   identity.AccountID.Reveal(),
		DisplayName: identity.DisplayName,
	})
	if err != nil {
		return nil, fmt.Errorf("store: encode garmin identity: %w", err)
	}
	sealed, err := s.crypt.encrypt(principalID, garminIdentityRecordType, payload)
	if err != nil {
		return nil, fmt.Errorf("store: seal garmin identity: %w", err)
	}
	return sealed, nil
}

// GarminIdentity returns the decrypted linkage for a principal. It reports
// ErrPrincipalNotFound when the principal does not exist and ErrNoTokens when no
// account is linked.
func (s *SQLiteStore) GarminIdentity(ctx context.Context, principalID string) (GarminIdentity, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return GarminIdentity{}, err
	}
	var sealed []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT garmin_identity_sealed FROM principals WHERE id = ?`, principalID).Scan(&sealed)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return GarminIdentity{}, fmt.Errorf("store: principal %s does not exist: %w",
			principalID, ErrPrincipalNotFound)
	case err != nil:
		return GarminIdentity{}, fmt.Errorf("store: read garmin identity: %w", err)
	case sealed == nil:
		return GarminIdentity{}, fmt.Errorf("store: principal %s has no linked garmin account: %w",
			principalID, ErrNoTokens)
	}
	return s.openGarminIdentity(principalID, sealed)
}

// openGarminIdentity decrypts and decodes a sealed linkage.
func (s *SQLiteStore) openGarminIdentity(principalID string, sealed []byte) (GarminIdentity, error) {
	opened, _, err := s.crypt.decrypt(principalID, garminIdentityRecordType, sealed)
	if err != nil {
		return GarminIdentity{}, fmt.Errorf("store: open garmin identity: %w: %w", ErrCorruptRecord, err)
	}
	var payload garminIdentityPayload
	if err := json.Unmarshal(opened, &payload); err != nil {
		return GarminIdentity{}, fmt.Errorf("store: decode garmin identity (%d bytes): %w",
			len(opened), ErrCorruptRecord)
	}
	return GarminIdentity{
		AccountID:   NewSecret(payload.AccountID),
		DisplayName: payload.DisplayName,
	}, nil
}
