package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"
)

// Reconciliation of the operator-registered clients.
//
// This is the second, privileged half of client registration and it exists because
// the two halves live in two places: configuration holds the OAuth policy — the
// scope bound, the resource indicators and the secret digest — and the database
// holds the identity and the exact redirect URIs an authorization transaction
// references by foreign key. Without a way to write a row under the operator's own
// identifier, a configured client can open no transaction and a fresh deployment
// can never become usable.
//
// It is deliberately not RegisterClient with an id field. RegisterClient mints the
// identifier so that a registration cannot squat the id another client uses, and
// that rule is what makes it safe for anything less privileged than the composition
// root. Reconciliation is the operator path: the id is the operator's choice, the
// row is made to equal the configuration, and nothing about it is reachable from a
// request.

// ClientReconciliation is the desired state of one operator-registered client.
//
// The whole value is the desired state: every field is applied, and a field that
// changed in configuration changes the row. Nothing is merged, because a merge
// would let a redirect URI an operator withdrew survive in the database.
type ClientReconciliation struct {
	// ID is the operator's client identifier and the natural key. Required.
	ID string

	// Name is the display name shown on the consent screen. It is operator text,
	// so a caller must escape it before rendering. Required.
	Name string

	// RedirectURIs are the exact URIs this client may be redirected to, in the
	// order configured. They replace whatever the row held. Required.
	RedirectURIs []string

	// IsPublic reports whether the client authenticates with no secret, exactly as
	// configuration says. It is applied in both directions.
	IsPublic bool
}

// ReconcileClient makes the row for spec.ID equal spec, creating it when it does
// not exist, and returns the resulting client.
//
// It is idempotent: running it again with the same spec reaches the same state,
// reports no error, and leaves created_at where it was, so a restart does not look
// like a fresh registration.
//
// Two refusals are the point of the call:
//
//   - A disabled client is never re-enabled. It reports ErrClientDisabled, which a
//     composition root turns into a start-up failure, so an operator who switched a
//     client off cannot have it switched back on by a restart.
//   - The row carries no client secret. A confidential client's credential is
//     verified against the operator's configured digest by the authorization
//     server, so the store keeps secret_hash NULL: AuthenticateClient can
//     authenticate nobody against a reconciled confidential client, which is the
//     fail-closed direction.
//
// Everything else follows configuration, in both directions, including the public
// flag: a capability is only ever as wide as the spec, and the spec is what the
// operator wrote.
func (s *SQLiteStore) ReconcileClient(ctx context.Context, spec ClientReconciliation) (Client, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return Client{}, err
	}
	desired, err := checkReconciliation(spec)
	if err != nil {
		return Client{}, err
	}

	var client Client
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		applied, err := s.applyReconciliation(ctx, tx, desired)
		client = applied
		return err
	})
	if err != nil {
		return Client{}, err
	}
	return client, nil
}

// checkReconciliation validates a spec and returns it with a copied URI list, so a
// later mutation of the caller's slice cannot change what was reconciled.
func checkReconciliation(spec ClientReconciliation) (ClientReconciliation, error) {
	if err := checkIdentifier("client id", spec.ID); err != nil {
		return ClientReconciliation{}, err
	}
	if err := checkIdentifier("client name", spec.Name); err != nil {
		return ClientReconciliation{}, err
	}
	uris, err := checkRedirectURIs(spec.RedirectURIs)
	if err != nil {
		return ClientReconciliation{}, err
	}
	return ClientReconciliation{
		ID:           spec.ID,
		Name:         spec.Name,
		RedirectURIs: uris,
		IsPublic:     spec.IsPublic,
	}, nil
}

// applyReconciliation is the transactional body of ReconcileClient.
func (s *SQLiteStore) applyReconciliation(ctx context.Context, tx *sql.Tx,
	spec ClientReconciliation,
) (Client, error) {
	createdAt, exists, err := reconcileTarget(ctx, tx, spec.ID)
	if err != nil {
		return Client{}, err
	}

	client := Client{
		ID:           spec.ID,
		Name:         spec.Name,
		IsPublic:     spec.IsPublic,
		RedirectURIs: slices.Clone(spec.RedirectURIs),
		CreatedAt:    createdAt,
	}
	if !exists {
		client.CreatedAt = s.now().UTC()
		if err := insertClient(ctx, tx, client, sql.NullString{}); err != nil {
			return Client{}, err
		}
		return client, nil
	}
	if err := updateClientIdentity(ctx, tx, client); err != nil {
		return Client{}, err
	}
	if err := replaceRedirectURIs(ctx, tx, client); err != nil {
		return Client{}, err
	}
	return client, nil
}

// reconcileTarget reports the existing row's creation time, whether it exists at
// all, and refuses a disabled one.
func reconcileTarget(ctx context.Context, tx *sql.Tx, id string) (time.Time, bool, error) {
	var (
		createdText string
		disabledAt  sql.NullString
	)
	err := tx.QueryRowContext(ctx,
		`SELECT created_at, disabled_at FROM oauth_clients WHERE id = ?`, id).
		Scan(&createdText, &disabledAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return time.Time{}, false, nil
	case err != nil:
		return time.Time{}, false, fmt.Errorf("store: read client for reconciliation: %w", err)
	case disabledAt.Valid:
		return time.Time{}, false, fmt.Errorf(
			"store: client %s is disabled and a reconciliation does not re-enable it: %w",
			id, ErrClientDisabled)
	}

	parsed, err := parseTime(createdText)
	if err != nil {
		return time.Time{}, false, err
	}
	return parsed, true, nil
}

// updateClientIdentity applies the name and the public flag.
func updateClientIdentity(ctx context.Context, tx *sql.Tx, client Client) error {
	isPublic := 0
	if client.IsPublic {
		isPublic = 1
	}
	_, err := tx.ExecContext(ctx,
		`UPDATE oauth_clients SET name = ?, secret_hash = NULL, is_public = ?
		  WHERE id = ? AND disabled_at IS NULL`,
		client.Name, isPublic, client.ID)
	if err != nil {
		return fmt.Errorf("store: reconcile client: %w", err)
	}
	return nil
}

// replaceRedirectURIs makes the stored URI list equal the configured one.
//
// The stored rows are deleted first and the configured ones re-inserted in order,
// so the stored order is the configured order and a URI an operator removed stops
// being a redirect target in the same transaction.
func replaceRedirectURIs(ctx context.Context, tx *sql.Tx, client Client) error {
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM oauth_client_redirect_uris WHERE client_id = ?`, client.ID); err != nil {
		return fmt.Errorf("store: withdraw redirect uris: %w", err)
	}
	for _, uri := range client.RedirectURIs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO oauth_client_redirect_uris (client_id, redirect_uri) VALUES (?, ?)`,
			client.ID, uri); err != nil {
			return fmt.Errorf("store: insert redirect uri: %w", err)
		}
	}
	return nil
}
