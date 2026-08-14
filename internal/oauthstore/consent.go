package oauthstore

import (
	"context"

	"github.com/tamcore/garmin-mcp/internal/oauthserver"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// Consent, on the exact tuple.
//
// Both sides key consent on the principal, the client, the exact redirect URI and
// the resource, so the mapping is field for field. Nothing here widens a key: an
// empty redirect URI or an empty resource is a distinct key, never a wildcard over
// the non-empty ones, which is what makes a changed redirect URI force a fresh
// decision rather than reuse someone else's.
//
// A family written before the consent key was widened carries an empty resource and
// therefore sits under its own key. RevokeConsent revokes the key it is given and no
// other; reaching such a family means revoking its own key, or revoking the
// principal.

// Consent returns the consent for the exact tuple.
func (a *Adapter) Consent(ctx context.Context, key oauthserver.ConsentKey,
) (oauthserver.Consent, error) {
	const op = "read consent"
	stored, err := a.sqlite.ConsentFor(ctx, consentKeyOf(key))
	if err != nil {
		return oauthserver.Consent{}, translate(op, err)
	}
	scopes, err := scopesOf(op, stored.Scopes)
	if err != nil {
		return oauthserver.Consent{}, err
	}
	return oauthserver.Consent{Key: key, Scopes: scopes, GrantedAt: stored.GrantedAt}, nil
}

// SaveConsent stores or replaces the consent for its tuple. Re-granting clears an
// earlier revocation, which is what a user re-approving a consent screen means.
func (a *Adapter) SaveConsent(ctx context.Context, consent oauthserver.Consent) error {
	const op = "save consent"
	return translate(op, a.sqlite.GrantConsentFor(ctx,
		consentKeyOf(consent.Key), consent.Scopes.Strings()))
}

// RevokeConsent deletes the consent and, in the same transaction, revokes every
// token family issued for that principal, client and resource, and deletes the
// pending authorization state under the same key. It is idempotent and it fails
// closed: the store re-counts what must be gone before it commits.
func (a *Adapter) RevokeConsent(ctx context.Context, key oauthserver.ConsentKey) error {
	const op = "revoke consent"
	_, err := a.sqlite.RevokeConsentFor(ctx, consentKeyOf(key))
	return translate(op, err)
}

// consentKeyOf renders the tuple for the store. The zero principal, the zero
// redirect URI and the zero resource all render as the empty string, which is the
// key a grant recorded without them is stored under.
func consentKeyOf(key oauthserver.ConsentKey) store.ConsentKey {
	return store.ConsentKey{
		PrincipalID: principalIDOf(key.Principal),
		ClientID:    key.ClientID,
		RedirectURI: key.RedirectURI.String(),
		Resource:    key.Resource.String(),
	}
}
