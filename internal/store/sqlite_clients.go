package store

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Registered OAuth clients and their exact redirect URIs.
//
// Redirect URI matching is exact string equality against a registered row. There is
// no prefix rule, no wildcard, no normalization at match time and no
// query-parameter tolerance, because every one of those is a redirect-URI validation
// bypass: a prefix rule accepts https://client.example/callback.attacker.test, and
// normalizing at match time lets two spellings of one URI disagree between the check
// and the redirect. Validation happens once, at registration, and the stored
// spelling is the only accepted one.

// maxRedirectURIs bounds how many URIs one client may register.
const maxRedirectURIs = 16

// maxRedirectURILength bounds one URI.
const maxRedirectURILength = 2048

// Client is a registered MCP client.
type Client struct {
	// ID is the client_id. It is a random UUID, not a caller-chosen string, so a
	// registration cannot squat the id another client uses.
	ID string

	// Name is the display name shown on the consent screen. It is caller-supplied
	// text, so a caller must escape it before rendering.
	Name string

	// IsPublic reports whether the client authenticates with no secret. A public
	// client relies on PKCE alone.
	IsPublic bool

	// RedirectURIs are the exact URIs this client may be redirected to, in the
	// order registered. The slice is a fresh copy on every read.
	RedirectURIs []string

	CreatedAt time.Time
}

// There is deliberately no Disabled field. A disabled client is never returned: every
// lookup reports ErrClientNotFound instead. A boolean here would be a field that can
// only ever be false, and the first caller to trust it rather than the error would
// authorize a client an operator had switched off.

// HasRedirectURI reports whether uri is registered, comparing exactly.
func (c Client) HasRedirectURI(uri string) bool {
	return slices.Contains(c.RedirectURIs, uri)
}

// ClientRegistration is the input to RegisterClient.
type ClientRegistration struct {
	// Name is the display name. Required.
	Name string

	// RedirectURIs must hold at least one absolute https URI, or an http URI on a
	// loopback host, with no fragment. Required.
	RedirectURIs []string

	// Secret is the client secret. The zero Secret registers a public client. The
	// secret is never stored: only its keyed-HMAC lookup value is.
	Secret Secret
}

// RegisterClient stores a client and its redirect URIs in one transaction, so a
// client can never exist with a partial URI list.
func (s *SQLiteStore) RegisterClient(ctx context.Context, reg ClientRegistration) (Client, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return Client{}, err
	}
	if err := checkIdentifier("client name", reg.Name); err != nil {
		return Client{}, err
	}
	uris, err := checkRedirectURIs(reg.RedirectURIs)
	if err != nil {
		return Client{}, err
	}
	id, err := newPrincipalID()
	if err != nil {
		return Client{}, err
	}

	client := Client{
		ID:           id,
		Name:         reg.Name,
		IsPublic:     reg.Secret.IsZero(),
		RedirectURIs: uris,
		CreatedAt:    s.now().UTC(),
	}
	secretHash := nullableString(s.keys.lookup(purposeClientSecret, reg.Secret))
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		return insertClient(ctx, tx, client, secretHash)
	})
	if err != nil {
		return Client{}, err
	}
	return client, nil
}

// insertClient is the transactional body of RegisterClient.
func insertClient(ctx context.Context, tx *sql.Tx, client Client, secretHash sql.NullString) error {
	isPublic := 0
	if client.IsPublic {
		isPublic = 1
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO oauth_clients (id, name, secret_hash, is_public, created_at, disabled_at)
		 VALUES (?, ?, ?, ?, ?, NULL)`,
		client.ID, client.Name, secretHash, isPublic, formatTime(client.CreatedAt))
	if err != nil {
		return fmt.Errorf("store: insert client: %w", err)
	}

	for _, uri := range client.RedirectURIs {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO oauth_client_redirect_uris (client_id, redirect_uri) VALUES (?, ?)`,
			client.ID, uri)
		if err != nil {
			return fmt.Errorf("store: insert redirect uri: %w", err)
		}
	}
	return nil
}

// checkRedirectURIs validates and copies a redirect URI list.
//
// The rules are the ones a redirect URI must satisfy to be safe to send a code to:
// absolute, no fragment, and either https or http on a loopback host, which is the
// exception native clients need. The returned slice is a copy, so a later mutation
// of the caller's slice cannot change what was registered.
func checkRedirectURIs(uris []string) ([]string, error) {
	if len(uris) == 0 {
		return nil, fmt.Errorf("store: a client needs at least one redirect uri: %w", ErrInvalidArgument)
	}
	if len(uris) > maxRedirectURIs {
		return nil, fmt.Errorf("store: %d redirect uris, over the %d bound: %w",
			len(uris), maxRedirectURIs, ErrInvalidArgument)
	}

	checked := make([]string, 0, len(uris))
	for _, uri := range uris {
		if err := checkRedirectURI(uri); err != nil {
			return nil, err
		}
		if slices.Contains(checked, uri) {
			return nil, fmt.Errorf("store: redirect uri %q appears twice: %w", uri, ErrInvalidArgument)
		}
		checked = append(checked, uri)
	}
	return checked, nil
}

func checkRedirectURI(uri string) error {
	if uri == "" || len(uri) > maxRedirectURILength {
		return fmt.Errorf("store: redirect uri has length %d: %w", len(uri), ErrInvalidArgument)
	}
	parsed, err := url.Parse(uri)
	if err != nil {
		return fmt.Errorf("store: redirect uri %q does not parse: %w: %w", uri, ErrInvalidArgument, err)
	}
	switch {
	case parsed.Fragment != "" || strings.Contains(uri, "#"):
		return fmt.Errorf("store: redirect uri %q carries a fragment: %w", uri, ErrInvalidArgument)
	case parsed.Host == "":
		return fmt.Errorf("store: redirect uri %q is not absolute: %w", uri, ErrInvalidArgument)
	case parsed.Scheme == "https":
		return nil
	case parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname()):
		return nil
	}
	return fmt.Errorf("store: redirect uri %q must be https, or http on a loopback host: %w",
		uri, ErrInvalidArgument)
}

