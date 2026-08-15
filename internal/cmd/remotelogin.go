package cmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/loginweb"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// Bounds on the pending-login registry. They mirror the login transaction's own
// lifetime: an entry exists only between a challenge and the code that answers it.
const (
	// maxPendingLogins bounds the registry, so a flood of MFA challenges cannot
	// grow the process without limit.
	maxPendingLogins = 256
	// pendingLoginTTL is the absolute lifetime of an entry. It is never extended,
	// and the Garmin continuation behind it expires on its own schedule as well.
	pendingLoginTTL = 10 * time.Minute
)

// A principalDirectory resolves, creates and links the principals a remote login
// binds.
//
// The interface lives with its consumer, so this file depends on four operations
// rather than on the whole SQLite store, and a test can supply either.
type principalDirectory interface {
	// PrincipalByGarminAccount returns the principal a Garmin account is linked
	// to, or an error wrapping store.ErrPrincipalNotFound.
	PrincipalByGarminAccount(ctx context.Context, accountID store.Secret) (store.Principal, error)
	// PrincipalByEmail returns the principal registered under a login handle, or
	// an error wrapping store.ErrPrincipalNotFound.
	PrincipalByEmail(ctx context.Context, email string) (store.Principal, error)
	// CreatePrincipal mints a principal for a login handle.
	CreatePrincipal(ctx context.Context, email string) (store.Principal, error)
	// LinkGarminAccount attaches a Garmin account to a principal, refusing an
	// account another principal already owns.
	LinkGarminAccount(ctx context.Context, principalID string, identity store.GarminIdentity) error
}

// remoteLoginDeps is what the seam is assembled from. Every field is required.
type remoteLoginDeps struct {
	// authenticator runs the Garmin login.
	authenticator *auth.Authenticator
	// directory resolves and creates principals.
	directory principalDirectory
	// tokens is the store a committed token set is written to.
	tokens auth.TokenStore
	// staging holds a login's token set until its principal exists.
	staging *stagedTokens
	// gate serializes the commit against a concurrent refresh of the same
	// principal.
	gate *auth.TokenGate
	// now is the clock of the pending-login registry. Nil selects time.Now.
	now func() time.Time
}

// remoteLogin is the adapter between the remote login pages and the Garmin
// authenticator.
//
// It differs from [loginSeam] in exactly one way, and that way is the whole of
// multi-user mode: the account is discovered from the credentials rather than
// bound to the process. Everything else is identical, including the rule that the
// pages hand over two strings for one call and never see a token, a cookie jar, or
// a principal identifier they could choose.
//
// # Order of operations
//
// The login runs first, against a staging key, and the principal is resolved only
// after Garmin has accepted the credentials. That order is the point: a principal
// is the record of an account Garmin confirmed, so a failed login must leave no row
// behind, and the earlier shape — resolve the principal, then log in — let anyone
// who could reach the login page create a row for an email they do not own.
//
// The principal is then found or created by the Garmin account identifier the login
// reported, never by the email. Email is a login handle and a display string: its
// owner can change it, two people can dispute one, and one account reached under
// two spellings is still one tenant.
type remoteLogin struct {
	authenticator *auth.Authenticator
	directory     principalDirectory
	tokens        auth.TokenStore
	staging       *stagedTokens
	gate          *auth.TokenGate
	pending       *pendingLogins
}

// The assertion this type exists for.
var _ loginweb.Authenticator = (*remoteLogin)(nil)

// newRemoteLogin assembles the adapter. Every dependency is required, because each
// missing one would be a silent downgrade of a security property rather than a
// missing convenience.
func newRemoteLogin(deps remoteLoginDeps) (*remoteLogin, error) {
	if deps.authenticator == nil || deps.directory == nil || deps.tokens == nil ||
		deps.staging == nil || deps.gate == nil {
		return nil, errors.New("cmd: the remote login seam is missing a dependency")
	}
	now := deps.now
	if now == nil {
		now = time.Now
	}
	return &remoteLogin{
		authenticator: deps.authenticator,
		directory:     deps.directory,
		tokens:        deps.tokens,
		staging:       deps.staging,
		gate:          deps.gate,
		pending:       newPendingLogins(maxPendingLogins, pendingLoginTTL, now),
	}, nil
}

// Login runs one Garmin login for the account the credentials name.
//
// The credentials are sealed into a request-scoped value, used for this one call,
// and dropped when it returns. The token set the login produced is held in the
// staging area until the account it belongs to has been resolved; a login that
// fails, or that Garmin attributes to no account, writes nothing at all.
func (r *remoteLogin) Login(ctx context.Context, email, password string) (loginweb.Attempt, error) {
	staged, err := r.staging.begin()
	if err != nil {
		return loginweb.Attempt{}, err
	}

	result, err := r.authenticator.Login(ctx, staged, auth.NewCredentials(email, password))
	if err != nil {
		r.staging.drop(staged)
		return loginweb.Attempt{}, err
	}
	return r.attempt(ctx, staged, email, result)
}

// CompleteMFA submits one one-time code against the pending continuation.
//
// The staging key comes from the registry rather than from the request, so a
// continuation cannot be answered on behalf of another login: the capability
// addresses one pending login, and that login owns its own staging area.
func (r *remoteLogin) CompleteMFA(
	ctx context.Context, transactionID, code string,
) (loginweb.Attempt, error) {
	staged, ok := r.pending.get(transactionID)
	if !ok {
		return loginweb.Attempt{}, fmt.Errorf(
			"no pending login addresses that continuation: %w", auth.ErrNoTokens)
	}

	result, err := r.authenticator.CompleteMFA(ctx, transactionID, staged.principal, code)
	if err != nil {
		// The entry is left in place on purpose: a wrong code stays retryable
		// until the attempt budget or the lifetime runs out, and dropping it here
		// would turn a typo into a restart of the whole authorization.
		return loginweb.Attempt{}, err
	}
	r.pending.drop(transactionID)
	return r.attempt(ctx, staged.principal, staged.email, result)
}

