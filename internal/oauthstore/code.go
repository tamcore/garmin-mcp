package oauthstore

import (
	"context"

	"github.com/tamcore/garmin-mcp/internal/oauthserver"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// Single-use authorization codes.
//
// The bindings are the record: a code that leaks is useless without the client id,
// the exact redirect URI and the PKCE challenge it was issued against, so every one
// of them is written and re-validated on the way back out.
//
// The store requires a non-empty audience, so a code minted for the zero resource
// cannot be stored. That is a real constraint rather than a translation gap: an
// access token with no audience is a token valid everywhere.

// SaveCode stores a freshly issued code, with the instants the caller stamped on it
// rather than a lifetime re-derived from the store's clock.
func (a *Adapter) SaveCode(ctx context.Context, code oauthserver.AuthorizationCode) error {
	const op = "save authorization code"
	handle, err := material(op, code.Lookup)
	if err != nil {
		return err
	}
	return translate(op, a.sqlite.PutAuthCode(ctx, store.AuthCodeDraft{
		Code:          handle,
		PrincipalID:   principalIDOf(code.Principal),
		ClientID:      code.ClientID,
		RedirectURI:   code.RedirectURI.String(),
		Audience:      code.Resource.String(),
		Scopes:        code.Scopes.Strings(),
		CodeChallenge: code.Challenge.Value(),
		IssuedAt:      code.IssuedAt,
		ExpiresAt:     code.ExpiresAt,
	}))
}

// ConsumeCode atomically marks the code used and returns it.
//
// Exactly one of any number of concurrent redemptions succeeds; every other reports
// ErrCodeAlreadyUsed. An unknown digest reports ErrCodeNotFound, and an expired code
// reports ErrCodeExpired alongside ErrCodeNotFound, so expiry never becomes an
// oracle for whether a code ever existed.
func (a *Adapter) ConsumeCode(ctx context.Context, lookup oauthserver.Lookup,
) (oauthserver.AuthorizationCode, error) {
	const op = "consume authorization code"
	handle, err := material(op, lookup)
	if err != nil {
		return oauthserver.AuthorizationCode{}, err
	}
	stored, err := a.sqlite.ConsumeAuthCode(ctx, handle)
	if err != nil {
		return oauthserver.AuthorizationCode{}, translate(op, err)
	}
	return codeFrom(op, lookup, stored)
}

// codeFrom rebuilds a validated code from a row, re-attaching the digest the store
// does not keep.
func codeFrom(op string, lookup oauthserver.Lookup, stored store.AuthCode,
) (oauthserver.AuthorizationCode, error) {
	redirect, err := redirectOf(op, stored.RedirectURI)
	if err != nil {
		return oauthserver.AuthorizationCode{}, err
	}
	scopes, err := scopesOf(op, stored.Scopes)
	if err != nil {
		return oauthserver.AuthorizationCode{}, err
	}
	resource, err := resourceOf(op, stored.Audience)
	if err != nil {
		return oauthserver.AuthorizationCode{}, err
	}
	challenge, err := challengeOf(op, stored.CodeChallenge)
	if err != nil {
		return oauthserver.AuthorizationCode{}, err
	}
	holder, err := principalOf(op, stored.PrincipalID)
	if err != nil {
		return oauthserver.AuthorizationCode{}, err
	}
	return oauthserver.AuthorizationCode{
		Lookup:      lookup,
		ClientID:    stored.ClientID,
		RedirectURI: redirect,
		Scopes:      scopes,
		Resource:    resource,
		Challenge:   challenge,
		Principal:   holder,
		IssuedAt:    stored.IssuedAt,
		ExpiresAt:   stored.ExpiresAt,
	}, nil
}
