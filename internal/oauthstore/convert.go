package oauthstore

import (
	"strings"

	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/oauthserver"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// Conversions between the two vocabularies. Every one of them is total in one
// direction and validating in the other: a value going into the store is already
// validated, and a value coming out of a column is re-validated, because a column
// can be corrupt and this package is the only place that could notice.

// material renders a lookup as the opaque handle the store hashes. The hex digest
// is used on every write and every read, so the store's own keyed lookup is a
// deterministic function of it.
func material(op string, lookup oauthserver.Lookup) (store.Secret, error) {
	if lookup.IsZero() {
		return store.Secret{}, invalidLookup(op)
	}
	return store.NewSecret(lookup.Hex()), nil
}

// principalIDOf renders a principal for a column. The zero principal is the empty
// string, which is what "not yet identified" is in the schema.
func principalIDOf(principal identity.Principal) string {
	if !principal.IsValid() {
		return ""
	}
	return principal.ID()
}

// principalOf rebuilds a principal from a column. An empty id is the zero
// principal rather than an error.
func principalOf(op, id string) (identity.Principal, error) {
	if id == "" {
		return identity.Principal{}, nil
	}
	principal, err := identity.NewPrincipal(id)
	if err != nil {
		return identity.Principal{}, corrupt(op, err)
	}
	return principal, nil
}

// stageOf derives how far a transaction has got. The schema has no stage column,
// and it needs none: a resolved principal is exactly what authenticated means.
func stageOf(principalID string) oauthserver.TransactionStage {
	if principalID == "" {
		return oauthserver.StagePending
	}
	return oauthserver.StageAuthenticated
}

// scopesOf rebuilds a scope set from a stored list.
func scopesOf(op string, scopes []string) (oauthserver.ScopeSet, error) {
	set, err := oauthserver.ParseScopeSet(strings.Join(scopes, " "))
	if err != nil {
		return oauthserver.ScopeSet{}, corrupt(op, err)
	}
	return set, nil
}

// resourceOf rebuilds a resource indicator. An empty column is a request that
// named no resource, which is the zero Resource and not an error.
func resourceOf(op, raw string) (oauthserver.Resource, error) {
	if raw == "" {
		return oauthserver.Resource{}, nil
	}
	resource, err := oauthserver.ParseResource(raw)
	if err != nil {
		return oauthserver.Resource{}, corrupt(op, err)
	}
	return resource, nil
}

// redirectOf rebuilds a redirect URI. Unlike a resource it is never empty on a
// stored record, so an empty column is a corrupt row.
func redirectOf(op, raw string) (oauthserver.RedirectURI, error) {
	uri, err := oauthserver.ParseRedirectURI(raw)
	if err != nil {
		return oauthserver.RedirectURI{}, corrupt(op, err)
	}
	return uri, nil
}

// challengeOf rebuilds a PKCE challenge. The schema constrains the method column to
// S256, so the method is not read back: no other value can be stored.
func challengeOf(op, raw string) (oauthserver.CodeChallenge, error) {
	challenge, err := oauthserver.ParseCodeChallenge(string(oauthserver.MethodS256), raw)
	if err != nil {
		return oauthserver.CodeChallenge{}, corrupt(op, err)
	}
	return challenge, nil
}