// attempt projects a login result onto what the pages need: a pending continuation
// is recorded, and a completed login is bound to its principal.
func (r *remoteLogin) attempt(
	ctx context.Context, staged, email string, result auth.Result,
) (loginweb.Attempt, error) {
	out := attemptOf(result)
	if out.NeedsMFA {
		if err := r.pending.put(out.TransactionID, staged, email); err != nil {
			r.staging.drop(staged)
			return loginweb.Attempt{}, err
		}
		return out, nil
	}

	principal, err := r.bind(ctx, staged, email, result)
	if err != nil {
		r.staging.drop(staged)
		return loginweb.Attempt{}, err
	}
	out.Principal = principal
	return out, nil
}

// bind resolves the principal of a completed login and commits its token set.
//
// It fails closed on an account Garmin did not name: without an account identifier
// there is nothing to key isolation on, and falling back to the email would key it
// on exactly the value the design refuses.
func (r *remoteLogin) bind(
	ctx context.Context, staged, email string, result auth.Result,
) (string, error) {
	account := store.NewSecret(result.GarminAccountID())
	if account.IsZero() {
		return "", fmt.Errorf(
			"the login was accepted but named no garmin account: %w", ErrNoGarminAccount)
	}

	principal, err := r.resolve(ctx, account, email, result.GarminDisplayName())
	if err != nil {
		return "", err
	}
	if err := r.commit(ctx, staged, principal); err != nil {
		return "", err
	}
	return principal, nil
}

// resolve finds the principal the Garmin account belongs to, or creates one.
//
// The lookup is on the account and the creation carries the email, which is the
// division the whole design rests on: the account is the key, the email is the
// handle. A creation that loses the unique-email race reads the winner, and a
// linkage that loses the unique-account race reads the winner too, so two browsers
// logging in at once end with one principal rather than two.
func (r *remoteLogin) resolve(
	ctx context.Context, account store.Secret, email, displayName string,
) (string, error) {
	switch linked, err := r.directory.PrincipalByGarminAccount(ctx, account); {
	case err == nil:
		return linked.ID, nil
	case !errors.Is(err, store.ErrPrincipalNotFound):
		return "", fmt.Errorf("resolving the principal of the garmin account: %w", err)
	}

	principal, err := r.principalFor(ctx, email)
	if err != nil {
		return "", err
	}

	err = r.directory.LinkGarminAccount(ctx, principal,
		store.GarminIdentity{AccountID: account, DisplayName: displayName})
	if errors.Is(err, store.ErrGarminAccountLinked) {
		return r.linkedElsewhere(ctx, account)
	}
	if err != nil {
		return "", fmt.Errorf("linking the garmin account: %w", err)
	}
	return principal, nil
}

// principalFor returns the principal registered under a login handle, creating one
// when the handle is new.
func (r *remoteLogin) principalFor(ctx context.Context, email string) (string, error) {
	created, err := r.directory.CreatePrincipal(ctx, email)
	if err == nil {
		return created.ID, nil
	}
	if !errors.Is(err, store.ErrPrincipalExists) {
		return "", fmt.Errorf("creating the principal: %w", err)
	}

	existing, err := r.directory.PrincipalByEmail(ctx, email)
	if err != nil {
		return "", fmt.Errorf("resolving the principal for a registered handle: %w", err)
	}
	return existing.ID, nil
}

// linkedElsewhere reads the principal that won a concurrent linkage of the same
// Garmin account. One Garmin account is one principal, so the loser of that race
// uses the winner rather than creating a second tenant.
func (r *remoteLogin) linkedElsewhere(ctx context.Context, account store.Secret) (string, error) {
	winner, err := r.directory.PrincipalByGarminAccount(ctx, account)
	if err != nil {
		return "", fmt.Errorf("resolving the principal after a concurrent linkage: %w", err)
	}
	return winner.ID, nil
}

// commit moves the staged token set onto the resolved principal.
//
// It runs under the same token gate the authenticator and the refresher share, so
// this write queues behind a concurrent refresh of the same principal instead of
// racing it: a returning account may already hold a token set that a refresh is
// rotating right now, and an unserialized write here would overwrite a refresh
// token it never saw.
func (r *remoteLogin) commit(ctx context.Context, staged, principal string) error {
	set, err := r.staging.take(staged)
	if err != nil {
		return fmt.Errorf("reading what the login produced: %w", err)
	}

	release, err := r.gate.Acquire(ctx, principal)
	if err != nil {
		return fmt.Errorf("awaiting the token gate: %w", err)
	}
	defer release()

	baseline, err := r.storedVersion(ctx, principal)
	if err != nil {
		return err
	}
	if _, err := r.tokens.Save(ctx, principal, set, baseline); err != nil {
		return fmt.Errorf("storing what the login produced: %w", err)
	}
	return nil
}

// storedVersion reports the principal's current token version, or zero when
// nothing is stored.
func (r *remoteLogin) storedVersion(ctx context.Context, principal string) (int64, error) {
	_, version, err := r.tokens.Load(ctx, principal)
	switch {
	case err == nil:
		return version, nil
	case errors.Is(err, auth.ErrNoTokens):
		return 0, nil
	default:
		return 0, fmt.Errorf("reading the stored token set: %w", err)
	}
}