// isLoopbackHost reports whether host is a loopback name a native client may use.
func isLoopbackHost(host string) bool {
	switch host {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	return false
}

// ClientByID returns an enabled client and its exact redirect URIs. A disabled or
// unknown client reports ErrClientNotFound, so the two are indistinguishable to a
// caller and neither can be authorized.
func (s *SQLiteStore) ClientByID(ctx context.Context, id string) (Client, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return Client{}, err
	}
	client, _, err := s.readClient(ctx, id)
	return client, err
}

// readClient returns the client and its stored secret hash.
func (s *SQLiteStore) readClient(ctx context.Context, id string) (Client, string, error) {
	var (
		name        string
		secretHash  sql.NullString
		isPublic    int
		createdText string
		disabledAt  sql.NullString
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT name, secret_hash, is_public, created_at, disabled_at FROM oauth_clients WHERE id = ?`,
		id).Scan(&name, &secretHash, &isPublic, &createdText, &disabledAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Client{}, "", fmt.Errorf("store: client %s is unknown: %w", id, ErrClientNotFound)
	case err != nil:
		return Client{}, "", fmt.Errorf("store: read client: %w", err)
	case disabledAt.Valid:
		return Client{}, "", fmt.Errorf("store: client %s is disabled: %w", id, ErrClientNotFound)
	}

	createdAt, err := parseTime(createdText)
	if err != nil {
		return Client{}, "", err
	}
	uris, err := s.readRedirectURIs(ctx, id)
	if err != nil {
		return Client{}, "", err
	}
	return Client{
		ID:           id,
		Name:         name,
		IsPublic:     isPublic == 1,
		RedirectURIs: uris,
		CreatedAt:    createdAt,
	}, secretHash.String, nil
}

// readRedirectURIs returns a client's URIs in registration order.
func (s *SQLiteStore) readRedirectURIs(ctx context.Context, id string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT redirect_uri FROM oauth_client_redirect_uris WHERE client_id = ? ORDER BY rowid`, id)
	if err != nil {
		return nil, fmt.Errorf("store: read redirect uris: %w", err)
	}
	defer func() { _ = rows.Close() }()

	uris := []string{}
	for rows.Next() {
		var uri string
		if err := rows.Scan(&uri); err != nil {
			return nil, fmt.Errorf("store: scan redirect uri: %w", err)
		}
		uris = append(uris, uri)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate redirect uris: %w", err)
	}
	return uris, nil
}

// AuthenticateClient checks a confidential client's secret in constant time.
//
// A public client is authenticated by presenting no secret; presenting one for a
// public client, or the wrong one for a confidential client, reports
// ErrClientNotFound. The three cases share one error on purpose: distinguishing "no
// such client" from "wrong secret" is a client-enumeration oracle.
func (s *SQLiteStore) AuthenticateClient(ctx context.Context, id string, secret Secret) (Client, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return Client{}, err
	}
	client, storedHash, err := s.readClient(ctx, id)
	if err != nil {
		return Client{}, err
	}

	presented := s.keys.lookup(purposeClientSecret, secret)
	if client.IsPublic {
		if presented != "" {
			return Client{}, fmt.Errorf("store: public client %s presented a secret: %w",
				id, ErrClientNotFound)
		}
		return client, nil
	}
	// hmac.Equal keeps the comparison constant time. Comparing the hex strings with
	// == would leak their common prefix through timing, and this comparison gates
	// client authentication.
	if presented == "" || len(presented) != len(storedHash) ||
		!hmac.Equal([]byte(presented), []byte(storedHash)) {
		return Client{}, fmt.Errorf("store: client %s failed authentication: %w", id, ErrClientNotFound)
	}
	return client, nil
}

// CheckRedirectURI reports whether the client may be redirected to uri, comparing
// exactly. It reports ErrRedirectURIMismatch when the URI is not registered.
func (s *SQLiteStore) CheckRedirectURI(ctx context.Context, clientID, uri string) error {
	client, err := s.ClientByID(ctx, clientID)
	if err != nil {
		return err
	}
	if !client.HasRedirectURI(uri) {
		return fmt.Errorf("store: redirect uri is not one of the %d registered for client %s: %w",
			len(client.RedirectURIs), clientID, ErrRedirectURIMismatch)
	}
	return nil
}

// DisableClient switches a client off. It is idempotent: disabling an
// already-disabled client reports no error. Existing token families are not touched
// here; revoke them with RevokeConsent or RevokeTokenFamily.
func (s *SQLiteStore) DisableClient(ctx context.Context, id string) error {
	if err := checkStoreRequest(ctx); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE oauth_clients SET disabled_at = ? WHERE id = ? AND disabled_at IS NULL`,
		formatTime(s.now()), id)
	if err != nil {
		return fmt.Errorf("store: disable client: %w", err)
	}
	changed, err := affectedRows(result)
	if err != nil {
		return err
	}
	if changed == 1 {
		return nil
	}
	return s.confirmClientExists(ctx, id)
}

// confirmClientExists turns "nothing changed" into the right answer: an already
// disabled client is success, an unknown one is ErrClientNotFound.
func (s *SQLiteStore) confirmClientExists(ctx context.Context, id string) error {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM oauth_clients WHERE id = ?`, id).Scan(&count)
	if err != nil {
		return fmt.Errorf("store: confirm client: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("store: client %s is unknown: %w", id, ErrClientNotFound)
	}
	return nil
}
