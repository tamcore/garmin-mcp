package oauthserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/tamcore/garmin-mcp/internal/identity"
)

// maxRevocationBody bounds the form body of the revocation endpoint. Nothing legitimate
// comes anywhere near it.
const maxRevocationBody = 8 << 10

// A RevokeRequest is an RFC 7009 revocation request.
//
// TokenTypeHint is advisory and this server ignores it: the token is looked up as both a
// refresh token and an access token regardless, which RFC 7009 §2.1 permits and which
// keeps a wrong hint from silently failing to revoke.
type RevokeRequest struct {
	ClientID      string
	ClientSecret  Secret
	Token         string
	TokenTypeHint string
}

// RevokeConsent withdraws a client's consent for one principal.
//
// The cascade belongs to the storage layer and happens in one transaction: the consent
// row and every token family issued for that principal, client and resource go together.
// Doing it in two steps would leave a window in which the consent is gone but a live
// access token is still accepted.
//
// It is idempotent, so a user clicking twice, or a retry after a network failure, is not
// an error. It fails closed: a storage failure is reported rather than swallowed, and the
// caller must assume nothing was revoked.
//
// Closing the client's active transport sessions is internal/mcpserver's part of this
// cascade. It is out of scope here because this package holds no sessions; revoking the
// tokens is what makes those sessions unusable on their next request.
func (s *Server) RevokeConsent(ctx context.Context, key ConsentKey) error {
	if !key.Principal.IsValid() {
		return fmt.Errorf("consent cannot be revoked without a principal: %w",
			identity.ErrNoPrincipal)
	}
	if err := s.store.RevokeConsent(ctx, key); err != nil {
		return fmt.Errorf("revoking consent for client %q: %w", key.ClientID, storageOrCause(err))
	}
	return nil
}

// RevokePrincipal revokes everything this package holds for one principal.
//
// It is the OAuth half of unlinking a Garmin account: every token family for the principal
// dies and every consent goes, whichever client obtained it. The other halves — deleting
// the encrypted Garmin tokens, discarding pending login transactions, stopping background
// refresh and evicting cached clients — belong to internal/store, internal/garmin and
// internal/mcpserver, and the caller sequences them.
//
// It is idempotent and fails closed.
func (s *Server) RevokePrincipal(ctx context.Context, principal identity.Principal) error {
	if !principal.IsValid() {
		return fmt.Errorf("no principal to revoke: %w", identity.ErrNoPrincipal)
	}
	if err := s.store.RevokePrincipal(ctx, principal); err != nil {
		return fmt.Errorf("revoking principal %s: %w", principal, storageOrCause(err))
	}
	return nil
}

// RevokeToken implements RFC 7009 for a client revoking its own token.
//
// Revoking any token kills its whole family, which is the only useful meaning here: a
// client signing out should not be left holding a refresh token that still mints access
// tokens.
//
// A token that is unknown, already dead, or issued to a different client is not an error.
// RFC 7009 §2.2 requires that, and the reason is worth stating: an endpoint that answered
// differently for a valid token would confirm guesses. Only a failure that is genuinely
// the client's — an unregistered client id, or failed client authentication — is reported.
func (s *Server) RevokeToken(ctx context.Context, req RevokeRequest) error {
	client, err := s.authenticateRevocationClient(ctx, req)
	if err != nil {
		return err
	}
	presented := SecretFromString(req.Token)
	if presented.IsZero() {
		return nil
	}
	family, ok := s.familyOf(ctx, presented, client)
	if !ok {
		return nil
	}
	if err := s.store.RevokeFamily(ctx, family, RevokeReasonClient); err != nil {
		return fmt.Errorf("revoking token family: %w", storageOrCause(err))
	}
	return nil
}

// authenticateRevocationClient applies the same client authentication as the token
// endpoint, because the revocation endpoint is credential-bearing too.
func (s *Server) authenticateRevocationClient(
	ctx context.Context, req RevokeRequest,
) (Client, error) {
	if err := validateClientID(req.ClientID); err != nil {
		return Client{}, err
	}
	client, err := s.store.Client(ctx, req.ClientID)
	if err != nil {
		return Client{}, storageOrCause(err, ErrUnknownClient)
	}
	if err := client.Authenticate(req.ClientSecret); err != nil {
		return Client{}, err
	}
	return client, nil
}

// familyOf resolves a presented token to its family, and reports whether it belongs to
// this client. The token is looked up as a refresh token first and then as an access
// token, so a wrong or absent type hint changes nothing.
func (s *Server) familyOf(ctx context.Context, presented Secret, client Client) (FamilyID, bool) {
	lookup := presented.Lookup()
	refresh, err := s.store.RefreshToken(ctx, lookup)
	switch {
	case err == nil:
		return refresh.Family, refresh.ClientID == client.ID()
	case !errors.Is(err, ErrTokenNotFound) && !errors.Is(err, ErrTokenRevoked):
		return "", false
	}
	access, err := s.store.AccessToken(ctx, lookup)
	if err != nil {
		return "", false
	}
	return access.Family, access.ClientID == client.ID()
}

// RevocationHandler serves the RFC 7009 endpoint.
//
// It answers 200 with no body for every outcome that is not the client's own fault, and it
// never echoes the token it was given.
func (s *Server) RevocationHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxRevocationBody)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "malformed request", http.StatusBadRequest)
			return
		}
		req, err := parseRevokeForm(r)
		if err != nil {
			http.Error(w, "malformed request", http.StatusBadRequest)
			return
		}
		if err := s.RevokeToken(r.Context(), req); err != nil {
			http.Error(w, "the client could not be authenticated", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

// parseRevokeForm reads the revocation form, reusing the token endpoint's client
// credential rules so the two endpoints cannot drift apart.
func parseRevokeForm(r *http.Request) (RevokeRequest, error) {
	names := []string{paramClientID, paramClientSecret, paramToken, paramTokenTypeHint}
	for _, name := range names {
		if len(r.PostForm[name]) > 1 {
			return RevokeRequest{}, fmt.Errorf("the %q parameter appears %d times: %w",
				name, len(r.PostForm[name]), ErrDuplicateParameter)
		}
	}
	credentials, err := applyBasicAuth(TokenRequest{
		ClientID:     r.PostForm.Get(paramClientID),
		ClientSecret: SecretFromString(r.PostForm.Get(paramClientSecret)),
	}, r.Header.Get("Authorization"))
	if err != nil {
		return RevokeRequest{}, err
	}
	return RevokeRequest{
		ClientID:      credentials.ClientID,
		ClientSecret:  credentials.ClientSecret,
		Token:         r.PostForm.Get(paramToken),
		TokenTypeHint: r.PostForm.Get(paramTokenTypeHint),
	}, nil
}
