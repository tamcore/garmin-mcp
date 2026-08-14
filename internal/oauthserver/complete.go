package oauthserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/tamcore/garmin-mcp/internal/identity"
)

// A Completion is a terminal authorization outcome: the URL the browser is sent to.
//
// It is always the client's exact registered redirect URI, validated when the
// transaction was opened, with the client's state echoed byte for byte.
type Completion struct {
	RedirectTo string
}

// Transaction returns the live transaction a capability addresses.
//
// It is what internal/loginweb calls to render the disclosure, the credential form
// and the consent page. A capability that matches nothing, a transaction that has
// expired and a transaction that has already completed are all reported the same
// way — ErrTransactionNotFound or ErrTransactionExpired, with no account information
// — because discoverability is not a security boundary.
func (s *Server) Transaction(ctx context.Context, capability Secret) (Transaction, error) {
	if capability.IsZero() {
		return Transaction{}, fmt.Errorf("no capability presented: %w", ErrTransactionNotFound)
	}
	lookup := capability.Lookup()
	tx, err := s.store.Transaction(ctx, lookup)
	if err != nil {
		return Transaction{}, storageOrCause(err, ErrTransactionNotFound)
	}
	if tx.IsExpired(s.now()) {
		// An expired transaction is discarded on sight, so a terminal transaction stops
		// being addressable at the first request that notices. The record it returns is
		// of no interest here; only the deletion is.
		if _, err := s.store.ConsumeTransaction(ctx, lookup); err != nil &&
			!errors.Is(err, ErrTransactionNotFound) {
			return Transaction{}, storageOrCause(err)
		}
		return Transaction{}, fmt.Errorf("transaction expired: %w", ErrTransactionExpired)
	}
	return tx, nil
}

// AttachPrincipal records the principal a completed Garmin login resolved to.
//
// It advances the transaction from [StagePending] to [StageAuthenticated] with a
// compare-and-set, so a second login racing on the same transaction loses instead of
// overwriting the first. The Garmin login itself, and the DI token set it produced,
// are internal/garmin/auth's business: this package only learns which principal the
// rest of the flow is about.
func (s *Server) AttachPrincipal(
	ctx context.Context, capability Secret, principal identity.Principal,
) (Transaction, error) {
	if !principal.IsValid() {
		return Transaction{}, fmt.Errorf(
			"a transaction cannot be bound to an unresolved principal: %w", identity.ErrNoPrincipal)
	}
	tx, err := s.Transaction(ctx, capability)
	if err != nil {
		return Transaction{}, err
	}
	if tx.Stage != StagePending {
		return Transaction{}, fmt.Errorf(
			"transaction is %s, not pending: %w", tx.Stage, ErrTransactionStage)
	}
	advanced := tx
	advanced.Stage = StageAuthenticated
	advanced.Principal = principal
	if err := s.store.UpdateTransaction(ctx, advanced, tx.Version); err != nil {
		return Transaction{}, storageOrCause(err, ErrTransactionConflict, ErrTransactionNotFound)
	}
	advanced.Version = tx.Version + 1
	return advanced, nil
}

// ConsentRequired reports whether the user has to be asked again.
//
// Consent is looked up by the exact tuple (principal, client id, exact redirect URI,
// resource) and then checked for scope containment. A changed redirect URI or a
// changed resource is a different key and finds nothing; a widened scope finds the
// record and fails containment. Both therefore require a fresh decision, which is the
// confused-deputy mitigation: a client cannot reuse a sticky consent to obtain
// something the user never agreed to.
func (s *Server) ConsentRequired(ctx context.Context, capability Secret) (bool, error) {
	tx, err := s.Transaction(ctx, capability)
	if err != nil {
		return true, err
	}
	if tx.Stage != StageAuthenticated {
		return true, fmt.Errorf(
			"consent cannot be evaluated at stage %s: %w", tx.Stage, ErrTransactionStage)
	}
	consent, err := s.store.Consent(ctx, consentKeyOf(tx))
	if errors.Is(err, ErrConsentNotFound) {
		return true, nil
	}
	if err != nil {
		return true, storageOrCause(err)
	}
	return !consent.Covers(tx.Scopes), nil
}

func consentKeyOf(tx Transaction) ConsentKey {
	return ConsentKey{
		Principal:   tx.Principal,
		ClientID:    tx.ClientID,
		RedirectURI: tx.RedirectURI,
		Resource:    tx.Resource,
	}
}

// GrantConsent records the user's consent and issues the authorization code.
//
// The sequence matters. The transaction is consumed first, so exactly one of any
// number of concurrent submissions proceeds and the capability, the cookie and the
// consent form all become useless immediately. Consent is then persisted before the
// code is issued, so a code never exists without the consent that justifies it, and a
// failure anywhere after the claim leaves no code at all.
//
// The code is bound to the client, the exact redirect URI, the PKCE challenge, the
// resource, the scopes and the principal. Every one of those bindings is verified
// again at the token endpoint.
func (s *Server) GrantConsent(ctx context.Context, capability Secret) (Completion, error) {
	tx, err := s.claimAuthenticatedTransaction(ctx, capability)
	if err != nil {
		return Completion{}, err
	}
	now := s.now()
	consent := Consent{Key: consentKeyOf(tx), Scopes: tx.Scopes, GrantedAt: now}
	if err := s.store.SaveConsent(ctx, consent); err != nil {
		return Completion{}, storageOrCause(err)
	}
	code, err := NewSecret()
	if err != nil {
		return Completion{}, fmt.Errorf("issuing an authorization code: %w", err)
	}
	record := AuthorizationCode{
		Lookup:      code.Lookup(),
		ClientID:    tx.ClientID,
		RedirectURI: tx.RedirectURI,
		Scopes:      tx.Scopes,
		Resource:    tx.Resource,
		Challenge:   tx.Challenge,
		Principal:   tx.Principal,
		IssuedAt:    now,
		ExpiresAt:   now.Add(s.codeTTL),
	}
	if err := s.store.SaveCode(ctx, record); err != nil {
		return Completion{}, storageOrCause(err)
	}
	return completionFor(tx, map[string]string{paramCode: code.Reveal()})
}

// DenyAuthorization ends the transaction with an access_denied redirect.
//
// Nothing is persisted: no consent, no code. Any Garmin tokens the login produced
// were held only in the transaction, so deleting it discards them, which is what the
// brief requires on denial or expiry.
func (s *Server) DenyAuthorization(ctx context.Context, capability Secret) (Completion, error) {
	tx, err := s.claimAuthenticatedTransaction(ctx, capability)
	if err != nil {
		return Completion{}, err
	}
	return completionFor(tx, map[string]string{
		paramError:            ErrorAccessDenied,
		paramErrorDescription: "The user did not authorize this client.",
	})
}

// claimAuthenticatedTransaction takes exclusive ownership of a transaction that has a
// resolved principal, by consuming it.
//
// The stage is checked before the claim, so a transaction whose login has not
// finished is left intact rather than destroyed by a premature consent submission.
// After that check, the atomic consume is what makes completion single-use: a second
// concurrent caller finds nothing and issues nothing. A compare-and-set on the version
// would not be enough, because two submissions can serialize so that each wins its own
// compare-and-set in turn.
func (s *Server) claimAuthenticatedTransaction(
	ctx context.Context, capability Secret,
) (Transaction, error) {
	tx, err := s.Transaction(ctx, capability)
	if err != nil {
		return Transaction{}, err
	}
	if tx.Stage != StageAuthenticated || !tx.Principal.IsValid() {
		return Transaction{}, fmt.Errorf(
			"transaction is %s, not authenticated: %w", tx.Stage, ErrTransactionStage)
	}
	claimed, err := s.store.ConsumeTransaction(ctx, tx.Lookup)
	if err != nil {
		return Transaction{}, storageOrCause(err, ErrTransactionNotFound)
	}
	return claimed, nil
}

// completionFor builds the redirect, adding the client's state byte for byte.
func completionFor(tx Transaction, params map[string]string) (Completion, error) {
	if !tx.State.IsZero() {
		params[paramState] = tx.State.Reveal()
	}
	location, err := tx.RedirectURI.WithParams(params)
	if err != nil {
		return Completion{}, err
	}
	return Completion{RedirectTo: location}, nil
}
